package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptquiz"
	"github.com/multica-ai/multica/server/pkg/promptquality"
)

// Self-evolution workspace overview (RUYI-284).
//
// One read-only endpoint folding the six real data planes the tabs already
// serve — prompt versions, quality rollups, quiz readings, proposals,
// knowledge, skills — into a member-visible summary. It adds no writer and no
// second caliber: quality folds through the same Combine+Present path the
// quality tab uses, quiz verdicts come from the shared baseline reader
// (promptQuizBaselineData), and the counts run over the same tables and the
// same predicates as the list endpoints. Every number here must stay
// checkable against the tab that owns it — that is what forbids a default or
// a placeholder below.

type SelfEvolutionOverviewTier struct {
	Scope        string `json:"scope"`
	VersionCount int64  `json:"version_count"`
	SubjectCount int64  `json:"subject_count"`
	// CurrentVersion is set only where the tier has a single subject (the
	// workspace tier today). Across N subjects the highest version number
	// belongs to one of them and must not read as a fleet-wide state.
	CurrentVersion *int32 `json:"current_version,omitempty"`
	LastChangeAt   string `json:"last_change_at,omitempty"`
	// LastActor is the author of the tier's most recent change when that
	// change was attributed to a user; automatic snapshots have none, and an
	// absent operator is not reported as one.
	LastActor string `json:"last_actor,omitempty"`
}

type SelfEvolutionOverviewQuality struct {
	Since string `json:"since"`
	Days  int    `json:"days"`
	Runs  int    `json:"runs"`
	// SubjectsMeasured is how many agents carry rollup rows in the window.
	// A fold across zero subjects is an empty window, not a zero-run day.
	SubjectsMeasured int                        `json:"subjects_measured"`
	Measures         promptquality.Presentation `json:"measures"`
	Excluded         int                        `json:"excluded_failed_runs"`
}

type SelfEvolutionOverviewQuiz struct {
	ScopeID         string `json:"scope_id,omitempty"`
	ScopeName       string `json:"scope_name,omitempty"`
	CurrentVersion  int32  `json:"current_version,omitempty"`
	BaselineVersion *int32 `json:"baseline_version,omitempty"`
	// Verdict is the promptquiz verdict of that scope's two newest measured
	// versions. It stays "insufficient" (无读数) unless a real comparison
	// was made — absence of measurements never becomes a grade.
	Verdict        string `json:"verdict"`
	Measured       bool   `json:"measured"`
	RequiredSample int    `json:"required_sample"`
	RequiredBase   int    `json:"required_baseline"`
	LastMeasuredAt string `json:"last_measured_at,omitempty"`
}

type SelfEvolutionOverviewProposal struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type SelfEvolutionOverviewProposals struct {
	Total   int64 `json:"total"`
	Pending int64 `json:"pending"`
	Adopted int64 `json:"adopted"`
	// ByStatus carries the full status breakdown so the summary cannot hide
	// a status behind the two headline counts.
	ByStatus map[string]int64               `json:"by_status"`
	Latest   *SelfEvolutionOverviewProposal `json:"latest,omitempty"`
}

type SelfEvolutionOverviewScan struct {
	Result        string    `json:"result"`
	TriggerSource string    `json:"trigger_source"`
	StartedAt     time.Time `json:"started_at"`
}

type SelfEvolutionOverviewKnowledge struct {
	Dirs    int64 `json:"dirs"`
	Entries int64 `json:"entries"`
	// LastScan is the workspace's most recent scan batch, of any directory.
	LastScan *SelfEvolutionOverviewScan `json:"last_scan,omitempty"`
}

type SelfEvolutionOverviewSkills struct {
	Count int64 `json:"count"`
	// Invocations counts explicit Skill tool invocations only — the same
	// rule skill_usage.go applies per skill. A bound skill nobody invoked
	// is a bound skill, not a used one.
	Invocations int64 `json:"invocations"`
}

type SelfEvolutionOverviewResponse struct {
	Versions  []SelfEvolutionOverviewTier    `json:"versions"`
	Quality   SelfEvolutionOverviewQuality   `json:"quality"`
	Quiz      SelfEvolutionOverviewQuiz      `json:"quiz"`
	Proposals SelfEvolutionOverviewProposals `json:"proposals"`
	Knowledge SelfEvolutionOverviewKnowledge `json:"knowledge"`
	Skills    SelfEvolutionOverviewSkills    `json:"skills"`
}

