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

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Batch decision answering (RUYI-471): one request answers several open
// cards, each through the same per-card CAS as the single endpoint. Winners
// share ONE summary echo comment, so one mention pipeline pass wakes the
// creators; per-card failures are reported per card and never roll the batch
// back. The member comment path reuses the same core for "1A 2B" text
// answers (decision_text_answer.go).

const decisionBatchMaxAnswers = 50

type BatchIssueDecisionAnswer struct {
	DecisionID      string `json:"decision_id"`
	SelectedIndices []int  `json:"selected_indices"`
}

type BatchAnswerIssueDecisionsRequest struct {
	Answers []BatchIssueDecisionAnswer `json:"answers"`
}

// BatchDecisionAnswerOutcome is the per-card result. Status values:
// answered (CAS won), conflict (card left the open set first), invalid
// (selection failed validation), not_found (unknown id or another issue's
// card).
type BatchDecisionAnswerOutcome struct {
	DecisionID string                 `json:"decision_id"`
	Status     string                 `json:"status"`
	Error      string                 `json:"error,omitempty"`
	Decision   *IssueDecisionResponse `json:"decision,omitempty"`
}

type BatchAnswerIssueDecisionsResponse struct {
	Results         []BatchDecisionAnswerOutcome `json:"results"`
	EchoCommentID   string                       `json:"echo_comment_id,omitempty"`
	TriggerOutcomes []CommentTriggerOutcome      `json:"trigger_outcomes,omitempty"`
}

// batchDecisionAnswer pairs a card with its validated, serialized selection.
type batchDecisionAnswer struct {
	Decision     db.IssueDecision
	Indices      []int
	SelectedJSON []byte
}

func newBatchDecisionAnswer(card db.IssueDecision, indices []int) (batchDecisionAnswer, error) {
	raw, err := json.Marshal(indices)
	if err != nil {
		return batchDecisionAnswer{}, err
	}
	return batchDecisionAnswer{Decision: card, Indices: indices, SelectedJSON: raw}, nil
}

// AnswerIssueDecisionsBatch handles POST /api/issues/{id}/decisions/answer-batch.
// Member-only for the same reason as the single endpoint: the cards gate
// agent work on a human decision.
func (h *Handler) AnswerIssueDecisionsBatch(w http.ResponseWriter, r *http.Request) {
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

	var req BatchAnswerIssueDecisionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Answers) == 0 {
		writeError(w, http.StatusBadRequest, "answers must not be empty")
		return
	}
	if len(req.Answers) > decisionBatchMaxAnswers {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("answers must contain at most %d items", decisionBatchMaxAnswers))
		return
	}

	// Pre-answer snapshot: the "决策 N" numbering in the echo is the open
	// cards' created_at order at answer time, and per-card classification
	// (open vs already-closed vs unknown) reads from it. The CAS remains the
	// final arbiter for everything that looks open.
	open, err := h.listOpenDecisions(r, issue)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list decision cards")
		return
	}

	results := make([]BatchDecisionAnswerOutcome, 0, len(req.Answers))
	items := make([]batchDecisionAnswer, 0, len(req.Answers))
	for _, ans := range req.Answers {
		result := BatchDecisionAnswerOutcome{DecisionID: ans.DecisionID}
		card, state := h.classifyBatchTarget(r, issue, open, ans.DecisionID)
		switch state {
		case "not_found":
			result.Status = "not_found"
			result.Error = "decision card not found on this issue"
		case "conflict":
			result.Status = "conflict"
			result.Error = "decision card is no longer open"
		default:
			if err := validateDecisionAnswer(len(parseDecisionOptions(card.Options)), card.MultiSelect, ans.SelectedIndices); err != nil {
				result.Status = "invalid"
				result.Error = err.Error()
				break
			}
			item, err := newBatchDecisionAnswer(card, ans.SelectedIndices)
			if err != nil {
				result.Status = "invalid"
				result.Error = "failed to encode answer"
				break
			}
			result.Status = "open"
			items = append(items, item)
		}
		results = append(results, result)
	}

	resp := h.applyDecisionBatchAnswers(r, issue, authorID, open, items, results, decisionSourceBatch)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listOpenDecisions(r *http.Request, issue db.Issue) ([]db.IssueDecision, error) {
	decisions, err := h.Queries.ListIssueDecisionsForIssue(r.Context(), db.ListIssueDecisionsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return nil, err
	}
	open := make([]db.IssueDecision, 0, len(decisions))
	for _, d := range decisions {
		if d.Status != "open" {
			continue
		}
		// RUYI-630: authorization cards answer only by card click — they are
		// invisible to the batch numbering and the "1A 2B" text binding, so
		// a text reply can never constitute an authorization.
		if d.DecisionKind == "authorization" {
			continue
		}
		if !h.decisionCardVisibleToCaller(r, uuidToString(issue.WorkspaceID), d) {
			continue
		}
		open = append(open, d)
	}
	return open, nil
}

