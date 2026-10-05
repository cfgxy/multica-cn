//go:build ruyi349e2e

// RUYI-390 lifecycle E2E: the run-lifecycle regressions added to this
// issue's acceptance scope (Owner directive, RUYI-390 comment 2026-10-05):
// a finished worker must converge from its own recorded exit — no idle
// watchdog, no manual cancel — including when it finishes while no daemon
// is attached; a genuinely active worker must survive reconciliation
// untouched; and the watchdog/cancel kill path must race a natural exit
// without corrupting evidence or touching siblings. Same harness as the
// RUYI-349/390 files: real systemd user manager, real transient units, the
// daemon modelled by independent supervisor instances over one runs dir.
//
// Scope note (erratum, after the RUYI-424 unblock): the original note
// claimed the daemon task layer's final→completed state machine was owned
// by the RUYI-349 unified plan and deliberately not pinned here — that held
// only while the regression was blocked on RUYI-424's stdin-EOF bridge.
// With that merged, scenario 390-7 pins the supervised ACP-shape
// convergence end to end (stdin EOF → the worker's own exit → auto-collect
// as completed). RUYI-349 remains the owner of any further unified
// lifecycle reshape.
package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// e2eFiniteWorker has one-shot CLI semantics: boot lines, a bounded stretch
// of work, a final answer, then exit 0 with no daemon action — the shape a
// claude -p run or an ACP bridge after stdin EOF takes. Long enough that a
// reconcile pass lands mid-flight, short enough to keep the test brisk.
const e2eFiniteWorker = `#!/bin/sh
echo boot-line
i=0
while [ $i -lt 20 ]; do
  echo work-line-$i
  i=$((i+1))
  sleep 0.5
done
echo final-answer-line
`

// e2eOrphanWorker never finishes on its own: the watchdog branch has to stop
// it, and the kill — not the worker — must produce the exit evidence.
const e2eOrphanWorker = `#!/bin/sh
echo boot-line
sleep 300
`