// GetSelfEvolutionOverview — GET /api/self-evolution/overview?days=N
func (h *Handler) GetSelfEvolutionOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspaceID := parseUUID(h.resolveWorkspaceID(r))

	days := promptQualityDefaultDays
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= promptQualityMaxDays {
			days = n
		}
	}

	versions, err := h.selfEvolutionOverviewVersions(ctx, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read prompt versions")
		return
	}
	quality, err := h.selfEvolutionOverviewQuality(ctx, workspaceID, days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read prompt quality")
		return
	}
	quiz, err := h.selfEvolutionOverviewQuiz(ctx, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read quiz measurements")
		return
	}
	proposals, err := h.selfEvolutionOverviewProposals(ctx, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read proposals")
		return
	}
	knowledge, err := h.selfEvolutionOverviewKnowledge(ctx, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read knowledge directories")
		return
	}
	skills, err := h.selfEvolutionOverviewSkills(ctx, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read skill usage")
		return
	}

	writeJSON(w, http.StatusOK, SelfEvolutionOverviewResponse{
		Versions:  versions,
		Quality:   quality,
		Quiz:      quiz,
		Proposals: proposals,
		Knowledge: knowledge,
		Skills:    skills,
	})
}

func (h *Handler) selfEvolutionOverviewVersions(ctx context.Context, workspaceID pgtype.UUID) ([]SelfEvolutionOverviewTier, error) {
	rows, err := h.Queries.SummarizePromptVersionsByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	actors, err := h.Queries.LatestPromptVersionActorByScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	actorByScope := make(map[string]db.LatestPromptVersionActorByScopeRow, len(actors))
	for _, row := range actors {
		actorByScope[row.Scope] = row
	}

	out := make([]SelfEvolutionOverviewTier, 0, len(rows))
	for _, row := range rows {
		tier := SelfEvolutionOverviewTier{
			Scope:        row.Scope,
			VersionCount: row.VersionCount,
			SubjectCount: row.SubjectCount,
		}
		if row.SubjectCount == 1 {
			v := row.MaxVersion
			tier.CurrentVersion = &v
		}
		if row.LastChangeAt.Valid {
			tier.LastChangeAt = row.LastChangeAt.Time.UTC().Format(httpTimeFormat)
		}
		if actor, ok := actorByScope[row.Scope]; ok && actor.ActorName.Valid {
			tier.LastActor = actor.ActorName.String
		}
		out = append(out, tier)
	}
	return out, nil
}

func (h *Handler) selfEvolutionOverviewQuality(ctx context.Context, workspaceID pgtype.UUID, days int) (SelfEvolutionOverviewQuality, error) {
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
	rows, err := h.Queries.ListAgentPromptQualityDailyByWorkspace(ctx, db.ListAgentPromptQualityDailyByWorkspaceParams{
		Scope:       string(promptVersionScopeAgent),
		WorkspaceID: workspaceID,
		Since:       pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		return SelfEvolutionOverviewQuality{}, err
	}

	all := make([]promptquality.Result, 0, len(rows))
	subjects := map[string]struct{}{}
	for _, row := range rows {
		all = append(all, promptQualityRowToResult(row))
		subjects[uuidToString(row.ScopeID)] = struct{}{}
	}
	// promptQualityMeasures is the fold the quality tab's window row uses:
	// counts add up first, Present decides what may render as a number, and
	// the no-data / insufficient-sample states come out of that decision —
	// there is no branch here that could substitute a zero.
	window := promptQualityMeasures(0, rows, all)
	return SelfEvolutionOverviewQuality{
		Since:            since.Format("2006-01-02"),
		Days:             days,
		Runs:             window.Runs,
		SubjectsMeasured: len(subjects),
		Measures:         window.Measures,
		Excluded:         window.Excluded,
	}, nil
}

func (h *Handler) selfEvolutionOverviewQuiz(ctx context.Context, workspaceID pgtype.UUID) (SelfEvolutionOverviewQuiz, error) {
	quiz := SelfEvolutionOverviewQuiz{
		Verdict:        string(promptquiz.VerdictInsufficient),
		RequiredSample: promptquiz.NewVersionSampleSize,
		RequiredBase:   promptquiz.BaselineSampleSize,
	}
	latest, err := h.Queries.LatestQuizMeasuredScopeByWorkspace(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No measurement anywhere in the workspace: an honest 无读数,
			// not a verdict fabricated from absence.
			return quiz, nil
		}
		return quiz, err
	}
	quiz.ScopeID = uuidToString(latest.ScopeID)
	quiz.ScopeName = latest.ScopeName
	if latest.LastMeasuredAt.Valid {
		quiz.LastMeasuredAt = latest.LastMeasuredAt.Time.UTC().Format(httpTimeFormat)
	}

	base, err := h.promptQuizBaselineData(ctx, promptVersionScope(latest.Scope), latest.ScopeID)
	if err != nil {
		return quiz, err
	}
	quiz.CurrentVersion = base.CurrentVersion
	quiz.Measured = base.Measured
	if base.BaselineVersion != 0 {
		v := base.BaselineVersion
		quiz.BaselineVersion = &v
	}
	if base.Comparison != nil {
		quiz.Verdict = string(base.Comparison.Verdict)
	}
	return quiz, nil
}