// classifyBatchTarget sorts a requested card id into not_found (unknown,
// unparseable, or another issue's card), conflict (exists here but left the
// open set), or ok (open card returned).
func (h *Handler) classifyBatchTarget(r *http.Request, issue db.Issue, open []db.IssueDecision, decisionID string) (db.IssueDecision, string) {
	id, err := util.ParseUUID(decisionID)
	if err != nil {
		return db.IssueDecision{}, "not_found"
	}
	for _, card := range open {
		if card.ID == id {
			return card, "ok"
		}
	}
	card, err := h.Queries.GetIssueDecision(r.Context(), db.GetIssueDecisionParams{ID: id, WorkspaceID: issue.WorkspaceID})
	if err != nil || uuidToString(card.IssueID) != uuidToString(issue.ID) {
		return db.IssueDecision{}, "not_found"
	}
	return card, "conflict"
}

// answeredCardInfo carries one CAS winner into echo construction.
type answeredCardInfo struct {
	seq      int
	decision db.IssueDecision
	indices  []int
}

// applyDecisionBatchAnswers CAS-answers each prepared item, then posts ONE
// summary echo for the winners and runs it through the trigger pipeline
// once. Results are updated in place so per-card outcomes keep the caller's
// request order; results carrying status "open" are exactly the prepared
// items, in order. The open snapshot feeds the echo's 决策 N numbering.
func (h *Handler) applyDecisionBatchAnswers(r *http.Request, issue db.Issue, authorID string, open []db.IssueDecision, items []batchDecisionAnswer, results []BatchDecisionAnswerOutcome, answerSource string) BatchAnswerIssueDecisionsResponse {
	resp := BatchAnswerIssueDecisionsResponse{Results: results}
	seqByID := make(map[string]int, len(open))
	for i, card := range open {
		seqByID[uuidToString(card.ID)] = i + 1
	}

	var answered []answeredCardInfo
	itemIdx := 0
	for i := range resp.Results {
		if resp.Results[i].Status != "open" {
			continue
		}
		item := items[itemIdx]
		itemIdx++
		answeredRow, err := h.Queries.AnswerIssueDecision(r.Context(), db.AnswerIssueDecisionParams{
			ID:              item.Decision.ID,
			WorkspaceID:     issue.WorkspaceID,
			SelectedIndices: item.SelectedJSON,
			AnsweredByType:  pgtype.Text{String: "member", Valid: true},
			AnsweredByID:    parseUUID(authorID),
			AnswerSource:    answerSource,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				resp.Results[i].Status = "conflict"
				resp.Results[i].Error = "decision card is no longer open"
				continue
			}
			slog.Warn("batch answer issue decision failed", append(logger.RequestAttrs(r),
				"error", err, "decision_id", uuidToString(item.Decision.ID))...)
			resp.Results[i].Status = "invalid"
			resp.Results[i].Error = "failed to answer decision card"
			continue
		}
		cardResp := decisionToResponse(answeredRow)
		resp.Results[i].Status = "answered"
		resp.Results[i].Decision = &cardResp
		answered = append(answered, answeredCardInfo{
			seq:      seqByID[uuidToString(item.Decision.ID)],
			decision: answeredRow,
			indices:  item.Indices,
		})
	}

	if len(answered) == 0 {
		return resp
	}

	echoID, outcomes := h.postDecisionBatchEcho(r, issue, authorID, answered)
	if echoID != "" {
		resp.EchoCommentID = echoID
		for i := range resp.Results {
			if resp.Results[i].Status == "answered" && resp.Results[i].Decision != nil {
				linked := *resp.Results[i].Decision
				id := echoID
				linked.AnswerCommentID = &id
				resp.Results[i].Decision = &linked
			}
		}
	}
	resp.TriggerOutcomes = outcomes
	for i := range resp.Results {
		if resp.Results[i].Status != "answered" || resp.Results[i].Decision == nil {
			continue
		}
		h.publish(protocol.EventDecisionUpdated, uuidToString(issue.WorkspaceID), "member", authorID, map[string]any{
			"decision":    *resp.Results[i].Decision,
			"issue_id":    uuidToString(issue.ID),
			"issue_title": issue.Title,
		})
	}
	return resp
}

