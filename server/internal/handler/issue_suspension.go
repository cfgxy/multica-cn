package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Issue suspension (RUYI-690): "waiting" as a dimension orthogonal to the
// lifecycle status. Suspending never touches issue.status; it inserts one
// issue_suspension row (the audit record) and mirrors activeness onto
// issue.suspended_at (the read-side snapshot the board/list/detail badge
// from). The two writes share one transaction; the system comment, activity
// row, audit twin and WS broadcast ride best-effort after the commit, the
// same disposition every other platform notice takes.
//
// Machine auto-resume is deliberately narrow: only `issue_done` (waiting
// target entered the done category) and `decision_answered` (card answered —
// a cancelled card withdraws the question without granting it) ever fire a
// system resume. `external` / `manual` waits never wake on their own.

const (
	// maxSuspensionTextLen bounds the free-text fields (reason / summaries /
	// notes). Suspension text is display prose, not a document.
	maxSuspensionTextLen = 2000

	// resumePredicateKind* are the closed machine-predicate set. Anything
	// else in resume_predicate is an application-layer validation bug.
	resumePredicateKindIssueDone        = "issue_done"
	resumePredicateKindDecisionAnswered = "decision_answered"
	resumePredicateKindManual           = "manual"

	// waitingOn* are the waiting_on_type values. issue/decision carry the
	// matching id and a machine predicate; external/manual never auto-resume.
	waitingOnIssue    = "issue"
	waitingOnDecision = "decision"
	waitingOnExternal = "external"
	waitingOnManual   = "manual"

	suspensionStatusActive    = "active"
	suspensionStatusResumed   = "resumed"
	suspensionStatusCancelled = "cancelled"
)

// resumePredicate is the JSONB shape stored in
// issue_suspension.resume_predicate / resumed_by_predicate. Keep the field
// set closed: a new machine predicate is a behavioral change (it wakes work),
// not a storage extension.
type resumePredicate struct {
	Kind       string `json:"kind"`
	IssueID    string `json:"issue_id,omitempty"`
	DecisionID string `json:"decision_id,omitempty"`
}

func parseResumePredicate(raw []byte) (resumePredicate, bool) {
	var p resumePredicate
	if len(raw) == 0 {
		return p, false
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, false
	}
	return p, true
}

func isValidSuspensionStatus(s string) bool {
	return s == suspensionStatusActive || s == suspensionStatusResumed || s == suspensionStatusCancelled
}

func isValidWaitingOnType(s string) bool {
	return s == waitingOnIssue || s == waitingOnDecision || s == waitingOnExternal || s == waitingOnManual
}

// sanitizeSuspensionText null-byte-strips, trims and caps a free-text field.
func sanitizeSuspensionText(s string) string {
	s = strings.TrimSpace(sanitizeNullBytes(s))
	if len(s) > maxSuspensionTextLen {
		runes := []rune(s)
		if len(runes) > maxSuspensionTextLen {
			s = string(runes[:maxSuspensionTextLen])
		}
	}
	return s
}

// IssueSuspensionResponse is the API shape of one suspension row.
type IssueSuspensionResponse struct {
	ID                  string  `json:"id"`
	WorkspaceID         string  `json:"workspace_id"`
	IssueID             string  `json:"issue_id"`
	Status              string  `json:"status"`
	Reason              string  `json:"reason"`
	WaitingOnType       string  `json:"waiting_on_type"`
	WaitingOnIssueID    *string `json:"waiting_on_issue_id"`
	WaitingOnDecisionID *string `json:"waiting_on_decision_id"`
	WaitingOnSummary    string  `json:"waiting_on_summary"`
	ResumeCondition     string  `json:"resume_condition"`
	ResumePredicate     *string `json:"resume_predicate"`
	SuspendedByType     string  `json:"suspended_by_type"`
	SuspendedByID       string  `json:"suspended_by_id"`
	SuspendedAt         string  `json:"suspended_at"`
	ResumedByType       *string `json:"resumed_by_type"`
	ResumedByID         *string `json:"resumed_by_id"`
	ResumedAt           *string `json:"resumed_at"`
	ResumeNote          string  `json:"resume_note"`
	CancelledByType     *string `json:"cancelled_by_type"`
	CancelledByID       *string `json:"cancelled_by_id"`
	CancelledAt         *string `json:"cancelled_at"`
	CancelNote          string  `json:"cancel_note"`
	ResumedByPredicate  *string `json:"resumed_by_predicate"`
}

func issueSuspensionToResponse(s db.IssueSuspension) IssueSuspensionResponse {
	return IssueSuspensionResponse{
		ID:                  uuidToString(s.ID),
		WorkspaceID:         uuidToString(s.WorkspaceID),
		IssueID:             uuidToString(s.IssueID),
		Status:              s.Status,
		Reason:              s.Reason,
		WaitingOnType:       s.WaitingOnType,
		WaitingOnIssueID:    uuidToPtr(s.WaitingOnIssueID),
		WaitingOnDecisionID: uuidToPtr(s.WaitingOnDecisionID),
		WaitingOnSummary:    s.WaitingOnSummary,
		ResumeCondition:     s.ResumeCondition,
		ResumePredicate:     jsonBytesToPtr(s.ResumePredicate),
		SuspendedByType:     s.SuspendedByType,
		SuspendedByID:       uuidToString(s.SuspendedByID),
		SuspendedAt:         timestampToString(s.SuspendedAt),
		ResumedByType:       textToPtr(s.ResumedByType),
		ResumedByID:         uuidToPtr(s.ResumedByID),
		ResumedAt:           timestampToPtr(s.ResumedAt),
		ResumeNote:          s.ResumeNote,
		CancelledByType:     textToPtr(s.CancelledByType),
		CancelledByID:       uuidToPtr(s.CancelledByID),
		CancelledAt:         timestampToPtr(s.CancelledAt),
		CancelNote:          s.CancelNote,
		ResumedByPredicate:  jsonBytesToPtr(s.ResumedByPredicate),
	}
}

