package daemon

import (
	"bytes"
	"context"
	"encoding/json"
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
	sup, err := supervisor.New(mgr, nil, "/tmp/fake-daemon", "", "test", nil)
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
	t.Run("phase 2 target set goes supervised", func(t *testing.T) {
		// RUYI-390: every ACP-family provider, zcode and deerflow reattach
		// mid-turn without a prompt replay, so each must plan a supervised
		// run — the feature test pinning the launch path per provider.
		d := newDaemon(newTestSupervisor(t))
		for _, p := range supervisedTargets {
			got := d.planSupervisedRun(p, testTaskID, 1)
			if got == nil {
				t.Fatalf("%s: want supervised plan, got legacy", p)
			}
			if got.Runtime != p || got.Reattach {
				t.Fatalf("%s: want fresh supervised plan, got %+v", p, got)
			}
		}
	})
	t.Run("phase 2 reattach plan across providers", func(t *testing.T) {
		// Same deterministic run id for every provider: a restart reenters
		// the live worker regardless of which family launched it.
		sup := newTestSupervisor(t)
		d := newDaemon(sup)
		if err := sup.Manager().WriteManifest(&supervisor.Manifest{
			Version: 1, RunID: testTaskID + "-1", TaskID: testTaskID, State: supervisor.StateRunning,
		}); err != nil {
			t.Fatalf("seed manifest: %v", err)
		}
		for _, p := range supervisedTargets {
			got := d.planSupervisedRun(p, testTaskID, 1)
			if got == nil || !got.Reattach || got.RunID != testTaskID+"-1" {
				t.Fatalf("%s: want reattach %s-1, got %+v", p, testTaskID, got)
			}
		}
	})
	t.Run("non-target providers stay legacy", func(t *testing.T) {
		d := newDaemon(newTestSupervisor(t))
		for _, p := range []string{"codex", "cursor", "copilot", "qwen", "antigravity", "opencode", "pi", "omp"} {
			if got := d.planSupervisedRun(p, testTaskID, 1); got != nil {
				t.Fatalf("%s: not a phase 2 target; want legacy, got %+v", p, got)
			}
		}
	})
	t.Run("builtin runtime resolves to its protocol family", func(t *testing.T) {
		// "omp" dispatches to the pi family; the whitelist decision must
		// follow the family, not the runtime id, so a future ACP-family
		// builtin runtime inherits supervision without a whitelist edit.
		if got := supervisedProviderFamily("omp"); got != "pi" {
			t.Fatalf("supervisedProviderFamily(omp) = %q, want pi", got)
		}
		if got := supervisedProviderFamily("kimi"); got != "kimi" {
			t.Fatalf("supervisedProviderFamily(kimi) = %q, want kimi", got)
		}
		if got := supervisedProviderFamily("no-such-provider"); got != "no-such-provider" {
			t.Fatalf("unknown provider must pass through, got %q", got)
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
// verbs the daemon drives — list-units from $FAKE_ACTIVE_UNITS, is-active
// ("active" only for units in $FAKE_ACTIVE_UNITS, "inactive" otherwise),
// and every other verb is recorded as a kill in $FAKE_KILL_LOG — so the kill
// path and the liveness cross-check are both observable in-process. The
// caller must t.Setenv PATH (and the env vars) before NewSystemdCtl — the
// controller snapshots os.Environ() at construction.
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
*is-active*)
	unit=""
	for a in "$@"; do unit="$a"; done
	for u in $FAKE_ACTIVE_UNITS; do
		if [ "$u" = "$unit" ]; then
			printf 'active\n'
			exit 0
		fi
	done
	printf 'inactive\n'
	exit 3
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
func newReconcileHarness(t *testing.T, runtimeIDs []string, respond func(runtimeID string) (int, string), activeUnits ...string) (*Daemon, *supervisor.Manager, string, string) {
	t.Helper()
	if len(activeUnits) == 0 {
		activeUnits = []string{supervisor.UnitName(reconcileTestRunID)}
	}
	binDir := t.TempDir()
	killLog := filepath.Join(t.TempDir(), "kills.log")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_KILL_LOG", killLog)
	t.Setenv("FAKE_ACTIVE_UNITS", strings.Join(activeUnits, " "))
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
		Owner: supervisor.OwnerIdentity(""),
	}); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	sys := supervisor.NewSystemdCtl(nil)
	sup, err := supervisor.New(mgr, sys, "/tmp/fake-daemon", "", supervisor.OwnerIdentity(""), nil)
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

// RUYI-607 regression: a dev/QA daemon starting on this host used to sweep
// the SHARED legacy run store, find a production run's live unit absent from
// its own server's in-flight list, and SIGKILL it via StopOrphan. Two gates
// must hold in one startup pass: the legacy-store migration only adopts runs
// this daemon can prove it launched (task in ITS in-flight set), and the
// reconcile matrix never acts on runs its identity does not own. Zero kills,
// own run adopted with owner stamped, foreign run left untouched.
func TestReconcileSupervisedRunsLegacyStoreOwnership(t *testing.T) {
	foreignTask := "01deb00f-0000-0000-0000-00000000dead"
	foreignRun := "99990000-0000-0000-0000-000000000009"
	ownRun := "88880000-0000-0000-0000-000000000008"

	legacy := t.TempDir()
	seedLegacySharedRun(t, legacy, foreignRun, foreignTask, "")
	seedLegacySharedRun(t, legacy, ownRun, testTaskID, "")

	d, mgr, _, killLog := newReconcileHarness(t, []string{"rt-1"}, func(string) (int, string) {
		return http.StatusOK, `[{"id":"` + testTaskID + `"}]`
	}, supervisor.UnitName(reconcileTestRunID), supervisor.UnitName(foreignRun), supervisor.UnitName(ownRun))
	d.supervisedRunsBase = legacy
	d.reconcileSupervisedRuns(context.Background())

	if kills := killLogEntries(t, killLog); kills != nil {
		t.Fatalf("foreign live unit must survive another daemon's startup reconcile, shim recorded %q", kills)
	}

	// Own in-flight run: adopted out of the shared store, owner stamped.
	own, err := mgr.ReadManifest(ownRun)
	if err != nil {
		t.Fatalf("own in-flight run not adopted from legacy store: %v", err)
	}
	if own.Owner != supervisor.OwnerIdentity("") || own.TaskID != testTaskID {
		t.Fatalf("adopted run not stamped with this daemon's identity: %+v", own)
	}
	if _, err := os.Stat(filepath.Join(legacy, ownRun)); !os.IsNotExist(err) {
		t.Fatalf("adopted run must leave the legacy store, stat err = %v", err)
	}

	// Foreign run: untouched in the shared store, never adopted into ours.
	// (Our store may still hold a quarantine shell for its live unit — the
	// pre-RUYI-349 unknown-unit record that proves it was seen, not killed.)
	foreignData, err := os.ReadFile(filepath.Join(legacy, foreignRun, "manifest.json"))
	if err != nil {
		t.Fatalf("foreign run must stay in the legacy store: %v", err)
	}
	var foreignMan supervisor.Manifest
	if err := json.Unmarshal(foreignData, &foreignMan); err != nil {
		t.Fatal(err)
	}
	if foreignMan.Owner != "" || foreignMan.TaskID != foreignTask {
		t.Fatalf("foreign run manifest was mutated: %+v", foreignMan)
	}
	if _, err := mgr.ReadManifest(foreignRun); err == nil {
		t.Fatalf("foreign run must never be adopted into this daemon's run store")
	}
}

// RUYI-607 acceptance: kill/skip decisions must be visible in the daemon
// log. A foreign-owner manifest sitting in THIS daemon's own run store with
// its unit live is the exact shape the identity gate exists for — its task
// is absent from the in-flight list, so without the gate the matrix would
// issue StopOrphan and kill. One reconcile pass must log decision=foreign_skip
// with a reason echoing the foreign owner, kill nothing, and leave the
// manifest untouched.
func TestReconcileSupervisedRunsForeignSkipLogged(t *testing.T) {
	foreignTask := "01deb00f-0000-0000-0000-00000000beef"
	foreignRun := "99990000-0000-0000-0000-000000000006"
	foreignOwner := supervisor.OwnerIdentity("prod-profile")

	d, mgr, _, killLog := newReconcileHarness(t, []string{"rt-1"}, func(string) (int, string) {
		return http.StatusOK, `[{"id":"` + testTaskID + `"}]`
	}, supervisor.UnitName(reconcileTestRunID), supervisor.UnitName(foreignRun))
	var logs bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&logs, nil))
	if err := mgr.WriteManifest(&supervisor.Manifest{
		Version: 1, RunID: foreignRun, TaskID: foreignTask, Runtime: "claude",
		Unit: supervisor.UnitName(foreignRun), State: supervisor.StateRunning,
		StartedAt: time.Now().UTC(), Owner: foreignOwner,
	}); err != nil {
		t.Fatalf("seed foreign manifest: %v", err)
	}

	d.reconcileSupervisedRuns(context.Background())

	if kills := killLogEntries(t, killLog); kills != nil {
		t.Fatalf("foreign-owned live unit must never be killed on this daemon's startup reconcile, shim recorded %q", kills)
	}
	// Substring checks avoid quoting forms: slog TextHandler escapes inner
	// quotes, so assert on pieces that survive any handler encoding.
	logged := logs.String()
	if !strings.Contains(logged, "decision=foreign_skip") {
		t.Fatalf("reconcile pass must log the foreign_skip decision, log captured:\n%s", logged)
	}
	if !strings.Contains(logged, foreignRun) || !strings.Contains(logged, "manifest owner ") || !strings.Contains(logged, foreignOwner) {
		t.Fatalf("foreign_skip log must name run %s and echo foreign owner %q in its reason, log captured:\n%s", foreignRun, foreignOwner, logged)
	}
	man, err := mgr.ReadManifest(foreignRun)
	if err != nil || man == nil {
		t.Fatalf("foreign manifest must survive the pass: %v (man %+v)", err, man)
	}
	if man.Exit != nil || man.State != supervisor.StateRunning {
		t.Fatalf("foreign_skip must leave the manifest untouched, got %+v", man)
	}
}

