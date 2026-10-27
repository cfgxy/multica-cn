package execenv

import (
	"strings"
	"testing"
)

// RUYI-286: quiz runs have no issue; issue_context.md must carry the item
// under test verbatim instead of the empty-issue assignment frame.
func TestRenderIssueContextQuizCarriesItem(t *testing.T) {
	item := "Which system prompt line makes the agent refuse unsafe edits?"
	content := renderIssueContext("claude", TaskContextForEnv{QuizPrompt: item})

	if !strings.Contains(content, item) {
		t.Fatalf("issue_context.md missing the item under test:\n%s", content)
	}
	if strings.Contains(content, "**Issue ID:**") {
		t.Errorf("quiz context must not render the empty-issue assignment frame:\n%s", content)
	}
}

// Non-quiz contexts keep the existing assignment frame untouched.
func TestRenderIssueContextIssueFrameUnchangedByQuizBranch(t *testing.T) {
	content := renderIssueContext("claude", TaskContextForEnv{IssueID: "RUYI-1"})
	if !strings.Contains(content, "**Issue ID:** RUYI-1") {
		t.Fatalf("issue assignment frame changed:\n%s", content)
	}
}
