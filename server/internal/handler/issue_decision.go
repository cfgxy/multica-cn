package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Decision cards (RUYI-345): a run raises a structured question with 2-4
// options; a human member answers by picking options; the platform echoes the
// answer as a comment that mentions the creating agent so the standard
// mention pipeline resumes the run. Agents are hard-blocked from answering —
// the card exists to gate agent work on a human decision, so an agent
// answering its own card would bypass that gate.

const (
	decisionMinOptions        = 2
	decisionMaxOptions        = 4
	decisionMaxQuestionLen    = 500
	decisionMaxOptionLabelLen = 200
)

type DecisionOption struct {
	Label string `json:"label"`
}

type IssueDecisionResponse struct {
	ID                 string                  `json:"id"`
	IssueID            string                  `json:"issue_id"`
	SourceCommentID    *string                 `json:"source_comment_id"`
	Question           string                  `json:"question"`
	Options            []DecisionOption        `json:"options"`
	MultiSelect        bool                    `json:"multi_select"`
	RecommendedIndices []int                   `json:"recommended_indices"`
	Status             string                  `json:"status"`
	SelectedIndices    []int                   `json:"selected_indices"`
	AnsweredByType     *string                 `json:"answered_by_type"`
	AnsweredByID       *string                 `json:"answered_by_id"`
	AnsweredAt         *time.Time              `json:"answered_at"`
	AnswerCommentID    *string                 `json:"answer_comment_id"`
	CreatedByType      string                  `json:"created_by_type"`
	CreatedByID        string                  `json:"created_by_id"`
	CreatedAt          time.Time               `json:"created_at"`
	UpdatedAt          time.Time               `json:"updated_at"`
	TriggerOutcomes    []CommentTriggerOutcome `json:"trigger_outcomes,omitempty"`
}

type CreateIssueDecisionRequest struct {
	Question           string   `json:"question"`
	Options            []string `json:"options"`
	MultiSelect        bool     `json:"multi_select"`
	RecommendedIndices []int    `json:"recommended_indices"`
	SourceCommentID    *string  `json:"source_comment_id"`
}

type AnswerIssueDecisionRequest struct {
	SelectedIndices []int `json:"selected_indices"`
}

// errDecisionValidation marks input-shape rejections (400), as opposed to
// state or permission rejections.
type errDecisionValidation struct{ msg string }

func (e errDecisionValidation) Error() string { return e.msg }

// validateDecisionInput normalizes and validates card creation input.
// Returned slice copies are what the caller persists.
func validateDecisionInput(question string, labels []string, recommended []int) ([]DecisionOption, []int, error) {
	question = strings.TrimSpace(sanitizeNullBytes(question))
	if question == "" {
		return nil, nil, errDecisionValidation{"question is required"}
	}
	if len([]rune(question)) > decisionMaxQuestionLen {
		return nil, nil, errDecisionValidation{fmt.Sprintf("question must be at most %d characters", decisionMaxQuestionLen)}
	}
	if len(labels) < decisionMinOptions || len(labels) > decisionMaxOptions {
		return nil, nil, errDecisionValidation{fmt.Sprintf("options must contain between %d and %d items", decisionMinOptions, decisionMaxOptions)}
	}
	options := make([]DecisionOption, 0, len(labels))
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		label = strings.TrimSpace(sanitizeNullBytes(label))
		if label == "" {
			return nil, nil, errDecisionValidation{"option labels must not be empty"}
		}
		if len([]rune(label)) > decisionMaxOptionLabelLen {
			return nil, nil, errDecisionValidation{fmt.Sprintf("option labels must be at most %d characters", decisionMaxOptionLabelLen)}
		}
		if _, dup := seen[label]; dup {
			return nil, nil, errDecisionValidation{"option labels must be unique"}
		}
		seen[label] = struct{}{}
		options = append(options, DecisionOption{Label: label})
	}
	recs, err := validateIndices(recommended, len(options), true)
	if err != nil {
		return nil, nil, err
	}
	return options, recs, nil
}

// validateDecisionAnswer checks the picked indices against the card's option
// count and select mode. Single-select takes exactly one option; multi-select
// takes between one and all of them, without duplicates.
func validateDecisionAnswer(numOptions int, multiSelect bool, selected []int) error {
	if len(selected) == 0 {
		return errDecisionValidation{"selected_indices must not be empty"}
	}
	if !multiSelect && len(selected) != 1 {
		return errDecisionValidation{"this card is single-select: exactly one option must be chosen"}
	}
	seen := make(map[int]struct{}, len(selected))
	for _, idx := range selected {
		if idx < 0 || idx >= numOptions {
			return errDecisionValidation{fmt.Sprintf("selected_indices contains out-of-range index %d (options: %d)", idx, numOptions)}
		}
		if _, dup := seen[idx]; dup {
			return errDecisionValidation{"selected_indices must not repeat an option"}
		}
		seen[idx] = struct{}{}
	}
	return nil
}

