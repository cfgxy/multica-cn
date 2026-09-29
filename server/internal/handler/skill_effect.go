package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Skill effect comparison (self-evolution §S.5/§S.6). The use group is runs
// with at least one explicit Skill-tool invocation; the control group is runs
// of the same roles on the same project that never invoked the skill. Both
// groups only count terminal runs, and every metric keeps its "not measured"
// state separate from zero (nullable medians, issue counters that only count
// issues that actually reached review).
type SkillEffectMetrics struct {
	Runs              int64    `json:"runs"`
	TokenSamples      int64    `json:"token_samples"`
	MedianTotalTokens *float64 `json:"median_total_tokens"`
	RetriedRuns       int64    `json:"retried_runs"`
	ReviewedIssues    int64    `json:"reviewed_issues"`
	FirstPassIssues   int64    `json:"first_pass_issues"`
}

// D3 perplexity is a prompt-version-level readout, not a run outcome: the runs
// on both sides of the comparison read the same agent brief (skills are
// injected as a name index only), so both columns report the same band set and
// the delta is not comparable. The row is rendered from real scores when they
// exist and an explicit "not scored" state when they do not — never invented.
type SkillEffectPerplexity struct {
	Scored bool     `json:"scored"`
	Bands  []string `json:"bands,omitempty"`
}

