package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ledgerEntryForTest builds one entry whose tree root is
// <envRoot>/local-snapshots/<stem>, the same layout PlanSnapshotTree produces
// (tree root = the stem dir containing pristine/upper/work).
func ledgerEntryForTest(envRoot, taskID, stem, state string, createdAt time.Time) SnapshotLedgerEntry {
	base := filepath.Join(SnapshotRoot(envRoot), stem)
	return SnapshotLedgerEntry{
		TaskID:    taskID,
		IssueID:   "RUYI-TEST",
		Root:      envRoot,
		Primary:   filepath.Join(envRoot, "user-dir"),
		Units:     []SnapshotUnitSpec{{Target: filepath.Join(envRoot, "user-dir"), Alias: filepath.Join(base, "pristine"), Upper: filepath.Join(base, "upper"), Work: filepath.Join(base, "work")}},
		CreatedAt: createdAt,
		State:     state,
	}
}

func seedLedger(t *testing.T, envRoot string, entries []SnapshotLedgerEntry) {
	t.Helper()
	if err := recordSnapshotLedger(envRoot, func(led *snapshotLedger) {
		led.Entries = append(led.Entries, entries...)
	}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
}

// multiUnitEntryForTest builds one entry with a unit — and therefore a stem
// tree — per declared subdir, the layout a multi-subdirectory snapshot run
// produces.
func multiUnitEntryForTest(envRoot, taskID string, stems []string, state string, createdAt time.Time) SnapshotLedgerEntry {
	entry := SnapshotLedgerEntry{
		TaskID:    taskID,
		IssueID:   "RUYI-TEST",
		Root:      envRoot,
		Primary:   filepath.Join(envRoot, "user-dir"),
		CreatedAt: createdAt,
		State:     state,
	}
	for _, stem := range stems {
		base := filepath.Join(SnapshotRoot(envRoot), stem)
		entry.Units = append(entry.Units, SnapshotUnitSpec{
			Target: filepath.Join(envRoot, "user-dir", filepath.FromSlash(stem)),
			Alias:  filepath.Join(base, "pristine"),
			Upper:  filepath.Join(base, "upper"),
			Work:   filepath.Join(base, "work"),
		})
	}
	return entry
}

func makeStemDirs(t *testing.T, envRoot string, stems ...string) {
	t.Helper()
	for _, stem := range stems {
		if err := os.MkdirAll(filepath.Join(SnapshotRoot(envRoot), stem, "upper"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func readLedgerEntries(t *testing.T, envRoot string) []SnapshotLedgerEntry {
	t.Helper()
	raw, err := os.ReadFile(SnapshotLedgerPath(envRoot))
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var led snapshotLedger
	if err := json.Unmarshal(raw, &led); err != nil {
		t.Fatalf("parse ledger: %v", err)
	}
	return led.Entries
}

func TestRecordSnapshotStartForcesActiveState(t *testing.T) {
	t.Parallel()
	envRoot := t.TempDir()
	err := RecordSnapshotStart(envRoot, SnapshotLedgerEntry{
		TaskID:  "t1",
		Root:    envRoot,
		Primary: filepath.Join(envRoot, "u"),
		State:   "whatever-a-buggy-caller-passed",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	entries := readLedgerEntries(t, envRoot)
	if len(entries) != 1 || entries[0].State != "active" {
		t.Fatalf("expected exactly one active entry, got %+v", entries)
	}
}

func TestFinalizeSnapshotKeepFalseRemovesEntryAndKeepTrueMarksFailed(t *testing.T) {
	t.Parallel()
	envRoot := t.TempDir()
	if err := RecordSnapshotStart(envRoot, SnapshotLedgerEntry{TaskID: "t1", Root: envRoot}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := FinalizeSnapshot(envRoot, "t1", false); err != nil {
		t.Fatalf("finalize keep=false: %v", err)
	}
	if entries := readLedgerEntries(t, envRoot); len(entries) != 0 {
		t.Fatalf("keep=false must drop the entry, got %+v", entries)
	}

	// keep=true preserves the record as failed — the inspection pointer for a
	// run whose upper layer is worth reading. Finalizing an unknown task (or
	// twice) must stay a no-op, because the success path finalizes before the
	// deferred keep=true runs.
	if err := RecordSnapshotStart(envRoot, SnapshotLedgerEntry{TaskID: "t2", Root: envRoot}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := FinalizeSnapshot(envRoot, "t2", true); err != nil {
		t.Fatalf("finalize keep=true: %v", err)
	}
	if err := FinalizeSnapshot(envRoot, "t2", true); err != nil {
		t.Fatalf("second finalize keep=true: %v", err)
	}
	if err := FinalizeSnapshot(envRoot, "t-unknown", true); err != nil {
		t.Fatalf("finalize unknown task: %v", err)
	}
	entries := readLedgerEntries(t, envRoot)
	if len(entries) != 1 || entries[0].TaskID != "t2" || entries[0].State != "failed" {
		t.Fatalf("expected t2 flipped to failed exactly once, got %+v", entries)
	}
}

// TestFinalizeSnapshotKeepFalseDeletesAllUnitTrees pins the success path's
// disk contract: keep=false removes the record AND every unit's stem tree
// (each declared subdirectory has its own), keep=true preserves the record
// and leaves every tree on disk for inspection.
func TestFinalizeSnapshotKeepFalseDeletesAllUnitTrees(t *testing.T) {
	t.Parallel()
	envRoot := t.TempDir()

	entry := multiUnitEntryForTest(envRoot, "t1", []string{"i1-a", "i1-b"}, snapshotStateActive, time.Now())
	if err := RecordSnapshotStart(envRoot, entry); err != nil {
		t.Fatalf("record: %v", err)
	}
	makeStemDirs(t, envRoot, "i1-a", "i1-b")
	if err := FinalizeSnapshot(envRoot, "t1", false); err != nil {
		t.Fatalf("finalize keep=false: %v", err)
	}
	if entries := readLedgerEntries(t, envRoot); len(entries) != 0 {
		t.Fatalf("keep=false must drop the entry, got %+v", entries)
	}
	for _, stem := range []string{"i1-a", "i1-b"} {
		if _, err := os.Stat(filepath.Join(SnapshotRoot(envRoot), stem)); !os.IsNotExist(err) {
			t.Errorf("keep=false must delete every unit's tree, %s stat err=%v", stem, err)
		}
	}

	kept := multiUnitEntryForTest(envRoot, "t2", []string{"i2-a", "i2-b"}, snapshotStateActive, time.Now())
	if err := RecordSnapshotStart(envRoot, kept); err != nil {
		t.Fatalf("record: %v", err)
	}
	makeStemDirs(t, envRoot, "i2-a", "i2-b")
	if err := FinalizeSnapshot(envRoot, "t2", true); err != nil {
		t.Fatalf("finalize keep=true: %v", err)
	}
	entries := readLedgerEntries(t, envRoot)
	if len(entries) != 1 || entries[0].TaskID != "t2" || entries[0].State != snapshotStateFailed {
		t.Fatalf("keep=true must flip the entry to failed, got %+v", entries)
	}
	for _, stem := range []string{"i2-a", "i2-b"} {
		if _, err := os.Stat(filepath.Join(SnapshotRoot(envRoot), stem)); err != nil {
			t.Errorf("keep=true must leave every tree on disk, %s: %v", stem, err)
		}
	}
}

func TestReapExpiredSnapshotsLedgerDriven(t *testing.T) {
	t.Parallel()
	envRoot := t.TempDir()
	now := time.Now()

	// failed past retention → tree and record released
	staleFailed := ledgerEntryForTest(envRoot, "t-stale-failed", "i1-stale-failed", "failed", now.Add(-8*24*time.Hour))
	// failed inside retention → kept and claimed against the orphan sweep
	freshFailed := ledgerEntryForTest(envRoot, "t-fresh-failed", "i1-fresh-failed", "failed", now.Add(-1*time.Hour))
	// active inside the orphan window → claimed, never reaped (an entry this
	// young under a live daemon is a task that may still be running)
	active := ledgerEntryForTest(envRoot, "t-active", "i1-active", "active", now.Add(-1*time.Hour))
	seedLedger(t, envRoot, []SnapshotLedgerEntry{staleFailed, freshFailed, active})

	// crash leftover: directory on disk no ledger entry claims, past the
	// orphan age → reaped purely by age
	orphanOld := filepath.Join(SnapshotRoot(envRoot), "i1-orphan-old")
	if err := os.MkdirAll(filepath.Join(orphanOld, "upper"), 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := now.Add(-30 * time.Hour)
	if err := os.Chtimes(orphanOld, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	// fresh orphan → the age bound keeps a concurrent prepare out of the sweep
	orphanFresh := filepath.Join(SnapshotRoot(envRoot), "i1-orphan-fresh")
	if err := os.MkdirAll(filepath.Join(orphanFresh, "upper"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Stem directories for the ledger-driven trees: the ledger records
	// ownership, but the directories on disk are what the sweep deletes.
	for _, stem := range []string{"i1-stale-failed", "i1-fresh-failed", "i1-active"} {
		if err := os.MkdirAll(filepath.Join(SnapshotRoot(envRoot), stem, "upper"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	reaped := ReapExpiredSnapshots(envRoot, now)
	reapedSet := map[string]bool{}
	for _, p := range reaped {
		reapedSet[p] = true
	}

	staleFailedRoot := filepath.Join(SnapshotRoot(envRoot), "i1-stale-failed")
	freshFailedRoot := filepath.Join(SnapshotRoot(envRoot), "i1-fresh-failed")
	activeRoot := filepath.Join(SnapshotRoot(envRoot), "i1-active")

	if !reapedSet[staleFailedRoot] {
		t.Errorf("stale failed tree must be reaped, reaped=%v", reaped)
	}
	if _, err := os.Stat(staleFailedRoot); !os.IsNotExist(err) {
		t.Errorf("stale failed tree must be deleted from disk, stat err=%v", err)
	}
	if reapedSet[freshFailedRoot] {
		t.Errorf("failed tree inside the retention window must be kept")
	}
	if _, err := os.Stat(freshFailedRoot); err != nil {
		t.Errorf("fresh failed tree must survive on disk: %v", err)
	}
	if reapedSet[activeRoot] {
		t.Errorf("active trees are claimed by their ledger entry and must never be reaped")
	}
	if _, err := os.Stat(activeRoot); err != nil {
		t.Errorf("active tree must survive on disk: %v", err)
	}
	if !reapedSet[orphanOld] {
		t.Errorf("old crash-orphan directory must be reaped, reaped=%v", reaped)
	}
	if _, err := os.Stat(orphanFresh); err != nil {
		t.Errorf("fresh orphan must survive the age bound: %v", err)
	}

	entries := readLedgerEntries(t, envRoot)
	states := map[string]string{}
	for _, e := range entries {
		states[e.TaskID] = e.State
	}
	if states["t-stale-failed"] != "" {
		t.Errorf("reaped failed entry must leave no record, got %+v", states)
	}
	if states["t-fresh-failed"] != "failed" || states["t-active"] != "active" {
		t.Errorf("fresh failed and active entries must survive the sweep, got %+v", states)
	}
}

func TestReapExpiredSnapshotsWithoutSnapshotRoot(t *testing.T) {
	t.Parallel()
	// An env root that never hosted a snapshot must be a cheap no-op — the
	// GC calls this for every task directory every cycle.
	reaped := ReapExpiredSnapshots(t.TempDir(), time.Now())
	if len(reaped) != 0 {
		t.Fatalf("expected no reaps, got %v", reaped)
	}
}

// TestReapExpiredSnapshotsKeepsUnexpiredFailedPastOrphanAge is the F3
// regression: a failed run 3 days into its 7-day preservation window, whose
// tree's mtime is already past the 24h orphan age. The orphan sweep must not
// touch it — the ledger still claims the tree, and the claim outranks the
// mtime.
func TestReapExpiredSnapshotsKeepsUnexpiredFailedPastOrphanAge(t *testing.T) {
	t.Parallel()
	envRoot := t.TempDir()
	now := time.Now()
	entry := ledgerEntryForTest(envRoot, "t-failed", "i1-failed", snapshotStateFailed, now.Add(-3*24*time.Hour))
	seedLedger(t, envRoot, []SnapshotLedgerEntry{entry})
	stem := filepath.Join(SnapshotRoot(envRoot), "i1-failed")
	makeStemDirs(t, envRoot, "i1-failed")
	stamp := now.Add(-3 * 24 * time.Hour)
	if err := os.Chtimes(stem, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	reaped := ReapExpiredSnapshots(envRoot, now)
	if len(reaped) != 0 {
		t.Fatalf("unexpired failed tree must not be reaped, reaped=%v", reaped)
	}
	if _, err := os.Stat(stem); err != nil {
		t.Fatalf("unexpired failed tree must survive on disk: %v", err)
	}
	entries := readLedgerEntries(t, envRoot)
	if len(entries) != 1 || entries[0].State != snapshotStateFailed {
		t.Fatalf("failed record must survive, got %+v", entries)
	}
}

// TestReapExpiredSnapshotsReclaimsDeadDaemonActiveEntry is the F4 contract:
// the GC only scans env roots no live task holds, so an "active" entry older
// than the orphan age can only be a crashed daemon's leftover — reclaim its
// trees and drop the record.
func TestReapExpiredSnapshotsReclaimsDeadDaemonActiveEntry(t *testing.T) {
	t.Parallel()
	envRoot := t.TempDir()
	now := time.Now()
	entry := multiUnitEntryForTest(envRoot, "t-dead", []string{"i1-dead-a", "i1-dead-b"}, snapshotStateActive, now.Add(-25*time.Hour))
	seedLedger(t, envRoot, []SnapshotLedgerEntry{entry})
	makeStemDirs(t, envRoot, "i1-dead-a", "i1-dead-b")

	reaped := ReapExpiredSnapshots(envRoot, now)
	reapedSet := map[string]bool{}
	for _, p := range reaped {
		reapedSet[p] = true
	}
	for _, stem := range []string{"i1-dead-a", "i1-dead-b"} {
		want := filepath.Join(SnapshotRoot(envRoot), stem)
		if !reapedSet[want] {
			t.Errorf("dead daemon stem %s must be reclaimed, reaped=%v", stem, reaped)
		}
		if _, err := os.Stat(want); !os.IsNotExist(err) {
			t.Errorf("dead daemon stem %s must be deleted, stat err=%v", stem, err)
		}
	}
	if entries := readLedgerEntries(t, envRoot); len(entries) != 0 {
		t.Fatalf("dead daemon record must be dropped, got %+v", entries)
	}
}

// TestReapExpiredSnapshotsReapsEveryUnitStem pins the multi-unit sweep: a
// task that declared three subdirectories owns three stem trees, and expiry
// reclaims all of them — not just the first unit's.
func TestReapExpiredSnapshotsReapsEveryUnitStem(t *testing.T) {
	t.Parallel()
	envRoot := t.TempDir()
	now := time.Now()
	entry := multiUnitEntryForTest(envRoot, "t-multi", []string{"i1-a", "i1-b", "i1-c"}, snapshotStateFailed, now.Add(-8*24*time.Hour))
	seedLedger(t, envRoot, []SnapshotLedgerEntry{entry})
	makeStemDirs(t, envRoot, "i1-a", "i1-b", "i1-c")

	reaped := ReapExpiredSnapshots(envRoot, now)
	if len(reaped) != 3 {
		t.Fatalf("every unit stem must be reaped, reaped=%v", reaped)
	}
	for _, stem := range []string{"i1-a", "i1-b", "i1-c"} {
		if _, err := os.Stat(filepath.Join(SnapshotRoot(envRoot), stem)); !os.IsNotExist(err) {
			t.Errorf("stem %s must be gone, stat err=%v", stem, err)
		}
	}
}

// TestReapExpiredSnapshotsSkipsOrphanSweepWhenLedgerUnreadable is the G1
// regression: a ledger that exists but cannot be parsed leaves the claim set
// empty and untrustworthy, so the sweep must fail closed and skip the whole
// env root. Running the orphan sweep anyway would delete every stem past the
// 24h orphan age — including failed trees inside their 7-day preservation
// window — through a side door that bypasses the F3 retention promise.
func TestReapExpiredSnapshotsSkipsOrphanSweepWhenLedgerUnreadable(t *testing.T) {
	t.Parallel()
	envRoot := t.TempDir()
	now := time.Now()
	entry := ledgerEntryForTest(envRoot, "t-failed", "i1-failed", snapshotStateFailed, now.Add(-3*24*time.Hour))
	seedLedger(t, envRoot, []SnapshotLedgerEntry{entry})
	stem := filepath.Join(SnapshotRoot(envRoot), "i1-failed")
	makeStemDirs(t, envRoot, "i1-failed")
	stamp := now.Add(-3 * 24 * time.Hour)
	if err := os.Chtimes(stem, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("{not-json")
	if err := os.WriteFile(SnapshotLedgerPath(envRoot), corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	reaped := ReapExpiredSnapshots(envRoot, now)
	if len(reaped) != 0 {
		t.Fatalf("unreadable ledger must fail closed, reaped=%v", reaped)
	}
	if _, err := os.Stat(stem); err != nil {
		t.Fatalf("unexpired failed tree must survive an unreadable ledger: %v", err)
	}
	raw, err := os.ReadFile(SnapshotLedgerPath(envRoot))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(corrupt) {
		t.Fatalf("unreadable ledger must be left untouched for diagnosis, got %q", raw)
	}
}

// TestReapExpiredSnapshotsMissingLedgerStillSweepsOrphans pins the healthy
// crash-leftover shape: a snapshot root with no ledger file at all is a
// daemon that died before its first record, and age-based orphan reclamation
// must keep working there. The G1 fail-closed skip covers an unreadable
// ledger, not an absent one.
func TestReapExpiredSnapshotsMissingLedgerStillSweepsOrphans(t *testing.T) {
	t.Parallel()
	envRoot := t.TempDir()
	now := time.Now()
	orphan := filepath.Join(SnapshotRoot(envRoot), "i1-orphan")
	if err := os.MkdirAll(filepath.Join(orphan, "upper"), 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := now.Add(-30 * time.Hour)
	if err := os.Chtimes(orphan, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	reaped := ReapExpiredSnapshots(envRoot, now)
	if len(reaped) != 1 || reaped[0] != orphan {
		t.Fatalf("orphan must still be reaped without a ledger file, reaped=%v", reaped)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan must be gone from disk, stat err=%v", err)
	}
}