// validateIndices range-checks an index list against numOptions. allowAll
// permits the full range (recommendations); otherwise at least one and at
// most numOptions-1 entries are accepted (answers). Duplicates are always
// rejected.
func validateIndices(indices []int, numOptions int, allowEmpty bool) ([]int, error) {
	if len(indices) == 0 && !allowEmpty {
		return nil, errDecisionValidation{"at least one index is required"}
	}
	if len(indices) > numOptions {
		return nil, errDecisionValidation{"too many indices"}
	}
	seen := make(map[int]struct{}, len(indices))
	out := make([]int, 0, len(indices))
	for _, idx := range indices {
		if idx < 0 || idx >= numOptions {
			return nil, errDecisionValidation{fmt.Sprintf("index %d out of range (options: %d)", idx, numOptions)}
		}
		if _, dup := seen[idx]; dup {
			return nil, errDecisionValidation{"indices must not repeat"}
		}
		seen[idx] = struct{}{}
		out = append(out, idx)
	}
	return out, nil
}

func parseDecisionOptions(raw []byte) []DecisionOption {
	var options []DecisionOption
	if err := json.Unmarshal(raw, &options); err != nil {
		// Rows are only written through validateDecisionInput, so a decode
		// failure means hand-edited data; render an empty card rather than 500.
		return []DecisionOption{}
	}
	return options
}

func parseDecisionIndices(raw []byte) []int {
	if len(raw) == 0 {
		return []int{}
	}
	var indices []int
	if err := json.Unmarshal(raw, &indices); err != nil {
		return []int{}
	}
	return indices
}

func decisionToResponse(d db.IssueDecision) IssueDecisionResponse {
	resp := IssueDecisionResponse{
		ID:                 uuidToString(d.ID),
		IssueID:            uuidToString(d.IssueID),
		Question:           d.Question,
		Options:            parseDecisionOptions(d.Options),
		MultiSelect:        d.MultiSelect,
		RecommendedIndices: parseDecisionIndices(d.RecommendedIndices),
		Status:             d.Status,
		SelectedIndices:    parseDecisionIndices(d.SelectedIndices),
		CreatedByType:      d.CreatedByType,
		CreatedByID:        uuidToString(d.CreatedByID),
		CreatedAt:          d.CreatedAt.Time,
		UpdatedAt:          d.UpdatedAt.Time,
	}
	if d.SourceCommentID.Valid {
		v := uuidToString(d.SourceCommentID)
		resp.SourceCommentID = &v
	}
	if d.AnsweredByType.Valid {
		v := d.AnsweredByType.String
		resp.AnsweredByType = &v
	}
	if d.AnsweredByID.Valid {
		v := uuidToString(d.AnsweredByID)
		resp.AnsweredByID = &v
	}
	if d.AnsweredAt.Valid {
		v := d.AnsweredAt.Time
		resp.AnsweredAt = &v
	}
	if d.AnswerCommentID.Valid {
		v := uuidToString(d.AnswerCommentID)
		resp.AnswerCommentID = &v
	}
	return resp
}

