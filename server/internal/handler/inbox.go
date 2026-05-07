package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type InboxItemResponse struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	RecipientType string          `json:"recipient_type"`
	RecipientID   string          `json:"recipient_id"`
	Type          string          `json:"type"`
	Severity      string          `json:"severity"`
	IssueID       *string         `json:"issue_id"`
	Title         string          `json:"title"`
	Body          *string         `json:"body"`
	Read          bool            `json:"read"`
	Archived      bool            `json:"archived"`
	CreatedAt     string          `json:"created_at"`
	IssueStatus   *string         `json:"issue_status"`
	ActorType     *string         `json:"actor_type"`
	ActorID       *string         `json:"actor_id"`
	Details       json.RawMessage `json:"details"`
}

// InboxListResponse is the paginated wrapper served when the client opts into
// pagination via ?limit / ?before. Old clients (no params) still receive the
// raw []InboxItemResponse array via the legacy path; see ListInbox.
type InboxListResponse struct {
	Entries    []InboxItemResponse `json:"entries"`
	NextCursor *string             `json:"next_cursor"`
	HasMore    bool                `json:"has_more"`
}

const (
	inboxDefaultLimit = 50
	inboxMaxLimit     = 100
	// inboxLegacyCap honours the spirit of #1968 — old clients couldn't
	// render thousands of entries without freezing the tab anyway, so the
	// no-params legacy path returns at most this many newest items.
	inboxLegacyCap = 200
)

func inboxToResponse(i db.InboxItem) InboxItemResponse {
	return InboxItemResponse{
		ID:            uuidToString(i.ID),
		WorkspaceID:   uuidToString(i.WorkspaceID),
		RecipientType: i.RecipientType,
		RecipientID:   uuidToString(i.RecipientID),
		Type:          i.Type,
		Severity:      i.Severity,
		IssueID:       uuidToPtr(i.IssueID),
		Title:         i.Title,
		Body:          textToPtr(i.Body),
		Read:          i.Read,
		Archived:      i.Archived,
		CreatedAt:     timestampToString(i.CreatedAt),
		ActorType:     textToPtr(i.ActorType),
		ActorID:       uuidToPtr(i.ActorID),
		Details:       json.RawMessage(i.Details),
	}
}

func inboxLatestRowToResponse(r db.ListInboxItemsLatestRow) InboxItemResponse {
	return InboxItemResponse{
		ID:            uuidToString(r.ID),
		WorkspaceID:   uuidToString(r.WorkspaceID),
		RecipientType: r.RecipientType,
		RecipientID:   uuidToString(r.RecipientID),
		Type:          r.Type,
		Severity:      r.Severity,
		IssueID:       uuidToPtr(r.IssueID),
		Title:         r.Title,
		Body:          textToPtr(r.Body),
		Read:          r.Read,
		Archived:      r.Archived,
		CreatedAt:     timestampToString(r.CreatedAt),
		IssueStatus:   textToPtr(r.IssueStatus),
		ActorType:     textToPtr(r.ActorType),
		ActorID:       uuidToPtr(r.ActorID),
		Details:       json.RawMessage(r.Details),
	}
}

func inboxBeforeRowToResponse(r db.ListInboxItemsBeforeRow) InboxItemResponse {
	return InboxItemResponse{
		ID:            uuidToString(r.ID),
		WorkspaceID:   uuidToString(r.WorkspaceID),
		RecipientType: r.RecipientType,
		RecipientID:   uuidToString(r.RecipientID),
		Type:          r.Type,
		Severity:      r.Severity,
		IssueID:       uuidToPtr(r.IssueID),
		Title:         r.Title,
		Body:          textToPtr(r.Body),
		Read:          r.Read,
		Archived:      r.Archived,
		CreatedAt:     timestampToString(r.CreatedAt),
		IssueStatus:   textToPtr(r.IssueStatus),
		ActorType:     textToPtr(r.ActorType),
		ActorID:       uuidToPtr(r.ActorID),
		Details:       json.RawMessage(r.Details),
	}
}

func (h *Handler) enrichInboxResponse(ctx context.Context, resp InboxItemResponse, issueID pgtype.UUID) InboxItemResponse {
	if !issueID.Valid {
		return resp
	}
	issue, err := h.Queries.GetIssue(ctx, issueID)
	if err == nil {
		s := issue.Status
		resp.IssueStatus = &s
	}
	return resp
}

