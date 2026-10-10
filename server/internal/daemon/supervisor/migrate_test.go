package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedLegacyRun writes a pre-RUYI-607 shared-store run directory: a flat
// run-id directory holding manifest.json, ownerless unless stated.
func seedLegacyRun(t *testing.T, root, runID, taskID, owner string) {
	t.Helper()
	man := &Manifest{
		Version: 1, RunID: runID, TaskID: taskID, Runtime: "claude",
		Unit: UnitName(runID), State: StateRunning, StartedAt: time.Now().UTC(), Owner: owner,
	}
	if err := writeJSONFile(filepath.Join(root, runID, "manifest.json"), man, 0o600); err != nil {
		t.Fatalf("seed legacy run %s: %v", runID, err)
	}
}

func readLegacyManifest(t *testing.T, root, runID string) *Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, runID, "manifest.json"))
	if err != nil {
		t.Fatalf("legacy manifest %s gone: %v", runID, err)
	}
	var man Manifest
	if err := json.Unmarshal(data, &man); err != nil {
		t.Fatalf("legacy manifest %s: %v", runID, err)
	}
	return &man
}

// The in-flight set of this daemon's server is the only ownership evidence
// a pre-owner legacy manifest carries: a task in flight on my runtimes was
// necessarily launched by me, so exactly those runs move into my namespaced
// store; everything else stays put and is never killed (RUYI-607 acceptance
// 2: the upgrade window neither mis-kills nor drops tracking).
func TestMigrateLegacyStoreMovesOwnedInFlightRuns(t *testing.T) {
	legacy := t.TempDir()
	mgr, _ := NewManager(t.TempDir())
	const id = "11110000-0000-0000-0000-000000000001"
	seedLegacyRun(t, legacy, id, "task-a", "")
	res, err := MigrateLegacyStore(legacy, mgr, "dev1", func(taskID string) bool { return taskID == "task-a" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 1 || res.Moved[0] != id || len(res.Left) != 0 {
		t.Fatalf("migration result = %+v, want exactly the owned run moved", res)
	}
	man, err := mgr.ReadManifest(id)
	if err != nil {
		t.Fatalf("adopted run missing from namespaced store: %v", err)
	}
	if man.Owner != "dev1" {
		t.Fatalf("adopted run owner = %q, want dev1", man.Owner)
	}
	if _, err := os.Stat(filepath.Join(legacy, id)); !os.IsNotExist(err) {
		t.Fatalf("legacy dir must be gone after adoption, stat err = %v", err)
	}
}

func TestMigrateLegacyStoreLeavesForeignAndUnknownRuns(t *testing.T) {
	legacy := t.TempDir()
	mgr, _ := NewManager(t.TempDir())
	const unknown = "22220000-0000-0000-0000-000000000002"
	const foreign = "33330000-0000-0000-0000-000000000003"
	seedLegacyRun(t, legacy, unknown, "task-unknown", "")
	seedLegacyRun(t, legacy, foreign, "task-foreign", "prod")
	res, err := MigrateLegacyStore(legacy, mgr, "dev1", func(string) bool { return false }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 0 || len(res.Left) != 2 {
		t.Fatalf("migration result = %+v, want both runs left", res)
	}
	readLegacyManifest(t, legacy, unknown)
	readLegacyManifest(t, legacy, foreign)
	if _, err := mgr.ReadManifest(unknown); err == nil {
		t.Fatal("unknown-ownership run must not enter the namespaced store")
	}
	if _, err := mgr.ReadManifest(foreign); err == nil {
		t.Fatal("foreign-owned run must not enter the namespaced store")
	}
}

func TestMigrateLegacyStoreNilOwnsMovesNothing(t *testing.T) {
	legacy := t.TempDir()
	mgr, _ := NewManager(t.TempDir())
	const id = "44440000-0000-0000-0000-000000000004"
	seedLegacyRun(t, legacy, id, "task-a", "")
	res, err := MigrateLegacyStore(legacy, mgr, "dev1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 0 || len(res.Left) != 1 {
		t.Fatalf("nil owns must move nothing, got %+v", res)
	}
}

func TestMigrateLegacyStoreIgnoresNonRunEntries(t *testing.T) {
	legacy := t.TempDir()
	mgr, _ := NewManager(t.TempDir())
	// A namespace-shaped dir without a top-level manifest (a namespaced
	// store root, or a quarantine-only record) is not a run and stays
	// invisible to migration.
	if err := os.MkdirAll(filepath.Join(legacy, "default", "55550000-0000-0000-0000-000000000005"), 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := MigrateLegacyStore(legacy, mgr, "dev1", func(string) bool { return true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 0 || len(res.Left) != 0 {
		t.Fatalf("non-run entries must be invisible to migration, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(legacy, "default")); err != nil {
		t.Fatalf("legacy tree must be untouched: %v", err)
	}
}

func TestMigrateLegacyStoreKeepsExistingNamespacedRun(t *testing.T) {
	legacy := t.TempDir()
	mgr, _ := NewManager(t.TempDir())
	const id = "66660000-0000-0000-0000-000000000006"
	seedLegacyRun(t, legacy, id, "task-b", "")
	if err := mgr.WriteManifest(&Manifest{
		Version: 1, RunID: id, TaskID: "task-already-here", Runtime: "claude",
		Unit: UnitName(id), State: StateRunning, StartedAt: time.Now().UTC(), Owner: "dev1",
	}); err != nil {
		t.Fatal(err)
	}
	res, err := MigrateLegacyStore(legacy, mgr, "dev1", func(string) bool { return true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 0 || len(res.Left) != 1 {
		t.Fatalf("existing namespaced run must not be clobbered, got %+v", res)
	}
	man, err := mgr.ReadManifest(id)
	if err != nil || man.TaskID != "task-already-here" {
		t.Fatalf("namespaced manifest was overwritten: %+v err %v", man, err)
	}
	readLegacyManifest(t, legacy, id)
}

func TestMigrateLegacyStoreLeavesUnreadableManifest(t *testing.T) {
	legacy := t.TempDir()
	mgr, _ := NewManager(t.TempDir())
	const id = "77770000-0000-0000-0000-000000000007"
	if err := os.MkdirAll(filepath.Join(legacy, id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, id, "manifest.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := MigrateLegacyStore(legacy, mgr, "dev1", func(string) bool { return true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 0 || len(res.Left) != 1 {
		t.Fatalf("unreadable evidence must be left, got %+v", res)
	}
}
