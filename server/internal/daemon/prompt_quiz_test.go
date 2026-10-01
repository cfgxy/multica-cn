package daemon

import (
	"strings"
	"testing"
)

// RUYI-286: a quiz task's assignment is the item under test. The claim
// response carries it as quiz_prompt; the opening prompt must show that text
// verbatim and must not fall through to the issue-assignment frame, which
// renders an empty issue ID for quiz tasks (they have none by construction).
func TestBuildPromptBodyQuizCarriesItemVerbatim(t *testing.T) {
	item := "当用户说\"帮我看看\"时应该先做什么？\n请给出可执行的第一步。"
	body := buildPromptBody(Task{QuizPrompt: item}, "claude")

	if !strings.Contains(body, item) {
		t.Fatalf("quiz prompt missing the item under test:\n%s", body)
	}
	if strings.Contains(body, "Your assigned issue ID") {
		t.Errorf("quiz prompt fell through to the issue-assignment frame:\n%s", body)
	}
	if strings.Contains(body, "quick-create") {
		t.Errorf("quiz prompt must not render the quick-create channel:\n%s", body)
	}
	// The grader reads the run's last text message as the answer, so the fixed
	// frame has to name the final message as the answer channel.
	if !strings.Contains(strings.ToLower(body), "final") {
		t.Errorf("quiz prompt must point the answer at the final text message:\n%s", body)
	}
}
