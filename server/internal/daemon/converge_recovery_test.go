package daemon

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/supervisor"
)

// seedExitedManifest installs the offline-completion shape converge exists
// for: a manifest whose launcher recorded a proven exit while the daemon was
// down, plus the per-run log files the resumable tail drains from. stdout /
// stderr carry the raw bytes appended after the daemon's last read; pass ""
// to omit the file entirely (a worker that wrote nothing).
func seedExitedManifest(t *testing.T, mgr *supervisor.Manager, runID, taskID string, startedAt time.Time, exit *supervisor.ExitRecord, stdout, stderr string) {
	t.Helper()
	man := &supervisor.Manifest{
		Version:   1,
		RunID:     runID,
		TaskID:    taskID,
		Runtime:   "claude",
		Unit:      supervisor.UnitName(runID),
		State:     supervisor.StateRunning,
		StartedAt: startedAt,
		StdoutLog: filepath.Join(mgr.Dir(runID), "worker-stdout.log"),
		StderrLog: filepath.Join(mgr.Dir(runID), "worker-stderr.log"),
		Exit:      exit,
	}
	if exit != nil {
		man.State = supervisor.StateExited
	}
	if err := mgr.WriteManifest(man); err != nil {
		t.Fatalf("seed manifest %s: %v", runID, err)
	}
	for path, content := range map[string]string{man.StdoutLog: stdout, man.StderrLog: stderr} {
		if content == "" {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("seed log %s: %v", path, err)
		}
	}
}

func exitRecord(code int, signal string) *supervisor.ExitRecord {
	return &supervisor.ExitRecord{Code: code, Signal: signal, At: time.Now().UTC(), Source: supervisor.ExitSourceLauncher}
}

// resultFrame is the stream-json terminal frame the claude/codebuddy family
// prints as its last stdout line.
func resultFrame(text string, isError bool) string {
	flag := "false"
	if isError {
		flag = "true"
	}
	return `{"type":"result","subtype":"success","is_error":` + flag + `,"result":"` + text + `","num_turns":3}`
}

// wireConvergeFixture gives a recovery fixture a shimmed supervisor with no
// live units and returns the manager for seeding manifests.
func wireConvergeFixture(t *testing.T) (*recoveryFixture, *supervisor.Manager) {
	t.Helper()
	fx := newRecoveryFixture(t)
	mgr := wireShimmedSupervisor(t, fx.daemon)
	return fx, mgr
}

// TestConvergeFinishedSupervisedRun_ExitZeroReportsComplete: a worker that
// finished cleanly while the daemon was down converges into CompleteTask with
// the drained result-frame text — never FailTask — and the manifest is marked
// consumed afterwards (RUYI-464 acceptance 1 + 5).
func TestConvergeFinishedSupervisedRun_ExitZeroReportsComplete(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""),
		"{\"type\":\"assistant\",\"message\":{\"content\":[]}}\n"+resultFrame("all done", false)+"\n", "")

	out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID)
	if out != convergeReported {
		t.Fatalf("outcome = %v, want reported", out)
	}
	completes := fx.recordedCompletes()
	if len(completes) != 1 || completes[0].TaskID != testTaskID || completes[0].Output != "all done" {
		t.Fatalf("completes = %+v, want [%s/all done]", completes, testTaskID)
	}
	if fails := fx.recordedFails(); len(fails) != 0 {
		t.Fatalf("clean exit must not fail the task: %+v", fails)
	}
	man, err := mgr.ReadManifest(runID)
	if err != nil || man.ConvergedAt == nil {
		t.Fatalf("manifest not marked converged: %+v err %v", man, err)
	}
}

