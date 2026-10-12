package daemon

import (
	"strings"
	"testing"
)

// RUYI-719: when the server attaches typed decision-card answer context to a
// claim, the comment prompt renders it as the authoritative answer record —
// question text plus each picked option's position and label — so the run
// knows exactly what the human decided without re-reading the comment
// thread. Runs without injected answers keep the prompt byte-identical.

func TestBuildCommentPromptRendersDecisionAnswerContext(t *testing.T) {
	task := Task{
		IssueID:               "issue-1",
		TriggerCommentID:      "comment-1",
		TriggerCommentContent: "[@Agent](mention://agent/x) — decision card answered: \"Ship which day?\"",
		TriggerAuthorType:     "member",
		TriggerAuthorName:     "Alice",
		DecisionAnswers: []DecisionAnswerContextData{
			{
				DecisionID:   "decision-1",
				Question:     "Ship which day?",
				AnswerSource: "card_click",
				Selected: []DecisionAnswerSelection{
					{Position: 2, Label: "Tuesday"},
				},
			},
			{
				DecisionID:   "decision-2",
				Question:     "Enable retries?",
				MultiSelect:  true,
				AnswerSource: "text_token",
				Selected: []DecisionAnswerSelection{
					{Position: 1, Label: "Alpha"},
					{Position: 3, Label: "Gamma"},
				},
			},
		},
	}
	out := buildCommentPrompt(task, "claude")

	for _, want := range []string{
		"Decision card answers",
		"Ship which day?",
		"option 2",
		"Tuesday",
		"Enable retries?",
		"option 1",
		"Alpha",
		"option 3",
		"Gamma",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("buildCommentPrompt decision answer context: want %q in output, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "re-read") {
		t.Errorf("the decision answer block must state its expected usage (no thread re-read needed), got:\n%s", out)
	}
}

func TestBuildCommentPromptOmitsDecisionAnswerSectionWithoutAnswers(t *testing.T) {
	out := buildCommentPrompt(Task{
		IssueID:               "issue-1",
		TriggerCommentID:      "comment-1",
		TriggerCommentContent: "plain comment",
		TriggerAuthorType:     "member",
	}, "claude")
	if strings.Contains(out, "Decision card answers") {
		t.Errorf("decision answer section must be absent without injected answers, got:\n%s", out)
	}
}
