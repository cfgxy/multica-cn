package supervisor

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func retentionTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManager(filepath.Join(t.TempDir(), "runs"))
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}

func seedRun(t *testing.T, mgr *Manager, runID string, man *Manifest, quarantined bool) {
	t.Helper()
	if man == nil {
		// A directory with no manifest at all: half-written or corrupted.
		if err := os.MkdirAll(mgr.Dir(runID), 0o700); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := mgr.WriteManifest(man); err != nil {
		t.Fatal(err)
	}
	if quarantined {
		if err := mgr.WriteQuarantine(&QuarantineRecord{RunID: runID, Unit: man.Unit, Reason: "test", FirstSeen: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
}

func convergedManifest(runID string, convergedAgo time.Duration) *Manifest {
	now := time.Now().UTC().Add(-convergedAgo)
	return &Manifest{
		Version: 1, RunID: runID, TaskID: "task-" + runID, Runtime: "claude",
		Unit: UnitName(runID), State: StateExited,
		StartedAt:   now.Add(-time.Hour),
		Exit:        &ExitRecord{Code: 0, At: now.Add(-time.Minute), Source: ExitSourceLauncher},
		ConvergedAt: &now,
	}
}

func runExists(mgr *Manager, runID string) bool {
	_, err := os.Stat(mgr.Dir(runID))
	return err == nil
}

// TestRetentionGCRemovesExpiredConvergedRuns pins the core contract: a run
// whose output the daemon already consumed (ConvergedAt), with proven exit
// evidence, older than the retention window, on a dead unit, is deleted.
func TestRetentionGCRemovesExpiredConvergedRuns(t *testing.T) {
	mgr := retentionTestManager(t)
	seedRun(t, mgr, "aabbccdd-1-1", convergedManifest("aabbccdd-1-1", 25*time.Hour), false)
	seedRun(t, mgr, "aabbccdd-2-1", convergedManifest("aabbccdd-2-1", 72*time.Hour), false)

	gc := &RetentionGC{Mgr: mgr, Log: slog.Default(), Retain: 24 * time.Hour}
	summary, err := gc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Removed != 2 {
		t.Fatalf("removed = %d (%v), want 2", summary.Removed, summary.RemovedRunIDs)
	}
	if runExists(mgr, "aabbccdd-1-1") || runExists(mgr, "aabbccdd-2-1") {
		t.Fatal("expired converged runs survived GC")
	}
}

// TestRetentionGCKeepsUnqualifiedRuns pins the conservative side: anything
// that has not fully crossed the converged+exit+age bar stays.
func TestRetentionGCKeepsUnqualifiedRuns(t *testing.T) {
	mgr := retentionTestManager(t)

	// Consumed but inside the retention window.
	seedRun(t, mgr, "aabbccdd-3-1", convergedManifest("aabbccdd-3-1", time.Hour), false)
	// Exited and old but never consumed — the daemon may still need the
	// output to converge the run on its next startup pass.
	old := convergedManifest("aabbccdd-4-1", 72 * time.Hour)
	old.ConvergedAt = nil
	seedRun(t, mgr, "aabbccdd-4-1", old, false)
	// Consumed and old but with no exit record — the terminal evidence is
	// the run's story; a consumed manifest without it is odd, keep it.
	noExit := convergedManifest("aabbccdd-5-1", 72*time.Hour)
	noExit.Exit = nil
	seedRun(t, mgr, "aabbccdd-5-1", noExit, false)

	gc := &RetentionGC{Mgr: mgr, Log: slog.Default(), Retain: 24 * time.Hour}
	summary, err := gc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Removed != 0 {
		t.Fatalf("removed = %d (%v), want 0", summary.Removed, summary.RemovedRunIDs)
	}
	for _, id := range []string{"aabbccdd-3-1", "aabbccdd-4-1", "aabbccdd-5-1"} {
		if !runExists(mgr, id) {
			t.Fatalf("%s was removed but does not meet the retention bar", id)
		}
	}
}

// TestRetentionGCKeepsEvidenceItCannotRead pins the "never delete what we
// cannot classify" rule: corrupted manifests and quarantined runs survive.
func TestRetentionGCKeepsEvidenceItCannotRead(t *testing.T) {
	mgr := retentionTestManager(t)

	seedRun(t, mgr, "aabbccdd-6-1", nil, false) // no manifest
	if err := os.WriteFile(filepath.Join(mgr.Dir("aabbccdd-6-1"), "manifest.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedRun(t, mgr, "aabbccdd-7-1", convergedManifest("aabbccdd-7-1", 72*time.Hour), true)

	gc := &RetentionGC{Mgr: mgr, Log: slog.Default(), Retain: 24 * time.Hour}
	summary, err := gc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Removed != 0 {
		t.Fatalf("removed = %d (%v), want 0", summary.Removed, summary.RemovedRunIDs)
	}
	if !runExists(mgr, "aabbccdd-6-1") || !runExists(mgr, "aabbccdd-7-1") {
		t.Fatal("unclassifiable evidence was removed")
	}
}

// TestRetentionGCKeepsLiveUnit runs the extra belt: even a fully qualified
// manifest whose unit systemd still reports active is left alone.
func TestRetentionGCKeepsLiveUnit(t *testing.T) {
	mgr := retentionTestManager(t)
	seedRun(t, mgr, "aabbccdd-8-1", convergedManifest("aabbccdd-8-1", 72*time.Hour), false)

	gc := &RetentionGC{
		Mgr:    mgr,
		Log:    slog.Default(),
		Retain: 24 * time.Hour,
		Units:  &staticUnits{active: map[string]bool{UnitName("aabbccdd-8-1"): true}},
	}
	summary, err := gc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Removed != 0 {
		t.Fatalf("removed = %d (%v), want 0", summary.Removed, summary.RemovedRunIDs)
	}
	if !runExists(mgr, "aabbccdd-8-1") {
		t.Fatal("run on a live unit was removed")
	}
}

// staticUnits is the UnitLister fake for retention tests.
type staticUnits struct {
	active map[string]bool
}

func (s *staticUnits) UnitActive(_ context.Context, unit string) (bool, error) {
	return s.active[unit], nil
}

func (s *staticUnits) ListUnits(_ context.Context) ([]string, error) {
	var units []string
	for u, ok := range s.active {
		if ok {
			units = append(units, u)
		}
	}
	return units, nil
}

func (s *staticUnits) KillUnit(_ context.Context, _ string) error { return nil }

// TestRetentionGCThenReconcile pins the interleaving contract: after GC
// removes a consumed run, a reconcile pass neither sees nor trips over it,
// and reconciling first (marking a run converged) does not make it
// GC-eligible until the retention window passes.
func TestRetentionGCThenReconcile(t *testing.T) {
	mgr := retentionTestManager(t)
	// This run is converged and past retention: reconcile skips it
	// (ConvergedAt set), GC removes it.
	seedRun(t, mgr, "aabbccdd-9-1", convergedManifest("aabbccdd-9-1", 72*time.Hour), false)
	// This run was just consumed: reconcile skips it and GC keeps it.
	seedRun(t, mgr, "aabbccdd-10-1", convergedManifest("aabbccdd-10-1", time.Second), false)

	units := &staticUnits{active: map[string]bool{}}
	gc := &RetentionGC{Mgr: mgr, Units: units, Log: slog.Default(), Retain: 24 * time.Hour}
	if _, err := gc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runExists(mgr, "aabbccdd-9-1") {
		t.Fatal("expired converged run survived GC")
	}
	if !runExists(mgr, "aabbccdd-10-1") {
		t.Fatal("freshly converged run was removed inside the retention window")
	}

	// A reconcile pass over the post-GC root completes without error and
	// does not resurrect or quarantine anything from the removed run.
	rec := &Reconciler{Mgr: mgr, Units: units, Log: slog.Default()}
	if _, err := rec.Run(context.Background()); err != nil {
		t.Fatalf("reconcile after GC: %v", err)
	}
}