// TestConvergeFinishedSupervisedRun_DrainAdvancesCursor: the drained offsets
// land in the persisted read state, so a replayed drain re-delivers nothing
// (idempotent retransmission contract of the resumable tail).
func TestConvergeFinishedSupervisedRun_DrainAdvancesCursor(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	stdout := "line one\n" + resultFrame("done", false) + "\n"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""), stdout, "boom\n")

	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeReported {
		t.Fatalf("outcome = %v, want reported", out)
	}
	rs, err := mgr.LoadReadState(runID)
	if err != nil {
		t.Fatalf("load read state: %v", err)
	}
	if rs.StdoutOffset != int64(len(stdout)) || rs.StderrOffset != int64(len("boom\n")) {
		t.Fatalf("read state = %+v, want offsets %d/%d", rs, len(stdout), len("boom\n"))
	}
}

// TestConvergeFinishedSupervisedRun_ExitZeroWithoutFrameCompletes: no parse
// frame (ACP family, non-JSON output) falls back to the ExitRecord mapping —
// exit 0 still completes, with empty output (the sanctioned bounded loss).
func TestConvergeFinishedSupervisedRun_ExitZeroWithoutFrameCompletes(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""), "plain text tail\n", "")

	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeReported {
		t.Fatalf("outcome = %v, want reported", out)
	}
	if fails := fx.recordedFails(); len(fails) != 0 {
		t.Fatalf("exit 0 must not fail: %+v", fails)
	}
	completes := fx.recordedCompletes()
	if len(completes) != 1 || completes[0].TaskID != testTaskID {
		t.Fatalf("completes = %+v, want one for %s", completes, testTaskID)
	}
}

// TestConvergeFinishedSupervisedRun_NonZeroExitFailsWithRealReason: an
// abnormal exit reports the real evidence with the process-failure reason —
// never runtime_recovery, which would silently re-execute the task
// (RUYI-464 acceptance 2).
func TestConvergeFinishedSupervisedRun_NonZeroExitFailsWithRealReason(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(1, ""), "partial output\n", "")

	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeReported {
		t.Fatalf("outcome = %v, want reported", out)
	}
	if completes := fx.recordedCompletes(); len(completes) != 0 {
		t.Fatalf("failing exit must not complete: %+v", completes)
	}
	fails := fx.recordedFails()
	if len(fails) != 1 || fails[0].TaskID != testTaskID {
		t.Fatalf("fails = %+v, want one for %s", fails, testTaskID)
	}
	if fails[0].FailureReason != "agent_error.process_failure" {
		t.Fatalf("failureReason = %q, want agent_error.process_failure", fails[0].FailureReason)
	}
	if fails[0].FailureReason == "runtime_recovery" {
		t.Fatalf("converge must not ride the runtime_recovery reason")
	}
	if !strings.Contains(fails[0].ErrorMsg, "exit code 1") {
		t.Fatalf("errorMsg = %q, want exit evidence", fails[0].ErrorMsg)
	}
	if man, err := mgr.ReadManifest(runID); err != nil || man.ConvergedAt == nil {
		t.Fatalf("manifest not marked converged after successful fail report: %+v err %v", man, err)
	}
}

// TestConvergeFinishedSupervisedRun_SignalExitFails: a signalled worker
// (code -1) carries the signal in the failure evidence.
func TestConvergeFinishedSupervisedRun_SignalExitFails(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(-1, "killed"), "", "")

	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeReported {
		t.Fatalf("outcome = %v, want reported", out)
	}
	fails := fx.recordedFails()
	if len(fails) != 1 || !strings.Contains(fails[0].ErrorMsg, "killed") || fails[0].FailureReason == "runtime_recovery" {
		t.Fatalf("fails = %+v, want signal evidence with real reason", fails)
	}
}

// TestConvergeFinishedSupervisedRun_ErrorResultFrameFails: a result frame
// that itself declares is_error wins over the exit code — the run's own
// verdict is the stronger terminal evidence.
func TestConvergeFinishedSupervisedRun_ErrorResultFrameFails(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""),
		resultFrame("context exhausted", true)+"\n", "")

	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeReported {
		t.Fatalf("outcome = %v, want reported", out)
	}
	if completes := fx.recordedCompletes(); len(completes) != 0 {
		t.Fatalf("error frame must not complete: %+v", completes)
	}
	fails := fx.recordedFails()
	if len(fails) != 1 || !strings.Contains(fails[0].ErrorMsg, "context exhausted") {
		t.Fatalf("fails = %+v, want error-frame text", fails)
	}
}

