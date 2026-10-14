package supervisor

// RUYI-593 implementation round 1, direction 1 (RUYI-592 fix direction 1):
// the stop_orphan kill may only fire on a unit the reconciling daemon
// launched itself. A second daemon on the same host sees foreign workers
// through a blind spot — its in-flight set only covers its own runtimes —
// and the pre-fix matrix turned that blind spot into a mass SIGKILL
// (RUYI-592 investigation: 13 journal-confirmed kills, 10 tied to dev-slot
// and desktop daemon starts). These tests pin the ownership gate at both
// the matrix level and the full reconcile pass level. Rebased onto
// RUYI-607, the gate is the second attribution axis: the matrix first
// skips every manifest whose Owner identity is not the reconciler's Self
// (foreign_skip), then the daemonID gate below quarantines what shares
// our namespace but not our daemon installation. Fixtures carry
// Owner "test-self" (manifestFor's stamp) so cases reach this gate.

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

const (
	ownershipProdDaemon = "0aaaaaaa-0000-0000-0000-00000000prod"
	ownershipDevDaemon  = "0bbbbbbb-0000-0000-0000-00000000devd"
)

func ownedManifestFor(id, owner string) *Manifest {
	man := manifestFor(id, nil)
	man.DaemonID = owner
	return man
}

func TestDecideOwnershipGatesStopOrphan(t *testing.T) {
	id := testRunID()
	cases := []struct {
		name string
		in   ReconcileInput
		want Decision
	}{
		{
			name: "own live unit with finished task stops orphan",
			in: ReconcileInput{Manifest: ownedManifestFor(id, ownershipProdDaemon), UnitActive: true,
				TaskInFlight: false, DaemonID: ownershipProdDaemon, Self: "test-self"},
			want: DecisionStopOrphan,
		},
		{
			name: "same namespace but another daemon installation quarantines",
			in: ReconcileInput{Manifest: ownedManifestFor(id, ownershipProdDaemon), UnitActive: true,
				TaskInFlight: false, DaemonID: ownershipDevDaemon, Self: "test-self"},
			want: DecisionQuarantine,
		},
		{
			name: "daemon_id-less live unit quarantines under an identified daemon",
			in: ReconcileInput{Manifest: manifestFor(id, nil), UnitActive: true,
				TaskInFlight: false, DaemonID: ownershipProdDaemon, Self: "test-self"},
			want: DecisionQuarantine,
		},
		{
			name: "daemon_id-less live unit under an unidentified reconciler keeps legacy stop_orphan",
			in: ReconcileInput{Manifest: manifestFor(id, nil), UnitActive: true,
				TaskInFlight: false, DaemonID: "", Self: "test-self"},
			want: DecisionStopOrphan,
		},
		{
			name: "foreign live unit with in-flight task still resumes",
			in: ReconcileInput{Manifest: ownedManifestFor(id, ownershipProdDaemon), UnitActive: true,
				TaskInFlight: true, DaemonID: ownershipDevDaemon, Self: "test-self"},
			want: DecisionResume,
		},
		{
			// RUYI-607 composition ordering: a foreign Owner stamp is
			// decided before the daemonID gate ever runs — untouched
			// foreign_skip, even with the daemon installation matching.
			name: "foreign identity wins over matching daemon installation",
			in: func() ReconcileInput {
				man := ownedManifestFor(id, ownershipProdDaemon)
				man.Owner = "prod-profile"
				return ReconcileInput{Manifest: man, UnitActive: true,
					TaskInFlight: false, DaemonID: ownershipProdDaemon, Self: "test-self"}
			}(),
			want: DecisionForeignSkip,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.in); got != tc.want {
				t.Fatalf("Decide(%+v) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestReconcileCrossDaemonBlindSpotQuarantines is the RUYI-592 reproduction
// stub (TestRUYI592CrossDaemonReconcileKillsLiveUnits) reshaped into the
// regression contract: a second daemon reconciling live units it cannot see
// in its own in-flight set must quarantine them — record, never kill —
// instead of SIGKILLing the whole crop.
func TestReconcileCrossDaemonBlindSpotQuarantines(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	const (
		runA = "01a11d65-3bc1-7b57-b9f1-4b7df4382cdc-1"
		runB = "01a11d88-cd66-7399-99ea-7127ae79b3f1-1"
	)
	units := &fakeUnits{list: []string{}}
	for _, id := range []string{runA, runB} {
		man := ownedManifestFor(id, ownershipProdDaemon)
		if err := mgr.WriteManifest(man); err != nil {
			t.Fatalf("WriteManifest %s: %v", id, err)
		}
		units.list = append(units.list, man.Unit)
	}

	// The second daemon's view: its server-side in-flight set does not know
	// these tasks at all (cross-daemon blind spot), so TaskInFlight is
	// always false.
	rec := &Reconciler{
		Mgr:          mgr,
		Units:        units,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		TaskInFlight: func(string) bool { return false },
		DaemonID:     ownershipDevDaemon,
		Self:         "test-self",
	}
	results, err := rec.Run(context.Background())
	if err != nil {
		t.Fatalf("reconcile Run: %v", err)
	}

	decisions := map[string]ReconcileResult{}
	for _, res := range results {
		decisions[res.RunID] = res
	}
	for _, id := range []string{runA, runB} {
		res, ok := decisions[id]
		if !ok {
			t.Fatalf("run %s missing from reconcile results", id)
		}
		if res.Decision != DecisionQuarantine {
			t.Errorf("run %s: decision = %s, want quarantine (foreign daemon must not kill on its blind spot)", id, res.Decision)
		}
		if _, err := mgr.ReadQuarantine(id); err != nil {
			t.Errorf("run %s: quarantine record missing: %v", id, err)
		}
		man, err := mgr.ReadManifest(id)
		if err != nil {
			t.Fatalf("ReadManifest %s: %v", id, err)
		}
		if man.Exit != nil {
			t.Errorf("run %s: quarantine must not write an exit record, got %+v", id, man.Exit)
		}
	}
	if len(units.killed) != 0 {
		t.Fatalf("foreign reconcile killed %v; quarantine must not touch any unit", units.killed)
	}
}

// TestReconcileOwnUnitStillStopsOrphan pins the other half of the gate:
// ownership satisfied, the pre-existing orphan stop keeps working — the fix
// must not disarm a daemon on its own units.
func TestReconcileOwnUnitStillStopsOrphan(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	const runID = "01a11d65-3bc1-7b57-b9f1-4b7df4382cdc-1"
	man := ownedManifestFor(runID, ownershipProdDaemon)
	if err := mgr.WriteManifest(man); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	units := &fakeUnits{list: []string{man.Unit}}

	rec := &Reconciler{
		Mgr:          mgr,
		Units:        units,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		TaskInFlight: func(string) bool { return false },
		DaemonID:     ownershipProdDaemon,
		Self:         "test-self",
	}
	results, err := rec.Run(context.Background())
	if err != nil {
		t.Fatalf("reconcile Run: %v", err)
	}
	if len(results) != 1 || results[0].Decision != DecisionStopOrphan {
		t.Fatalf("decisions = %+v, want one stop_orphan", results)
	}
	if len(units.killed) != 1 || units.killed[0] != man.Unit {
		t.Fatalf("killed = %v, want the orphan unit", units.killed)
	}
	stored, err := mgr.ReadManifest(runID)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if stored.Exit == nil || stored.Exit.Source != ExitSourceSupervisor {
		t.Fatalf("orphan exit record = %+v, want supervisor source", stored.Exit)
	}
}