// ListInbox serves both legacy and cursor-paginated callers. Old desktop
// clients (pre-pagination) call GET /inbox with no query string and consume
// the response body as []InboxItemResponse directly — wrapping the body
// would crash them with "inbox.filter is not a function" (cf. #2143/#2147
// on the timeline endpoint). Absence of every pagination param uniquely
// identifies a legacy caller; new clients always send ?limit=...
//
// IMPORTANT: the legacy branch is PERMANENT, not transitional. Desktop
// users may never auto-update — removing this branch would white-screen
// every install still on the old client. Treat the no-params contract as a
// long-term API surface, not as something to drop "once everyone upgrades".
func (h *Handler) ListInbox(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	recipientID := parseUUID(userID)

	q := r.URL.Query()
	if q.Get("limit") == "" && q.Get("before") == "" {
		h.listInboxLegacy(w, r, wsUUID, recipientID)
		return
	}

	limit := inboxDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > inboxMaxLimit {
			writeError(w, http.StatusBadRequest, "limit exceeds maximum of 100")
			return
		}
		limit = n
	}

	if before := q.Get("before"); before != "" {
		h.listInboxBefore(w, r, wsUUID, recipientID, before, limit)
		return
	}
	h.listInboxLatest(w, r, wsUUID, recipientID, limit)
}

