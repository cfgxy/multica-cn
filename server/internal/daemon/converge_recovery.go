package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/multica-ai/multica/server/internal/daemon/supervisor"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// Offline-completion converge (RUYI-464): a supervised worker can finish
// while its daemon is down. The launcher proves the exit into the run
// manifest, but before RUYI-464 nothing ever reported that evidence to the
// server — the in-flight row went through the fail("runtime_recovery") +
// auto-retry chain and the task re-executed from scratch, the user receiving
// the rerun's result while the original run's exit and output stayed local.
//
// convergeFinishedSupervisedRun closes that gap for one task: find the
// newest exited-but-unconverged run manifest, drain the output the dead
// daemon never consumed through the run's resumable tail (persisted cursor +
// dedup ring, so a replay re-delivers nothing), parse the stream's terminal
// result frame when present, and report the real terminal state through
// reportTerminalTask — the same guarded complete/fail exit every live run
// uses, with the server's already-terminal tolerance as the idempotency
// backstop. Success marks the manifest converged; a transient rejection
// marks nothing and waits for the next cycle or restart; a permanent
// rejection falls back to the pre-existing bounded fail+retry path.

// convergeOutcome reports what a converge pass did with a task's finished
// supervised run, for the caller's log line and branch decision.
type convergeOutcome int

const (
	// convergeNone: no exited-unconverged manifest exists for the task.
	convergeNone convergeOutcome = iota
	// convergeReported: the run's proven exit and drained output were
	// reported to the server as the task's terminal state; the manifest is
	// marked consumed.
	convergeReported
	// convergeFellBack: the server permanently rejected the report; the
	// existing bounded FailTask("runtime_recovery") path fired instead.
	convergeFellBack
	// convergeTransient: the report failed transiently; nothing was marked
	// and nothing degraded — the next cycle or restart retries.
	convergeTransient
	// convergeSuperseded: a newer generation of the task exists; the stale
	// generation's evidence must not race the live one.
	convergeSuperseded
	// convergeActive: the task is active in this process or a converge pass
	// for it is already running.
	convergeActive
)

func (o convergeOutcome) String() string {
	switch o {
	case convergeNone:
		return "none"
	case convergeReported:
		return "reported"
	case convergeFellBack:
		return "fell_back"
	case convergeTransient:
		return "transient"
	case convergeSuperseded:
		return "superseded"
	case convergeActive:
		return "active"
	default:
		return "unknown"
	}
}

// offlineResultFrame is the daemon-side subset of the stream-json terminal
// frame the claude/codebuddy family prints as its last stdout line. Protocol
// families without such a frame converge by exit-code mapping — the plan's
// sanctioned fallback; full per-protocol parsing stays in pkg/agent.
type offlineResultFrame struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// scanOfflineResultFrame returns the last terminal frame in the drained
// stdout, if any line parses as one.
func scanOfflineResultFrame(stdout []byte) (offlineResultFrame, bool) {
	var found offlineResultFrame
	ok := false
	for _, line := range bytes.Split(stdout, []byte{'\n'}) {
		var f offlineResultFrame
		if json.Unmarshal(line, &f) != nil || f.Type != "result" {
			continue
		}
		found, ok = f, true
	}
	return found, ok
}

// exitEvidenceMsg builds the failure text for an exited run without a usable
// result frame: the raw exit facts plus where the full logs live.
func exitEvidenceMsg(man *supervisor.Manifest) string {
	e := man.Exit
	desc := fmt.Sprintf("exit code %d", e.Code)
	if e.Signal != "" {
		desc += fmt.Sprintf(" (signal %s)", e.Signal)
	}
	return fmt.Sprintf("supervised worker exited abnormally while daemon was offline (run %s): %s; stdout log: %s; stderr log: %s",
		man.RunID, desc, man.StdoutLog, man.StderrLog)
}