// TestConvergeFinishedSupervisedRun_TransientReportFailureStaysUnconverged:
// a transient (5xx) rejection marks nothing and degrades nothing — the
// manifest stays unconverged so the next cycle retries the real report
// (RUYI-464 acceptance 3).
func TestConvergeFinishedSupervisedRun_TransientReportFailureStaysUnconverged(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""), resultFrame("done", false)+"\n", "")
	shrinkTerminalRetrySchedule(t)
	fx.injectTerminalStatus(testTaskID, http.StatusBadGateway, 0)

	out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID)
	if out != convergeTransient {
		t.Fatalf("outcome = %v, want transient", out)
	}
	if fails := fx.recordedFails(); len(fails) != 0 {
		t.Fatalf("transient failure must not degrade to runtime_recovery: %+v", fails)
	}
	if man, err := mgr.ReadManifest(runID); err != nil || man.ConvergedAt != nil {
		t.Fatalf("transient failure must leave the manifest unconverged: %+v err %v", man, err)
	}
}

// TestConvergeFinishedSupervisedRun_PermanentRejectionFallsBack: a permanent
// (4xx) rejection falls back to the existing bounded FailTask("runtime_recovery")
// path and leaves the manifest unmarked for the audit trail — the status quo
// behavior, never a new permanent-running shape (RUYI-464 acceptance 3).
func TestConvergeFinishedSupervisedRun_PermanentRejectionFallsBack(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""), resultFrame("done", false)+"\n", "")
	fx.injectTerminalStatus(testTaskID, http.StatusForbidden, 0)

	out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID)
	if out != convergeFellBack {
		t.Fatalf("outcome = %v, want fellBack", out)
	}
	fails := fx.recordedFails()
	if len(fails) != 1 || fails[0].FailureReason != "runtime_recovery" {
		t.Fatalf("fails = %+v, want the runtime_recovery fallback", fails)
	}
	if man, err := mgr.ReadManifest(runID); err != nil || man.ConvergedAt != nil {
		t.Fatalf("fallback must leave the manifest unconverged for audit: %+v err %v", man, err)
	}
}

// TestConvergeFinishedSupervisedRun_AlreadyConvergedSkips: a manifest the
// task layer already consumed never reports again (RUYI-464 acceptance 4).
func TestConvergeFinishedSupervisedRun_AlreadyConvergedSkips(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""), resultFrame("done", false)+"\n", "")
	if err := mgr.MarkConverged(runID); err != nil {
		t.Fatalf("pre-converge: %v", err)
	}

	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeNone {
		t.Fatalf("outcome = %v, want none", out)
	}
	if n := len(fx.recordedCompletes()) + len(fx.recordedFails()); n != 0 {
		t.Fatalf("converged run generated %d terminal calls, want 0", n)
	}
}

// TestConvergeFinishedSupervisedRun_SupersededGenerationSkips: a newer
// generation of the same task (running or exited) means the task moved on —
// reporting the stale generation's terminal state would race the live
// generation (RUYI-464 acceptance 4).
func TestConvergeFinishedSupervisedRun_SupersededGenerationSkips(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	base := time.Now().Add(-2 * time.Hour)
	seedExitedManifest(t, mgr, testTaskID+"-1", testTaskID, base, exitRecord(0, ""), resultFrame("stale", false)+"\n", "")
	seedExitedManifest(t, mgr, testTaskID+"-1-2", testTaskID, base.Add(time.Hour), exitRecord(0, ""), resultFrame("done", false)+"\n", "")

	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeReported {
		t.Fatalf("outcome = %v, want reported (newest exited generation converges)", out)
	}
	completes := fx.recordedCompletes()
	if len(completes) != 1 || completes[0].Output != "done" {
		t.Fatalf("completes = %+v, want the newest generation's output", completes)
	}
}

