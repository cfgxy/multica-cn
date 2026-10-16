package issuecoordinator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/featureflag"
)

func TestEnabledRequiresOptedInCodexLeaderIssue(t *testing.T) {
	provider := featureflag.NewStaticProvider()
	provider.Set("issue_coordinator_session_poc", featureflag.Rule{Default: true})
	flags := featureflag.NewService(provider)

	if !Enabled(context.Background(), flags, "codex", "workspace", "issue", "agent", true) {
		t.Fatal("opted-in Codex leader task should enable its coordinator")
	}
	for _, tc := range []struct {
		name      string
		provider  string
		workspace string
		issue     string
		agent     string
		isLeader  bool
	}{
		{name: "non-leader", provider: "codex", workspace: "workspace", issue: "issue", agent: "agent"},
		{name: "non-Codex", provider: "claude", workspace: "workspace", issue: "issue", agent: "agent", isLeader: true},
		{name: "no Issue", provider: "codex", workspace: "workspace", agent: "agent", isLeader: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if Enabled(context.Background(), flags, tc.provider, tc.workspace, tc.issue, tc.agent, tc.isLeader) {
				t.Fatal("coordinator enabled outside its opt-in scope")
			}
		})
	}
}

func TestEnabledDefaultsOff(t *testing.T) {
	if Enabled(context.Background(), nil, "codex", "workspace", "issue", "agent", true) {
		t.Fatal("coordinator must remain disabled when its flag is absent")
	}
}

func TestRequiresColdStartForUnsafeContinuityInputs(t *testing.T) {
	for _, tc := range []struct {
		name              string
		runtimeMatches    bool
		sessionAvailable  bool
		state             string
		priorBrief        bool
		resumeUnavailable bool
		want              bool
	}{
		{name: "healthy resume", runtimeMatches: true, sessionAvailable: true, state: "resumable"},
		{name: "runtime switch", sessionAvailable: true, state: "resumable", want: true},
		{name: "session absent", runtimeMatches: true, state: "cold_start", want: true},
		{name: "context overflow", runtimeMatches: true, sessionAvailable: true, state: "resumable", priorBrief: true, want: true},
		{name: "resume rejection", runtimeMatches: true, sessionAvailable: true, state: "resumable", resumeUnavailable: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RequiresColdStart(tc.runtimeMatches, tc.sessionAvailable, tc.state, tc.priorBrief, tc.resumeUnavailable); got != tc.want {
				t.Fatalf("RequiresColdStart() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestMarshalSnapshotContainsBoundedConfirmedReferencesOnly(t *testing.T) {
	snapshot, err := MarshalSnapshot(Snapshot{
		IssueID: "issue-1", IssueRevision: 7, IssueStatus: "in_progress",
		DecisionRefs:      []DecisionRef{{ID: "decision-1", Status: "answered", AnswerCommentID: "comment-1"}},
		CompletedTaskRefs: []string{"task-1"},
	})
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}
	var value Snapshot
	if err := json.Unmarshal(snapshot, &value); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if value.Version != 1 || value.IssueID != "issue-1" || value.IssueRevision != 7 ||
		len(value.DecisionRefs) != 1 || value.DecisionRefs[0].AnswerCommentID != "comment-1" ||
		len(value.CompletedTaskRefs) != 1 || value.CompletedTaskRefs[0] != "task-1" {
		t.Fatalf("unexpected snapshot: %s", snapshot)
	}
	for _, secret := range []string{"transcript", "credential", "private task result"} {
		if strings.Contains(string(snapshot), secret) {
			t.Fatalf("snapshot contains %q: %s", secret, snapshot)
		}
	}
	if len(snapshot) > MaxSnapshotBytes {
		t.Fatalf("snapshot is %d bytes, max is %d", len(snapshot), MaxSnapshotBytes)
	}
}

func TestMarshalSnapshotRejectsUnboundedAndUnidentifiedState(t *testing.T) {
	if _, err := MarshalSnapshot(Snapshot{IssueID: "issue-1", IssueRevision: -1, IssueStatus: "todo"}); err == nil {
		t.Fatal("snapshot with a negative issue revision should fail")
	}
	refs := make([]string, MaxSnapshotRefs+1)
	if _, err := MarshalSnapshot(Snapshot{IssueID: "issue-1", IssueRevision: 1, IssueStatus: "todo", PlanRefs: refs}); err == nil {
		t.Fatal("snapshot exceeding the reference bound should fail")
	}
	if _, err := MarshalSnapshot(Snapshot{IssueID: "issue-1", IssueRevision: 1, IssueStatus: "todo", ResourceRefs: refs}); err == nil {
		t.Fatal("snapshot exceeding the resource reference bound should fail")
	}
	if _, err := MarshalSnapshot(Snapshot{IssueID: "issue-1", IssueRevision: 1, IssueStatus: "todo", PlanRefs: []string{strings.Repeat("x", MaxSnapshotBytes)}}); err == nil {
		t.Fatal("snapshot exceeding the byte budget should fail")
	}
}