// tryHoldConvergeSlot reserves the per-task single-flight slot so the
// startup reconcile and the workspace-sync recovery loop cannot run two
// converge passes for one task concurrently. Lazy-init mirrors
// markTaskActiveInProcess so directly-constructed test daemons stay safe.
func (d *Daemon) tryHoldConvergeSlot(taskID string) (release func(), ok bool) {
	d.convergeInFlightMu.Lock()
	defer d.convergeInFlightMu.Unlock()
	if d.convergeInFlight == nil {
		d.convergeInFlight = make(map[string]struct{})
	}
	if _, held := d.convergeInFlight[taskID]; held {
		return nil, false
	}
	d.convergeInFlight[taskID] = struct{}{}
	return func() {
		d.convergeInFlightMu.Lock()
		delete(d.convergeInFlight, taskID)
		d.convergeInFlightMu.Unlock()
	}, true
}

// sortedManifestsForTask returns every readable manifest of the task,
// oldest first (StartedAt, then run id as the deterministic tie-break —
// generation suffixes sort naturally).
func (d *Daemon) sortedManifestsForTask(taskID string) []*supervisor.Manifest {
	runIDs, err := d.supervisor.Manager().ListRunIDs()
	if err != nil {
		return nil
	}
	var mans []*supervisor.Manifest
	for _, runID := range runIDs {
		man, err := d.supervisor.Manager().ReadManifest(runID)
		if err != nil || man == nil || man.TaskID != taskID {
			continue
		}
		mans = append(mans, man)
	}
	sort.Slice(mans, func(i, j int) bool {
		if !mans[i].StartedAt.Equal(mans[j].StartedAt) {
			return mans[i].StartedAt.Before(mans[j].StartedAt)
		}
		return mans[i].RunID < mans[j].RunID
	})
	return mans
}

// supersedes reports whether man is a newer generation than candidate.
func supersedes(man, candidate *supervisor.Manifest) bool {
	if man == candidate {
		return false
	}
	if !man.StartedAt.Equal(candidate.StartedAt) {
		return man.StartedAt.After(candidate.StartedAt)
	}
	return man.RunID > candidate.RunID
}

// convergeFinishedSupervisedRun reports a finished supervised run's real
// terminal state to the server instead of letting the recovery chain fail
// the task into a duplicate re-execution. Exactly one run per call is
// reported: the newest exited-but-unconvered generation, and only while no
// manifest of the task is newer than it — a newer generation (running or
// already consumed) means the task moved on, and a stale report would race
// the live generation through the server's first-terminal-writer guard.
func (d *Daemon) convergeFinishedSupervisedRun(ctx context.Context, taskID string) convergeOutcome {
	if d.supervisor == nil {
		return convergeNone
	}
	if d.isTaskActiveInProcess(taskID) {
		return convergeActive
	}
	release, ok := d.tryHoldConvergeSlot(taskID)
	if !ok {
		return convergeActive
	}
	defer release()

	mans := d.sortedManifestsForTask(taskID)
	var candidate *supervisor.Manifest
	for _, man := range mans {
		if man.Exit != nil && man.ConvergedAt == nil {
			candidate = man // keep walking: the newest exited-unconverged wins
		}
	}
	if candidate == nil {
		return convergeNone
	}
	for _, man := range mans {
		if supersedes(man, candidate) {
			return convergeSuperseded
		}
	}
	return d.reportConvergedRun(ctx, candidate)
}

