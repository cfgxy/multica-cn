package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Workspace decision inbox (RUYI-494): a read-only aggregation of every
// decision card in a workspace, one row PER CARD. The list's data unit is
// the card, not the issue — an issue with three cards yields three rows so
// an older still-open card is never hidden by a newer one. Issue fields
// (identifier + title) ride along as context for the list's first line.
// Status semantics stay the card's own: open/answered/cancelled; grouping
// into 待决策/已决策/已失效 sections is a client concern.

const (
	decisionInboxDefaultLimit = 200
	decisionInboxMaxLimit     = 500
	// RUYI-864: keyword length cap. The keyword is a user-typed filter box,
	// not a bulk dump request — 200 runes is far beyond a real search and
	// keeps the ILIKE scan bounded.
	decisionInboxMaxKeywordLen = 200
)

type WorkspaceDecisionInboxItem struct {
	IssueDecisionResponse
	WorkspaceID     string `json:"workspace_id"`
	IssueNumber     int32  `json:"issue_number"`
	IssueIdentifier string `json:"issue_identifier"`
	IssueTitle      string `json:"issue_title"`
}

type DecisionInboxCounts struct {
	Open      int `json:"open"`
	Answered  int `json:"answered"`
	Cancelled int `json:"cancelled"`
}

type WorkspaceDecisionInboxResponse struct {
	Items []WorkspaceDecisionInboxItem `json:"items"`
	Counts DecisionInboxCounts          `json:"counts"`
	// RUYI-864: the keyset cursor for the next page — the last row's
	// created_at (RFC3339Nano, sub-second precision) plus its id. Both are
	// null when the last page came back short (the window ended). Clients
	// page with ?after_created_at=&after_id= and pass a null cursor through
	// as "no more pages".
	NextCursor   *string `json:"next_cursor"`
	NextCursorID *string `json:"next_cursor_id"`
}

// issueDecisionFromInboxRow reassembles the plain card row from the inbox
// join row so the shared decisionToResponse mapping stays single-sourced.
// Every business column of the card must land here: decisionToResponse
// serializes whatever this function carries, and an unmapped column silently
// becomes its zero value on the wire (the RUYI-630 QA defect#2 shape — an
// authorization card with decision_kind="" that no client schema accepts).
func issueDecisionFromInboxRow(row db.ListWorkspaceIssueDecisionsRow) db.IssueDecision {
	return db.IssueDecision{
		ID:                    row.ID,
		WorkspaceID:           row.WorkspaceID,
		IssueID:               row.IssueID,
		SourceCommentID:       row.SourceCommentID,
		Question:              row.Question,
		Context:               row.Context,
		Options:               row.Options,
		MultiSelect:           row.MultiSelect,
		RecommendedIndices:    row.RecommendedIndices,
		Status:                row.Status,
		SelectedIndices:       row.SelectedIndices,
		AnsweredByType:        row.AnsweredByType,
		AnsweredByID:          row.AnsweredByID,
		AnsweredAt:            row.AnsweredAt,
		AnswerCommentID:       row.AnswerCommentID,
		CreatedByType:         row.CreatedByType,
		CreatedByID:           row.CreatedByID,
		CreatedAt:             row.CreatedAt,
		UpdatedAt:             row.UpdatedAt,
		ClientRequestID:       row.ClientRequestID,
		DecisionKind:          row.DecisionKind,
		VisibleTier:           row.VisibleTier,
		OperatorTier:          row.OperatorTier,
		NamedApproverIds:      row.NamedApproverIds,
		ApproveLabel:          row.ApproveLabel,
		DenyLabel:             row.DenyLabel,
		ExpiresAt:             row.ExpiresAt,
		AnswerSource:          row.AnswerSource,
		AuthState:             row.AuthState,
		PendingRequestGroupID: row.PendingRequestGroupID,
		PendingActionType:     row.PendingActionType,
		PendingActionParams:   row.PendingActionParams,
		ExecutionResult:       row.ExecutionResult,
		ExecutionError:        row.ExecutionError,
		ExecutedAt:            row.ExecutedAt,
	}
}

// parseDecisionInboxQueryParams reads the filter window of the inbox list:
// `status` (one card status), `limit`, the RUYI-864 keyword `q` (LIKE
// wildcards escaped server-side — the client sends the raw keyword and only
// this function knows how to quote it), and the keyset cursor pair
// `after_created_at` + `after_id`. The cursor pair must arrive together,
// exactly mirroring the audit-events cursor discipline: a timestamp without
// its tiebreaker id silently skips or repeats rows.
func parseDecisionInboxQueryParams(w http.ResponseWriter, r *http.Request) (
	workspaceID pgtype.UUID, status pgtype.Text, limit int32, q pgtype.Text,
	afterCreatedAt pgtype.Timestamptz, afterID pgtype.UUID, ok bool,
) {
	workspaceID, ok = parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return workspaceID, status, 0, q, afterCreatedAt, afterID, false
	}
	var raw string
	switch raw = r.URL.Query().Get("status"); raw {
	case "":
		// no status filter
	case "open", "answered", "cancelled":
		status = pgtype.Text{String: raw, Valid: true}
	default:
		writeError(w, http.StatusBadRequest, "status must be one of open, answered, cancelled")
		return workspaceID, status, 0, q, afterCreatedAt, afterID, false
	}
	limit = int32(decisionInboxDefaultLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > decisionInboxMaxLimit {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("limit must be between 1 and %d", decisionInboxMaxLimit))
			return workspaceID, status, 0, q, afterCreatedAt, afterID, false
		}
		limit = int32(n)
	}
	// Keyword: trim, cap, then escape LIKE wildcards so the literal match
	// stays literal even when a card's question contains % or _.
	if raw := strings.TrimSpace(r.URL.Query().Get("q")); raw != "" {
		if len([]rune(raw)) > decisionInboxMaxKeywordLen {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("q must be at most %d characters", decisionInboxMaxKeywordLen))
			return workspaceID, status, limit, q, afterCreatedAt, afterID, false
		}
		q = pgtype.Text{String: escapeLikePattern(raw), Valid: true}
	}
	// Cursor pair (RUYI-864 keyset paging): `after_created_at` is the
	// RFC3339Nano created_at of the last row of the previous page, `after_id`
	// its id — both together, or neither.
	if raw := r.URL.Query().Get("after_created_at"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid after_created_at parameter; expected RFC3339 timestamp")
			return workspaceID, status, limit, q, afterCreatedAt, afterID, false
		}
		afterCreatedAt = pgtype.Timestamptz{Time: t, Valid: true}
	}
	if raw := r.URL.Query().Get("after_id"); raw != "" {
		id, err := util.ParseUUID(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid after_id parameter; expected uuid")
			return workspaceID, status, limit, q, afterCreatedAt, afterID, false
		}
		afterID = id
	}
	if afterCreatedAt.Valid != afterID.Valid {
		writeError(w, http.StatusBadRequest, "after_created_at and after_id must arrive together")
		return workspaceID, status, limit, q, afterCreatedAt, afterID, false
	}
	return workspaceID, status, limit, q, afterCreatedAt, afterID, true
}