// jsonBytesToPtr renders a jsonb column as its raw JSON text. NULL → nil so
// the field is absent rather than "null"-as-string.
func jsonBytesToPtr(raw []byte) *string {
	if len(raw) == 0 {
		return nil
	}
	s := string(raw)
	return &s
}

// IssueSuspensionTargetResponse describes the waiting target the way the
// aggregate view renders it: identifier + current state, no extra fetch.
type IssueSuspensionTargetResponse struct {
	// For waiting_on_type=issue: the target issue's coordinates.
	IssueID    string  `json:"issue_id,omitempty"`
	Identifier string  `json:"identifier,omitempty"`
	Title      string  `json:"title,omitempty"`
	Status     string  `json:"status,omitempty"`
	// For waiting_on_type=decision: the card's coordinates.
	DecisionID string  `json:"decision_id,omitempty"`
	Question   *string `json:"question,omitempty"`
	CardStatus *string `json:"card_status,omitempty"`
}

// WorkspaceIssueSuspensionResponse is one row of the workspace aggregate view
// (the "which issues are waiting, on what, since when" answer).
type WorkspaceIssueSuspensionResponse struct {
	IssueSuspensionResponse
	IssueIdentifier  string                         `json:"issue_identifier"`
	IssueTitle       string                         `json:"issue_title"`
	IssueStatus      string                         `json:"issue_status"`
	IssueSuspendedAt string                         `json:"issue_suspended_at"`
	WaitingOnTarget  *IssueSuspensionTargetResponse `json:"waiting_on_target"`
}

func workspaceIssueSuspensionToResponse(row db.ListWorkspaceIssueSuspensionsRow, prefix string) WorkspaceIssueSuspensionResponse {
	resp := WorkspaceIssueSuspensionResponse{
		IssueSuspensionResponse: IssueSuspensionResponse{
			ID:                  uuidToString(row.ID),
			WorkspaceID:         uuidToString(row.WorkspaceID),
			IssueID:             uuidToString(row.IssueID),
			Status:              row.Status,
			Reason:              row.Reason,
			WaitingOnType:       row.WaitingOnType,
			WaitingOnIssueID:    uuidToPtr(row.WaitingOnIssueID),
			WaitingOnDecisionID: uuidToPtr(row.WaitingOnDecisionID),
			WaitingOnSummary:    row.WaitingOnSummary,
			ResumeCondition:     row.ResumeCondition,
			ResumePredicate:     jsonBytesToPtr(row.ResumePredicate),
			SuspendedByType:     row.SuspendedByType,
			SuspendedByID:       uuidToString(row.SuspendedByID),
			SuspendedAt:         timestampToString(row.SuspendedAt),
			ResumedByType:       textToPtr(row.ResumedByType),
			ResumedByID:         uuidToPtr(row.ResumedByID),
			ResumedAt:           timestampToPtr(row.ResumedAt),
			ResumeNote:          row.ResumeNote,
			CancelledByType:     textToPtr(row.CancelledByType),
			CancelledByID:       uuidToPtr(row.CancelledByID),
			CancelledAt:         timestampToPtr(row.CancelledAt),
			CancelNote:          row.CancelNote,
			ResumedByPredicate:  jsonBytesToPtr(row.ResumedByPredicate),
		},
		IssueIdentifier:  service.IssueIdentifier(prefix, row.IssueNumber),
		IssueTitle:       row.IssueTitle,
		IssueStatus:      row.IssueStatus,
		IssueSuspendedAt: timestampToString(row.IssueSuspendedAt),
	}
	switch row.WaitingOnType {
	case waitingOnIssue:
		if row.WaitingOnIssueID.Valid && row.WaitingIssueNumber.Valid {
			resp.WaitingOnTarget = &IssueSuspensionTargetResponse{
				IssueID:    uuidToString(row.WaitingOnIssueID),
				Identifier: service.IssueIdentifier(prefix, row.WaitingIssueNumber.Int32),
				Title:      row.WaitingIssueTitle.String,
				Status:     row.WaitingIssueStatus.String,
			}
		}
	case waitingOnDecision:
		if row.WaitingOnDecisionID.Valid {
			resp.WaitingOnTarget = &IssueSuspensionTargetResponse{
				DecisionID: uuidToString(row.WaitingOnDecisionID),
				Question:   textToPtr(row.WaitingDecisionQuestion),
				CardStatus: textToPtr(row.WaitingDecisionStatus),
			}
		}
	}
	return resp
}

// CreateIssueSuspensionRequest is the suspend payload. waiting_on_type
// selects which optional fields are meaningful; the handler derives the
// machine predicate from the type, so callers cannot request an auto-resume
// behavior the platform does not implement.
type CreateIssueSuspensionRequest struct {
	Reason              string  `json:"reason"`
	WaitingOnType       string  `json:"waiting_on_type"`
	WaitingOnIssueID    *string `json:"waiting_on_issue_id"`
	WaitingOnDecisionID *string `json:"waiting_on_decision_id"`
	WaitingOnSummary    *string `json:"waiting_on_summary"`
	ResumeCondition     *string `json:"resume_condition"`
}

// ResumeIssueSuspensionRequest carries the optional release note.
type ResumeIssueSuspensionRequest struct {
	Note string `json:"note"`
}

// CancelIssueSuspensionRequest carries the optional withdrawal note.
type CancelIssueSuspensionRequest struct {
	Note string `json:"note"`
}

// IssueSuspensionMutationResponse returns the mutated suspension alongside
// the refreshed issue snapshot (suspended_at flipped), so the caller's cache
// updates from one round-trip.
type IssueSuspensionMutationResponse struct {
	Suspension IssueSuspensionResponse `json:"suspension"`
	Issue      IssueResponse           `json:"issue"`
}

