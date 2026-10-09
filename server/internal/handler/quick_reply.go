package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/quickreply"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Workspace quick-reply catalog API (RUYI-435).
//
// Reading the catalog is open to any workspace member — every client's
// composer menu needs it to render the picker. Mutating it is owner/admin
// only: quick replies are message templates the whole workspace shares, and
// editing one changes what every member can insert with one tap. This mirrors
// the issue status catalog's authorization split exactly.
//
// The Web settings tab and the MCP quick-reply tools call THESE endpoints, so
// there is one data source and one permission gate for both management
// surfaces.

type QuickReplyResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	Content     string  `json:"content"`
	Position    float64 `json:"position"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func quickReplyToResponse(q db.QuickReply) QuickReplyResponse {
	return QuickReplyResponse{
		ID:          uuidToString(q.ID),
		WorkspaceID: uuidToString(q.WorkspaceID),
		Name:        q.Name,
		Content:     q.Content,
		Position:    q.Position,
		CreatedAt:   timestampToString(q.CreatedAt),
		UpdatedAt:   timestampToString(q.UpdatedAt),
	}
}

func quickRepliesToResponse(list []db.QuickReply) []QuickReplyResponse {
	out := make([]QuickReplyResponse, len(list))
	for i, qr := range list {
		out[i] = quickReplyToResponse(qr)
	}
	return out
}

type CreateQuickReplyRequest struct {
	Name     string   `json:"name"`
	Content  string   `json:"content"`
	Position *float64 `json:"position"`
}

// UpdateQuickReplyRequest is a PATCH: every field is optional and a null or
// absent field leaves the stored value alone.
type UpdateQuickReplyRequest struct {
	Name     *string  `json:"name"`
	Content  *string  `json:"content"`
	Position *float64 `json:"position"`
}

// ReorderQuickRepliesRequest carries the full catalog in its new order. As
// with the status catalog, a partial payload would make "move to top"
// ambiguous, so the client always sends every id.
type ReorderQuickRepliesRequest struct {
	IDs []string `json:"ids"`
}

const (
	maxQuickReplyNameLen    = 64
	maxQuickReplyContentLen = 10_000
)

// validateQuickReplyName trims and validates a display name. Returns the
// trimmed name or an error suitable for a 400 response.
func validateQuickReplyName(raw string) (string, error) {
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", errors.New("name cannot contain tabs, newlines, or control characters")
		}
	}
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("name is required")
	}
	if len([]rune(name)) > maxQuickReplyNameLen {
		return "", errors.New("name must be 64 characters or fewer")
	}
	return name, nil
}

// validateQuickReplyContent trims and validates the template body.
func validateQuickReplyContent(raw string) (string, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", errors.New("content is required")
	}
	if len([]rune(content)) > maxQuickReplyContentLen {
		return "", errors.New("content must be 10000 characters or fewer")
	}
	return content, nil
}

// publishQuickRepliesChanged announces that the workspace catalog moved.
// Clients re-read the whole list; nothing about the changed row travels in
// the frame (same rationale as the issue status catalog event).
func (h *Handler) publishQuickRepliesChanged(workspaceID string, actor db.Member, action string) {
	h.publish(protocol.EventQuickRepliesChanged, workspaceID, "member", uuidToString(actor.UserID), map[string]any{
		"action": action,
	})
}

// ListQuickReplies returns the workspace's quick replies in display order.
// Any member may read it. Self-heals the default seed for workspaces that
// predate the feature — idempotent, a no-op once the rows exist.
func (h *Handler) ListQuickReplies(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found"); !ok {
		return
	}

	if err := quickreply.Ensure(r.Context(), h.Queries, wsUUID); err != nil {
		slog.Warn("failed to ensure quick reply catalog", append(logger.RequestAttrs(r), "error", err)...)
	}

	replies, err := h.Queries.ListQuickReplies(r.Context(), wsUUID)
	if err != nil {
		slog.Warn("ListQuickReplies failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list quick replies")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"quick_replies": quickRepliesToResponse(replies),
		"total":         len(replies),
	})
}

// GetQuickReply returns one template. Any member may read it.
func (h *Handler) GetQuickReply(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found"); !ok {
		return
	}
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "quick reply id")
	if !ok {
		return
	}
	reply, err := h.Queries.GetQuickReply(r.Context(), db.GetQuickReplyParams{
		ID: idUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "quick reply not found")
			return
		}
		slog.Warn("GetQuickReply failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to get quick reply")
		return
	}
	writeJSON(w, http.StatusOK, quickReplyToResponse(reply))
}

// CreateQuickReply adds a template to the workspace catalog.
func (h *Handler) CreateQuickReply(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
	if !ok {
		return
	}

	var req CreateQuickReplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, err := validateQuickReplyName(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	content, err := validateQuickReplyContent(req.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// An explicit position slots the row between neighbours; without one the
	// template appends after everything currently in the catalog.
	position := 0.0
	if req.Position != nil {
		position = *req.Position
	} else {
		maxRow, err := h.Queries.GetMaxQuickReplyPosition(r.Context(), wsUUID)
		if err != nil {
			slog.Warn("GetMaxQuickReplyPosition failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to create quick reply")
			return
		}
		position = maxRow + 1
	}

	reply, err := h.Queries.CreateQuickReply(r.Context(), db.CreateQuickReplyParams{
		WorkspaceID: wsUUID,
		Name:        name,
		Content:     content,
		Position:    position,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a quick reply with that name already exists")
			return
		}
		slog.Warn("CreateQuickReply failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create quick reply")
		return
	}
	resp := quickReplyToResponse(reply)
	h.publishQuickRepliesChanged(workspaceID, member, "create")
	writeJSON(w, http.StatusCreated, resp)
}

// UpdateQuickReply edits name, content and/or position of one template.
func (h *Handler) UpdateQuickReply(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
	if !ok {
		return
	}
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "quick reply id")
	if !ok {
		return
	}

	var req UpdateQuickReplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	params := db.UpdateQuickReplyParams{
		ID:          idUUID,
		WorkspaceID: wsUUID,
	}
	if req.Name != nil {
		name, err := validateQuickReplyName(*req.Name)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.Name = pgtype.Text{String: name, Valid: true}
	}
	if req.Content != nil {
		content, err := validateQuickReplyContent(*req.Content)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.Content = pgtype.Text{String: content, Valid: true}
	}
	if req.Position != nil {
		params.Position = pgtype.Float8{Float64: *req.Position, Valid: true}
	}

	// Branch on pgx.ErrNoRows directly from the UPDATE — the WHERE clause
	// already enforces (id, workspace_id), so a missing row means either the
	// template doesn't exist or it's not in this workspace.
	reply, err := h.Queries.UpdateQuickReply(r.Context(), params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "quick reply not found")
			return
		}
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a quick reply with that name already exists")
			return
		}
		slog.Warn("UpdateQuickReply failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to update quick reply")
		return
	}
	resp := quickReplyToResponse(reply)
	h.publishQuickRepliesChanged(workspaceID, member, "update")
	writeJSON(w, http.StatusOK, resp)
}

// DeleteQuickReply removes one template from the workspace catalog.
func (h *Handler) DeleteQuickReply(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
	if !ok {
		return
	}
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "quick reply id")
	if !ok {
		return
	}

	// DeleteQuickReply is :one RETURNING id — ErrNoRows means the template
	// wasn't in this workspace (404). Any other error is a real 500.
	if _, err := h.Queries.DeleteQuickReply(r.Context(), db.DeleteQuickReplyParams{
		ID: idUUID, WorkspaceID: wsUUID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "quick reply not found")
			return
		}
		slog.Warn("DeleteQuickReply failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to delete quick reply")
		return
	}
	h.publishQuickRepliesChanged(workspaceID, member, "delete")
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": util.UUIDToString(idUUID)})
}

// ReorderQuickReplies rewrites the display order to match the payload. The
// full id list is required: positions are global (not per-category like the
// status catalog), so a partial payload would leave the moved rows' old
// positions fighting the untouched rows'.
func (h *Handler) ReorderQuickReplies(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
	if !ok {
		return
	}

	var req ReorderQuickRepliesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids must not be empty")
		return
	}
	ids := make([]pgtype.UUID, 0, len(req.IDs))
	seen := make(map[string]struct{}, len(req.IDs))
	for _, raw := range req.IDs {
		if _, duplicate := seen[raw]; duplicate {
			writeError(w, http.StatusBadRequest, "duplicate ids")
			return
		}
		seen[raw] = struct{}{}
		idUUID, err := util.ParseUUID(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid quick reply id")
			return
		}
		ids = append(ids, idUUID)
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Warn("ReorderQuickReplies begin failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to reorder quick replies")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// Rewrite the whole visible order as positions 0..n-1. A stale or foreign
	// id can never move another workspace's row (every UPDATE is scoped by
	// (id, workspace_id)), but it WOULD leave a gap in 0..n-1 — verify the
	// moved-row count instead of trusting the payload.
	var moved int64
	for i, id := range ids {
		rows, err := qtx.MoveQuickReply(r.Context(), db.MoveQuickReplyParams{
			ID:          id,
			Position:    float64(i),
			WorkspaceID: wsUUID,
		})
		if err != nil {
			slog.Warn("ReorderQuickReplies move failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to reorder quick replies")
			return
		}
		moved += rows
	}
	if moved != int64(len(ids)) {
		writeError(w, http.StatusBadRequest, "ids must cover every quick reply in this workspace")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Warn("ReorderQuickReplies commit failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to reorder quick replies")
		return
	}
	h.publishQuickRepliesChanged(workspaceID, member, "reorder")

	replies, err := h.Queries.ListQuickReplies(r.Context(), wsUUID)
	if err != nil {
		slog.Warn("ListQuickReplies after reorder failed", append(logger.RequestAttrs(r), "error", err)...)
		writeJSON(w, http.StatusOK, map[string]any{"reordered": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reordered":     true,
		"quick_replies": quickRepliesToResponse(replies),
	})
}
