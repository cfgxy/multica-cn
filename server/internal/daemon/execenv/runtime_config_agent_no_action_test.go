package execenv

import (
	"strings"
	"testing"
)

func TestOrdinaryIssueTaskCanRecordNoActionAndExitSilently(t *testing.T) {
	t.Parallel()

	ctx := TaskContextForEnv{IssueID: "issue-1", TriggerCommentID: "comment-1"}
	brief := buildMetaSkillContent("claude", ctx)
	for _, want := range []string{
		"multica issue no-action <issue-id> --reason",
		"you MUST exit without posting any comment",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("ordinary issue brief missing %q\n---\n%s", want, brief)
		}
	}

	reply := BuildCommentReplyInstructions("claude", "issue-1", "comment-1", false)
	if !strings.Contains(reply, "multica issue no-action issue-1 --reason") {
		t.Fatalf("ordinary per-turn reply instructions missing no-action exit\n---\n%s", reply)
	}

	multi := BuildMultiThreadCommentReplyInstructions("issue-1", []ThreadReplyTarget{
		{ThreadID: "thread-1", ParentID: "comment-1"},
		{ThreadID: "thread-2", ParentID: "comment-2"},
	}, false)
	if !strings.Contains(multi, "multica issue no-action issue-1 --reason") || !strings.Contains(multi, "skip this ENTIRE fan-out block") {
		t.Fatalf("ordinary multi-thread instructions missing no-action exit\n---\n%s", multi)
	}
}
