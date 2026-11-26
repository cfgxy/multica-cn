package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

type promptQuizAgentScopeResponse struct {
	Explicit bool     `json:"explicit"`
	ItemIDs  []string `json:"item_ids"`
}

// GetPromptQuizAgentScope exposes an agent's effective bank-selection mode.
// No marker retains legacy all-active behavior; an explicit marker with no ids
// is an intentional empty range.
func (h *Handler) GetPromptQuizAgentScope(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "agentId"), "agent id")
	if !ok {
		return
	}
	var exists bool
	if err := h.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM agent WHERE id = $1 AND workspace_id = $2)`, agentID, workspaceID).Scan(&exists); err != nil || !exists {
		writeError(w, http.StatusNotFound, "agent not found in this workspace")
		return
	}
	response := promptQuizAgentScopeResponse{ItemIDs: []string{}}
	if err := h.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM prompt_quiz_agent_scope WHERE agent_id = $1)`, agentID).Scan(&response.Explicit); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read quiz item range")
		return
	}
	if response.Explicit {
		rows, err := h.DB.Query(r.Context(), `SELECT item_id FROM prompt_quiz_agent_scope_item WHERE agent_id = $1 ORDER BY item_id`, agentID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read quiz item range")
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id pgtype.UUID
			if err := rows.Scan(&id); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to read quiz item range")
				return
			}
			response.ItemIDs = append(response.ItemIDs, uuidToString(id))
		}
		if err := rows.Err(); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read quiz item range")
			return
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// PutPromptQuizAgentScope saves an explicit Quiz range. The transaction locks
// the workspace and agent so a cross-workspace move/delete cannot race the
// selection; every id must be an active member-profile question in that same
// workspace. An empty list deliberately persists the marker.
func (h *Handler) PutPromptQuizAgentScope(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemIDs []string `json:"item_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "agentId"), "agent id")
	if !ok {
		return
	}
	items := make([]pgtype.UUID, 0, len(req.ItemIDs))
	seen := map[string]bool{}
	for _, raw := range req.ItemIDs {
		id, err := util.ParseUUID(raw)
		if err != nil || seen[raw] {
			writeError(w, http.StatusBadRequest, "item_ids must contain unique UUIDs")
			return
		}
		seen[raw] = true
		items = append(items, id)
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save quiz item range")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var agentWorkspace pgtype.UUID
	if err := tx.QueryRow(r.Context(), `SELECT workspace_id FROM agent WHERE id = $1 FOR UPDATE`, agentID).Scan(&agentWorkspace); errors.Is(err, pgx.ErrNoRows) || uuidToString(agentWorkspace) != uuidToString(workspaceID) {
		writeError(w, http.StatusNotFound, "agent not found in this workspace")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read agent")
		return
	}
	for _, itemID := range items {
		var lockedItem pgtype.UUID
		err := tx.QueryRow(r.Context(), `
SELECT id FROM prompt_quiz_item
WHERE id = $1 AND workspace_id = $2 AND active AND runtime_profile = 'member'
-- FOR SHARE conflicts with regular UPDATE as well as DELETE; a successful
-- scope transaction therefore cannot commit an item that was concurrently
-- archived or moved out of the member profile.
FOR SHARE`, itemID, workspaceID).Scan(&lockedItem)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "item is not an active member-profile item in this workspace")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate quiz item range")
			return
		}
	}
	var beforeExplicit bool
	if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM prompt_quiz_agent_scope WHERE agent_id = $1)`, agentID).Scan(&beforeExplicit); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read quiz item range")
		return
	}
	beforeIDs := []string{}
	beforeRows, queryErr := tx.Query(r.Context(), `SELECT item_id FROM prompt_quiz_agent_scope_item WHERE agent_id = $1 ORDER BY item_id`, agentID)
	if queryErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to read quiz item range")
		return
	}
	for beforeRows.Next() {
		var id pgtype.UUID
		if queryErr = beforeRows.Scan(&id); queryErr != nil {
			break
		}
		beforeIDs = append(beforeIDs, uuidToString(id))
	}
	beforeRows.Close()
	if queryErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to read quiz item range")
		return
	}
	beforeJSON, _ := json.Marshal(beforeIDs)
	afterJSON, _ := json.Marshal(req.ItemIDs)
	if _, err = tx.Exec(r.Context(), `INSERT INTO prompt_quiz_agent_scope(agent_id, workspace_id, configured_by) VALUES ($1, $2, $3) ON CONFLICT(agent_id) DO UPDATE SET workspace_id = EXCLUDED.workspace_id, configured_by = EXCLUDED.configured_by, configured_at = now()`, agentID, workspaceID, requestUserID(r)); err == nil {
		_, err = tx.Exec(r.Context(), `DELETE FROM prompt_quiz_agent_scope_item WHERE agent_id = $1`, agentID)
	}
	if err == nil {
		for _, itemID := range items {
			if _, err = tx.Exec(r.Context(), `INSERT INTO prompt_quiz_agent_scope_item(agent_id, item_id) VALUES ($1, $2)`, agentID, itemID); err != nil {
				break
			}
		}
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO prompt_quiz_agent_scope_audit(workspace_id, agent_id, actor_id, before_explicit, after_explicit, before_item_ids, after_item_ids) VALUES ($1, $2, $3, $4, TRUE, $5::jsonb, $6::jsonb)`, workspaceID, agentID, requestUserID(r), beforeExplicit, beforeJSON, afterJSON)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save quiz item range")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save quiz item range")
		return
	}
	writeJSON(w, http.StatusOK, promptQuizAgentScopeResponse{Explicit: true, ItemIDs: req.ItemIDs})
}