func (h *Handler) selfEvolutionOverviewProposals(ctx context.Context, workspaceID pgtype.UUID) (SelfEvolutionOverviewProposals, error) {
	out := SelfEvolutionOverviewProposals{ByStatus: map[string]int64{}}
	rows, err := h.DB.Query(ctx, `
SELECT status, count(*) FROM proposal
WHERE workspace_id = $1 GROUP BY status`, workspaceID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return out, err
		}
		out.ByStatus[status] = count
		out.Total += count
		switch status {
		case "draft", "needs_revision":
			// In-pool states: a draft is unread, a needs_revision is waiting
			// on its author. Both are awaiting a decision, unlike the three
			// statuses that have already had one.
			out.Pending += count
		case "adopted":
			out.Adopted = count
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	var latest SelfEvolutionOverviewProposal
	var id pgtype.UUID
	err = h.DB.QueryRow(ctx, `
SELECT id, title, status, created_at FROM proposal
WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, workspaceID).
		Scan(&id, &latest.Title, &latest.Status, &latest.CreatedAt)
	switch {
	case err == nil:
		latest.ID = uuidToString(id)
		out.Latest = &latest
	case errors.Is(err, pgx.ErrNoRows):
		// An empty pool is a real state; Latest stays nil.
	default:
		return out, err
	}
	return out, nil
}

func (h *Handler) selfEvolutionOverviewKnowledge(ctx context.Context, workspaceID pgtype.UUID) (SelfEvolutionOverviewKnowledge, error) {
	out := SelfEvolutionOverviewKnowledge{}
	// removed rows keep unregistered directories retrievable in the mirror;
	// the overview counts live registrations, matching the dirs tab's list.
	if err := h.DB.QueryRow(ctx, `
SELECT count(*) FROM knowledge_dir WHERE workspace_id = $1 AND NOT removed`, workspaceID).
		Scan(&out.Dirs); err != nil {
		return out, err
	}
	if err := h.DB.QueryRow(ctx, `
SELECT count(*) FROM knowledge_entry WHERE workspace_id = $1`, workspaceID).
		Scan(&out.Entries); err != nil {
		return out, err
	}

	var scan SelfEvolutionOverviewScan
	err := h.DB.QueryRow(ctx, `
SELECT result, trigger_source, started_at FROM knowledge_scan_batch
WHERE workspace_id = $1 ORDER BY started_at DESC LIMIT 1`, workspaceID).
		Scan(&scan.Result, &scan.TriggerSource, &scan.StartedAt)
	switch {
	case err == nil:
		out.LastScan = &scan
	case errors.Is(err, pgx.ErrNoRows):
		// Never scanned is a real state; LastScan stays nil.
	default:
		return out, err
	}
	return out, nil
}

func (h *Handler) selfEvolutionOverviewSkills(ctx context.Context, workspaceID pgtype.UUID) (SelfEvolutionOverviewSkills, error) {
	out := SelfEvolutionOverviewSkills{}
	if err := h.DB.QueryRow(ctx, `SELECT count(*) FROM skill WHERE workspace_id = $1`, workspaceID).
		Scan(&out.Count); err != nil {
		return out, err
	}
	// The same explicit-invocation rule as GetSkillUsage (skill_usage.go):
	// a tool_use message naming a workspace skill whose version already
	// existed at claim time. The LATERAL match is the evidence of use —
	// without it a name in a message body would count as a call.
	err := h.DB.QueryRow(ctx, `
SELECT count(*) FROM task_message m
JOIN agent_task_queue t ON t.id = m.task_id
JOIN agent a ON a.id = t.agent_id
JOIN LATERAL (
    SELECT 1 FROM skill_version sv
    WHERE sv.workspace_id = a.workspace_id AND sv.name = m.input->>'skill'
        AND sv.created_at <= COALESCE(t.started_at, t.created_at)
    ORDER BY sv.version DESC LIMIT 1
) v ON TRUE
WHERE a.workspace_id = $1 AND m.type = 'tool_use'
    AND m.tool IN ('Skill', 'functions.Skill')`, workspaceID).Scan(&out.Invocations)
	if err != nil {
		return out, err
	}
	return out, nil
}
