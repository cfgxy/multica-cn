package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/supervisor"
	"github.com/multica-ai/multica/server/pkg/agent"
)

const testTaskID = "01a0fad5-cc0c-7165-a08c-0467d42e6f27"

func TestSanitizeRunID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"01a0fad5-cc0c-7165-a08c-0467d42e6f27", "01a0fad5-cc0c-7165-a08c-0467d42e6f27"},
		{"MUL-1234", "mul-1234"},
		{"weird id!!", "weird-id"},
		{"---", "task"},
		{"", "task"},
	}
	for _, c := range cases {
		if got := sanitizeRunID(c.in); got != c.want {
			t.Errorf("sanitizeRunID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := make([]byte, 0, 64)
	for i := 0; i < 64; i++ {
		long = append(long, 'a')
	}
	if got := sanitizeRunID(string(long)); len(got) != 48 {
		t.Errorf("sanitizeRunID(long) length = %d, want 48", len(got))
	}
}

func newTestSupervisor(t *testing.T) *supervisor.Supervisor {
	t.Helper()
	mgr, err := supervisor.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	sup, err := supervisor.New(mgr, nil, "/tmp/fake-daemon", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sup
}

func TestPlanSupervisedRun(t *testing.T) {
	newDaemon := func(sup *supervisor.Supervisor) *Daemon {
		return &Daemon{supervisor: sup, logger: slog.Default()}
	}

	t.Run("nil supervisor keeps legacy", func(t *testing.T) {
		if got := newDaemon(nil).planSupervisedRun("claude", testTaskID, 1); got != nil {
			t.Fatalf("want nil, got %+v", got)
		}
	})
	t.Run("provider whitelist", func(t *testing.T) {
		if got := newDaemon(newTestSupervisor(t)).planSupervisedRun("kimi", testTaskID, 1); got != nil {
			t.Fatalf("kimi is Phase 2; want nil, got %+v", got)
		}
	})
	t.Run("non-uuid task id falls back to legacy", func(t *testing.T) {
		// "mul-1234-1" cannot head a systemd unit name per the run-id
		// grammar (hex first char required); legacy is the only safe answer.
		if got := newDaemon(newTestSupervisor(t)).planSupervisedRun("claude", "MUL-1234", 1); got != nil {
			t.Fatalf("want nil for non-uuid task id, got %+v", got)
		}
	})
	t.Run("fresh launch", func(t *testing.T) {
		got := newDaemon(newTestSupervisor(t)).planSupervisedRun("claude", testTaskID, 1)
		if got == nil || got.Reattach || got.RunID != testTaskID+"-1" {
			t.Fatalf("want fresh run %s-1, got %+v", testTaskID, got)
		}
	})
	t.Run("reattach on live manifest", func(t *testing.T) {
		sup := newTestSupervisor(t)
		d := newDaemon(sup)
		if err := sup.Manager().WriteManifest(&supervisor.Manifest{
			Version: 1, RunID: testTaskID + "-1", TaskID: testTaskID, State: supervisor.StateRunning,
		}); err != nil {
			t.Fatalf("seed manifest: %v", err)
		}
		got := d.planSupervisedRun("claude", testTaskID, 1)
		if got == nil || !got.Reattach || got.RunID != testTaskID+"-1" {
			t.Fatalf("want reattach -1, got %+v", got)
		}
	})
	t.Run("exited manifest bumps generation", func(t *testing.T) {
		sup := newTestSupervisor(t)
		d := newDaemon(sup)
		if err := sup.Manager().WriteManifest(&supervisor.Manifest{
			Version: 1, RunID: testTaskID + "-1", TaskID: testTaskID, State: supervisor.StateExited,
			Exit: &supervisor.ExitRecord{Code: 0, Source: supervisor.ExitSourceLauncher},
		}); err != nil {
			t.Fatalf("seed manifest: %v", err)
		}
		got := d.planSupervisedRun("claude", testTaskID, 1)
		if got == nil || got.Reattach || got.RunID != testTaskID+"-1-2" {
			t.Fatalf("want fresh generation -1-2, got %+v", got)
		}
	})
	t.Run("live g2 manifest reattaches", func(t *testing.T) {
		sup := newTestSupervisor(t)
		d := newDaemon(sup)
		for _, m := range []*supervisor.Manifest{
			{Version: 1, RunID: testTaskID + "-1", TaskID: testTaskID, State: supervisor.StateExited,
				Exit: &supervisor.ExitRecord{Code: 0, Source: supervisor.ExitSourceLauncher}},
			{Version: 1, RunID: testTaskID + "-1-2", TaskID: testTaskID, State: supervisor.StateRunning},
		} {
			if err := sup.Manager().WriteManifest(m); err != nil {
				t.Fatalf("seed manifest: %v", err)
			}
		}
		got := d.planSupervisedRun("claude", testTaskID, 1)
		if got == nil || !got.Reattach || got.RunID != testTaskID+"-1-2" {
			t.Fatalf("want reattach generation -1-2, got %+v", got)
		}
	})
}

var _ = agent.Supervision{}

// writeSystemctlShim drops a minimal `systemctl` onto binDir: it answers the
// two verbs a reconcile pass uses (list-units from $FAKE_ACTIVE_UNITS, kill
// appended to $FAKE_KILL_LOG) so the kill path is observable in-process. The
// caller must t.Setenv PATH (and the two env vars) before NewSystemdCtl —
// the controller snapshots os.Environ() at construction.
func writeSystemctlShim(t *testing.T, binDir string) {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
*list-units*)
	for u in $FAKE_ACTIVE_UNITS; do
		printf '%%s loaded active running -\n' "$u"
	done
	exit 0
	;;
*)
	unit=""
	for a in "$@"; do unit="$a"; done
	printf '%%s\n' "$unit" >> "$FAKE_KILL_LOG"
	exit 0
	;;
esac
`)
	if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write systemctl shim: %v", err)
	}
}

// killLogEntries returns the units the shim recorded as killed; nil when the
// log was never created (no kill invocation happened).
func killLogEntries(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read kill log: %v", err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// reconcileTestRunID is the one seeded supervised run every harness pass
// reconciles: manifest present, no exit record, unit reported active.
const reconcileTestRunID = "77770000-0000-0000-0000-000000000007"

// newReconcileHarness wires a daemon for a full in-process reconcile pass:
// one live supervised run seeded into a temp run store, a shimmed systemctl
// reporting that unit active, an httptest server answering ListInFlightTasks
// per runtime, and workspaces registered so allRuntimeIDs sees the runtimes.
func newReconcileHarness(t *testing.T, runtimeIDs []string, respond func(runtimeID string) (int, string)) (*Daemon, *supervisor.Manager, string, string) {
	t.Helper()
	binDir := t.TempDir()
	killLog := filepath.Join(t.TempDir(), "kills.log")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_KILL_LOG", killLog)
	t.Setenv("FAKE_ACTIVE_UNITS", supervisor.UnitName(reconcileTestRunID))
	writeSystemctlShim(t, binDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/daemon/runtimes/"), "/tasks/in-flight")
		code, body := respond(rid)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	mgr, err := supervisor.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := mgr.WriteManifest(&supervisor.Manifest{
		Version: 1, RunID: reconcileTestRunID, TaskID: testTaskID, Runtime: "claude",
		Unit: supervisor.UnitName(reconcileTestRunID), State: supervisor.StateRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	sys := supervisor.NewSystemdCtl(nil)
	sup, err := supervisor.New(mgr, sys, "/tmp/fake-daemon", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d := &Daemon{
		client:     NewClient(srv.URL),
		supervisor: sup,
		logger:     slog.Default(),
		workspaces: map[string]*workspaceState{
			"ws-under-test": {workspaceID: "ws-under-test", runtimeIDs: runtimeIDs},
		},
	}
	return d, mgr, reconcileTestRunID, killLog
}

// The reconcile kill path may only fire on complete in-flight evidence: a
// failed server listing must never downgrade a live unit to StopOrphan —
// that is the daemon-redeploy scenario this package exists to survive.
func TestReconcileSupervisedRunsKillSafety(t *testing.T) {
	manifestUntouched := func(t *testing.T, mgr *supervisor.Manager, runID string) {
		t.Helper()
		man, err := mgr.ReadManifest(runID)
		if err != nil || man == nil {
			t.Fatalf("read manifest: %v (man %+v)", err, man)
		}
		if man.Exit != nil || man.State != supervisor.StateRunning {
			t.Fatalf("manifest was converged by the reconcile pass: state=%s exit=%+v", man.State, man.Exit)
		}
	}

	t.Run("list failure keeps every live unit alive", func(t *testing.T) {
		d, mgr, runID, killLog := newReconcileHarness(t, []string{"rt-1"}, func(string) (int, string) {
			return http.StatusInternalServerError, "boom"
		})
		d.reconcileSupervisedRuns(context.Background())
		if kills := killLogEntries(t, killLog); kills != nil {
			t.Fatalf("list failure must not kill anything, shim recorded %q", kills)
		}
		manifestUntouched(t, mgr, runID)
	})

	t.Run("partial list failure keeps units of healthy runtimes alive too", func(t *testing.T) {
		d, mgr, runID, killLog := newReconcileHarness(t, []string{"rt-1", "rt-2"}, func(rid string) (int, string) {
			if rid == "rt-1" {
				return http.StatusOK, "[]" // healthy runtime: task provably not in flight
			}
			return http.StatusInternalServerError, "boom" // flaky runtime
		})
		d.reconcileSupervisedRuns(context.Background())
		if kills := killLogEntries(t, killLog); kills != nil {
			t.Fatalf("one failing runtime must disable the kill path for the whole pass, shim recorded %q", kills)
		}
		manifestUntouched(t, mgr, runID)
	})

	t.Run("healthy listings still recover real orphans", func(t *testing.T) {
		d, mgr, runID, killLog := newReconcileHarness(t, []string{"rt-1"}, func(string) (int, string) {
			return http.StatusOK, "[]"
		})
		d.reconcileSupervisedRuns(context.Background())
		kills := killLogEntries(t, killLog)
		if len(kills) != 1 || kills[0] != supervisor.UnitName(runID) {
			t.Fatalf("orphaned live unit must be stopped precisely, shim recorded %q", kills)
		}
		man, err := mgr.ReadManifest(runID)
		if err != nil || man == nil {
			t.Fatalf("read manifest: %v (man %+v)", err, man)
		}
		if man.Exit == nil || man.Exit.Source != supervisor.ExitSourceSupervisor {
			t.Fatalf("orphan stop must leave a supervisor exit record, got %+v", man.Exit)
		}
	})
}