// TestConvergeFinishedSupervisedRun_SupersededByRunningGenerationSkips: a
// newer generation still running (a server retry claimed while converge was
// pending) vetoes the report entirely.
func TestConvergeFinishedSupervisedRun_SupersededByRunningGenerationSkips(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	base := time.Now().Add(-2 * time.Hour)
	seedExitedManifest(t, mgr, testTaskID+"-1", testTaskID, base, exitRecord(0, ""), resultFrame("stale", false)+"\n", "")
	seedExitedManifest(t, mgr, testTaskID+"-1-2", testTaskID, base.Add(time.Hour), nil, "", "")

	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeSuperseded {
		t.Fatalf("outcome = %v, want superseded", out)
	}
	if n := len(fx.recordedCompletes()) + len(fx.recordedFails()); n != 0 {
		t.Fatalf("superseded run generated %d terminal calls, want 0", n)
	}
}

// TestConvergeFinishedSupervisedRun_SkipsTaskActiveInProcess: a task this
// process is executing right now is not converging anything.
func TestConvergeFinishedSupervisedRun_SkipsTaskActiveInProcess(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	seedExitedManifest(t, mgr, testTaskID+"-1", testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""), resultFrame("done", false)+"\n", "")
	fx.daemon.markTaskActiveInProcess(testTaskID)
	t.Cleanup(func() { fx.daemon.unmarkTaskActiveInProcess(testTaskID) })

	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeActive {
		t.Fatalf("outcome = %v, want active", out)
	}
	if n := len(fx.recordedCompletes()) + len(fx.recordedFails()); n != 0 {
		t.Fatalf("active task generated %d terminal calls, want 0", n)
	}
}

// TestConvergeFinishedSupervisedRun_SingleFlight: a converge pass already
// running for the task blocks a second concurrent one (recovery loop vs
// startup reconcile), and releasing the slot lets the next pass through.
func TestConvergeFinishedSupervisedRun_SingleFlight(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	seedExitedManifest(t, mgr, testTaskID+"-1", testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""), resultFrame("done", false)+"\n", "")

	release, ok := fx.daemon.tryHoldConvergeSlot(testTaskID)
	if !ok {
		t.Fatalf("fresh slot acquire failed")
	}
	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeActive {
		t.Fatalf("outcome under held slot = %v, want active", out)
	}
	release()
	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeReported {
		t.Fatalf("outcome after release = %v, want reported", out)
	}
}

// TestConvergeFinishedSupervisedRun_RepeatIsIdempotent: after a successful
// report the next pass finds nothing to do and the server sees exactly one
// terminal call (RUYI-464 acceptance 5).
func TestConvergeFinishedSupervisedRun_RepeatIsIdempotent(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	seedExitedManifest(t, mgr, testTaskID+"-1", testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""), resultFrame("done", false)+"\n", "")

	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeReported {
		t.Fatalf("first outcome = %v, want reported", out)
	}
	if out := fx.daemon.convergeFinishedSupervisedRun(context.Background(), testTaskID); out != convergeNone {
		t.Fatalf("second outcome = %v, want none", out)
	}
	if completes := fx.recordedCompletes(); len(completes) != 1 {
		t.Fatalf("completes = %d, want exactly 1", len(completes))
	}
}

