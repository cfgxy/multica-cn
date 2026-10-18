package handler

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/issuecoordinator"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
	if got := issueCoordinatorSnapshotBrief([]byte(`{"version":1,"issue_id":"issue-1","issue_revision":9,"issue_status":""}`)); got != "" {
		t.Fatalf("snapshot missing issue status was rendered: %q", got)
	}
	if got := issueCoordinatorSnapshotBrief([]byte(`{"version":1,"issue_id":"issue-1","issue_status":"todo"`)); got != "" {
		t.Fatalf("malformed snapshot was rendered: %q", got)
	}
	if got := issueCoordinatorSnapshotBrief(make([]byte, issuecoordinator.MaxSnapshotBytes+1)); got != "" {
		t.Fatalf("oversized snapshot was rendered: %q", got)
	}
}

func TestIssueCoordinatorColdStartDropsUnsafeSessionAndKeepsSnapshotBrief(t *testing.T) {
	raw, err := issuecoordinator.MarshalSnapshot(issuecoordinator.Snapshot{
		IssueID: "issue-1", IssueRevision: 9, IssueStatus: "in_progress",
		EvidenceRefs: []string{"comment-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := AgentTaskResponse{
		PriorSessionID:                "unsafe-session",
		PriorSessionResumeUnavailable: true,
		PriorContextBrief:             "continuity handoff",
	}
	(&Handler{}).applyIssueCoordinatorColdStartBrief(
		t.Context(), &resp, db.AgentTaskQueue{}, "", db.GetIssueCoordinatorSessionRow{
			Generation: 2, SnapshotVersion: 3, LastEventSequence: 5, Snapshot: raw,
		},
	)

	if resp.PriorSessionID != "" {
		t.Fatalf("unsafe prior session = %q, want empty", resp.PriorSessionID)
	}
	if resp.PriorSessionResumeUnavailable {
		t.Fatal("cold start brief must consume the resume-unavailable signal")
	}
	for _, want := range []string{"Confirmed Issue snapshot", "issue-1", "comment-1", "continuity handoff", "generation 2"} {
		if !strings.Contains(resp.PriorContextBrief, want) {
			t.Fatalf("cold start brief missing %q: %s", want, resp.PriorContextBrief)
		}
	}
}