// SuspendIssue handles POST /api/issues/{id}/suspensions. Members and agents
// may suspend — an agent parking its issue on a decision card is the primary
// unattended use case, mirroring who may raise decision cards.
func (h *Handler) SuspendIssue(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req CreateIssueSuspensionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))

	params, err := h.validateSuspensionInput(r, issue, req, actorType, actorID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	created, err := h.insertSuspension(r.Context(), issue, params)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uidx_issue_suspension_active_per_issue" {
			writeError(w, http.StatusConflict, "issue already has an active suspension")
			return
		}
		slog.Warn("suspend issue failed", append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(issue.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to suspend issue")
		return
	}

	refreshed, ok := h.loadIssueForUser(w, r, uuidToString(issue.ID))
	if !ok {
		refreshed = issue
	}
	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	resp := IssueSuspensionMutationResponse{
		Suspension: issueSuspensionToResponse(created),
		Issue:      issueToResponse(refreshed, prefix),
	}
	h.fillStatusCategory(r.Context(), issue.WorkspaceID, &resp.Issue)

	commentID := h.noteSuspensionSystemComment(r.Context(), issue, h.suspensionSuspendContent(r.Context(), created))
	h.recordSuspensionActivity(r.Context(), created, service.AuditIssueSuspended, actorType, actorID, map[string]any{
		"suspension_id":      uuidToString(created.ID),
		"reason":             created.Reason,
		"waiting_on_type":    created.WaitingOnType,
		"waiting_on_summary": created.WaitingOnSummary,
		"resume_condition":   created.ResumeCondition,
	})
	h.publishSuspensionUpdated(r.Context(), issue.WorkspaceID, actorType, actorID, created, refreshed, commentID)

	writeJSON(w, http.StatusCreated, resp)
}

// ListIssueSuspensions handles GET /api/issues/{id}/suspensions — the issue's
// full suspension history, newest first.
func (h *Handler) ListIssueSuspensions(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	rows, err := h.Queries.ListIssueSuspensionsForIssue(r.Context(), db.ListIssueSuspensionsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		slog.Warn("list issue suspensions failed", append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(issue.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to list suspensions")
		return
	}
	out := make([]IssueSuspensionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, issueSuspensionToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"suspensions": out})
}

// ListWorkspaceIssueSuspensions handles GET /api/workspaces/{id}/suspensions
// — the workspace aggregate view ("which issues are waiting, on what, since
// when", the suspension counterpart of the decision inbox). Defaults to
// active rows; ?status=all lists the workspace's full suspension history.
func (h *Handler) ListWorkspaceIssueSuspensions(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, uuidToString(workspaceID)); !ok {
		return
	}

	statusParam := strings.TrimSpace(r.URL.Query().Get("status"))
	var statusFilter pgtype.Text
	switch statusParam {
	case "", "active":
		statusFilter = pgtype.Text{String: suspensionStatusActive, Valid: true}
	case "all":
		statusFilter = pgtype.Text{Valid: false}
	default:
		if !isValidSuspensionStatus(statusParam) {
			writeError(w, http.StatusBadRequest, "invalid status filter")
			return
		}
		statusFilter = pgtype.Text{String: statusParam, Valid: true}
	}

	limit := 200
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil || v < 1 || v > 1000 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = v
	}

	rows, err := h.Queries.ListWorkspaceIssueSuspensions(r.Context(), db.ListWorkspaceIssueSuspensionsParams{
		WorkspaceID: workspaceID,
		Status:      statusFilter,
		Limit:       int32(limit),
	})
	if err != nil {
		slog.Warn("list workspace suspensions failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", uuidToString(workspaceID))...)
		writeError(w, http.StatusInternalServerError, "failed to list suspensions")
		return
	}
	count, err := h.Queries.CountWorkspaceIssueSuspensions(r.Context(), workspaceID)
	if err != nil {
		slog.Warn("count workspace suspensions failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", uuidToString(workspaceID))...)
		count = 0
	}
	prefix := h.getIssuePrefix(r.Context(), workspaceID)
	out := make([]WorkspaceIssueSuspensionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, workspaceIssueSuspensionToResponse(row, prefix))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"suspensions": out,
		"total":       count,
	})
}