// seedLegacySharedRun writes a pre-RUYI-607 shared-store run directory: flat
// under the store root, manifest carrying no owner stamp (owner "").
func seedLegacySharedRun(t *testing.T, root, runID, taskID, owner string) {
	t.Helper()
	dir := filepath.Join(root, runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"version": 1, "run_id": runID, "task_id": taskID, "runtime": "claude",
		"unit": supervisor.UnitName(runID), "state": "running",
		"started_at": time.Now().UTC().Format(time.RFC3339),
		"owner":      owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// wireShimmedSupervisor replaces d.supervisor with one that probes a shimmed
// systemctl: units listed in activeUnits answer "active", everything else
// answers "inactive". Unlike newTestSupervisor (nil systemd, so liveness
// cross-checks fail open to the manifest-trusting answer), this one
// exercises the cross-check itself. Returns the manager so callers can seed
// manifests.
func wireShimmedSupervisor(t *testing.T, d *Daemon, activeUnits ...string) *supervisor.Manager {
	t.Helper()
	binDir := t.TempDir()
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_KILL_LOG", filepath.Join(t.TempDir(), "kills.log"))
	t.Setenv("FAKE_ACTIVE_UNITS", strings.Join(activeUnits, " "))
	writeSystemctlShim(t, binDir)
	mgr, err := supervisor.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	sup, err := supervisor.New(mgr, supervisor.NewSystemdCtl(nil), "/tmp/fake-daemon", "", "test", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.supervisor = sup
	return mgr
}

// seedRunningManifest installs the stranded-record shape the cross-check
// exists for: a manifest written at launch, claiming a running worker, with
// no exit record — exactly what a host reboot or user-manager restart leaves
// behind (the reconcile matrix's Lost corner).
func seedRunningManifest(t *testing.T, mgr *supervisor.Manager, runID string) {
	t.Helper()
	if err := mgr.WriteManifest(&supervisor.Manifest{
		Version: 1, RunID: runID, TaskID: testTaskID,
		Unit: supervisor.UnitName(runID), State: supervisor.StateRunning,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
}

// TestPlanSupervisedRunUnitCrossCheck: the claim-side reattach probe must not
// trust a manifest that claims a running worker once systemd says its unit is
// gone — reattaching a corpse wedges the task on a dead control socket, so
// the plan steps to a fresh generation instead. A unit systemd still reports
// active keeps the reattach (no double launch). Supervisors without a systemd
// controller fail open to the manifest-trusting answer (pinned by
// TestPlanSupervisedRun, which runs on newTestSupervisor's nil systemd).
func TestPlanSupervisedRunUnitCrossCheck(t *testing.T) {
	runID := testTaskID + "-1"
	unit := supervisor.UnitName(runID)

	t.Run("inactive unit steps to a fresh generation", func(t *testing.T) {
		d := &Daemon{logger: slog.Default()}
		mgr := wireShimmedSupervisor(t, d)
		seedRunningManifest(t, mgr, runID)
		got := d.planSupervisedRun("claude", testTaskID, 1)
		if got == nil || got.Reattach || got.RunID != testTaskID+"-1-2" {
			t.Fatalf("unit gone: want fresh generation %s-1-2, got %+v", testTaskID, got)
		}
	})
	t.Run("active unit keeps the reattach", func(t *testing.T) {
		d := &Daemon{logger: slog.Default()}
		mgr := wireShimmedSupervisor(t, d, unit)
		seedRunningManifest(t, mgr, runID)
		got := d.planSupervisedRun("claude", testTaskID, 1)
		if got == nil || !got.Reattach || got.RunID != runID {
			t.Fatalf("unit live: want reattach %s, got %+v", runID, got)
		}
	})
}

// TestSupervisedWorkerAliveUnitCrossCheck: a "running" manifest is liveness
// evidence only while its unit exists. After a host reboot the stranded
// manifest would permanently veto the RUYI-225 in-flight recovery (the
// env-root lock reads dead because the daemon held it), leaving the task
// running forever — the server-side stale-running sweeper exempts runtimes
// that are back online. Inactive unit → not alive; active unit → alive.
func TestSupervisedWorkerAliveUnitCrossCheck(t *testing.T) {
	runID := testTaskID + "-1"
	unit := supervisor.UnitName(runID)

	t.Run("inactive unit reads dead", func(t *testing.T) {
		d := &Daemon{logger: slog.Default()}
		mgr := wireShimmedSupervisor(t, d)
		seedRunningManifest(t, mgr, runID)
		if d.supervisedWorkerAlive(testTaskID) {
			t.Fatalf("unit gone but stranded manifest still reads alive")
		}
	})
	t.Run("active unit reads alive", func(t *testing.T) {
		d := &Daemon{logger: slog.Default()}
		mgr := wireShimmedSupervisor(t, d, unit)
		seedRunningManifest(t, mgr, runID)
		if !d.supervisedWorkerAlive(testTaskID) {
			t.Fatalf("unit live but worker reads dead")
		}
	})
	t.Run("no manifest reads dead", func(t *testing.T) {
		d := &Daemon{logger: slog.Default()}
		wireShimmedSupervisor(t, d)
		if d.supervisedWorkerAlive(testTaskID) {
			t.Fatalf("no manifest must read dead")
		}
	})
}