// postDecisionBatchEcho posts the shared summary echo as the answering
// member, links every winning card to it, publishes the comment event, and
// hands the comment to the trigger pipeline exactly once. Returns the echo
// comment id ("" when the echo failed — the answers stand; the linkage is
// best-effort, matching the single-card path) and the echo's per-target
// trigger outcomes for the response payload.
func (h *Handler) postDecisionBatchEcho(r *http.Request, issue db.Issue, authorID string, answered []answeredCardInfo) (string, []CommentTriggerOutcome) {
	content := h.buildDecisionBatchEchoContent(r.Context(), answered)
	if content == "" {
		return "", nil
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
		slog.Warn("decision batch echo comment failed", append(logger.RequestAttrs(r),
			"error", err, "issue_id", uuidToString(issue.ID), "cards", len(answered))...)
		return "", nil
	}
	comment := created.Comment()

	for _, a := range answered {
		if _, err := h.Queries.SetIssueDecisionAnswerComment(r.Context(), db.SetIssueDecisionAnswerCommentParams{
			ID:              a.decision.ID,
			WorkspaceID:     issue.WorkspaceID,
			AnswerCommentID: comment.ID,
		}); err != nil {
			slog.Warn("decision batch echo link failed", append(logger.RequestAttrs(r),
				"error", err, "decision_id", uuidToString(a.decision.ID))...)
		}
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

	outcomes := h.triggerTasksForComment(r.Context(), issue, comment, nil, "member", authorID, "", nil)
	return uuidToString(comment.ID), outcomes
}

// buildDecisionBatchEchoContent renders the shared echo: a compact summary
// line in the owner-facing "决策 1 选 A、决策 2 选 B" shape, then one block per
// card with question and picked labels, so a wrong text-answer binding is
// visible and correctable in the stream.
func (h *Handler) buildDecisionBatchEchoContent(ctx context.Context, answered []answeredCardInfo) string {
	if len(answered) == 0 {
		return ""
	}
	parts := make([]string, 0, len(answered))
	for _, a := range answered {
		parts = append(parts, fmt.Sprintf("决策 %d 选 %s", a.seq, decisionLettersFromIndices(a.indices)))
	}
	head := fmt.Sprintf("decision cards answered: %s", strings.Join(parts, "、"))
	if mentions := h.decisionEchoMentions(ctx, answered); len(mentions) > 0 {
		head = strings.Join(mentions, " ") + " — " + head
	}

	var body strings.Builder
	body.WriteString(head + "\n\n")
	for _, a := range answered {
		options := parseDecisionOptions(a.decision.Options)
		body.WriteString(fmt.Sprintf("Decision %d: \"%s\"\n", a.seq, a.decision.Question))
		for _, idx := range a.indices {
			if idx < 0 || idx >= len(options) {
				continue
			}
			body.WriteString(fmt.Sprintf("- Option %d: %s\n", idx+1, options[idx].Label))
		}
	}
	return body.String()
}

// decisionEchoMentions returns one mention per distinct agent creator among
// the answered cards, in card order — several agents asked, so several get
// woken by the one echo.
func (h *Handler) decisionEchoMentions(ctx context.Context, answered []answeredCardInfo) []string {
	var mentions []string
	seen := make(map[string]struct{}, len(answered))
	for _, a := range answered {
		if a.decision.CreatedByType != "agent" {
			continue
		}
		id := uuidToString(a.decision.CreatedByID)
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		// A missing or deleted creator agent still leaves the record; the
		// echo just loses that wake-up mention.
		if agent, err := h.Queries.GetAgent(ctx, a.decision.CreatedByID); err == nil && agent.Name != "" {
			mentions = append(mentions, fmt.Sprintf("[@%s](mention://agent/%s)", agent.Name, id))
		}
	}
	return mentions
}

func decisionLettersFromIndices(indices []int) string {
	letters := make([]rune, 0, len(indices))
	for _, idx := range indices {
		letters = append(letters, rune('A'+idx))
	}
	return string(letters)
}

// maybeAnswerDecisionsFromComment is the text-answer hook on the member
// comment path. It reports handled=true when the comment was consumed by the
// decision flow and the caller must skip the ordinary comment trigger pass:
//
//   - not a token sequence → not handled (an ordinary comment, always was)
//   - token sequence but no open cards → not handled (silent ignore)
//   - token sequence over open cards but incomplete/invalid binding →
//     handled with no effects (fail-closed: no card moves, no echo, no run)
//   - full valid binding → cards answered via the batch core; handled with
//     the echo's trigger outcomes (the single wake)
func (h *Handler) maybeAnswerDecisionsFromComment(r *http.Request, issue db.Issue, content, authorID string) ([]CommentTriggerOutcome, bool) {
	tokens, ok := parseDecisionAnswerTokens(content)
	if !ok {
		return nil, false
	}
	open, err := h.listOpenDecisions(r, issue)
	if err != nil || len(open) == 0 {
		// A listing failure must not invent a decision flow around an
		// ordinary comment; treat it as silence.
		return nil, false
	}
	bound, ok := bindDecisionTextAnswer(tokens, open)
	if !ok {
		return nil, true
	}
	items := make([]batchDecisionAnswer, 0, len(bound))
	for _, b := range bound {
		item, err := newBatchDecisionAnswer(b.Decision, b.Indices)
		if err != nil {
			// Indices come from A-D letters; marshalling cannot fail. Treat
			// any surprise as an ordinary comment rather than a partial flow.
			return nil, false
		}
		items = append(items, item)
	}
	echoOutcomes := h.applyDecisionBatchAnswersWithOutcomes(r, issue, authorID, open, items)
	return echoOutcomes, true
}

// applyDecisionBatchAnswersWithOutcomes is the text-path entry: it prepares
// per-card results for the full bound set (no request-order mixing to
// preserve), answers them, and returns the echo's trigger outcomes so the
// comment response can surface the single wake.
func (h *Handler) applyDecisionBatchAnswersWithOutcomes(r *http.Request, issue db.Issue, authorID string, open []db.IssueDecision, items []batchDecisionAnswer) []CommentTriggerOutcome {
	results := make([]BatchDecisionAnswerOutcome, len(items))
	for i, item := range items {
		results[i] = BatchDecisionAnswerOutcome{DecisionID: uuidToString(item.Decision.ID), Status: "open"}
	}
	resp := h.applyDecisionBatchAnswers(r, issue, authorID, open, items, results, decisionSourceTextToken)
	return resp.TriggerOutcomes
}