// ResumeIssueSuspension handles POST /api/issues/{id}/suspensions/{suspensionId}/resume.
// The CAS winner flips the row to resumed and clears the snapshot in one
// transaction; per the Owner's decision-3 ruling the release returns the
// issue to normal scheduling, so an agent/squad assignee is woken with the
// resume system comment as the trigger (same surface as a child-done wake).
func (h *Handler) ResumeIssueSuspension(w http.ResponseWriter, r *http.Request) {
	issue, suspension, ok := h.loadSuspensionForMutation(w, r)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req ResumeIssueSuspensionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))

	resumed, refreshed, err := h.releaseSuspension(r.Context(), issue, suspension, releaseSuspensionParams{
		arm:       releaseArmResume,
		actorType: actorType,
		actorID:   actorID,
		note:      sanitizeSuspensionText(req.Note),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "suspension is no longer active")
			return
		}
		slog.Warn("resume issue suspension failed", append(logger.RequestAttrs(r), "error", err, "suspension_id", uuidToString(suspension.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to resume suspension")
		return
	}

	h.respondSuspensionRelease(w, r, issue, refreshed, resumed, actorType, actorID)
}

// CancelIssueSuspension handles POST /api/issues/{id}/suspensions/{suspensionId}/cancel —
// withdraw a suspension as mistaken/obsolete. Unlike resume this does NOT
// wake the assignee: nothing was granted, so nothing resumes. Normal comment
// triggers are admitted again either way (the snapshot is cleared).
func (h *Handler) CancelIssueSuspension(w http.ResponseWriter, r *http.Request) {
	issue, suspension, ok := h.loadSuspensionForMutation(w, r)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req CancelIssueSuspensionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))

	cancelled, refreshed, err := h.releaseSuspension(r.Context(), issue, suspension, releaseSuspensionParams{
		arm:       releaseArmCancel,
		actorType: actorType,
		actorID:   actorID,
		note:      sanitizeSuspensionText(req.Note),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "suspension is no longer active")
			return
		}
		slog.Warn("cancel issue suspension failed", append(logger.RequestAttrs(r), "error", err, "suspension_id", uuidToString(suspension.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to cancel suspension")
		return
	}

	h.respondSuspensionRelease(w, r, issue, refreshed, cancelled, actorType, actorID)
}

// ---- shared internals ----

type releaseArm string

const (
	releaseArmResume releaseArm = "resume"
	releaseArmCancel releaseArm = "cancel"
)

type releaseSuspensionParams struct {
	arm       releaseArm
	actorType string
	actorID   string
	note      string
	// predicate is set only by the system auto-resume path; it names the
	// machine condition that fired and is denormalized onto the row.
	predicate *resumePredicate
}

// actorUUIDOrNull parses a member/agent actor id; the system actor has no
// id, and the audit columns stay NULL for it.
func actorUUIDOrNull(actorType, actorID string) pgtype.UUID {
	if actorType == "" || actorID == "" {
		return pgtype.UUID{}
	}
	id, err := util.ParseUUID(actorID)
	if err != nil {
		return pgtype.UUID{}
	}
	return id
}

// loadSuspensionForMutation resolves {id} + {suspensionId} from the URL and
// verifies the suspension belongs to that issue (and its workspace).
func (h *Handler) loadSuspensionForMutation(w http.ResponseWriter, r *http.Request) (db.Issue, db.IssueSuspension, bool) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return db.Issue{}, db.IssueSuspension{}, false
	}
	suspensionID, valid := parseUUIDOrBadRequest(w, chi.URLParam(r, "suspensionId"), "suspensionId")
	if !valid {
		return db.Issue{}, db.IssueSuspension{}, false
	}
	suspension, err := h.Queries.GetIssueSuspension(r.Context(), db.GetIssueSuspensionParams{
		ID:          suspensionID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "suspension not found")
			return db.Issue{}, db.IssueSuspension{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load suspension")
		return db.Issue{}, db.IssueSuspension{}, false
	}
	if uuidToString(suspension.IssueID) != uuidToString(issue.ID) {
		writeError(w, http.StatusNotFound, "suspension not found")
		return db.Issue{}, db.IssueSuspension{}, false
	}
	return issue, suspension, true
}

