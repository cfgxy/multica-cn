package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Decision is the reconcile matrix's verdict for one run. The matrix is a
// pure function so every startup decision is auditable and unit-testable —
// the daemon restart MUST NOT guess about live work.
type Decision int

const (
	// DecisionNone: nothing to do (already terminal).
	DecisionNone Decision = iota
	// DecisionResume: worker alive and its task is still in flight on the
	// server — the daemon re-attaches and the run continues.
	DecisionResume
	// DecisionStopOrphan: worker alive but its task is no longer in flight —
	// stop the orphan precisely (unit cgroup only).
	DecisionStopOrphan
	// DecisionConvergeExit: worker finished while the daemon was down; the
	// daemon re-attaches to the recorded output + exit and converges the run
	// by real evidence.
	DecisionConvergeExit
	// DecisionQuarantine: evidence contradicts classification (unit with no
	// manifest; manifest whose unit died without exit evidence but a task
	// lock still exists). Recorded, never silently killed — an operator or a
	// later pass with better evidence decides.
	DecisionQuarantine
	// DecisionLost: worker's unit is gone, no exit evidence, and no task
	// lock — the worker is provably not running anywhere. Converge as failed
	// with the honest explanation that the exit went unrecorded.
	DecisionLost
)

func (d Decision) String() string {
	switch d {
	case DecisionNone:
		return "none"
	case DecisionResume:
		return "resume"
	case DecisionStopOrphan:
		return "stop_orphan"
	case DecisionConvergeExit:
		return "converge_exit"
	case DecisionQuarantine:
		return "quarantine"
	case DecisionLost:
		return "lost"
	default:
		return fmt.Sprintf("decision(%d)", int(d))
	}
}

// ReconcileInput is one run's evidence snapshot.
type ReconcileInput struct {
	// Manifest is nil when a live unit has no run directory (unknown unit).
	Manifest *Manifest
	// UnitActive: systemd still lists the unit.
	UnitActive bool
	// LockHeld: the run's env-root task lock (.task_lock, RUYI-225) probes
	// alive — the strongest local liveness signal independent of systemd.
	LockHeld bool
	// TaskInFlight: the server still considers this task running.
	TaskInFlight bool
}

// Decide is the decision matrix.
func Decide(in ReconcileInput) Decision {
	if in.Manifest == nil {
		// A live multica-run unit we have no manifest for. Never silently
		// kill what we cannot classify (dispatch requirement).
		return DecisionQuarantine
	}
	exitProven := in.Manifest.Exit != nil
	switch {
	case in.UnitActive && in.TaskInFlight:
		return DecisionResume
	case in.UnitActive && !in.TaskInFlight:
		return DecisionStopOrphan
	case !in.UnitActive && exitProven:
		return DecisionConvergeExit
	case !in.UnitActive && !exitProven && in.LockHeld:
		// Unit gone but a live lock says something may still run: do not
		// fabricate a terminal state on conflicting evidence.
		return DecisionQuarantine
	default:
		// !UnitActive, no exit record, no lock: the worker is provably not
		// running; only its exit went unrecorded (systemd restart, OOM
		// manager, host crash).
		return DecisionLost
	}
}

// UnitLister is the systemd surface reconciliation needs (interface for fakes).
type UnitLister interface {
	UnitActive(ctx context.Context, unit string) (bool, error)
	ListUnits(ctx context.Context) ([]string, error)
	KillUnit(ctx context.Context, unit string) error
}

var _ UnitLister = (*systemdCtl)(nil)

// Reconciler walks manifest ∩ live units at daemon startup and executes the
// safe actions inline; Resume / ConvergeExit are reported back — their
// execution belongs to the daemon's task layer (re-attach or converge a run),
// not to the supervisor.
type Reconciler struct {
	Mgr   *Manager
	Units UnitLister
	Log   *slog.Logger

	// LockHeld probes the run's env-root task lock. nil means "unknown".
	LockHeld func(runID string) bool
	// TaskInFlight reports whether the SERVER still attributes the task to a
	// live run — the startup reconciliation runs before this process has
	// claimed anything, so "in flight" there would be always-false and would
	// downgrade every surviving worker to StopOrphan. nil means "unknown",
	// which Decide treats the same as false.
	TaskInFlight func(taskID string) bool
}

// ReconcileResult is one decision, for the audit comment and the daemon's
// follow-up work list.
type ReconcileResult struct {
	RunID    string   `json:"run_id"`
	TaskID   string   `json:"task_id"`
	Unit     string   `json:"unit"`
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason,omitempty"`
}

