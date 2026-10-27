package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type SetChatSessionModelRequest struct {
	ModelOverride json.RawMessage `json:"model_override"`
}

// SetChatSessionModel updates only the current session's model preference.
// A null override means future turns follow the Agent default.
func (h *Handler) SetChatSessionModel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	session, ok := h.gatePublicChatSessionForUser(w, r, userID, workspaceID, chi.URLParam(r, "sessionId"))
	if !ok {
		return
	}
	if session.Status != "active" {
		writeError(w, http.StatusConflict, "chat session is archived")
		return
	}
	var req SetChatSessionModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ModelOverride == nil {
		writeError(w, http.StatusBadRequest, "model_override is required")
		return
	}
	modelOverride := pgtype.Text{}
	if string(req.ModelOverride) != "null" {
		var model string
		if err := json.Unmarshal(req.ModelOverride, &model); err != nil {
			writeError(w, http.StatusBadRequest, "model_override must be a model id or null")
			return
		}
		model = strings.TrimSpace(model)
		if model == "" || len([]rune(model)) > 200 {
			writeError(w, http.StatusBadRequest, "model_override must be a non-empty model id up to 200 characters")
			return
		}
		modelOverride = pgtype.Text{String: model, Valid: true}
	}

	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start chat model update")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// Take the same first-statement lock as runtime rebinding and chat enqueue,
	// then use only state re-read under it for authorization and model validation.
	if _, err := qtx.LockChatSessionForRuntimeBind(r.Context(), session.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "chat session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to lock chat session")
		return
	}
	currentSession, err := qtx.GetChatSession(r.Context(), session.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "chat session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to reload chat session")
		return
	}
	if currentSession.Status != "active" {
		writeError(w, http.StatusConflict, "chat session is archived")
		return
	}
	agent, err := qtx.GetAgent(r.Context(), currentSession.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load chat agent")
		return
	}
	if agent.ArchivedAt.Valid {
		writeError(w, http.StatusConflict, "chat agent is archived")
		return
	}
	if !h.canInvokeAgent(r.Context(), agent, actorType, actorID, h.invokeOriginatorFromRequest(r, actorType, actorID), workspaceID) {
		h.writeDispatchBlocked(w, http.StatusForbidden, ReasonInvocationNotAllowed)
		return
	}
	if modelOverride.Valid {
		if !agent.RuntimeID.Valid {
			writeError(w, http.StatusConflict, "chat agent has no runtime")
			return
		}
		catalog, err := h.ModelCatalogCache.Get(r.Context(), uuidToString(agent.RuntimeID))
		if err != nil || catalog == nil || !catalog.Supported || len(catalog.Models) == 0 {
			writeError(w, http.StatusServiceUnavailable, "runtime model catalog is unavailable")
			return
		}
		available := false
		for _, model := range catalog.Models {
			if model.ID == modelOverride.String {
				available = true
				break
			}
		}
		if !available {
			writeError(w, http.StatusConflict, "model is not available on the chat runtime")
			return
		}
	}
	updated, err := qtx.UpdateChatSessionModelOverride(r.Context(), db.UpdateChatSessionModelOverrideParams{
		ID: currentSession.ID, WorkspaceID: currentSession.WorkspaceID, ModelOverride: modelOverride,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "chat session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update chat model")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit chat model update")
		return
	}
	modelValue := textToPtr(updated.ModelOverride)
	payload := protocol.ChatSessionUpdatedPayload{
		ChatSessionID: uuidToString(updated.ID),
		Title:         updated.Title,
		ModelOverride: &modelValue,
		UpdatedAt:     timestampToString(updated.UpdatedAt),
	}
	h.publishChat(protocol.EventChatSessionUpdated, workspaceID, "member", userID, payload.ChatSessionID, payload)
	writeJSON(w, http.StatusOK, chatSessionToResponse(updated))
}
