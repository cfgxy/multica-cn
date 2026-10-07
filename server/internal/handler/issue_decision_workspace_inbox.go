package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/logger"
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
	Items  []WorkspaceDecisionInboxItem `json:"items"`
	Counts DecisionInboxCounts          `json:"counts"`
}

// issueDecisionFromInboxRow reassembles the plain card row from the inbox
// join row so the shared decisionToResponse mapping stays single-sourced.
func issueDecisionFromInboxRow(row db.ListWorkspaceIssueDecisionsRow) db.IssueDecision {
	return db.IssueDecision{
		ID:                 row.ID,
		WorkspaceID:        row.WorkspaceID,
		IssueID:            row.IssueID,
		SourceCommentID:    row.SourceCommentID,
		Question:           row.Question,
		Options:            row.Options,
		MultiSelect:        row.MultiSelect,
		RecommendedIndices: row.RecommendedIndices,
		Status:             row.Status,
		SelectedIndices:    row.SelectedIndices,
		AnsweredByType:     row.AnsweredByType,
		AnsweredByID:       row.AnsweredByID,
		AnsweredAt:         row.AnsweredAt,
		AnswerCommentID:    row.AnswerCommentID,
		CreatedByType:      row.CreatedByType,
		CreatedByID:        row.CreatedByID,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

// ListWorkspaceDecisionInbox handles GET /api/workspaces/{id}/decision-inbox.
// Membership is enforced by the router middleware (and re-checked here so a
// direct call fails closed); `status` optionally filters to one status and
// `limit` bounds the window (the client groups sections and renders counts
// from the same response).
func (h *Handler) ListWorkspaceDecisionInbox(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, uuidToString(workspaceID)); !ok {
		return
	}

	var status pgtype.Text
	if raw := r.URL.Query().Get("status"); raw != "" {
		switch raw {
		case "open", "answered", "cancelled":
			status = pgtype.Text{String: raw, Valid: true}
		default:
			writeError(w, http.StatusBadRequest, "status must be one of open, answered, cancelled")
			return
		}
	}
	limit := decisionInboxDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > decisionInboxMaxLimit {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("limit must be between 1 and %d", decisionInboxMaxLimit))
			return
		}
		limit = n
	}

	rows, err := h.Queries.ListWorkspaceIssueDecisions(r.Context(), db.ListWorkspaceIssueDecisionsParams{
		WorkspaceID: workspaceID,
		Limit:       int32(limit),
		Status:      status,
	})
	if err != nil {
		slog.Warn("workspace decision inbox list failed", append(logger.RequestAttrs(r),
			"error", err, "workspace_id", uuidToString(workspaceID))...)
		writeError(w, http.StatusInternalServerError, "failed to list decision cards")
		return
	}
	counts, err := h.Queries.CountWorkspaceIssueDecisionsByStatus(r.Context(), workspaceID)
	if err != nil {
		slog.Warn("workspace decision inbox counts failed", append(logger.RequestAttrs(r),
			"error", err, "workspace_id", uuidToString(workspaceID))...)
		writeError(w, http.StatusInternalServerError, "failed to count decision cards")
		return
	}

	items := make([]WorkspaceDecisionInboxItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, WorkspaceDecisionInboxItem{
				IssueDecisionResponse: decisionToResponse(issueDecisionFromInboxRow(row)),
				WorkspaceID:           uuidToString(row.WorkspaceID),
				IssueNumber:           row.IssueNumber,
				IssueIdentifier:       row.IssueIdentifier,
				IssueTitle:            row.IssueTitle,
			})
	}
	resp := WorkspaceDecisionInboxResponse{
		Items: items,
		Counts: DecisionInboxCounts{
			Open:      int(counts.OpenCount),
			Answered:  int(counts.AnsweredCount),
			Cancelled: int(counts.CancelledCount),
		},
	}
	// Log ids/shape only — never question text or actor identities (they are
	// already on the card records; the log line must not duplicate readable
	// decision content).
	slog.Info("workspace decision inbox listed", append(logger.RequestAttrs(r),
		"workspace_id", uuidToString(workspaceID),
		"status_filter", status.String,
		"count", len(items))...)
	writeJSON(w, http.StatusOK, resp)
}