func e2eWriteScript(t *testing.T, body string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "r390-worker-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "worker.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func e2eLaunchScript(t *testing.T, sup *Supervisor, runID, taskID, scriptPath string) agent.WorkerHandle {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h, err := sup.Launch(ctx, agent.LaunchSpec{
		RunID:   runID,
		TaskID:  taskID,
		Runtime: "e2e-sh",
		Path:    scriptPath,
		Dir:     filepath.Dir(scriptPath),
	})
	if err != nil {
		t.Fatalf("launch %s: %v", runID, err)
	}
	return h
}

// e2eWaitExitRecord polls the on-disk manifest until the worker's exit has
// been recorded. The record is written by the launcher inside the unit — the
// worker's direct parent — so it appears even when no daemon is attached.
func e2eWaitExitRecord(t *testing.T, sup *Supervisor, runID string, timeout time.Duration) *ExitRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		man := e2eManifest(t, sup, runID)
		if man.Exit != nil {
			return man.Exit
		}
		if time.Now().After(deadline) {
			t.Fatalf("no exit record within %v\n%s", timeout, e2eDumpRunFiles(t, sup, runID))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func e2eExitJSON(t *testing.T, e *ExitRecord) string {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Scenario 390-4 — a supervised ACP run whose turn completes normally must
// auto-converge: the backend delivers the result (the final has landed), the
// session release closes the worker's stdin, the worker exits on its own,
// and the launcher records the exit — all with no idle watchdog and no
// manual cancel in the chain. One recovery pass then files the run as
// ConvergeExit, the consumed run disappears from later passes, and its
// evidence stays readable inside the retention window.
func TestE2e390NormalCompletionAutoConverges(t *testing.T) {
	launcher := buildE2eLauncher(t)
	runsDir := e2eRunsDir(t)
	worker := writeFakeACPWorker(t)
	backend := newE2eACPBackend(t, worker)
	runID := "390ddd1-1"
	taskID := "01a390a0-0000-4000-8000-000000000004"

	sup := newE2eSupervisor(t, runsDir, launcher)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sess, err := backend.Execute(ctx, "count to ten", agent.ExecOptions{
		Timeout: 110 * time.Second,
		Supervision: &agent.Supervision{
			Supervisor: sup, RunID: runID, TaskID: taskID, Runtime: "kimi",
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if seen := e2eCollectChunks(sess, 15*time.Second, 10); len(seen) < 10 {
		t.Fatalf("turn incomplete before result: %v", keys(seen))
	}
	select {
	case res := <-sess.Result:
		if res.Status != "completed" {
			t.Fatalf("status = %q (error=%q)", res.Status, res.Error)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("result never delivered\n%s", e2eDumpRunFiles(t, sup, runID))
	}

	// The final has landed. From here the worker must reach a proven exit on
	// its own, and the exit must be the worker's own — a supervisor- or
	// cancel-written record here would mean a watchdog killed a finishing
	// run. A kill is distinguishable from a natural exit by the record
	// itself: a natural one-shot CLI exit is code 0 with no signal, while
	// any SIGKILL/SIGTERM path leaves a signal (or a nonzero code) behind.
	exit := e2eWaitExitRecord(t, sup, runID, 30*time.Second)
	if exit.Source != ExitSourceLauncher {
		t.Fatalf("exit source = %q, want %q (watchdog/cancel killed a finishing run)", exit.Source, ExitSourceLauncher)
	}
	if exit.Code != 0 || exit.Signal != "" {
		t.Fatalf("exit = code %d signal %q, want the worker's own clean exit 0", exit.Code, exit.Signal)
	}
	e2eAssertUnitGone(t, sup, runID)

	// One recovery pass auto-collects the finished run.
	results := e2eReconcile(t, sup, func(string) bool { return true })
	if d := decisionFor(t, results, runID); d != DecisionConvergeExit {
		t.Fatalf("reconcile decision = %s, want converge_exit", d)
	}
	if err := sup.Manager().MarkConverged(runID); err != nil {
		t.Fatalf("mark converged: %v", err)
	}

	// Later passes skip the consumed run — a periodic watchdog pass has
	// nothing left to act on.
	results = e2eReconcile(t, sup, func(string) bool { return true })
	for _, res := range results {
		if res.RunID == runID {
			t.Fatalf("consumed run re-decided: %+v", res)
		}
	}

	// Convergence consumes the run, not the audit trail: the turn's output
	// stays readable inside the retention window.
	data, err := os.ReadFile(filepath.Join(sup.Manager().Dir(runID), "worker-stdout.log"))
	if err != nil {
		t.Fatalf("evidence log unreadable after convergence: %v", err)
	}
	if !strings.Contains(string(data), "chunk-09") {
		t.Fatalf("turn output missing from retained evidence: %q", string(data[max(0, len(data)-300):]))
	}
	_ = sup.Manager().RemoveRun(runID)
}

// Scenario 390-5 — daemon dies mid-run, worker finishes while no daemon is
// attached: the recovery daemon's first pass must leave the still-running
// worker strictly alone (Resume, same pid), and once the launcher has
// recorded the worker's own exit, the next pass must auto-collect the run
// (ConvergeExit) with the final output intact — no kill, no watchdog, no
// cancel, no lost evidence.
func TestE2e390OfflineCompletionConvergesOnRecovery(t *testing.T) {
	launcher := buildE2eLauncher(t)
	runsDir := e2eRunsDir(t)
	runID := "390eee1-1"
	taskID := "01a390a0-0000-4000-8000-000000000005"

	sup1 := newE2eSupervisor(t, runsDir, launcher)
	script := e2eWriteScript(t, e2eFiniteWorker)
	h1 := e2eLaunchScript(t, sup1, runID, taskID, script)
	e2eReadUntil(t, sup1, h1, runID, "boot-line", 30*time.Second)
	pid := e2eWaitWorkerPID(t, sup1, runID)

	// --- daemon-1 dies here: no Stop, no Detach, just gone. The worker
	// keeps working under systemd's cgroup; nothing in this process may
	// tear it down.

	sup2 := newE2eSupervisor(t, runsDir, launcher)
	results := e2eReconcile(t, sup2, func(string) bool { return true })
	if d := decisionFor(t, results, runID); d != DecisionResume {
		t.Fatalf("mid-run reconcile = %s, want resume\n%s", d, e2eDumpRunFiles(t, sup2, runID))
	}
	if !procAlive(pid) {
		t.Fatalf("active worker pid %d not alive after Resume", pid)
	}
	if man := e2eManifest(t, sup2, runID); man.WorkerPID != pid {
		t.Fatalf("worker pid changed across reconcile: %d -> %d", pid, man.WorkerPID)
	}

	// The worker finishes while no daemon is attached; the launcher records
	// the exit on its own.
	exit := e2eWaitExitRecord(t, sup2, runID, 60*time.Second)
	if exit.Source != ExitSourceLauncher {
		t.Fatalf("exit source = %q, want %q", exit.Source, ExitSourceLauncher)
	}

	// Recovery pass auto-collects the finished run.
	results = e2eReconcile(t, sup2, func(string) bool { return true })
	if d := decisionFor(t, results, runID); d != DecisionConvergeExit {
		t.Fatalf("recovery reconcile = %s, want converge_exit", d)
	}

	// The final answer survived the daemonless window.
	data, err := os.ReadFile(filepath.Join(sup2.Manager().Dir(runID), "worker-stdout.log"))
	if err != nil {
		t.Fatalf("evidence log unreadable after convergence: %v", err)
	}
	if !strings.Contains(string(data), "final-answer-line") {
		t.Fatalf("final answer missing from retained evidence: %q", string(data[max(0, len(data)-300):]))
	}
	_ = sup2.Manager().MarkConverged(runID)
	_ = sup2.Manager().RemoveRun(runID)
}

// Scenario 390-6 — watchdog/cancel/completed race: the watchdog branch
// (server no longer wants the run) stops an orphaned live worker precisely
// while its in-flight sibling rides untouched; the sibling then completes
// naturally and converges from its own exit evidence; a late cancel after
// completion is an evidence-preserving no-op; consumed runs vanish from
// later passes.
func TestE2e390WatchdogCancelCompletedRace(t *testing.T) {
	launcher := buildE2eLauncher(t)
	runsDir := e2eRunsDir(t)
	runA := "390fff1-1" // in flight, finishes naturally
	runB := "390fff2-1" // orphaned, watchdog stops it
	taskA := "01a390a0-0000-4000-8000-000000000006"
	taskB := "01a390a0-0000-4000-8000-000000000007"
	inFlight := map[string]bool{taskA: true, taskB: false}

	sup := newE2eSupervisor(t, runsDir, launcher)
	finite := e2eWriteScript(t, e2eFiniteWorker)
	orphan := e2eWriteScript(t, e2eOrphanWorker)
	hA := e2eLaunchScript(t, sup, runA, taskA, finite)
	e2eReadUntil(t, sup, hA, runA, "boot-line", 30*time.Second)
	// B's handle is dropped with the daemon that owns it; only the on-disk
	// manifest and systemd decide B's fate from here.
	e2eLaunchScript(t, sup, runB, taskB, orphan)
	pidA := e2eWaitWorkerPID(t, sup, runA)
	pidB := e2eWaitWorkerPID(t, sup, runB)
	if pidA == pidB {
		t.Fatalf("two runs share a worker pid: %d", pidA)
	}

	// One pass, two verdicts: the in-flight run resumes untouched, the
	// orphan is stopped by its cgroup alone.
	results := e2eReconcile(t, sup, func(taskID string) bool { return inFlight[taskID] })
	if d := decisionFor(t, results, runA); d != DecisionResume {
		t.Fatalf("in-flight sibling decision = %s, want resume", d)
	}
	if d := decisionFor(t, results, runB); d != DecisionStopOrphan {
		t.Fatalf("orphan decision = %s, want stop_orphan", d)
	}
	if !procAlive(pidA) {
		t.Fatalf("orphan stop took out the in-flight sibling (pid %d)", pidA)
	}
	deadline := time.Now().Add(30 * time.Second)
	for procAlive(pidB) {
		if time.Now().After(deadline) {
			t.Fatalf("orphan worker pid %d survived the watchdog stop", pidB)
		}
		time.Sleep(200 * time.Millisecond)
	}
	manB := e2eManifest(t, sup, runB)
	if manB.Exit == nil || manB.Exit.Source != ExitSourceSupervisor {
		t.Fatalf("orphan exit record = %+v, want a supervisor-sourced kill", manB.Exit)
	}

	// The in-flight sibling completes naturally while the orphan teardown
	// settles; its own exit evidence — not a kill — proves the run done.
	exitA := e2eWaitExitRecord(t, sup, runA, 60*time.Second)
	if exitA.Source != ExitSourceLauncher {
		t.Fatalf("sibling exit source = %q, want %q", exitA.Source, ExitSourceLauncher)
	}
	results = e2eReconcile(t, sup, func(taskID string) bool { return inFlight[taskID] })
	if d := decisionFor(t, results, runA); d != DecisionConvergeExit {
		t.Fatalf("completed sibling decision = %s, want converge_exit", d)
	}
	if d := decisionFor(t, results, runB); d != DecisionConvergeExit {
		t.Fatalf("stopped orphan decision = %s, want converge_exit", d)
	}

	// A late cancel after completion must be a no-op: whatever systemd
	// replies, the completed run's evidence may not change.
	before := e2eExitJSON(t, e2eManifest(t, sup, runA).Exit)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = sup.Systemd().KillUnit(ctx, UnitName(runA))
	after := e2eExitJSON(t, e2eManifest(t, sup, runA).Exit)
	if before != after {
		t.Fatalf("late cancel mutated the completed run's evidence:\nbefore=%s\nafter=%s", before, after)
	}
	if procAlive(pidA) {
		t.Fatalf("completed run's worker pid %d resurrected by the late cancel", pidA)
	}

	// Consumed runs are invisible to later passes.
	if err := sup.Manager().MarkConverged(runA); err != nil {
		t.Fatalf("mark converged A: %v", err)
	}
	if err := sup.Manager().MarkConverged(runB); err != nil {
		t.Fatalf("mark converged B: %v", err)
	}
	results = e2eReconcile(t, sup, func(taskID string) bool { return inFlight[taskID] })
	for _, res := range results {
		if res.RunID == runA || res.RunID == runB {
			t.Fatalf("consumed run re-decided: %+v", res)
		}
	}

	// The sibling's business output stays readable inside the retention
	// window.
	data, err := os.ReadFile(filepath.Join(sup.Manager().Dir(runA), "worker-stdout.log"))
	if err != nil {
		t.Fatalf("evidence log unreadable after convergence: %v", err)
	}
	if !strings.Contains(string(data), "final-answer-line") {
		t.Fatalf("final answer missing from retained evidence: %q", string(data[max(0, len(data)-300):]))
	}
	_ = sup.Manager().RemoveRun(runA)
	_ = sup.Manager().RemoveRun(runB)
}

// Scenario 390-7 — the daemon task layer's final→completed convergence,
// pinned end to end on the supervised ACP shape: a worker whose turn has
// fully delivered (turn_end notification plus the prompt result on the
// wire) must stay alive after the result while stdin is open — nothing
// kills it and nothing converges the run before EOF — and then exit BY
// ITSELF with a launcher-recorded clean exit 0 once stdin EOF arrives,
// which one recovery pass files as ConvergeExit and later passes leave
// alone. This is the regression the original phase-2 plan had deferred
// behind RUYI-424. Raw handle driving, same idiom as the RUYI-424
// systemd scenario: no Stop, no signal — stdin EOF is the only shutdown
// primitive this scenario allows.
func TestE2e390FinalCompletesThroughStdinEOFConvergence(t *testing.T) {
	launcher := buildE2eLauncher(t)
	runsDir := e2eRunsDir(t)
	worker := writeFakeACPWorker(t)
	runID := "390ddd1-7"
	taskID := "01a390a0-0000-4000-8000-000000000007"

	sup := newE2eSupervisor(t, runsDir, launcher)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h, err := sup.Launch(ctx, agent.LaunchSpec{
		RunID:   runID,
		TaskID:  taskID,
		Runtime: "kimi",
		Path:    worker,
		Dir:     filepath.Dir(worker),
	})
	if err != nil {
		t.Fatalf("launch %s: %v", runID, err)
	}
	pid := e2eWaitWorkerPID(t, sup, runID)

	// Drive the ACP wire directly: handshake, then one prompt turn. The
	// turn's full delivery is the moment the daemon task layer marks the
	// run final→completed; the raw handle models the daemon holding the
	// run with the worker's stdin still open.
	frames := []string{
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"ses-e2e-0001","content":[{"type":"text","text":"count to ten"}]}}`,
	}
	for _, frame := range frames {
		if _, err := io.WriteString(h.Stdin(), frame+"\n"); err != nil {
			t.Fatalf("stdin write: %v", err)
		}
	}
	// Both terminal markers flow back-to-back; consume them through ONE
	// stream pass. The resumable tail's read cursor persists what a returned
	// reader left buffered, so a second open would wait forever for lines
	// already consumed.
	wantTurnEnd := `"sessionUpdate": "turn_end"`
	wantResult := `"result": {"stopReason": "end_turn"`
	seenTurnEnd, seenResult := false, false
	stream, err := h.StdoutStream(ctx)
	if err != nil {
		t.Fatalf("stdout stream: %v", err)
	}
	reader := bufio.NewReader(stream)
	turnDeadline := time.Now().Add(60 * time.Second)
	for !seenTurnEnd || !seenResult {
		if time.Now().After(turnDeadline) {
			t.Fatalf("turn never fully delivered (turn_end=%v result=%v)\n%s", seenTurnEnd, seenResult, e2eDumpRunFiles(t, sup, runID))
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stdout read: %v", err)
		}
		switch {
		case strings.Contains(line, wantTurnEnd):
			seenTurnEnd = true
		case strings.Contains(line, wantResult):
			seenResult = true
		}
	}
	_ = stream.Close()

	// Negative adjacency: the completed turn alone must not end the worker
	// and must not converge the run. The ACP shape idles after its final
	// result waiting for stdin EOF — the exact window a prompt cancel()
	// used to SIGKILL (the RUYI-390 incident).
	aliveEnd := time.Now().Add(2 * time.Second)
	for time.Now().Before(aliveEnd) {
		if err := syscall.Kill(pid, 0); err != nil {
			t.Fatalf("worker exited before stdin EOF — ACP shape contract broken: %v", err)
		}
		if man := e2eManifest(t, sup, runID); man.Exit != nil {
			t.Fatalf("exit evidence appeared before stdin EOF: %+v", man.Exit)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Deliver stdin EOF — the only shutdown primitive — and the worker must
	// exit on its own: a launcher-recorded clean exit 0, distinguishable
	// from any kill by code and signal alone.
	if err := h.CloseStdin(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	exit := e2eWaitExit(t, h, 30*time.Second)
	man := e2eManifest(t, sup, runID)
	if man.State != StateExited || man.Exit == nil {
		t.Fatalf("manifest after stdin EOF: state=%s exit=%+v", man.State, man.Exit)
	}
	if exit.Code != 0 || exit.Signal != "" {
		t.Fatalf("worker did not exit cleanly on stdin EOF: %+v", exit)
	}
	if man.Exit.Source != ExitSourceLauncher {
		t.Fatalf("exit source = %s, want %s (the launcher must have observed the natural exit)", man.Exit.Source, ExitSourceLauncher)
	}
	e2eAssertUnitGone(t, sup, runID)

	// The completed run files itself as ConvergeExit on the next pass — the
	// supervisor-side shape of final→completed — and later passes have
	// nothing left to act on.
	results := e2eReconcile(t, sup, func(string) bool { return true })
	if d := decisionFor(t, results, runID); d != DecisionConvergeExit {
		t.Fatalf("reconcile decision = %s, want converge_exit", d)
	}
	if err := sup.Manager().MarkConverged(runID); err != nil {
		t.Fatalf("mark converged: %v", err)
	}
	results = e2eReconcile(t, sup, func(string) bool { return true })
	for _, res := range results {
		if res.RunID == runID {
			t.Fatalf("consumed run re-decided: %+v", res)
		}
	}

	// The turn's wire evidence stays readable inside the retention window.
	data, err := os.ReadFile(filepath.Join(sup.Manager().Dir(runID), "worker-stdout.log"))
	if err != nil {
		t.Fatalf("evidence log unreadable after convergence: %v", err)
	}
	if !strings.Contains(string(data), "turn_end") {
		t.Fatalf("turn evidence missing from retained stdout: %q", string(data[max(0, len(data)-300):]))
	}
	_ = sup.Manager().RemoveRun(runID)
}
