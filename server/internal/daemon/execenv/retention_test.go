package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func retentionFixture(t *testing.T) (string, EnvironmentRetention, time.Time) {
	t.Helper()
	root := t.TempDir()
	owner, _ := json.Marshal(EnvRootOwner{TaskID: "task-a", WorkspaceID: "workspace-a"})
	if err := os.WriteFile(filepath.Join(root, ".task_owner"), owner, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	return root, EnvironmentRetention{Owner: "QA", Reason: "candidate verification", Version: strings.Repeat("a", 40), Evidence: "candidate and rebuild recipe", Until: now.Add(time.Hour)}, now
}

func TestEnvironmentRetentionExpiryReleaseAndCompletion(t *testing.T) {
	root, hold, now := retentionFixture(t)
	if _, err := SetEnvironmentRetention(root, "task-a", "workspace-a", hold, false, now); err != nil {
		t.Fatal(err)
	}
	if err := WriteGCMeta(root, GCMeta{Kind: GCKindIssue, TaskID: "task-a", WorkspaceID: "workspace-a"}, discardLogger()); err != nil {
		t.Fatal(err)
	}
	if retained, err := EnvironmentRetained(root, now); err != nil || !retained {
		t.Fatalf("completion erased hold: retained=%v err=%v", retained, err)
		if err := resetEnvRootContents(root); err != nil {
			t.Fatal(err)
		}
	}
	if retained, err := EnvironmentRetained(root, now.Add(time.Hour)); err != nil || retained {
		t.Fatalf("expiry retained=%v err=%v", retained, err)
	}
	if _, err := SetEnvironmentRetention(root, "task-a", "workspace-a", EnvironmentRetention{}, true, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if retained, err := EnvironmentRetained(root, now.Add(2*time.Minute)); err != nil || retained {
		t.Fatalf("release retained=%v err=%v", retained, err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("release deleted environment: %v", err)
	}
}

func TestEnvironmentRetentionRejectsWrongIdentityAndInvalidContract(t *testing.T) {
	root, hold, now := retentionFixture(t)
	for _, tc := range []struct{ task, workspace string }{{"task-b", "workspace-a"}, {"task-a", "workspace-b"}, {"", "workspace-a"}} {
		if _, err := SetEnvironmentRetention(root, tc.task, tc.workspace, hold, false, now); err == nil {
			t.Fatalf("accepted identity %+v", tc)
		}
	}
	for _, mutate := range []func(*EnvironmentRetention){
		func(r *EnvironmentRetention) { r.Until = now },
		func(r *EnvironmentRetention) { r.Version = "short" },
		func(r *EnvironmentRetention) { r.Owner = "" },
		func(r *EnvironmentRetention) { r.Evidence = "" },
	} {
		bad := hold
		mutate(&bad)
		if _, err := SetEnvironmentRetention(root, "task-a", "workspace-a", bad, false, now); err == nil {
			t.Fatalf("accepted invalid hold %+v", bad)
		}
	}
}

func TestEnvironmentRetentionLockAndCorruptRecordFailClosed(t *testing.T) {
	root, hold, now := retentionFixture(t)
	unlock, err := LockEnvironmentRetention(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetEnvironmentRetention(root, "task-a", "workspace-a", hold, false, now); err == nil {
		t.Fatal("concurrent hold bypassed GC lock")
	}
	unlock()
	if err := os.WriteFile(filepath.Join(root, retentionFile), []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if retained, err := EnvironmentRetained(root, now); err == nil || !retained {
		t.Fatalf("corrupt record did not fail closed: retained=%v err=%v", retained, err)
	}
}
