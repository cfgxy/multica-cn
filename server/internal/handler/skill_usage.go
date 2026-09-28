package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type SkillUseResponse struct {
	TaskID  string    `json:"task_id"`
	IssueID *string   `json:"issue_id,omitempty"`
	Version int32     `json:"version"`
	UsedAt  time.Time `json:"used_at"`
}

type SkillVersionUsageResponse struct {
	Version int32 `json:"version"`
	Count   int64 `json:"count"`
}

type SkillUsageResponse struct {
	Total          int64                       `json:"total"`
	Last30Days     int64                       `json:"last_30_days"`
	AssignedAgents int64                       `json:"assigned_agents"`
	Since          string                      `json:"since,omitempty"`
	Versions       []SkillVersionUsageResponse `json:"versions"`
	Recent         []SkillUseResponse          `json:"recent"`
}

// Only explicit Skill tool invocations establish use. A bound or injected
// skill is not evidence that the agent chose to invoke it.
const skillInvocations = `WITH invocations AS (
	SELECT m.task_id, m.seq, m.created_at, t.issue_id, v.version
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
		AND m.tool IN ('Skill', 'functions.Skill')
) `

func (h *Handler) GetSkillUsage(w http.ResponseWriter, r *http.Request) {
	skill, ok := h.loadSkillForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	usage := SkillUsageResponse{
		Versions: make([]SkillVersionUsageResponse, 0),
		Recent:   make([]SkillUseResponse, 0),
	}
	if err := h.DB.QueryRow(r.Context(), `SELECT count(*) FROM agent_skill bound
		JOIN agent a ON a.id = bound.agent_id
		WHERE bound.skill_id = $1 AND a.workspace_id = $2 AND bound.enabled = true`,
		skill.ID, skill.WorkspaceID).Scan(&usage.AssignedAgents); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill usage")
		return
	}
	rows, err := h.DB.Query(r.Context(), skillInvocations+`
		SELECT version, count(*) FROM invocations GROUP BY version ORDER BY version DESC`,
		skill.ID, skill.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill usage")
		return
	}
	for rows.Next() {
		var v SkillVersionUsageResponse
		if err := rows.Scan(&v.Version, &v.Count); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "failed to load skill usage")
			return
		}
		usage.Total += v.Count
		usage.Versions = append(usage.Versions, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill usage")
		return
	}
	var first *time.Time
	if err := h.DB.QueryRow(r.Context(), skillInvocations+`
		SELECT count(*) FILTER (WHERE created_at >= now() - interval '30 days'), min(created_at)
		FROM invocations`, skill.ID, skill.WorkspaceID).Scan(&usage.Last30Days, &first); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill usage")
		return
	}
	if first != nil {
		usage.Since = first.Format(time.RFC3339)
	}
	rows, err = h.DB.Query(r.Context(), skillInvocations+`
		SELECT task_id, issue_id, version, created_at FROM invocations
		ORDER BY created_at DESC, seq DESC LIMIT 50`, skill.ID, skill.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill usage")
		return
	}
	for rows.Next() {
		var id, issueID pgtype.UUID
		var use SkillUseResponse
		if err := rows.Scan(&id, &issueID, &use.Version, &use.UsedAt); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "failed to load skill usage")
			return
		}
		use.TaskID = uuidToString(id)
		if issueID.Valid {
			issue := uuidToString(issueID)
			use.IssueID = &issue
		}
		usage.Recent = append(usage.Recent, use)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load skill usage")
		return
	}
	writeJSON(w, http.StatusOK, usage)
}