// insertSuspension commits the suspension row + snapshot in one transaction.
func (h *Handler) insertSuspension(ctx context.Context, issue db.Issue, params db.CreateIssueSuspensionParams) (db.IssueSuspension, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.IssueSuspension{}, err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	created, err := qtx.CreateIssueSuspension(ctx, params)
	if err != nil {
		return db.IssueSuspension{}, err
	}
	if _, err := qtx.SetIssueSuspendedAt(ctx, issue.ID); err != nil {
		return db.IssueSuspension{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.IssueSuspension{}, err
	}
	return created, nil
}

// releaseSuspension commits the CAS arm (resume or cancel) + snapshot clear
// in one transaction and returns (row, refreshedIssue). A lost CAS surfaces
// as pgx.ErrNoRows: a concurrent resume/cancel beat this caller.
func (h *Handler) releaseSuspension(ctx context.Context, issue db.Issue, suspension db.IssueSuspension, p releaseSuspensionParams) (db.IssueSuspension, db.Issue, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.IssueSuspension{}, db.Issue{}, err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	var row db.IssueSuspension
	switch p.arm {
	case releaseArmResume:
		var predicateRaw []byte
		if p.predicate != nil {
			predicateRaw, err = json.Marshal(p.predicate)
			if err != nil {
				return db.IssueSuspension{}, db.Issue{}, err
			}
		}
		row, err = qtx.ResumeIssueSuspension(ctx, db.ResumeIssueSuspensionParams{
			ID:                 suspension.ID,
			WorkspaceID:        issue.WorkspaceID,
			ResumedByType:      pgtype.Text{String: p.actorType, Valid: p.actorType != ""},
			ResumedByID:        actorUUIDOrNull(p.actorType, p.actorID),
			ResumeNote:         p.note,
			ResumedByPredicate: predicateRaw,
		})
	case releaseArmCancel:
		row, err = qtx.CancelIssueSuspension(ctx, db.CancelIssueSuspensionParams{
			ID:              suspension.ID,
			WorkspaceID:     issue.WorkspaceID,
			CancelledByType: pgtype.Text{String: p.actorType, Valid: p.actorType != ""},
			CancelledByID:   actorUUIDOrNull(p.actorType, p.actorID),
			CancelNote:      p.note,
		})
	}
	if err != nil {
		return db.IssueSuspension{}, db.Issue{}, err
	}
	refreshed, err := qtx.ClearIssueSuspendedAt(ctx, issue.ID)
	if err != nil {
		return db.IssueSuspension{}, db.Issue{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.IssueSuspension{}, db.Issue{}, err
	}
	return row, refreshed, nil
}

// respondSuspensionRelease writes the HTTP response and the shared
// best-effort trails for a committed release (system comment, activity+audit
// twin, WS event), then wakes the assignee on the resume arm per the
// decision-3 ruling.
func (h *Handler) respondSuspensionRelease(w http.ResponseWriter, r *http.Request, issue, refreshed db.Issue, row db.IssueSuspension, actorType, actorID string) {
	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	resp := IssueSuspensionMutationResponse{
		Suspension: issueSuspensionToResponse(row),
		Issue:      issueToResponse(refreshed, prefix),
	}
	h.fillStatusCategory(r.Context(), issue.WorkspaceID, &resp.Issue)

	var eventType string
	var content string
	details := map[string]any{
		"suspension_id": uuidToString(row.ID),
	}
	switch row.Status {
	case suspensionStatusResumed:
		eventType = service.AuditIssueSuspensionResumed
		content = suspensionResumeContent(row)
		details["resume_note"] = row.ResumeNote
		if p, ok := parseResumePredicate(row.ResumedByPredicate); ok {
			details["resumed_by_predicate"] = p
		}
	case suspensionStatusCancelled:
		eventType = service.AuditIssueSuspensionCancelled
		content = suspensionCancelContent(row)
		details["cancel_note"] = row.CancelNote
	}

	commentID := h.noteSuspensionSystemComment(r.Context(), issue, content)
	h.recordSuspensionActivity(r.Context(), row, eventType, actorType, actorID, details)
	h.publishSuspensionUpdated(r.Context(), issue.WorkspaceID, actorType, actorID, row, refreshed, commentID)

	if row.Status == suspensionStatusResumed {
		h.wakeAssigneeAfterRelease(r.Context(), issue, commentID)
	}
	writeJSON(w, http.StatusOK, resp)
}

// wakeAssigneeAfterRelease implements the decision-3 ruling: releasing a
// suspension returns the issue to normal scheduling, so an agent assignee is
// woken directly and a squad assignee wakes its leader. Mirrors the child-done
// wake (issue_child_done.go): readiness-filtered, pending-deduped, and
// best-effort — a failed wake leaves the issue unsuspended with normal
// comment triggers admitted, so the next comment reaches the agent anyway.
func (h *Handler) wakeAssigneeAfterRelease(ctx context.Context, issue db.Issue, triggerCommentID pgtype.UUID) {
	if !issue.AssigneeType.Valid || !issue.AssigneeID.Valid {
		return
	}
	switch issue.AssigneeType.String {
	case "agent":
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          issue.AssigneeID,
			WorkspaceID: issue.WorkspaceID,
		})
		if err != nil || !agent.RuntimeID.Valid || agent.ArchivedAt.Valid {
			return
		}
		hasPending, err := h.Queries.HasPendingTaskForIssueAndAgent(ctx, db.HasPendingTaskForIssueAndAgentParams{
			IssueID: issue.ID,
			AgentID: issue.AssigneeID,
			HeadSha: h.TaskService.ResolveIssueReviewSHAParam(ctx, issue.ID),
		})
		if err != nil || hasPending {
			return
		}
		if _, err := h.TaskService.EnqueueTaskForMention(ctx, issue, issue.AssigneeID, triggerCommentID); err != nil {
			slog.Warn("suspension resume: enqueue agent wake failed",
				"error", err, "issue_id", uuidToString(issue.ID), "agent_id", uuidToString(issue.AssigneeID))
		}
	case "squad":
		squad, err := h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
			ID:          issue.AssigneeID,
			WorkspaceID: issue.WorkspaceID,
		})
		if err != nil {
			return
		}
		leader, err := h.Queries.GetAgent(ctx, squad.LeaderID)
		if err != nil || !leader.RuntimeID.Valid || leader.ArchivedAt.Valid {
			return
		}
		hasPending, err := h.Queries.HasPendingTaskForIssueAndAgent(ctx, db.HasPendingTaskForIssueAndAgentParams{
			IssueID: issue.ID,
			AgentID: squad.LeaderID,
			HeadSha: h.TaskService.ResolveIssueReviewSHAParam(ctx, issue.ID),
		})
		if err != nil || hasPending {
			return
		}
		if _, err := h.TaskService.EnqueueTaskForSquadLeader(ctx, issue, squad.LeaderID, squad.ID, triggerCommentID); err != nil {
			slog.Warn("suspension resume: enqueue squad leader wake failed",
				"error", err, "issue_id", uuidToString(issue.ID), "leader_id", uuidToString(squad.LeaderID))
		}
	}
}

// ---- auto-resume hooks ----

// ResumeSuspensionsWaitingOnIssue is the issue_done auto-resume hook
// (RUYI-690): call after an issue commits a transition INTO the done
// category. Every active suspension waiting on that issue resumes: row →
// resumed (actor `system`, firing predicate denormalized), snapshot cleared,
// system comment posted, and the waiting issue's assignee woken (decision 3).
// Best-effort end to end — the CAS and the snapshot share one transaction so
// nothing lands half-way, and a failure is logged, never propagated: the
// issue IS done regardless.
func (h *Handler) ResumeSuspensionsWaitingOnIssue(ctx context.Context, target db.Issue) {
	rows, err := h.Queries.ListActiveSuspensionsWaitingOnIssue(ctx, target.ID)
	if err != nil {
		slog.Warn("suspension auto-resume scan failed", "waiting_on_issue", uuidToString(target.ID), "error", err)
		return
	}
	prefix := ""
	for _, suspension := range rows {
		waiting, err := h.Queries.GetIssue(ctx, suspension.IssueID)
		if err != nil {
			slog.Warn("suspension auto-resume: load waiting issue failed",
				"suspension_id", uuidToString(suspension.ID), "error", err)
			continue
		}
		predicate := resumePredicate{
			Kind:    resumePredicateKindIssueDone,
			IssueID: uuidToString(target.ID),
		}
		resumed, refreshed, err := h.releaseSuspension(ctx, waiting, suspension, releaseSuspensionParams{
			arm:       releaseArmResume,
			actorType: "system",
			predicate: &predicate,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue // lost a race (manual resume/cancel) — nothing to do
			}
			slog.Warn("suspension auto-resume failed",
				"suspension_id", uuidToString(suspension.ID), "waiting_issue", uuidToString(waiting.ID), "error", err)
			continue
		}
		if prefix == "" {
			prefix = h.getIssuePrefix(ctx, waiting.WorkspaceID)
		}
		content := suspensionAutoResumeIssueContent(resumed, target, prefix)
		commentID := h.noteSuspensionSystemComment(ctx, waiting, content)
		h.recordSuspensionActivity(ctx, resumed, service.AuditIssueSuspensionResumed, "system", "", map[string]any{
			"suspension_id":       uuidToString(resumed.ID),
			"resumed_by_predicate": predicate,
			"waiting_on_issue_id": uuidToString(target.ID),
		})
		h.publishSuspensionUpdated(ctx, waiting.WorkspaceID, "system", "", resumed, refreshed, commentID)
		h.wakeAssigneeAfterRelease(ctx, waiting, commentID)
		slog.Info("suspension auto-resumed",
			"suspension_id", uuidToString(resumed.ID),
			"waiting_issue", uuidToString(waiting.ID),
			"predicate", resumePredicateKindIssueDone,
			"target_issue", uuidToString(target.ID))
	}
}

