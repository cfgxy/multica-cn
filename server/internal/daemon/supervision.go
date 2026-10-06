package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/supervisor"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// Worker supervision wiring (RUYI-349): the daemon optionally launches each
// task worker inside a systemd transient user unit so a daemon restart or
// SIGKILL can no longer take running workers down. Everything here degrades
// to the legacy direct-child path by returning nil — a host without a usable
// systemd user bus runs exactly the pre-RUYI-349 semantics.

// supervisedRunsDirEnv redirects the supervisor run store (manifests, logs,
// control sockets). Tests and E2E exercises set it; production uses the
// default under ~/.multica, which survives daemon restarts by construction.
const supervisedRunsDirEnv = "MULTICA_SUPERVISOR_RUNS_DIR"

// supervisedProviders is the runtime whitelist whose workers tolerate a
// mid-run daemon restart on reattach. Phase 1 carried claude only; RUYI-390
// adds the ACP family plus zcode and deerflow: their bidirectional sessions
// rebuild daemon-side state from the wire instead of replaying it — session
// ids arrive on every session/update notification, responses to requests the
// dead daemon sent are tolerated as orphans, and a reattached client skips
// the handshake and prompt because the worker already consumed them.
// "cloud" needs no entry: it is not a provider key but the cloud-mode
// deployment of this same daemon, whose tasks still carry a concrete
// provider and therefore inherit this decision.
//
// The code layer routes every provider through agent's workerSession either
// way; this whitelist only decides where the daemon injects Supervision.
//
// Lifecycle note (updated 2026-10-05, RUYI-390): these entries rode Phase
// 1's terminal-collection and reattach judgment while the stuck-running
// root cause was open (provisional then). The lifecycle has since
// converged: stdin EOF reaches the worker (RUYI-424), a finishing worker
// gets a bounded natural-exit window with cancel as the fallback
// (finishWorkerStdin), and the final→completed convergence is pinned end
// to end by the ruyi349e2e lifecycle scenarios. New runtimes still enter
// through this whitelist plus the supervisedTargets registration, under
// RUYI-349's unified lifecycle plan.
var supervisedProviders = map[string]bool{
	"claude": true,
	// ACP family + zcode + deerflow (RUYI-390 phase 2).
	"hermes": true, "kimi": true, "kiro": true, "qoder": true,
	"qoderclicn": true, "traecli": true, "grok": true, "qwenpaw": true,
	"mcode": true, "dim": true, "zeroclaw": true, "deerflow": true,
	"reasonix": true, "zcode": true,
}

// supervisedTargets enumerates the phase 2 additions for tests: each entry
// must plan a supervised run, pinning its launch path.
var supervisedTargets = []string{
	"hermes", "kimi", "kiro", "qoder", "qoderclicn", "traecli", "grok",
	"qwenpaw", "mcode", "dim", "zeroclaw", "deerflow", "reasonix", "zcode",
}

// supervisedProviderFamily resolves a task provider to the protocol family
// the supervision decision applies to. Builtin runtime identities (e.g.
// "omp") are not protocol families; they dispatch to one via
// agent.BuiltinRuntimes, and the whitelist must follow that family so a
// future ACP-family builtin runtime inherits supervision without a
// whitelist edit. Non-runtime ids pass through unchanged.
func supervisedProviderFamily(provider string) string {
	if desc, ok := agent.BuiltinRuntimeByID(provider); ok {
		return desc.ProtocolFamily
	}
	return provider
}

// setupSupervisor builds the daemon's WorkerSupervisor when this host can
// run transient user units. Leaves d.supervisor nil (and logs why) when any
// prerequisite is missing — that nil is the legacy switch.
func (d *Daemon) setupSupervisor() {
	root := os.Getenv(supervisedRunsDirEnv)
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			d.logger.Warn("worker supervision disabled: no home directory for run store", "error", err)
			return
		}
		root = filepath.Join(home, ".multica", "supervisor-runs")
	}
	mgr, err := supervisor.NewManager(root)
	if err != nil {
		d.logger.Warn("worker supervision disabled: run store unavailable", "root", root, "error", err)
		return
	}
	binPath, err := os.Executable()
	if err != nil {
		d.logger.Warn("worker supervision disabled: daemon binary path unknown", "error", err)
		return
	}
	sys := supervisor.NewSystemdCtl(d.logger)
	if err := sys.Available(); err != nil {
		d.logger.Info("worker supervision disabled: systemd user manager unavailable; workers stay legacy children", "error", err)
		return
	}
	sup, err := supervisor.New(mgr, sys, binPath, d.logger)
	if err != nil {
		d.logger.Warn("worker supervision disabled: supervisor init failed", "error", err)
		return
	}
	d.supervisor = sup
	d.logger.Info("worker supervision enabled: task workers launch in systemd transient units",
		"providers", "claude", "run_root", root)
}

