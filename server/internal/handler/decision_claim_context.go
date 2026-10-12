package handler

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"

	"github.com/multica-ai/multica/server/internal/util"
)

// Decision card answer context at claim time (RUYI-719). A decision card's
// answer echo comment already wakes the creating run through the standard
// mention pipeline, but the woken run only sees the echo's human-readable
// text and has to re-read the issue thread to recover the question/answer
// semantics. The claim path now locates the answered cards behind the
// delivered echo comments and attaches a typed payload — the question text
// plus the selected options' positions and labels — read straight from
// issue_decisions, so the run knows exactly what the human decided without
// a thread re-read. Wire-mirrored by daemon.DecisionAnswerContextData.

// DecisionAnswerSelection is one picked option: Position is the 1-based
// index into the card's option list, Label the option text as asked.
type DecisionAnswerSelection struct {
	Position int    `json:"position"`
	Label    string `json:"label"`
}

// DecisionAnswerContextData is the typed answer context for ONE answered
// card whose echo was delivered with this claim.
type DecisionAnswerContextData struct {
	DecisionID   string                    `json:"decision_id"`
	Question     string                    `json:"question"`
	MultiSelect  bool                      `json:"multi_select,omitempty"`
	AnswerSource string                    `json:"answer_source,omitempty"`
	Selected     []DecisionAnswerSelection `json:"selected"`
}

// decisionAnswerContextForComments resolves the typed answer context for the
// decision-card echo comments among a claim's delivered comment ids. Cards
// are located purely by the answer_comment_id pointer the answer paths fill
// before the echo enters the trigger pipeline, so:
//
//   - a batch echo (RUYI-471) maps to every winner linked to it — one row
//     each, no silent card loss;
//   - a card whose pointer fill failed (the linkage is best-effort) resolves
//     to nothing and the claim degrades explicitly to the pre-RUYI-719
//     behavior — the echo text alone, still delivered as the trigger content;
//   - answered rows are immutable after the CAS (open→answered wins once),
//     so a retried claim of the same task recomputes the identical payload.
//
// Lookup failures degrade the same way, logged — a context enhancement must
// never fail a claim.
func (h *Handler) decisionAnswerContextForComments(ctx context.Context, workspaceID, issueID pgtype.UUID, commentIDs []pgtype.UUID) []DecisionAnswerContextData {
	if len(commentIDs) == 0 {
		return nil
	}
	rows, err := h.Queries.ListIssueDecisionsByAnswerComments(ctx, db.ListIssueDecisionsByAnswerCommentsParams{
		WorkspaceID:      workspaceID,
		IssueID:          issueID,
		AnswerCommentIds: commentIDs,
	})
	if err != nil {
		slog.Warn("decision answer context lookup failed; degrading claim to echo-text only",
			"issue_id", util.UUIDToString(issueID), "error", err)
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	out := make([]DecisionAnswerContextData, 0, len(rows))
	for _, row := range rows {
		options := parseDecisionOptions(row.Options)
		selected := parseDecisionIndices(row.SelectedIndices)
		picks := make([]DecisionAnswerSelection, 0, len(selected))
		for _, idx := range selected {
			if idx < 0 || idx >= len(options) {
				// Rows are only written through validateDecisionAnswer, so an
				// out-of-range index means hand-edited data; skip the row
				// rather than render a wrong option.
				continue
			}
			picks = append(picks, DecisionAnswerSelection{Position: idx + 1, Label: options[idx].Label})
		}
		if len(picks) == 0 {
			continue
		}
		out = append(out, DecisionAnswerContextData{
			DecisionID:   util.UUIDToString(row.ID),
			Question:     row.Question,
			MultiSelect:  row.MultiSelect,
			AnswerSource: row.AnswerSource.String,
			Selected:     picks,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