// ResumeSuspensionsWaitingOnDecision is the decision_answered auto-resume
// hook: call after a decision card commits its answer CAS (single and batch
// paths), BEFORE the answer echo comment runs the trigger pipeline — so an
// issue waiting on its own card is unsuspended by the time the echo reaches
// the admission gate. A cancelled card never reaches this: only the answer
// arm grants the question.
func (h *Handler) ResumeSuspensionsWaitingOnDecision(ctx context.Context, decision db.IssueDecision) {
	rows, err := h.Queries.ListActiveSuspensionsWaitingOnDecision(ctx, decision.ID)
	if err != nil {
		slog.Warn("suspension decision-resume scan failed", "decision_id", uuidToString(decision.ID), "error", err)
		return
	}
	for _, suspension := range rows {
		waiting, err := h.Queries.GetIssue(ctx, suspension.IssueID)
		if err != nil {
			slog.Warn("suspension decision-resume: load waiting issue failed",
				"suspension_id", uuidToString(suspension.ID), "error", err)
			continue
		}
		predicate := resumePredicate{
			Kind:       resumePredicateKindDecisionAnswered,
			DecisionID: uuidToString(decision.ID),
		}
		resumed, refreshed, err := h.releaseSuspension(ctx, waiting, suspension, releaseSuspensionParams{
			arm:       releaseArmResume,
			actorType: "system",
			predicate: &predicate,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			slog.Warn("suspension decision-resume failed",
				"suspension_id", uuidToString(suspension.ID), "waiting_issue", uuidToString(waiting.ID), "error", err)
			continue
		}
		prefix := h.getIssuePrefix(ctx, waiting.WorkspaceID)
		content := suspensionAutoResumeDecisionContent(resumed, decision, prefix)
		commentID := h.noteSuspensionSystemComment(ctx, waiting, content)
		h.recordSuspensionActivity(ctx, resumed, service.AuditIssueSuspensionResumed, "system", "", map[string]any{
			"suspension_id":          uuidToString(resumed.ID),
			"resumed_by_predicate":   predicate,
			"waiting_on_decision_id": uuidToString(decision.ID),
		})
		h.publishSuspensionUpdated(ctx, waiting.WorkspaceID, "system", "", resumed, refreshed, commentID)
		h.wakeAssigneeAfterRelease(ctx, waiting, commentID)
		slog.Info("suspension auto-resumed",
			"suspension_id", uuidToString(resumed.ID),
			"waiting_issue", uuidToString(waiting.ID),
			"predicate", resumePredicateKindDecisionAnswered,
			"decision_id", uuidToString(decision.ID))
	}
}

// ---- audit + notice helpers ----

// noteSuspensionSystemComment posts the suspension lifecycle comment
// (author_type='system', same shape as every other platform notice) and
// returns its id so the caller can anchor the resume wake. Best-effort by
// construction: the state change already committed and is correct whether or
// not the note lands.
func (h *Handler) noteSuspensionSystemComment(ctx context.Context, issue db.Issue, content string) pgtype.UUID {
	if content == "" {
		return pgtype.UUID{}
	}
	created, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		ID:          dbid.NewV7(),
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     content,
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("suspension notice: create system comment failed",
			"error", err, "issue_id", uuidToString(issue.ID))
		return pgtype.UUID{}
	}
	comment := created.Comment()
	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
		"comment":             commentToResponse(comment, nil, nil),
		"issue_title":         issue.Title,
		"issue_assignee_type": textToPtr(issue.AssigneeType),
		"issue_assignee_id":   uuidToPtr(issue.AssigneeID),
		"issue_status":        issue.Status,
		"issue_revision":      created.IssueRevision,
	})
	return comment.ID
}

// recordSuspensionActivity writes the activity_log row and its RUYI-355 audit
// twin, mirroring recordSuppressedIssueRun's disposition (log+metric trail,
// best-effort inserts, details payload mirrors the log line).
func (h *Handler) recordSuspensionActivity(ctx context.Context, row db.IssueSuspension, action, actorType, actorID string, details map[string]any) {
	payload, err := json.Marshal(details)
	if err != nil {
		slog.Warn("marshal suspension activity details", "suspension_id", uuidToString(row.ID), "error", err)
		return
	}
	slog.Info(string(action),
		"suspension_id", uuidToString(row.ID),
		"issue_id", uuidToString(row.IssueID),
		"actor_type", actorType,
		"details", string(payload))
	actorUUID, err := util.ParseUUID(actorID)
	if err != nil {
		// The system actor has no id; activity_log wants one, so only the
		// audit twin (which accepts system actors) records it.
		aType, aID := service.AuditActorFor(actorType, pgtype.UUID{})
		service.TryAppendAuditEvents(ctx, h.Queries,
			service.IssueEvent(action, aType, aID, row.WorkspaceID, row.IssueID, payload))
		return
	}
	if _, err := h.Queries.CreateActivity(ctx, db.CreateActivityParams{
		ID:          dbid.NewV7(),
		WorkspaceID: row.WorkspaceID,
		IssueID:     row.IssueID,
		ActorType:   pgtype.Text{String: actorType, Valid: true},
		ActorID:     actorUUID,
		Action:      action,
		Details:     payload,
	}); err != nil {
		slog.Warn("insert suspension activity", "suspension_id", uuidToString(row.ID), "error", err)
	}
	aType, aID := service.AuditActorFor(actorType, actorUUID)
	service.TryAppendAuditEvents(ctx, h.Queries,
		service.IssueEvent(action, aType, aID, row.WorkspaceID, row.IssueID, payload))
}