// reconcileSupervisedRuns runs the startup reconciliation matrix (manifest ∩
// live units ∩ server in-flight) and executes the safe actions inline:
// StopOrphan kills provably unclaimed units, Quarantine records unknown
// evidence. Resume runs are left untouched — their workers keep running and
// the next claim of their task reenters them via the reattach probe in
// planSupervisedRun. ConvergeExit runs are marked consumed; their task-level
// fate stays with the server's own recovery paths, which already own the
// in-flight row.
//
// The server in-flight set is best effort: on a listing failure every active
// unit is treated as in-flight (Resume), because the one irreversible action
// in this matrix is the kill and it must never run on a guess.
func (d *Daemon) reconcileSupervisedRuns(ctx context.Context) {
	if d.supervisor == nil {
		return
	}
	inFlight := map[string]bool{}
	// listFailed marks a pass whose in-flight evidence is incomplete. The
	// matrix's kill branch keys off TaskInFlight=false, so a task missing
	// after a failed listing would read as "provably orphaned" and its live
	// unit would be killed on a guess — the exact daemon-redeploy scenario
	// this package exists to survive. Any failure therefore disables the
	// kill path for the whole pass; the next healthy pass reclassifies.
	listFailed := false
	for _, rid := range d.allRuntimeIDs() {
		tasks, err := d.client.ListInFlightTasks(ctx, rid)
		if err != nil {
			listFailed = true
			d.logger.Warn("supervisor reconcile: in-flight list failed; kill decisions disabled for this pass",
				"runtime_id", rid, "error", err)
			continue
		}
		for _, t := range tasks {
			inFlight[t.ID] = true
		}
	}
	rec := &supervisor.Reconciler{
		Mgr:   d.supervisor.Manager(),
		Units: d.supervisor.Systemd(),
		Log:   d.logger,
		// On a failed listing every task reads as in flight: active units
		// resolve to Resume (non-destructive) and dead-unit classifications
		// are untouched (they never consult TaskInFlight), so the one
		// irreversible action in the matrix never runs on incomplete
		// evidence.
		TaskInFlight: func(taskID string) bool { return listFailed || inFlight[taskID] },
	}
	results, err := rec.Run(ctx)
	if err != nil {
		d.logger.Warn("supervisor reconcile failed", "error", err)
		return
	}
	for _, res := range results {
		switch res.Decision {
		case supervisor.DecisionResume:
			d.logger.Info("supervisor reconcile: run resumed for reattach on next claim",
				"run_id", res.RunID, "task_id", res.TaskID)
		case supervisor.DecisionConvergeExit:
			// Mark consumed so later passes skip it; the run's exit evidence
			// stays in the manifest for the audit trail.
			if err := d.supervisor.Manager().MarkConverged(res.RunID); err != nil {
				d.logger.Warn("supervisor reconcile: mark converged failed", "run_id", res.RunID, "error", err)
			}
			d.logger.Info("supervisor reconcile: finished run converged", "run_id", res.RunID, "task_id", res.TaskID)
		default:
			d.logger.Info("supervisor reconcile", "run_id", res.RunID, "task_id", res.TaskID,
				"decision", res.Decision.String(), "reason", res.Reason)
		}
	}

	// After the pass settles the classifications, reclaim spent evidence:
	// runs consumed past the retention window. Runs converged by THIS pass
	// are inside the window and survive until a later pass.
	gc := &supervisor.RetentionGC{
		Mgr:   d.supervisor.Manager(),
		Units: d.supervisor.Systemd(),
		Log:   d.logger,
	}
	summary, err := gc.Run(ctx)
	if err != nil {
		d.logger.Warn("supervisor retention GC failed", "error", err)
		return
	}
	if summary.Removed > 0 {
		d.logger.Info("supervisor retention GC", "removed", summary.Removed, "run_ids", summary.RemovedRunIDs)
	}
}

// runIDSanitizer matches what ValidateRunID accepts: lowercase, digits and
// dashes. Task ids are UUIDs, but the sanitizer keeps a malformed id from
// wedging every launch of its task.
var runIDSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

// sanitizeRunID folds an arbitrary task id into ValidateRunID's alphabet.
func sanitizeRunID(taskID string) string {
	s := strings.ToLower(taskID)
	s = runIDSanitizer.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "task"
	}
	if len(s) > 48 {
		s = s[len(s)-48:]
	}
	return s
}

