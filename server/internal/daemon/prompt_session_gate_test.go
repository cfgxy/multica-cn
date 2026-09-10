package daemon

import (
	"strings"
	"testing"
)

const testPriorContextBrief = "## Prior Session Context\n\nYour earlier session grew close to its limit.\n\n### Unresolved threads\n\n- `abcd1234` (Bohan, 2026-09-08T10:00:00Z, 3 replies): still broken\n"

// The brief must reach the agent, and it must land in the per-turn message
// rather than the cached brief prefix — it is per-run data, so rendering it in
// messages[0] would invalidate the prompt cache for the whole history on every
// run (MUL-5377).
func TestPriorContextBriefReachesPerTurnMessage(t *testing.T) {
	t.Parallel()

	prompt := BuildPrompt(Task{
		IssueID:               "issue-1",
		TriggerCommentID:      "comment-1",
		TriggerCommentContent: "please continue",
		PriorContextBrief:     testPriorContextBrief,
	}, "claude")

	for _, want := range []string{
		"## Prior Session Context",
		"### Unresolved threads",
		"still broken",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("per-turn prompt lost prior context brief content %q\n---\n%s", want, prompt)
		}
	}
}

// The brief and the continuity notice make contradictory claims — "here is what
// carried over" vs "nothing carried over". A prompt containing both gives the
// agent no way to decide which to believe, so exactly one must render.
func TestPriorContextBriefSuppressesContinuityNotice(t *testing.T) {
	t.Parallel()

	prompt := BuildPrompt(Task{
		IssueID:               "issue-1",
		TriggerCommentID:      "comment-1",
		TriggerCommentContent: "please continue",
		PriorContextBrief:     testPriorContextBrief,
		// A server that sent both would be buggy; the daemon still must not
		// produce a self-contradicting prompt because of it.
		PriorSessionResumeUnavailable: true,
	}, "claude")

	if !strings.Contains(prompt, "## Prior Session Context") {
		t.Fatalf("brief must win over the notice, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "## Session Continuity Notice") {
		t.Errorf("continuity notice must be suppressed when a brief is present, got:\n%s", prompt)
	}
}

// Without a brief the pre-existing behavior is unchanged: the notice still
// renders on its own precondition.
func TestContinuityNoticeUnaffectedWithoutBrief(t *testing.T) {
	t.Parallel()

	prompt := BuildPrompt(Task{
		IssueID:                       "issue-1",
		TriggerCommentID:              "comment-1",
		TriggerCommentContent:         "please continue",
		PriorSessionResumeUnavailable: true,
	}, "claude")

	if !strings.Contains(prompt, "## Session Continuity Notice") {
		t.Errorf("notice must still render when no brief is present, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "## Prior Session Context") {
		t.Errorf("no brief was supplied, so none may be rendered, got:\n%s", prompt)
	}
}

// A run with no gate decision at all must gain nothing.
func TestNoBriefAndNoNoticeByDefault(t *testing.T) {
	t.Parallel()

	prompt := BuildPrompt(Task{IssueID: "issue-1"}, "claude")
	for _, unwanted := range []string{"## Prior Session Context", "## Session Continuity Notice"} {
		if strings.Contains(prompt, unwanted) {
			t.Errorf("unconditional prompt must not contain %q\n---\n%s", unwanted, prompt)
		}
	}
}

// backendResumeContinuityNotice feeds the backend's own resume-notice channel.
// It already suppresses itself when the prompt carries the notice; it must do
// the same when the prompt carries a brief, or the agent hears the same fact
// twice in two voices.
func TestBackendResumeContinuityNoticeSuppressedByBrief(t *testing.T) {
	t.Parallel()

	if got := backendResumeContinuityNotice(Task{IssueID: "issue-1", PriorContextBrief: testPriorContextBrief}); got != "" {
		t.Errorf("backend notice must be empty when a brief is present, got %q", got)
	}
	// Whitespace-only is not a brief.
	if got := backendResumeContinuityNotice(Task{IssueID: "issue-1", PriorContextBrief: "   \n"}); got == "" {
		t.Error("a blank brief must not suppress the backend notice")
	}
}
