package supervisor

import (
	"context"
	"testing"
	"time"
)

func manifestFor(id string, exit *ExitRecord) *Manifest {
	man := &Manifest{
		Version: 1, RunID: id, TaskID: "task-" + id[:4], Runtime: "claude",
		Unit: UnitName(id), State: StateRunning, StartedAt: time.Now().UTC(),
	}
	if exit != nil {
		man.State = StateExited
		man.Exit = exit
	}
	return man
}

func TestDecideMatrix(t *testing.T) {
	id := testRunID()
	cases := []struct {
		name string
		in   ReconcileInput
		want Decision
	}{
		{
			name: "unknown live unit quarantines",
			in:   ReconcileInput{Manifest: nil, UnitActive: true},
			want: DecisionQuarantine,
		},
		{
			name: "alive unit + in-flight task resumes",
			in:   ReconcileInput{Manifest: manifestFor(id, nil), UnitActive: true, TaskInFlight: true},
			want: DecisionResume,
		},
		{
			name: "alive unit + finished task stops orphan",
			in:   ReconcileInput{Manifest: manifestFor(id, nil), UnitActive: true, TaskInFlight: false},
			want: DecisionStopOrphan,
		},
		{
			name: "dead unit + exit record converges by evidence",
			in:   ReconcileInput{Manifest: manifestFor(id, &ExitRecord{Code: 0, Source: ExitSourceLauncher}), UnitActive: false},
			want: DecisionConvergeExit,
		},
		{
			name: "dead unit without exit but live lock quarantines",
			in:   ReconcileInput{Manifest: manifestFor(id, nil), UnitActive: false, LockHeld: true},
			want: DecisionQuarantine,
		},
		{
			name: "dead unit without exit or lock is lost",
			in:   ReconcileInput{Manifest: manifestFor(id, nil), UnitActive: false, LockHeld: false},
			want: DecisionLost,
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

type fakeUnits struct {
	list   []string
	killed []string
}

func (f *fakeUnits) UnitActive(ctx context.Context, unit string) (bool, error) {
	for _, u := range f.list {
		if u == unit {
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeUnits) ListUnits(ctx context.Context) ([]string, error) { return f.list, nil }
func (f *fakeUnits) KillUnit(ctx context.Context, unit string) error {
	f.killed = append(f.killed, unit)
	return nil
}

func TestReconcilerRunActions(t *testing.T) {
	mgr := newTestManager(t)
	idResume := "11110000-0000-0000-0000-000000000001"
	idOrphan := "22220000-0000-0000-0000-000000000002"
	idExited := "33330000-0000-0000-0000-000000000003"
	idLost := "44440000-0000-0000-0000-000000000004"
	idUnknown := "55550000-0000-0000-0000-000000000005"

	for _, id := range []string{idResume, idOrphan, idExited, idLost} {
		var exit *ExitRecord
		if id == idExited {
			exit = &ExitRecord{Code: 0, Source: ExitSourceLauncher}
		}
		if err := mgr.WriteManifest(manifestFor(id, exit)); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeUnits{list: []string{UnitName(idResume), UnitName(idOrphan), UnitName(idUnknown)}}

	r := &Reconciler{
		Mgr:   mgr,
		Units: fake,
		LockHeld: func(runID string) bool {
			return runID == idLost // conflicts with no unit: quarantine
		},
		TaskInFlight: func(taskID string) bool { return taskID == "task-1111" },
	}
	results, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ReconcileResult{}
	for _, res := range results {
		byID[res.RunID] = res
	}
	if got := byID[idResume].Decision; got != DecisionResume {
		t.Errorf("resume case = %s (%s)", got, byID[idResume].Reason)
	}
	if got := byID[idOrphan].Decision; got != DecisionStopOrphan {
		t.Errorf("orphan case = %s (%s)", got, byID[idOrphan].Reason)
	}
	if got := byID[idExited].Decision; got != DecisionConvergeExit {
		t.Errorf("converge case = %s", got)
	}
	if got := byID[idLost].Decision; got != DecisionQuarantine {
		t.Errorf("lock-conflict case = %s (%s)", got, byID[idLost].Reason)
	}
	if got, ok := byID[idUnknown]; !ok || got.Decision != DecisionQuarantine {
		t.Errorf("unknown unit = %+v (ok=%v)", got, ok)
	}

	// Actions: only the orphan was stopped, with supervisor-source evidence.
	if len(fake.killed) != 1 || fake.killed[0] != UnitName(idOrphan) {
		t.Fatalf("killed = %v, want only orphan", fake.killed)
	}
	man, _ := mgr.ReadManifest(idOrphan)
	if man.Exit == nil || man.Exit.Source != ExitSourceSupervisor {
		t.Fatalf("orphan exit record = %+v", man.Exit)
	}
	// Quarantine records exist for both quarantined runs.
	if _, err := mgr.ReadQuarantine(idLost); err != nil {
		t.Errorf("lock-conflict quarantine missing: %v", err)
	}
	if _, err := mgr.ReadQuarantine(idUnknown); err != nil {
		t.Errorf("unknown-unit quarantine missing: %v", err)
	}
	// Terminal runs are not re-decided.
	if _, ok := byID[idExited]; !ok {
		t.Errorf("converge case missing from results") // reported, not acted
	}
}

func TestRunIDFromUnitRejectsForeignNames(t *testing.T) {
	if got := runIDFromUnit("ssh.service"); got != "" {
		t.Fatalf("foreign unit parsed as %q", got)
	}
	if got := runIDFromUnit(unitPrefix + "short.service"); got != "" {
		t.Fatalf("invalid run id parsed as %q", got)
	}
	if got := runIDFromUnit(UnitName(testRunID())); got != testRunID() {
		t.Fatalf("round trip = %q", got)
	}
}
