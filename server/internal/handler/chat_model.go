package handler

import (
	"encoding/json"
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

	agent, err := h.Queries.GetAgent(r.Context(), session.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load chat agent")
		return
	}
	if agent.ArchivedAt.Valid {
		writeError(w, http.StatusConflict, "chat agent is archived")
		return
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
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
	updated, err := h.Queries.UpdateChatSessionModelOverride(r.Context(), db.UpdateChatSessionModelOverrideParams{
		ID: session.ID, WorkspaceID: session.WorkspaceID, ModelOverride: modelOverride,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "chat session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update chat model")
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