// supervisedUnitAlive cross-checks the systemd unit recorded in a run
// manifest against the user manager itself. Manifests are written once at
// launch and only gain an exit record on a proven exit, so a host reboot or
// user-manager restart strands them claiming a worker whose unit no longer
// exists — the reconcile matrix's Lost corner. An inactive unit proves the
// recorded worker is gone; a nil systemd (supervision disabled, test
// supervisors) or a probe error fails open and keeps the
// manifest-trusting answer, so no reattach-or-abandon decision ever rides on
// a probe failure.
func (d *Daemon) supervisedUnitAlive(man *supervisor.Manifest) bool {
	if d.supervisor == nil || d.supervisor.Systemd() == nil {
		return true
	}
	active, err := d.supervisor.Systemd().UnitActive(context.Background(), man.Unit)
	if err != nil {
		d.logger.Warn("supervised liveness: unit probe failed; assuming alive",
			"run_id", man.RunID, "unit", man.Unit, "error", err)
		return true
	}
	return active
}

// planSupervisedRun decides how (and whether) one backend Execute is
// supervised. It is the claim-side reattach probe: a manifest that exists
// without an exit record means a previous daemon launched this task's worker
// and died before consuming it — the Execute must REENTER that worker (no
// prompt rewrite, logs resume from the persisted offset) instead of launching
// a second one on top of it. The reattach trusts the manifest only as far as
// systemd corroborates it: a manifest whose unit the user manager reports
// gone is a stranded record (host reboot, manager restart), not a live
// worker, and the plan steps to a fresh generation instead of reattaching a
// corpse.
//
// Runs are one Execute each: a task's segmented-continuation retry is a new
// worker and takes the next attempt slot. Attempt generations (`-2`, `-3`)
// step past manifests whose run already exited, so a server-side retry of a
// finished task gets a fresh unit id deterministically — every daemon that
// looks at the same task + attempt derives the same run id, which is what
// makes reattach survive the restart. Generation suffixes stay numeric
// because the run id must keep the UUID-ish grammar unit names are built
// from (hex digits and dashes only); a task id outside that grammar after
// sanitizing falls back to the legacy path rather than guessing.
func (d *Daemon) planSupervisedRun(provider, taskID string, attempt int) *agent.Supervision {
	if d.supervisor == nil || attempt < 1 || !supervisedProviders[supervisedProviderFamily(provider)] {
		return nil
	}
	base := fmt.Sprintf("%s-%d", sanitizeRunID(taskID), attempt)
	if supervisor.ValidateRunID(base) != nil {
		return nil
	}
	runID := base
	reattach := false
	for gen := 2; ; gen++ {
		man, err := d.supervisor.Manager().ReadManifest(runID)
		if err != nil || man == nil {
			break // no manifest: fresh launch under this id
		}
		if man.Exit == nil && d.supervisedUnitAlive(man) {
			reattach = true // live worker from a previous daemon
			break
		}
		if man.Exit == nil {
			// The manifest claims a running worker but its unit is gone:
			// the worker cannot come back, so step past the stale record to
			// a fresh generation instead of reattaching a corpse. The
			// manifest stays on disk as audit trail.
			d.logger.Info("supervised run plan: manifest claims running but unit is gone; stepping to a fresh generation",
				"run_id", runID, "task_id", taskID, "unit", man.Unit)
		}
		if gen > 64 {
			// Absurd attempt count: fall back to legacy rather than loop.
			return nil
		}
		runID = fmt.Sprintf("%s-%d", base, gen)
	}
	sup := &agent.Supervision{
		Supervisor: d.supervisor,
		RunID:      runID,
		TaskID:     taskID,
		Runtime:    provider,
		Reattach:   reattach,
	}
	if reattach {
		d.logger.Info("supervised run reattach: continuing worker launched by a previous daemon",
			"run_id", runID, "task_id", taskID, "provider", provider)
	}
	return sup
}

// supervisedWorkerAlive reports whether any supervised run manifest for
// taskID records a worker that launched and never exited. This is the
// daemon-side liveness signal that survives daemon death — unlike the
// env-root lock, which the daemon process holds and therefore releases on
// exactly the crash this package exists to survive. The manifest is only
// corroborated as far as systemd agrees: a running-shaped manifest whose
// unit the user manager reports gone is a stranded record (host reboot,
// manager restart), not a live worker, and must not veto the in-flight
// recovery forever.
func (d *Daemon) supervisedWorkerAlive(taskID string) bool {
	if d.supervisor == nil {
		return false
	}
	runIDs, err := d.supervisor.Manager().ListRunIDs()
	if err != nil {
		return false
	}
	for _, runID := range runIDs {
		man, err := d.supervisor.Manager().ReadManifest(runID)
		if err != nil || man == nil || man.TaskID != taskID {
			continue
		}
		if man.Exit == nil && man.State == supervisor.StateRunning && d.supervisedUnitAlive(man) {
			return true
		}
	}
	return false
}