// Run performs one reconciliation pass over the whole runs root.
func (r *Reconciler) Run(ctx context.Context) ([]ReconcileResult, error) {
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	live, err := r.Units.ListUnits(ctx)
	if err != nil {
		return nil, err
	}
	liveSet := make(map[string]bool, len(live))
	for _, u := range live {
		liveSet[u] = true
	}

	ids, err := r.Mgr.ListRunIDs()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(ids))
	var results []ReconcileResult
	for _, id := range ids {
		man, err := r.Mgr.ReadManifest(id)
		if err != nil {
			log.Warn("supervisor: reconcile skipping unreadable manifest", "run_id", id, "error", err)
			continue
		}
		seen[man.Unit] = true
		if man.ConvergedAt != nil {
			continue // consumed by a previous pass; retention GC is a later phase
		}
		res := r.classify(ctx, man, liveSet[man.Unit])
		results = append(results, res)
		r.act(ctx, res, man)
	}

	// Live units we have no manifest for: unknown, quarantine.
	for _, unit := range live {
		if seen[unit] {
			continue
		}
		runID := runIDFromUnit(unit)
		if runID == "" {
			continue
		}
		res := ReconcileResult{
			RunID:    runID,
			Unit:     unit,
			Decision: DecisionQuarantine,
			Reason:   "live unit without manifest",
		}
		results = append(results, res)
		if err := r.Mgr.WriteQuarantine(&QuarantineRecord{
			RunID:     runID,
			Unit:      unit,
			Reason:    res.Reason,
			FirstSeen: time.Now().UTC(),
		}); err != nil {
			log.Error("supervisor: quarantine write failed", "run_id", runID, "error", err)
		}
	}
	return results, nil
}

func (r *Reconciler) classify(ctx context.Context, man *Manifest, unitActive bool) ReconcileResult {
	in := ReconcileInput{Manifest: man, UnitActive: unitActive}
	if r.LockHeld != nil {
		in.LockHeld = r.LockHeld(man.RunID)
	}
	if r.TaskInFlight != nil {
		in.TaskInFlight = r.TaskInFlight(man.TaskID)
	}
	d := Decide(in)
	res := ReconcileResult{RunID: man.RunID, TaskID: man.TaskID, Unit: man.Unit, Decision: d}
	switch d {
	case DecisionResume:
		res.Reason = "unit active and task in flight"
	case DecisionStopOrphan:
		res.Reason = "unit active but task not in flight"
	case DecisionConvergeExit:
		res.Reason = fmt.Sprintf("worker exited while daemon was down (%s)", exitSourceLabel(man.Exit))
	case DecisionQuarantine:
		if !unitActive && man.Exit == nil {
			res.Reason = "unit gone without exit evidence but task lock held"
		} else {
			res.Reason = "live unit without manifest"
		}
	case DecisionLost:
		res.Reason = "unit gone, no exit record, no task lock"
	}
	return res
}

func exitSourceLabel(e *ExitRecord) string {
	if e == nil {
		return "no record"
	}
	return "source " + e.Source
}

// act executes the decisions the supervisor itself owns: precise orphan
// stops and quarantine records. Resume and ConvergeExit stay with the daemon
// task layer; Lost is recorded only — fabricating a fake exit is exactly what
// Phase 1 must not do, so convergence with an unrecorded exit is the daemon's
// explicit failure path.
func (r *Reconciler) act(ctx context.Context, res ReconcileResult, man *Manifest) {
	switch res.Decision {
	case DecisionStopOrphan:
		if err := r.Units.KillUnit(ctx, man.Unit); err != nil {
			r.logError(res, "orphan stop failed", err)
			return
		}
		man.State = StateExited
		man.Exit = &ExitRecord{Code: -1, Signal: "killed", At: time.Now().UTC(), Source: ExitSourceSupervisor}
		if err := r.Mgr.WriteManifest(man); err != nil {
			r.logError(res, "orphan stop exit record failed", err)
		}
	case DecisionQuarantine:
		if err := r.Mgr.WriteQuarantine(&QuarantineRecord{
			RunID:     res.RunID,
			Unit:      res.Unit,
			Reason:    res.Reason,
			FirstSeen: time.Now().UTC(),
		}); err != nil {
			r.logError(res, "quarantine write failed", err)
		}
	}
}

func (r *Reconciler) logError(res ReconcileResult, what string, err error) {
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	log.Error("supervisor: reconcile action failed", "run_id", res.RunID, "decision", res.Decision.String(), "what", what, "error", err)
}

// runIDFromUnit reverses UnitName; it returns "" for anything outside the
// managed namespace (the supervisor never acts on those).
func runIDFromUnit(unit string) string {
	if !strings.HasPrefix(unit, unitPrefix) || !strings.HasSuffix(unit, ".service") {
		return ""
	}
	id := strings.TrimSuffix(strings.TrimPrefix(unit, unitPrefix), ".service")
	if ValidateRunID(id) != nil {
		return ""
	}
	return id
}