type SkillEffectEvent struct {
	Version     int32               `json:"version"`
	FromVersion int32               `json:"from_version"`
	Source      string              `json:"source"`
	OperatorID  string              `json:"operator_id,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	Before      *SkillEffectMetrics `json:"before,omitempty"`
	After       *SkillEffectMetrics `json:"after,omitempty"`
	WindowDays  int                 `json:"window_days"`
	WindowUses  int                 `json:"window_uses"`
	BeforeMode  string              `json:"before_mode"`
	AfterMode   string              `json:"after_mode"`
}

type SkillEffectResponse struct {
	Since         string                `json:"since,omitempty"`
	UseGroup      SkillEffectMetrics    `json:"use_group"`
	ControlGroup  SkillEffectMetrics    `json:"control_group"`
	Perplexity    SkillEffectPerplexity `json:"perplexity"`
	VersionEvents []SkillEffectEvent    `json:"version_events"`
}

// skillEffectInvocations matches the attribution rule of GetSkillUsage: an
// explicit Skill-tool call whose input names the skill resolves (via the
// version LATERAL) to the version that was current when the run started.
// Parameters: $1 skill id, $2 workspace id.
const skillEffectInvocations = `
SELECT m.task_id, m.created_at AS used_at, v.version, t.agent_id, t.issue_id
FROM task_message m
JOIN agent_task_queue t ON t.id = m.task_id
JOIN agent a ON a.id = t.agent_id
JOIN LATERAL (
	SELECT version, name FROM skill_version
	WHERE skill_id = $1 AND workspace_id = $2
		AND created_at <= COALESCE(t.started_at, t.created_at)
	ORDER BY version DESC LIMIT 1
) v ON v.name = m.input->>'skill'
WHERE a.workspace_id = $2 AND m.type = 'tool_use'
	AND m.tool IN ('Skill', 'functions.Skill')`

// skillEffectMetrics aggregates the four §S.5 metrics over a task-id set
// provided as a UUID array parameter. Token median comes from summed
// task_usage rows; the D5 rework counter from attempt; the D7 first-pass
// counters from the issue's status-transition history (an issue that never
// reached review is counted in neither column — the existing coarse D7 rule).
const skillEffectMetrics = `
WITH measured AS (
	SELECT t.id, t.attempt, t.issue_id, cost.total_tokens
	FROM agent_task_queue t
	LEFT JOIN LATERAL (
		SELECT SUM(u.input_tokens + u.output_tokens)::bigint AS total_tokens
		FROM task_usage u WHERE u.task_id = t.id
	) cost ON TRUE
	WHERE t.id = ANY($1::uuid[])
		AND t.status IN ('completed', 'failed', 'cancelled')
		AND t.completed_at IS NOT NULL
), review AS (
	SELECT
		count(*) FILTER (WHERE entered_review > 0) AS reviewed_issues,
		count(*) FILTER (WHERE entered_review > 0 AND sent_back = 0) AS first_pass_issues
	FROM (
		SELECT al.issue_id,
			count(*) FILTER (WHERE al.details->>'to' = 'in_review') AS entered_review,
			count(*) FILTER (WHERE al.details->>'from' = 'in_review'
				AND al.details->>'to' = 'in_progress') AS sent_back
		FROM activity_log al
		WHERE al.issue_id IN (SELECT DISTINCT issue_id FROM measured)
			AND al.workspace_id = $2 AND al.action = 'status_changed'
		GROUP BY al.issue_id
	) outcomes
)
SELECT m.runs, m.token_samples, m.median_total_tokens, m.retried_runs,
	COALESCE(r.reviewed_issues, 0), COALESCE(r.first_pass_issues, 0)
FROM (
	SELECT count(*) AS runs,
		count(total_tokens) AS token_samples,
		percentile_cont(0.5) WITHIN GROUP (ORDER BY total_tokens) AS median_total_tokens,
		count(*) FILTER (WHERE attempt > 1) AS retried_runs
	FROM measured
) m CROSS JOIN review r`

func (h *Handler) skillEffectMetricsFor(w http.ResponseWriter, r *http.Request,
	workspaceID pgtype.UUID, taskIDs []string) (SkillEffectMetrics, bool) {
	var metrics SkillEffectMetrics
	if len(taskIDs) == 0 {
		return metrics, true
	}
	if err := h.DB.QueryRow(r.Context(), skillEffectMetrics, taskIDs, workspaceID).Scan(
		&metrics.Runs, &metrics.TokenSamples, &metrics.MedianTotalTokens,
		&metrics.RetriedRuns, &metrics.ReviewedIssues, &metrics.FirstPassIssues); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill effect comparison")
		return metrics, false
	}
	return metrics, true
}

// GetSkillEffect serves GET /api/skills/{id}/effect: the use-group vs
// control-group four-metric card plus the per-version event timeline.
func (h *Handler) GetSkillEffect(w http.ResponseWriter, r *http.Request) {
	skill, ok := h.loadSkillForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	// The instrumentation window starts at the skill's first version; runs
	// before that could not have invoked it under versioned attribution.
	var firstVersionAt time.Time
	if err := h.DB.QueryRow(r.Context(),
		`SELECT min(created_at) FROM skill_version WHERE skill_id = $1 AND workspace_id = $2`,
		skill.ID, skill.WorkspaceID).Scan(&firstVersionAt); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill effect comparison")
		return
	}

	response := SkillEffectResponse{
		Since:         firstVersionAt.Format(time.RFC3339),
		VersionEvents: make([]SkillEffectEvent, 0),
	}

	// Use group: every run with at least one explicit invocation.
	useTasks, ok := h.skillEffectTaskIDs(w, r, `
SELECT DISTINCT task_id FROM (`+skillEffectInvocations+`) invocations`,
		skill.ID, skill.WorkspaceID)
	if !ok {
		return
	}
	if response.UseGroup, ok = h.skillEffectMetricsFor(w, r, skill.WorkspaceID, useTasks); !ok {
		return
	}

	// Control group: terminal runs of the same roles on the same project,
	// inside the instrumentation window, with zero invocations of this skill.
	// "Same role" is operationalized as agents that invoked the skill at least
	// once elsewhere — a run by an agent that never had the skill available
	// cannot serve as the counterfactual for choosing not to use it.
	controlTasks, ok := h.skillEffectTaskIDs(w, r, `
SELECT t.id
FROM agent_task_queue t
JOIN agent a ON a.id = t.agent_id
JOIN issue i ON i.id = t.issue_id
JOIN (
	SELECT DISTINCT inv.agent_id, i2.project_id
	FROM (`+skillEffectInvocations+`) inv
	JOIN issue i2 ON i2.id = inv.issue_id
) pairs ON pairs.agent_id = t.agent_id
	AND pairs.project_id IS NOT DISTINCT FROM i.project_id
WHERE a.workspace_id = $2
	AND t.status IN ('completed', 'failed', 'cancelled') AND t.completed_at IS NOT NULL
	AND COALESCE(t.started_at, t.created_at) >= $3
	AND NOT EXISTS (
		SELECT 1 FROM task_message m
		JOIN LATERAL (
			SELECT name FROM skill_version
			WHERE skill_id = $1 AND workspace_id = $2
				AND created_at <= COALESCE(t.started_at, t.created_at)
			ORDER BY version DESC LIMIT 1
		) v ON v.name = m.input->>'skill'
		WHERE m.task_id = t.id AND m.type = 'tool_use'
			AND m.tool IN ('Skill', 'functions.Skill')
	)`, skill.ID, skill.WorkspaceID, firstVersionAt)
	if !ok {
		return
	}
	if response.ControlGroup, ok = h.skillEffectMetricsFor(w, r, skill.WorkspaceID, controlTasks); !ok {
		return
	}

	// D3 row: the agent-prompt perplexity bands covering the use group's runs
	// (member profile). Identical on both sides by construction; the UI states
	// this rather than rendering a fabricated difference.
	perplexity, ok := h.skillEffectPerplexity(w, r, skill.ID, skill.WorkspaceID)
	if !ok {
		return
	}
	response.Perplexity = perplexity

	// Version event timeline: each version >= 2 compares the outgoing
	// version's last uses with the incoming version's first uses, each window
	// capped at 30 days or 20 uses — whichever boundary is hit first.
	versions, ok := h.skillEffectVersions(w, r, skill.ID, skill.WorkspaceID)
	if !ok {
		return
	}
	const windowDays, windowUses = 30, 20
	for _, v := range versions {
		event := SkillEffectEvent{
			Version:     v.version,
			FromVersion: v.version - 1,
			Source:      v.source,
			CreatedAt:   v.createdAt,
			WindowDays:  windowDays,
			WindowUses:  windowUses,
		}
		if v.authorID.Valid {
			event.OperatorID = uuidToString(v.authorID)
		}

		beforeUses, ok := h.skillEffectWindowUses(w, r, skill.ID, skill.WorkspaceID,
			v.version-1, v.createdAt, true, windowDays, windowUses)
		if !ok {
			return
		}
		beforeTasks := distinctTaskIDs(beforeUses)
		beforeMetrics, ok := h.skillEffectMetricsFor(w, r, skill.WorkspaceID, beforeTasks)
		if !ok {
			return
		}
		event.Before = &beforeMetrics
		event.BeforeMode = windowMode(len(beforeUses), windowUses)

		afterUses, ok := h.skillEffectWindowUses(w, r, skill.ID, skill.WorkspaceID,
			v.version, v.createdAt, false, windowDays, windowUses)
		if !ok {
			return
		}
		afterTasks := distinctTaskIDs(afterUses)
		afterMetrics, ok := h.skillEffectMetricsFor(w, r, skill.WorkspaceID, afterTasks)
		if !ok {
			return
		}
		event.After = &afterMetrics
		event.AfterMode = windowMode(len(afterUses), windowUses)

		response.VersionEvents = append(response.VersionEvents, event)
	}

	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) skillEffectTaskIDs(w http.ResponseWriter, r *http.Request,
	query string, args ...any) ([]string, bool) {
	rows, err := h.DB.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill effect comparison")
		return nil, false
	}
	defer rows.Close()
	ids := make([]string, 0, 64)
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read skill effect comparison")
			return nil, false
		}
		ids = append(ids, uuidToString(id))
	}
	return ids, rows.Err() == nil
}

type skillEffectUse struct {
	taskID pgtype.UUID
	usedAt time.Time
}

// skillEffectWindowUses fetches one side of a version-event comparison window:
// up to `uses` invocations of `version` inside the `days` window before (or
// after) the event time, newest-first before the event and oldest-first after.
func (h *Handler) skillEffectWindowUses(w http.ResponseWriter, r *http.Request,
	skillID, workspaceID pgtype.UUID, version int32, eventAt time.Time,
	before bool, days, uses int) ([]skillEffectUse, bool) {
	order, bound := "DESC", "<"
	if !before {
		order, bound = "ASC", ">="
	}
	rows, err := h.DB.Query(r.Context(), `
SELECT task_id, used_at FROM (
	SELECT task_id, used_at FROM (`+skillEffectInvocations+`) inv
	WHERE inv.version = $3 AND inv.used_at `+bound+` $4
		AND inv.used_at >= $5 AND inv.used_at < $6
	ORDER BY used_at `+order+` LIMIT $7
) windowed`,
		skillID, workspaceID, version, eventAt,
		eventAt.Add(-time.Duration(days)*24*time.Hour),
		eventAt.Add(time.Duration(days)*24*time.Hour), uses)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill effect comparison")
		return nil, false
	}
	defer rows.Close()
	usesList := make([]skillEffectUse, 0, uses)
	for rows.Next() {
		var use skillEffectUse
		if err := rows.Scan(&use.taskID, &use.usedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read skill effect comparison")
			return nil, false
		}
		usesList = append(usesList, use)
	}
	return usesList, rows.Err() == nil
}

// windowMode names which boundary closed the window: "uses" when the per-side
// use cap was reached inside the day range, "days" when the day range closed
// first, "none" when there were no uses at all. The mode is declared in the
// timeline row (§S.6 window discipline note).
func windowMode(useCount, cap int) string {
	switch {
	case useCount == 0:
		return "none"
	case useCount >= cap:
		return "uses"
	default:
		return "days"
	}
}

func distinctTaskIDs(uses []skillEffectUse) []string {
	seen := make(map[pgtype.UUID]struct{}, len(uses))
	ids := make([]string, 0, len(uses))
	for _, use := range uses {
		if _, dup := seen[use.taskID]; dup {
			continue
		}
		seen[use.taskID] = struct{}{}
		ids = append(ids, uuidToString(use.taskID))
	}
	return ids
}

func (h *Handler) skillEffectPerplexity(w http.ResponseWriter, r *http.Request,
	skillID, workspaceID pgtype.UUID) (SkillEffectPerplexity, bool) {
	var result SkillEffectPerplexity
	fail := func() (SkillEffectPerplexity, bool) {
		writeError(w, http.StatusInternalServerError, "failed to load skill effect comparison")
		return SkillEffectPerplexity{}, false
	}
	rows, err := h.DB.Query(r.Context(), `
SELECT DISTINCT pps.band
FROM (`+skillEffectInvocations+`) inv
JOIN agent_task_queue t ON t.id = inv.task_id
JOIN LATERAL (
	SELECT version FROM prompt_version
	WHERE workspace_id = $2 AND scope = 'agent' AND scope_id = inv.agent_id
		AND created_at <= COALESCE(t.started_at, t.created_at)
	ORDER BY version DESC LIMIT 1
) pv ON TRUE
JOIN prompt_perplexity_score pps ON pps.workspace_id = $2
	AND pps.scope = 'agent' AND pps.scope_id = inv.agent_id
	AND pps.version = pv.version AND pps.runtime_profile = 'member'`,
		skillID, workspaceID)
	if err != nil {
		return fail()
	}
	defer rows.Close()
	for rows.Next() {
		var band string
		if err := rows.Scan(&band); err != nil {
			return fail()
		}
		result.Bands = append(result.Bands, band)
	}
	if rows.Err() != nil {
		return fail()
	}
	result.Scored = len(result.Bands) > 0
	return result, true
}

type skillEffectVersionRow struct {
	version   int32
	source    string
	authorID  pgtype.UUID
	createdAt time.Time
}

func (h *Handler) skillEffectVersions(w http.ResponseWriter, r *http.Request,
	skillID, workspaceID pgtype.UUID) ([]skillEffectVersionRow, bool) {
	rows, err := h.DB.Query(r.Context(), `
SELECT version, source, author_user_id, created_at FROM skill_version
WHERE skill_id = $1 AND workspace_id = $2 AND version > 1
ORDER BY version DESC LIMIT 20`, skillID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill effect comparison")
		return nil, false
	}
	defer rows.Close()
	var versions []skillEffectVersionRow
	for rows.Next() {
		var v skillEffectVersionRow
		if err := rows.Scan(&v.version, &v.source, &v.authorID, &v.createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read skill effect comparison")
			return nil, false
		}
		versions = append(versions, v)
	}
	return versions, rows.Err() == nil
}
