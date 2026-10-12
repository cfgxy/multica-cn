package supervisor

// RUYI-593 implementation round 1, direction 4 (RUYI-592 fix direction 4):
// the reconcile kill decision must be on the log record BEFORE the kill
// fires. Pre-fix the decision lines printed only after the whole pass
// returned, so a reconciler dying mid-pass took the audit with it — the
// RUYI-592 forensics lost every kill's decision line to exactly that. The
// assertion here is at-kill-time: the fake unit lister snapshots the log
// buffer inside KillUnit, and the test demands the decision line already be
// in it.

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

type killingUnits struct {
	fakeUnits

	mu     sync.Mutex
	logBuf *bytes.Buffer
	atKill string
}

func (k *killingUnits) KillUnit(ctx context.Context, unit string) error {
	k.mu.Lock()
	k.atKill = k.logBuf.String()
	k.mu.Unlock()
	return k.fakeUnits.KillUnit(ctx, unit)
}

func TestReconcileLogsDecisionBeforeKill(t *testing.T) {
	mgr := newTestManager(t)
	const runID = "88880000-0000-0000-0000-000000000008"

	var buf bytes.Buffer
	units := &killingUnits{logBuf: &buf, fakeUnits: fakeUnits{list: []string{UnitName(runID)}}}
	if err := mgr.WriteManifest(manifestFor(runID, nil)); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	rec := &Reconciler{
		Mgr:          mgr,
		Units:        units,
		Log:          slog.New(slog.NewTextHandler(&buf, nil)),
		TaskInFlight: func(string) bool { return false },
		// RUYI-607 composition: Self must match manifestFor's Owner stamp
		// for the matrix to reach the kill branch at all.
		Self: "test-self",
	}
	if _, err := rec.Run(context.Background()); err != nil {
		t.Fatalf("reconcile Run: %v", err)
	}

	snapshot := units.atKill
	if !strings.Contains(snapshot, "stop_orphan") {
		t.Fatalf("decision line not on record at kill time; log buffer at the KillUnit call was %q", snapshot)
	}
	if !strings.Contains(snapshot, runID) {
		t.Fatalf("decision line at kill time does not name the run; log buffer was %q", snapshot)
	}
}
