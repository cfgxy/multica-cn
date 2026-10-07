package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateRunIDRejectsPathTraversalAndEmpty(t *testing.T) {
	for _, bad := range []string{"", "../../etc", "Run-With-Upper", "id with space", "a", "x;y", "unit.service"} {
		if err := ValidateRunID(bad); err == nil {
			t.Fatalf("ValidateRunID(%q) = nil, want error", bad)
		}
	}
	if err := ValidateRunID("0123abcd-1234-5678-9abc-def012345678"); err != nil {
		t.Fatalf("valid run id rejected: %v", err)
	}
}

func TestManifestRoundTripAtomic(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	man := &Manifest{
		Version: 1, RunID: "0123abcd-1234-5678-9abc-def012345678", TaskID: "task-1",
		Runtime: "claude", Unit: UnitName("0123abcd-1234-5678-9abc-def012345678"),
		State: StateRunning, WorkerPID: 4242, LauncherPID: 4241,
		ControlSocket: "/x/control.sock", StdoutLog: "/x/out.log", StderrLog: "/x/err.log",
		StartedAt: time.Now().UTC(),
	}
	if err := mgr.WriteManifest(man); err != nil {
		t.Fatal(err)
	}
	got, err := mgr.ReadManifest(man.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkerPID != 4242 || got.Unit != man.Unit || got.State != StateRunning {
		t.Fatalf("manifest round trip mismatch: %+v", got)
	}
	// No .tmp leftovers: the rename is the atomicity contract.
	entries, _ := os.ReadDir(mgr.Dir(man.RunID))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestQuarantineKeepsFirstSeen(t *testing.T) {
	mgr, _ := NewManager(t.TempDir())
	id := "0123abcd-1234-5678-9abc-def012345678"
	first := time.Now().UTC().Add(-time.Hour)
	if err := mgr.WriteQuarantine(&QuarantineRecord{RunID: id, Unit: UnitName(id), Reason: "r1", FirstSeen: first}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.WriteQuarantine(&QuarantineRecord{RunID: id, Unit: UnitName(id), Reason: "r2", FirstSeen: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	rec, err := mgr.ReadQuarantine(id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Reason != "r2" {
		t.Fatalf("latest reason not stored: %q", rec.Reason)
	}
	if !rec.FirstSeen.Equal(first) {
		t.Fatalf("first seen not preserved: %v vs %v", rec.FirstSeen, first)
	}
}

func TestListRunIDsFiltersByPattern(t *testing.T) {
	root := t.TempDir()
	mgr, _ := NewManager(root)
	valid := "0123abcd-1234-5678-9abc-def012345678"
	for _, name := range []string{valid, "not-a-run", "UPPER-CASE-id"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := mgr.ListRunIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != valid {
		t.Fatalf("ListRunIDs = %v, want [%s]", ids, valid)
	}
}

func TestStreamReadStateRoundTrip(t *testing.T) {
	mgr, _ := NewManager(t.TempDir())
	id := "0123abcd-1234-5678-9abc-def012345678"
	rs := streamReadState{Offset: 12, Hashes: []uint64{1, 2}}
	if err := mgr.StoreStreamReadState(id, "stdout", rs); err != nil {
		t.Fatal(err)
	}
	got, err := mgr.LoadStreamReadState(id, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	if got.Offset != 12 || len(got.Hashes) != 2 {
		t.Fatalf("read state mismatch: %+v", got)
	}
	// Streams are independent files: stderr starts at zero even though
	// stdout has advanced.
	other, err := mgr.LoadStreamReadState(id, "stderr")
	if err != nil {
		t.Fatal(err)
	}
	if other.Offset != 0 {
		t.Fatalf("stderr cursor not independent: %+v", other)
	}
	empty, err := mgr.LoadStreamReadState("ffffffff-1234-5678-9abc-def012345678", "stdout")
	if err != nil {
		t.Fatalf("missing read state should default: %v", err)
	}
	if empty.Offset != 0 {
		t.Fatalf("default read state not zero: %+v", empty)
	}
}