// CreateIssueDecision handles POST /api/issues/{id}/decisions. Any actor may
// raise a card — agents do it from a run via the CLI, members from scripts —
// the recorded creator is what the answer echo mentions.
func (h *Handler) CreateIssueDecision(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req CreateIssueDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	options, recommended, err := validateDecisionInput(req.Question, req.Options, req.RecommendedIndices)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var sourceCommentID pgtype.UUID
	if req.SourceCommentID != nil && *req.SourceCommentID != "" {
		var valid bool
		sourceCommentID, valid = parseUUIDOrBadRequest(w, *req.SourceCommentID, "source_comment_id")
		if !valid {
			return
		}
	}

	authorType, authorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	optionsJSON, err := json.Marshal(options)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode options")
		return
	}
	recommendedJSON, err := json.Marshal(recommended)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode recommendations")
		return
	}

	created, err := h.Queries.CreateIssueDecision(r.Context(), db.CreateIssueDecisionParams{
		ID:                 dbid.NewV7(),
		WorkspaceID:        issue.WorkspaceID,
		IssueID:            issue.ID,
		SourceCommentID:    sourceCommentID,
		Question:           sanitizeNullBytes(req.Question),
		Options:            optionsJSON,
		MultiSelect:        req.MultiSelect,
		RecommendedIndices: recommendedJSON,
		CreatedByType:      authorType,
		CreatedByID:        parseUUID(authorID),
	})
	if err != nil {
		slog.Warn("create issue decision failed", append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(issue.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to create decision card")
		return
	}

	resp := decisionToResponse(created)
	h.publish(protocol.EventDecisionUpdated, uuidToString(issue.WorkspaceID), authorType, authorID, map[string]any{
		"decision":    resp,
		"issue_id":    uuidToString(issue.ID),
		"issue_title": issue.Title,
	})
	slog.Info("issue decision created", append(logger.RequestAttrs(r),
		"decision_id", resp.ID, "issue_id", resp.IssueID, "created_by", authorType)...)
	writeJSON(w, http.StatusCreated, resp)
}

// ListIssueDecisions handles GET /api/issues/{id}/decisions.
func (h *Handler) ListIssueDecisions(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	decisions, err := h.Queries.ListIssueDecisionsForIssue(r.Context(), db.ListIssueDecisionsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list decision cards")
		return
	}
	resp := make([]IssueDecisionResponse, 0, len(decisions))
	for _, d := range decisions {
		resp = append(resp, decisionToResponse(d))
	}
	writeJSON(w, http.StatusOK, resp)
}

// loadDecisionForIssue fetches a card and verifies it belongs to the issue in
// the URL — the same tenant scoping ListIssueDecisionsForIssue enforces.
func (h *Handler) loadDecisionForIssue(w http.ResponseWriter, r *http.Request, issue db.Issue) (db.IssueDecision, bool) {
	decisionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "decisionId"), "decision id")
	if !ok {
		return db.IssueDecision{}, false
	}
	decision, err := h.Queries.GetIssueDecision(r.Context(), db.GetIssueDecisionParams{
		ID:          decisionID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "decision card not found")
			return db.IssueDecision{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load decision card")
		return db.IssueDecision{}, false
	}
	if uuidToString(decision.IssueID) != uuidToString(issue.ID) {
		writeError(w, http.StatusNotFound, "decision card not found")
		return db.IssueDecision{}, false
	}
	return decision, true
}

// AnswerIssueDecision handles POST /api/issues/{id}/decisions/{decisionId}/answer.
// Member-only by design: the card gates agent work on a human decision, so an
// agent answering its own card would bypass that gate (resolveActor classifies
// the caller; task-token traffic classifies as "agent" and is refused here).
//
// On success the platform posts an echo comment authored by the answerer that
// mentions the creating agent, then hands it to the standard comment trigger
// pipeline — that mention is what resumes the creating run.
func (h *Handler) AnswerIssueDecision(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	authorType, authorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	if authorType != "member" {
		writeError(w, http.StatusForbidden, "decision cards can only be answered by human members")
		return
	}

	decision, ok := h.loadDecisionForIssue(w, r, issue)
	if !ok {
		return
	}

	var req AnswerIssueDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateDecisionAnswer(len(parseDecisionOptions(decision.Options)), decision.MultiSelect, req.SelectedIndices); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	selectedJSON, err := json.Marshal(req.SelectedIndices)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode answer")
		return
	}

	// CAS first: the echo comment is only posted for the request that wins the
	// card. A concurrent answer (double click) or a cancel racing the answer
	// loses here with ErrNoRows.
	answered, err := h.Queries.AnswerIssueDecision(r.Context(), db.AnswerIssueDecisionParams{
		ID:              decision.ID,
		WorkspaceID:     issue.WorkspaceID,
		SelectedIndices: selectedJSON,
		AnsweredByType:  pgtype.Text{String: "member", Valid: true},
		AnsweredByID:    parseUUID(authorID),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusConflict, "decision card is no longer open")
			return
		}
		slog.Warn("answer issue decision failed", append(logger.RequestAttrs(r), "error", err, "decision_id", uuidToString(decision.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to answer decision card")
		return
	}

	resp := decisionToResponse(answered)
	resp.TriggerOutcomes = h.postDecisionAnswerEcho(r, issue, answered, req.SelectedIndices, authorID, &resp)

	h.publish(protocol.EventDecisionUpdated, uuidToString(issue.WorkspaceID), "member", authorID, map[string]any{
		"decision":    resp,
		"issue_id":    uuidToString(issue.ID),
		"issue_title": issue.Title,
	})
	writeJSON(w, http.StatusOK, resp)
}

// postDecisionAnswerEcho posts the answer echo comment as the answering
// member and runs it through the standard trigger pipeline. Best-effort on
// the linkage: the card is already answered once the CAS wins, so a failed
// echo leaves an answered card without its comment rather than failing the
// answer the user just submitted.
func (h *Handler) postDecisionAnswerEcho(r *http.Request, issue db.Issue, answered db.IssueDecision, selected []int, authorID string, resp *IssueDecisionResponse) []CommentTriggerOutcome {
	options := parseDecisionOptions(answered.Options)
	content := h.buildDecisionAnswerContent(r.Context(), answered, options, selected)
	if content == "" {
		return nil
	}

	created, err := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		ID:          dbid.NewV7(),
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "member",
		AuthorID:    parseUUID(authorID),
		Content:     content,
		Type:        "comment",
	})
	if err != nil {
		slog.Warn("decision answer echo comment failed", append(logger.RequestAttrs(r),
			"error", err, "decision_id", uuidToString(answered.ID))...)
		return nil
	}
	comment := created.Comment()

	// Pointer fill; the returned row refreshes the response so the answerer
	// sees the linked echo immediately.
	if linked, err := h.Queries.SetIssueDecisionAnswerComment(r.Context(), db.SetIssueDecisionAnswerCommentParams{
		ID:              answered.ID,
		WorkspaceID:     issue.WorkspaceID,
		AnswerCommentID: comment.ID,
	}); err != nil {
		slog.Warn("decision answer comment link failed", append(logger.RequestAttrs(r),
			"error", err, "decision_id", uuidToString(answered.ID))...)
	} else {
		refreshed := decisionToResponse(linked)
		resp.AnswerCommentID = refreshed.AnswerCommentID
		resp.UpdatedAt = refreshed.UpdatedAt
	}

	commentResp := commentToResponse(comment, nil, nil)
	commentResp.IssueRevision = created.IssueRevision
	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", authorID, map[string]any{
		"comment":             commentResp,
		"issue_title":         issue.Title,
		"issue_assignee_type": textToPtr(issue.AssigneeType),
		"issue_assignee_id":   uuidToPtr(issue.AssigneeID),
		"issue_status":        issue.Status,
		"issue_revision":      created.IssueRevision,
	})

	return h.triggerTasksForComment(r.Context(), issue, comment, nil, "member", authorID, "", nil)
}

