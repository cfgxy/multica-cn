package issuecoordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func TestSnapshotContainsOnlyTaskReferenceAndDigest(t *testing.T) {
	result := []byte("private task result")
	snapshot, err := Snapshot("task-1", result)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var value map[string]string
	if err := json.Unmarshal(snapshot, &value); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	sum := sha256.Sum256(result)
	if value["last_task_id"] != "task-1" || value["result_sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected snapshot: %s", snapshot)
	}
	if string(snapshot) == string(result) {
		t.Fatal("snapshot contains raw task result")
	}
}