// listInboxLegacy serves pre-pagination clients with the old []InboxItemResponse
// shape, capped at inboxLegacyCap to avoid OOM on very large inboxes. This
// is a permanent compatibility surface (see ListInbox doc) — never delete.
//
// Two visible degradations for old desktops are accepted as the price of
// graceful compatibility:
//   1. items beyond inboxLegacyCap are unreachable on the old client (it
//      has no scroll-to-load UI to fetch them). Better than rendering
//      thousands and freezing the tab — see #1968.
//   2. the unread badge on old desktops is derived from this list, so it
//      caps at the unread count within the first 200 items. New clients use
//      the dedicated /inbox/unread-count endpoint and don't have this cap.
func (h *Handler) listInboxLegacy(w http.ResponseWriter, r *http.Request, wsUUID, recipientID pgtype.UUID) {
	items, err := h.Queries.ListInboxItemsLatest(r.Context(), db.ListInboxItemsLatestParams{
		WorkspaceID:   wsUUID,
		RecipientType: "member",
		RecipientID:   recipientID,
		Limit:         int32(inboxLegacyCap),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list inbox")
		return
	}
	resp := make([]InboxItemResponse, len(items))
	for i, item := range items {
		resp[i] = inboxLatestRowToResponse(item)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listInboxLatest(w http.ResponseWriter, r *http.Request, wsUUID, recipientID pgtype.UUID, limit int) {
	// Fetch limit+1 to detect "more rows exist" without a separate COUNT.
	items, err := h.Queries.ListInboxItemsLatest(r.Context(), db.ListInboxItemsLatestParams{
		WorkspaceID:   wsUUID,
		RecipientType: "member",
		RecipientID:   recipientID,
		Limit:         int32(limit + 1),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list inbox")
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	entries := make([]InboxItemResponse, len(items))
	for i, item := range items {
		entries[i] = inboxLatestRowToResponse(item)
	}

	resp := InboxListResponse{Entries: entries, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		c := encodeTimelineCursor(last.CreatedAt, last.ID)
		resp.NextCursor = &c
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listInboxBefore(w http.ResponseWriter, r *http.Request, wsUUID, recipientID pgtype.UUID, cursor string, limit int) {
	t, id, err := decodeTimelineCursor(cursor)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	items, err := h.Queries.ListInboxItemsBefore(r.Context(), db.ListInboxItemsBeforeParams{
		WorkspaceID:     wsUUID,
		RecipientType:   "member",
		RecipientID:     recipientID,
		CursorCreatedAt: t,
		CursorID:        id,
		RowLimit:        int32(limit + 1),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list inbox")
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	entries := make([]InboxItemResponse, len(items))
	for i, item := range items {
		entries[i] = inboxBeforeRowToResponse(item)
	}

	resp := InboxListResponse{Entries: entries, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		c := encodeTimelineCursor(last.CreatedAt, last.ID)
		resp.NextCursor = &c
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) MarkInboxRead(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	prev, ok := h.loadInboxItemForUser(w, r, id)
	if !ok {
		return
	}
	item, err := h.Queries.MarkInboxRead(r.Context(), prev.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark read")
		return
	}

	userID := requestUserID(r)
	workspaceID := uuidToString(item.WorkspaceID)
	h.publish(protocol.EventInboxRead, workspaceID, "member", userID, map[string]any{
		"item_id":      uuidToString(item.ID),
		"recipient_id": uuidToString(item.RecipientID),
	})

	resp := h.enrichInboxResponse(r.Context(), inboxToResponse(item), item.IssueID)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ArchiveInboxItem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	prev, ok := h.loadInboxItemForUser(w, r, id)
	if !ok {
		return
	}
	item, err := h.Queries.ArchiveInboxItem(r.Context(), prev.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive")
		return
	}

	// Archive all sibling inbox items for the same issue (issue-level archive)
	if item.IssueID.Valid {
		h.Queries.ArchiveInboxByIssue(r.Context(), db.ArchiveInboxByIssueParams{
			WorkspaceID:   item.WorkspaceID,
			RecipientType: item.RecipientType,
			RecipientID:   item.RecipientID,
			IssueID:       item.IssueID,
		})
	}

	userID := requestUserID(r)
	workspaceID := uuidToString(item.WorkspaceID)
	h.publish(protocol.EventInboxArchived, workspaceID, "member", userID, map[string]any{
		"item_id":      uuidToString(item.ID),
		"issue_id":     uuidToPtr(item.IssueID),
		"recipient_id": uuidToString(item.RecipientID),
	})

	resp := h.enrichInboxResponse(r.Context(), inboxToResponse(item), item.IssueID)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CountUnreadInbox(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	count, err := h.Queries.CountUnreadInbox(r.Context(), db.CountUnreadInboxParams{
		WorkspaceID:   wsUUID,
		RecipientType: "member",
		RecipientID:   parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count unread inbox")
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{"count": count})
}

func (h *Handler) MarkAllInboxRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	count, err := h.Queries.MarkAllInboxRead(r.Context(), db.MarkAllInboxReadParams{
		WorkspaceID: wsUUID,
		RecipientID: parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark all inbox read")
		return
	}

	slog.Info("inbox: mark all read", append(logger.RequestAttrs(r), "user_id", userID, "count", count)...)
	h.publish(protocol.EventInboxBatchRead, workspaceID, "member", userID, map[string]any{
		"recipient_id": userID,
		"count":        count,
	})

	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

func (h *Handler) ArchiveAllInbox(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	count, err := h.Queries.ArchiveAllInbox(r.Context(), db.ArchiveAllInboxParams{
		WorkspaceID: wsUUID,
		RecipientID: parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive all inbox")
		return
	}

	slog.Info("inbox: archive all", append(logger.RequestAttrs(r), "user_id", userID, "count", count)...)
	h.publish(protocol.EventInboxBatchArchived, workspaceID, "member", userID, map[string]any{
		"recipient_id": userID,
		"count":        count,
	})

	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

func (h *Handler) ArchiveAllReadInbox(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	count, err := h.Queries.ArchiveAllReadInbox(r.Context(), db.ArchiveAllReadInboxParams{
		WorkspaceID: wsUUID,
		RecipientID: parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive all read inbox")
		return
	}

	slog.Info("inbox: archive all read", append(logger.RequestAttrs(r), "user_id", userID, "count", count)...)
	h.publish(protocol.EventInboxBatchArchived, workspaceID, "member", userID, map[string]any{
		"recipient_id": userID,
		"count":        count,
	})

	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

func (h *Handler) ArchiveCompletedInbox(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	count, err := h.Queries.ArchiveCompletedInbox(r.Context(), db.ArchiveCompletedInboxParams{
		WorkspaceID: wsUUID,
		RecipientID: parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive completed inbox")
		return
	}

	slog.Info("inbox: archive completed", append(logger.RequestAttrs(r), "user_id", userID, "count", count)...)
	h.publish(protocol.EventInboxBatchArchived, workspaceID, "member", userID, map[string]any{
		"recipient_id": userID,
		"count":        count,
	})

	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}