// publishSuspensionUpdated broadcasts the dedicated suspension event with the
// refreshed issue snapshot embedded, so boards badge/unbadge the hold from
// the same event that reports the mutation (the issue revision deliberately
// does not move — see SetIssueSuspendedAt).
func (h *Handler) publishSuspensionUpdated(ctx context.Context, workspaceID pgtype.UUID, actorType, actorID string, suspension db.IssueSuspension, issue db.Issue, triggerCommentID pgtype.UUID) {
	prefix := h.getIssuePrefix(ctx, workspaceID)
	issueResp := issueToResponse(issue, prefix)
	h.fillStatusCategory(ctx, workspaceID, &issueResp)
	h.publish(protocol.EventIssueSuspensionUpdated, uuidToString(workspaceID), actorType, actorID, map[string]any{
		"suspension":         issueSuspensionToResponse(suspension),
		"issue":              issueResp,
		"trigger_comment_id": uuidToString(triggerCommentID),
	})
}

// ---- suspension notice content ----

// issueMention renders an issue cross-reference link, the one mention shape
// that renders as a link without triggering anyone.
func issueMention(prefix string, number int32, id pgtype.UUID) string {
	return fmt.Sprintf("[%s](mention://issue/%s)", service.IssueIdentifier(prefix, number), uuidToString(id))
}

// derefString nil-safe *string read for optional request fields.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// suspensionSuspendContent builds the "issue put on hold" system comment.
// Target labels are resolved best-effort — the comment names the wait even if
// the label lookup fails.
func (h *Handler) suspensionSuspendContent(ctx context.Context, row db.IssueSuspension) string {
	var b strings.Builder
	switch row.WaitingOnType {
	case waitingOnIssue:
		targetLabel := "the target issue"
		if targetID, err := util.ParseUUID(uuidToString(row.WaitingOnIssueID)); err == nil {
			if target, err := h.Queries.GetIssue(ctx, targetID); err == nil {
				targetLabel = issueMention(h.getIssuePrefix(ctx, row.WorkspaceID), target.Number, target.ID)
			}
		}
		fmt.Fprintf(&b, "Issue suspended — waiting on %s to be done.\n\n", targetLabel)
	case waitingOnDecision:
		cardLabel := "the decision card"
		if decisionID, err := util.ParseUUID(uuidToString(row.WaitingOnDecisionID)); err == nil {
			if card, err := h.Queries.GetIssueDecision(ctx, db.GetIssueDecisionParams{ID: decisionID, WorkspaceID: row.WorkspaceID}); err == nil {
				question := sanitizeSuspensionText(card.Question)
				if len([]rune(question)) > 120 {
					question = string([]rune(question)[:120]) + "…"
				}
				cardLabel = fmt.Sprintf("the decision card \"%s\"", question)
			}
		}
		fmt.Fprintf(&b, "Issue suspended — waiting on %s to be answered.\n\n", cardLabel)
	case waitingOnExternal:
		b.WriteString("Issue suspended — waiting on an external dependency.\n\n")
	default: // manual
		b.WriteString("Issue suspended.\n\n")
	}
	fmt.Fprintf(&b, "Reason: %s\n", row.Reason)
	if row.ResumeCondition != "" {
		fmt.Fprintf(&b, "Resume when: %s\n", row.ResumeCondition)
	}
	return strings.TrimRight(b.String(), "\n")
}

// suspensionResumeContent builds the manual-resume system comment.
func suspensionResumeContent(row db.IssueSuspension) string {
	var b strings.Builder
	b.WriteString("Suspension lifted — the issue returned to normal scheduling.\n")
	if row.ResumeNote != "" {
		fmt.Fprintf(&b, "\nNote: %s\n", row.ResumeNote)
	}
	fmt.Fprintf(&b, "\nIt had been suspended: %s\n", row.Reason)
	return strings.TrimRight(b.String(), "\n")
}

// suspensionCancelContent builds the cancel system comment. Cancel withdraws
// the hold without waking the assignee — nothing was granted, so nothing
// resumed.
func suspensionCancelContent(row db.IssueSuspension) string {
	var b strings.Builder
	b.WriteString("Suspension withdrawn — the hold was removed as no longer relevant; the issue is no longer suspended.\n")
	if row.CancelNote != "" {
		fmt.Fprintf(&b, "\nNote: %s\n", row.CancelNote)
	}
	fmt.Fprintf(&b, "\nIt had been suspended: %s\n", row.Reason)
	return strings.TrimRight(b.String(), "\n")
}

// suspensionAutoResumeIssueContent builds the issue_done auto-resume comment.
func suspensionAutoResumeIssueContent(row db.IssueSuspension, target db.Issue, prefix string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Suspension auto-lifted — %s is done, so the condition this issue was waiting on is satisfied. The issue returned to normal scheduling.\n",
		issueMention(prefix, target.Number, target.ID))
	fmt.Fprintf(&b, "\nIt had been suspended: %s\n", row.Reason)
	return strings.TrimRight(b.String(), "\n")
}

