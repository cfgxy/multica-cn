package handler

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/issuecoordinator"
)

func TestIssueCoordinatorSnapshotBriefRestoresStructuredReferences(t *testing.T) {
	raw, err := issuecoordinator.MarshalSnapshot(issuecoordinator.Snapshot{
		IssueID: "issue-1", IssueRevision: 9, IssueStatus: "in_progress",
		DecisionRefs:      []issuecoordinator.DecisionRef{{ID: "decision-1", Status: "answered", AnswerCommentID: "comment-1"}},
		CompletedTaskRefs: []string{"task-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	brief := issueCoordinatorSnapshotBrief(raw)
	for _, want := range []string{"issue-1", "revision 9", "decision-1", "comment-1", "task-1", "read them before treating"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("snapshot brief missing %q: %s", want, brief)
		}
	}
}

func TestIssueCoordinatorSnapshotBriefRejectsMalformedAndOversizedInput(t *testing.T) {
	if got := issueCoordinatorSnapshotBrief([]byte(`{"version":1,"issue_id":"issue-1","issue_status":"todo"}`)); got != "" {
		t.Fatalf("malformed snapshot was rendered: %q", got)
	}
	if got := issueCoordinatorSnapshotBrief(make([]byte, issuecoordinator.MaxSnapshotBytes+1)); got != "" {
		t.Fatalf("oversized snapshot was rendered: %q", got)
	}
}