// CancelIssueDecision handles POST /api/issues/{id}/decisions/{decisionId}/cancel.
// Member-only: closing a card is the same human-side lifecycle act as
// answering it. No echo comment — a cancelled card asked nothing.
func (h *Handler) CancelIssueDecision(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	authorType, authorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	if authorType != "member" {
		writeError(w, http.StatusForbidden, "decision cards can only be cancelled by human members")
		return
	}

	decision, ok := h.loadDecisionForIssue(w, r, issue)
	if !ok {
		return
	}

	cancelled, err := h.Queries.CancelIssueDecision(r.Context(), db.CancelIssueDecisionParams{
		ID:          decision.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusConflict, "decision card is no longer open")
			return
		}
		slog.Warn("cancel issue decision failed", append(logger.RequestAttrs(r), "error", err, "decision_id", uuidToString(decision.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to cancel decision card")
		return
	}

	resp := decisionToResponse(cancelled)
	h.publish(protocol.EventDecisionUpdated, uuidToString(issue.WorkspaceID), "member", authorID, map[string]any{
		"decision":    resp,
		"issue_id":    uuidToString(issue.ID),
		"issue_title": issue.Title,
	})
	writeJSON(w, http.StatusOK, resp)
}

// buildDecisionAnswerContent renders the echo comment body. Cards created by
// an agent get a mention link so the pipeline wakes that agent; member-created
// cards are answered with a plain record (no run to resume).
func (h *Handler) buildDecisionAnswerContent(ctx context.Context, answered db.IssueDecision, options []DecisionOption, selected []int) string {
	var picked []string
	for _, idx := range selected {
		if idx < 0 || idx >= len(options) {
			continue
		}
		picked = append(picked, fmt.Sprintf("- Option %d: %s", idx+1, options[idx].Label))
	}
	if len(picked) == 0 {
		return ""
	}

	head := fmt.Sprintf("Decision card answered: \"%s\"", answered.Question)
	if answered.CreatedByType == "agent" {
		// A missing or deleted creator agent still leaves the record; the
		// echo just loses its wake-up mention.
		if agent, err := h.Queries.GetAgent(ctx, answered.CreatedByID); err == nil && agent.Name != "" {
			head = fmt.Sprintf("[@%s](mention://agent/%s) — decision card answered: \"%s\"", agent.Name, uuidToString(answered.CreatedByID), answered.Question)
		}
	}
	body := head + "\n\n"
	for _, line := range picked {
		body += line + "\n"
	}
	return body
}