// reportConvergedRun drains one exited run's unconsumed output, maps the
// proven exit to a terminal state, reports it, and marks the run consumed.
func (d *Daemon) reportConvergedRun(ctx context.Context, man *supervisor.Manifest) convergeOutcome {
	runID := man.RunID
	// Reattach binds the drain handles without a live control connection:
	// the manifest's proven exit ends the resumable tails, and each stream
	// resumes from the persisted read cursor with the dedup ring, so a
	// partial drain before a crash re-delivers nothing already handed out.
	handle, err := d.supervisor.Reattach(ctx, runID)
	if err != nil {
		d.logger.Warn("supervised converge: reattach failed; leaving task to the recovery chain",
			"run_id", runID, "task_id", man.TaskID, "error", err)
		return convergeNone
	}
	stdout := d.drainStream(ctx, "stdout", handle.StdoutStream)
	stderr := d.drainStream(ctx, "stderr", handle.StderrStream)

	// Terminal state: the run's own result frame first, the ExitRecord
	// mapping as the sanctioned fallback.
	var kind terminalTaskReportKind
	var output, errMsg, failureReason string
	frame, hasFrame := scanOfflineResultFrame(stdout)
	switch {
	case hasFrame && frame.IsError:
		kind = terminalTaskReportFail
		errMsg = orExitEvidence(frame.Result, man)
		failureReason = taskfailure.ReasonAgentProcessFailure.String()
	case hasFrame:
		kind = terminalTaskReportComplete
		output = frame.Result
	case man.Exit.Code == 0 && man.Exit.Signal == "":
		kind = terminalTaskReportComplete
	default:
		kind = terminalTaskReportFail
		errMsg = exitEvidenceMsg(man)
		failureReason = taskfailure.ReasonAgentProcessFailure.String()
	}

	if err := d.reportTerminalTask(ctx, terminalTaskReport{
		kind:          kind,
		taskID:        man.TaskID,
		output:        output,
		errorMessage:  errMsg,
		failureReason: failureReason,
	}); err != nil {
		if isTransientError(err) {
			d.logger.Warn("supervised converge: terminal report failed transiently; leaving task for the next pass",
				"run_id", runID, "task_id", man.TaskID, "error", err)
			return convergeTransient
		}
		// Permanent (4xx) rejection: fall back to the existing bounded
		// fail+retry path — the exact pre-RUYI-464 behavior — with the
		// manifest left unconverged as audit trail.
		d.logger.Warn("supervised converge: terminal report permanently rejected; falling back to runtime_recovery",
			"run_id", runID, "task_id", man.TaskID, "error", err)
		if failErr := d.client.FailTask(ctx, man.TaskID, orphanRecoveryErrMsg, "", "", "", "runtime_recovery", false, "", ""); failErr != nil {
			d.logger.Warn("supervised converge: runtime_recovery fallback failed; leaving task for the next pass",
				"run_id", runID, "task_id", man.TaskID, "error", failErr)
			return convergeTransient
		}
		return convergeFellBack
	}

	if err := d.supervisor.Manager().MarkConverged(runID); err != nil {
		// The server already holds the truth; a marking failure only risks
		// one idempotent re-report (already-terminal tolerance) next pass.
		d.logger.Warn("supervised converge: mark converged failed", "run_id", runID, "task_id", man.TaskID, "error", err)
	}
	d.logger.Info("supervised converge: offline-finished run reported and marked",
		"run_id", runID, "task_id", man.TaskID, "kind", kindString(kind),
		"stdout_bytes", len(stdout), "stderr_bytes", len(stderr))
	return convergeReported
}

// drainStream reads one resumable stream to its proven EOF and closes it.
// A short read is safe: the cursor advanced with each handed-out batch, so
// the next pass drains only the remainder.
func (d *Daemon) drainStream(ctx context.Context, name string, open func(context.Context) (io.ReadCloser, error)) []byte {
	rc, err := open(ctx)
	if err != nil {
		d.logger.Warn("supervised converge: open stream failed", "stream", name, "error", err)
		return nil
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		d.logger.Warn("supervised converge: drain failed", "stream", name, "error", err)
	}
	return data
}

func kindString(kind terminalTaskReportKind) string {
	if kind == terminalTaskReportComplete {
		return "complete"
	}
	return "fail"
}

func orExitEvidence(text string, man *supervisor.Manifest) string {
	if text != "" {
		return text
	}
	return exitEvidenceMsg(man)
}