// suspensionAutoResumeDecisionContent builds the decision_answered
// auto-resume comment.
func suspensionAutoResumeDecisionContent(row db.IssueSuspension, decision db.IssueDecision, prefix string) string {
	question := sanitizeSuspensionText(decision.Question)
	if len([]rune(question)) > 120 {
		question = string([]rune(question)[:120]) + "…"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Suspension auto-lifted — the decision card \"%s\" was answered, so the condition this issue was waiting on is satisfied. The issue returned to normal scheduling.\n", question)
	fmt.Fprintf(&b, "\nIt had been suspended: %s\n", row.Reason)
	return strings.TrimRight(b.String(), "\n")
}

// ---- input validation ----

// validateSuspensionInput normalizes the request into insert params. The
// machine predicate is derived here — from waiting_on_type, never from the
// wire — so the closed auto-resume set stays closed. Already-satisfied waits
// (target done, card answered) are rejected: suspending on them would strand
// the issue until a manual release, since the auto-resume hooks fire on
// transitions, not states.
func (h *Handler) validateSuspensionInput(r *http.Request, issue db.Issue, req CreateIssueSuspensionRequest, actorType, actorID string) (db.CreateIssueSuspensionParams, error) {
	params := db.CreateIssueSuspensionParams{
		ID:               dbid.NewV7(),
		WorkspaceID:      issue.WorkspaceID,
		IssueID:          issue.ID,
		Reason:           sanitizeSuspensionText(req.Reason),
		WaitingOnType:    req.WaitingOnType,
		WaitingOnSummary: sanitizeSuspensionText(derefString(req.WaitingOnSummary)),
		ResumeCondition:  sanitizeSuspensionText(derefString(req.ResumeCondition)),
		SuspendedByType:  actorType,
		SuspendedByID:    actorUUIDOrNull(actorType, actorID),
	}
	if params.Reason == "" {
		return params, errors.New("reason is required")
	}
	if !isValidWaitingOnType(params.WaitingOnType) {
		return params, fmt.Errorf("waiting_on_type must be one of %s, %s, %s, %s", waitingOnIssue, waitingOnDecision, waitingOnExternal, waitingOnManual)
	}

	switch params.WaitingOnType {
	case waitingOnIssue:
		rawID := derefString(req.WaitingOnIssueID)
		if rawID == "" {
			return params, errors.New("waiting_on_issue_id is required when waiting_on_type is 'issue'")
		}
		targetID := parseUUID(rawID)
		if !targetID.Valid {
			return params, errors.New("waiting_on_issue_id is not a valid UUID")
		}
		if uuidToString(targetID) == uuidToString(issue.ID) {
			return params, errors.New("an issue cannot wait on itself")
		}
		target, err := h.Queries.GetIssue(r.Context(), targetID)
		if err != nil {
			if isNotFound(err) {
				return params, errors.New("waiting_on_issue_id does not exist")
			}
			return params, err
		}
		if uuidToString(target.WorkspaceID) != uuidToString(issue.WorkspaceID) {
			return params, errors.New("waiting_on_issue_id is outside this workspace")
		}
		if issuestatus.NewResolver(target.WorkspaceID).Effective(r.Context(), h.Queries, target.Status) == issuestatus.Done {
			return params, errors.New("waiting_on_issue_id is already done — nothing to wait for")
		}
		params.WaitingOnIssueID = targetID
		raw, err := json.Marshal(resumePredicate{Kind: resumePredicateKindIssueDone, IssueID: uuidToString(targetID)})
		if err != nil {
			return params, err
		}
		params.ResumePredicate = raw

	case waitingOnDecision:
		rawID := derefString(req.WaitingOnDecisionID)
		if rawID == "" {
			return params, errors.New("waiting_on_decision_id is required when waiting_on_type is 'decision'")
		}
		decisionID := parseUUID(rawID)
		if !decisionID.Valid {
			return params, errors.New("waiting_on_decision_id is not a valid UUID")
		}
		card, err := h.Queries.GetIssueDecision(r.Context(), db.GetIssueDecisionParams{ID: decisionID, WorkspaceID: issue.WorkspaceID})
		if err != nil {
			if isNotFound(err) {
				return params, errors.New("waiting_on_decision_id does not exist in this workspace")
			}
			return params, err
		}
		if card.Status != "open" {
			return params, errors.New("waiting_on_decision_id is already answered or cancelled — nothing to wait for")
		}
		params.WaitingOnDecisionID = decisionID
		raw, err := json.Marshal(resumePredicate{Kind: resumePredicateKindDecisionAnswered, DecisionID: uuidToString(decisionID)})
		if err != nil {
			return params, err
		}
		params.ResumePredicate = raw

	case waitingOnExternal, waitingOnManual:
		// These waits can never auto-resume, so a described release
		// condition is the only thing keeping the hold reviewable.
		if params.ResumeCondition == "" {
			return params, fmt.Errorf("resume_condition is required when waiting_on_type is '%s' — describe what would release the hold", params.WaitingOnType)
		}
	}
	return params, nil
}

// enteredDoneCategory reports a transition INTO the done category. Both sides
// resolve through the status catalog, so a custom done status fires exactly
// like the built-in `done` (same rule notifyParentOfChildDone applies to
// terminal statuses; MUL-6243).
func (h *Handler) enteredDoneCategory(ctx context.Context, prev, issue db.Issue) bool {
	if issuestatus.Effective(ctx, h.Queries, issue.WorkspaceID, issue.Status) != issuestatus.Done {
		return false
	}
	return issuestatus.Effective(ctx, h.Queries, prev.WorkspaceID, prev.Status) != issuestatus.Done
}