// TestRecoverInFlightTasks_ConvergesExitedSupervisedWorker: the recovery
// chain, on finding an exited supervised manifest for a probe-dead task,
// reports the run's real terminal state instead of failing the task into a
// duplicate re-execution — the Gap B fix at its primary call site.
func TestRecoverInFlightTasks_ConvergesExitedSupervisedWorker(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""), resultFrame("offline result", false)+"\n", "")
	envRoot := buildEnvRoot(t, fx.root, "ws1", testTaskID)
	fx.setInFlight(InFlightTask{ID: testTaskID, WorkspaceID: "ws1", Status: "running", WorkDir: filepath.Join(envRoot, "repo")})

	fx.daemon.recoverInFlightTasksForRuntime(context.Background(), "rt-1")

	completes := fx.recordedCompletes()
	if len(completes) != 1 || completes[0].TaskID != testTaskID || completes[0].Output != "offline result" {
		t.Fatalf("completes = %+v, want the offline result reported", completes)
	}
	if fails := fx.recordedFails(); len(fails) != 0 {
		t.Fatalf("finished run was failed for re-execution: %+v", fails)
	}
	if man, err := mgr.ReadManifest(runID); err != nil || man.ConvergedAt == nil {
		t.Fatalf("manifest not marked converged: %+v err %v", man, err)
	}
}

// TestRecoverInFlightTasks_TransientConvergeLeavesTaskAlone: when the
// converge report transiently fails the task stays in flight untouched —
// failing it would fire exactly the duplicate execution Gap B exists to
// remove; the next workspace-sync cycle retries the report.
func TestRecoverInFlightTasks_TransientConvergeLeavesTaskAlone(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""), resultFrame("done", false)+"\n", "")
	shrinkTerminalRetrySchedule(t)
	fx.injectTerminalStatus(testTaskID, http.StatusBadGateway, 0)
	envRoot := buildEnvRoot(t, fx.root, "ws1", testTaskID)
	fx.setInFlight(InFlightTask{ID: testTaskID, WorkspaceID: "ws1", Status: "running", WorkDir: filepath.Join(envRoot, "repo")})

	fx.daemon.recoverInFlightTasksForRuntime(context.Background(), "rt-1")

	// The fixture records every HTTP attempt, so the transient report's
	// retries show up as complete calls; the invariants are that nothing
	// degraded to the runtime_recovery fail path and nothing was marked.
	if fails := fx.recordedFails(); len(fails) != 0 {
		t.Fatalf("transient converge must not degrade to runtime_recovery: %+v", fails)
	}
	if man, err := mgr.ReadManifest(runID); err != nil || man.ConvergedAt != nil {
		t.Fatalf("transient converge must leave the manifest unconverged: %+v err %v", man, err)
	}
}

// TestReconcileSupervisedRuns_ConvergeExitReportsToServer: the startup
// reconcile's DecisionConvergeExit branch reports the finished run to the
// server (previously a local-only MarkConverged), so a worker that finished
// while the daemon was down converges immediately on restart.
func TestReconcileSupervisedRuns_ConvergeExitReportsToServer(t *testing.T) {
	fx, mgr := wireConvergeFixture(t)
	runID := testTaskID + "-1"
	seedExitedManifest(t, mgr, runID, testTaskID, time.Now().Add(-time.Hour), exitRecord(0, ""), resultFrame("reconciled result", false)+"\n", "")

	fx.daemon.reconcileSupervisedRuns(context.Background())

	completes := fx.recordedCompletes()
	if len(completes) != 1 || completes[0].TaskID != testTaskID || completes[0].Output != "reconciled result" {
		t.Fatalf("completes = %+v, want the finished run reported on startup", completes)
	}
	if man, err := mgr.ReadManifest(runID); err != nil || man.ConvergedAt == nil {
		t.Fatalf("manifest not marked converged after reconcile: %+v err %v", man, err)
	}
}

// shrinkTerminalRetrySchedule collapses the terminal callbacks' backoff so a
// transient-rejection test exercises the retry-exhausted path in milliseconds
// instead of the production 124 seconds.
func shrinkTerminalRetrySchedule(t *testing.T) {
	t.Helper()
	orig := defaultTerminalRetrySchedule
	defaultTerminalRetrySchedule = []time.Duration{time.Millisecond}
	t.Cleanup(func() { defaultTerminalRetrySchedule = orig })
}