// ListWorkspaceDecisionInbox handles GET /api/workspaces/{id}/decision-inbox.
// Membership is enforced by the router middleware (and re-checked here so a
// direct call fails closed). The filter window is `status` (one card status,
// a 1:1 real state — the request lifecycle statuses are never folded onto a
// card) and the RUYI-864 keyword `q` (question / structured context / issue
// identifier or title, LIKE'd server-side); `limit` bounds the page and the
// `after_created_at` + `after_id` pair is the keyset cursor for the next one.
// Counts ride along scoped to status + q (never the page window), so the
// section headers and empty states can't disagree with the rows on screen.
func (h *Handler) ListWorkspaceDecisionInbox(w http.ResponseWriter, r *http.Request) {
	workspaceID, status, limit, q, afterCreatedAt, afterID, ok := parseDecisionInboxQueryParams(w, r)
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, uuidToString(workspaceID)); !ok {
		return
	}

	rows, err := h.Queries.ListWorkspaceIssueDecisions(r.Context(), db.ListWorkspaceIssueDecisionsParams{
		WorkspaceID: workspaceID,
		Limit:       limit,
		Status:      status,
		Q:           q,
		AfterCreatedAt: afterCreatedAt,
		AfterID:     afterID,
	})
	if err != nil {
		slog.Warn("workspace decision inbox list failed", append(logger.RequestAttrs(r),
			"error", err, "workspace_id", uuidToString(workspaceID))...)
		writeError(w, http.StatusInternalServerError, "failed to list decision cards")
		return
	}
	counts, err := h.Queries.CountWorkspaceIssueDecisionsByStatus(r.Context(), db.CountWorkspaceIssueDecisionsByStatusParams{
		WorkspaceID: workspaceID,
		Status:      status,
		Q:           q,
	})
	if err != nil {
		slog.Warn("workspace decision inbox counts failed", append(logger.RequestAttrs(r),
			"error", err, "workspace_id", uuidToString(workspaceID))...)
		writeError(w, http.StatusInternalServerError, "failed to count decision cards")
		return
	}

	items := make([]WorkspaceDecisionInboxItem, 0, len(rows))
	for _, row := range rows {
		card := issueDecisionFromInboxRow(row)
		// RUYI-630: authorization cards never aggregate into the decision
		// inbox — their authorization summary lives in the decision-requests
		// surface, and the inbox entry would only duplicate the entry point.
		if card.DecisionKind == "authorization" {
			continue
		}
		if !h.decisionCardVisibleToCaller(r, uuidToString(workspaceID), card) {
			continue
		}
		items = append(items, WorkspaceDecisionInboxItem{
			IssueDecisionResponse: decisionToResponse(card),
			WorkspaceID:           uuidToString(row.WorkspaceID),
			IssueNumber:           row.IssueNumber,
			IssueIdentifier:       row.IssueIdentifier,
			IssueTitle:            row.IssueTitle,
		})
	}
	resp := WorkspaceDecisionInboxResponse{
		Items:  items,
		Counts: DecisionInboxCounts{Open: int(counts.OpenCount), Answered: int(counts.AnsweredCount), Cancelled: int(counts.CancelledCount)},
	}
	// RUYI-864: the response's own last row is the safe cursor for the next
	// page — the created_at must keep sub-second precision, exactly as the
	// audit-events cursor does (a truncated second would drop every row
	// sharing it).
	// The cursor rides the last row of the DB page (not of the filtered
	// items — a page whose tail rows all get dropped by the visibility or
	// authorization filters must still advance past them), and only when
	// the page came back full, so a short page means the window ended.
	if len(rows) == int(limit) {
		lastRow := rows[len(rows)-1]
		ts := lastRow.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
		id := uuidToString(lastRow.ID)
		resp.NextCursor = &ts
		resp.NextCursorID = &id
	}
	// Log ids/shape only — never question text or actor identities (they are
	// already on the card records; the log line must not duplicate readable
	// decision content).
	slog.Info("workspace decision inbox listed", append(logger.RequestAttrs(r),
		"workspace_id", uuidToString(workspaceID),
		"status_filter", status.String,
		"keyword", q.Valid,
		"count", len(items))...)
	writeJSON(w, http.StatusOK, resp)
}
