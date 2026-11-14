package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestGCApplyHonorsExpiringEnvironmentDependency(t *testing.T) {
	d := newGCTestDaemon(t, nil)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "workspace", "task-retained", &execenv.GCMeta{
		Kind: execenv.GCKindIssue, TaskID: "retained", WorkspaceID: "workspace", IssueID: "issue",
		CompletedAt: time.Now().Add(-48 * time.Hour),
	})
	now := time.Now()
	hold := execenv.EnvironmentRetention{Owner: "QA", Reason: "remaining verification", Version: strings.Repeat("b", 40), Evidence: "candidate and rebuild inputs", Until: now.Add(time.Hour)}
	if _, err := execenv.SetEnvironmentRetention(root, "retained", "workspace", hold, false, now); err != nil {
		t.Fatal(err)
	}
	stats := &gcStats{}
	if got := d.applyGCAction(root, gcActionClean, stats); got != 0 {
		t.Fatalf("GC removed held dependency: %d", got)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("held environment missing: %v", err)
	}
	if _, err := execenv.SetEnvironmentRetention(root, "retained", "workspace", execenv.EnvironmentRetention{}, true, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := d.applyGCAction(root, gcActionClean, &gcStats{}); got != 1 {
		t.Fatalf("GC did not reclaim released environment: %d", got)
	}
}

func TestSnapshotReapingHonorsRetentionAndActiveReservation(t *testing.T) {
	d := newGCTestDaemon(t, nil)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "workspace", "task-retained", nil)
	tree := filepath.Join(execenv.SnapshotRoot(root), "expired-tree")
	if err := os.MkdirAll(filepath.Join(tree, "upper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := execenv.RecordSnapshotStart(root, execenv.SnapshotLedgerEntry{TaskID: "retained", Root: tree, Primary: tree}); err != nil {
		t.Fatal(err)
	}
	if err := execenv.FinalizeSnapshot(root, "retained", true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(8 * 24 * time.Hour)
	hold := execenv.EnvironmentRetention{Owner: "QA", Reason: "verification", Version: strings.Repeat("a", 40), Evidence: "rebuild", Until: now.Add(time.Hour)}
	if _, err := execenv.SetEnvironmentRetention(root, "retained", "workspace", hold, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if reaped := d.reapSnapshotsWithRetention(root, now); len(reaped) != 0 {
		t.Fatalf("reaped retained snapshot: %v", reaped)
	}
	if _, err := execenv.SetEnvironmentRetention(root, "retained", "workspace", execenv.EnvironmentRetention{}, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	release, ok := d.reserveEnvRootForGC(root)
	if !ok {
		t.Fatal("reserve root")
	}
	if reaped := d.reapSnapshotsWithRetention(root, now); len(reaped) != 0 {
		t.Fatalf("reaped active root: %v", reaped)
	}
	release()
	if reaped := d.reapSnapshotsWithRetention(root, now); len(reaped) == 0 {
		t.Fatal("released snapshot was not reaped")
	}
}
