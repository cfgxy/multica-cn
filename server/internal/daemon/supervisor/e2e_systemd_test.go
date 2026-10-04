//go:build ruyi349e2e

// RUYI-349 local E2E: exercises the supervisor against the REAL systemd user
// manager and the REAL multica launcher binary (built from ./cmd/multica), so
// every scenario here is a live transient unit, not a fake. The "daemon" is
// modelled by supervisor instances built purely from the runs dir + systemd —
// exactly the state a restarted daemon process has. Run with:
//
//	env -u MULTICA_TOKEN … go test -tags ruyi349e2e ./internal/daemon/supervisor/ -run TestE2e -v
package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

const (
	e2eTaskID = "01a0fad5-cc0c-7165-a08c-0467d42e6f27"
	// Emits boot lines, then keeps emitting numbered work lines forever so a
	// restarted daemon has fresh output to converge on.
	e2eWorkerScript = `#!/bin/sh
echo boot-line-1
echo boot-line-2
i=0
while :; do
  echo work-line-$i
  i=$((i+1))
  sleep 0.3
done
`
)

// buildE2eLauncher compiles the real multica binary whose __worker-launcher
// subcommand systemd will run inside the transient unit.
func buildE2eLauncher(t *testing.T) string {
	t.Helper()
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "multica-e2e")
	build := exec.Command("go", "build", "-o", bin, "./cmd/multica")
	build.Dir = filepath.Join(pkgDir, "..", "..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build launcher binary: %v\n%s", err, out)
	}
	return bin
}

// newE2eSupervisor builds the supervisor stack a fresh daemon process would
// build: everything derived from the runs dir on disk + the live systemd user
// manager. No state is shared with any previous instance in this process.
func newE2eSupervisor(t *testing.T, runsDir, launcherBin string) *Supervisor {
	t.Helper()
	mgr, err := NewManager(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	sys := NewSystemdCtl(slog.Default())
	if err := sys.Available(); err != nil {
		t.Fatalf("systemd user manager unavailable for E2E: %v", err)
	}
	sup, err := New(mgr, sys, launcherBin, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	return sup
}

func e2eLaunch(t *testing.T, sup *Supervisor, runID string) agent.WorkerHandle {
	t.Helper()
	workDir := t.TempDir()
	script := filepath.Join(workDir, "worker.sh")
	if err := os.WriteFile(script, []byte(e2eWorkerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h, err := sup.Launch(ctx, agent.LaunchSpec{
		RunID:   runID,
		TaskID:  e2eTaskID,
		Runtime: "e2e-sh",
		Path:    script,
		Dir:     workDir,
	})
	if err != nil {
		t.Fatalf("launch %s: %v", runID, err)
	}
	return h
}

// e2eWaitWorkerPID polls the on-disk manifest until the launcher has recorded
// the worker PID (the handle's manifest copy is written before startUnit and
// never refreshed, so disk is the truth here).
func e2eWaitWorkerPID(t *testing.T, sup *Supervisor, runID string) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		man := e2eManifest(t, sup, runID)
		if man.WorkerPID > 0 {
			return man.WorkerPID
		}
		if time.Now().After(deadline) {
			t.Fatalf("launcher never recorded the worker pid: %+v", man)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// e2eReadUntil drains the handle's stdout until a line matching want appears
// or the deadline hits. The timeout path dumps the on-disk truth (manifest,
// read cursor, log sizes, stdout-log tail) so a stalled stream is diagnosable
// from the test log alone.
func e2eReadUntil(t *testing.T, sup *Supervisor, h agent.WorkerHandle, runID, want string, timeout time.Duration) int {
	t.Helper()
	stream, err := h.StdoutStream(context.Background())
	if err != nil {
		t.Fatalf("stdout stream: %v", err)
	}
	defer stream.Close()
	deadline := time.Now().Add(timeout)
	var seen bytes.Buffer
	reader := bufio.NewReader(stream)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("did not see %q within %v; got %d bytes:\n%s\n%s", want, timeout, seen.Len(), seen.String(), e2eDumpRunFiles(t, sup, runID))
		}
		line, err := reader.ReadString('\n')
		seen.WriteString(line)
		if err != nil {
			t.Fatalf("stdout read: %v (got:\n%s\n%s)", err, seen.String(), e2eDumpRunFiles(t, sup, runID))
		}
		if strings.Contains(line, want) {
			return seen.Len()
		}
	}
}

func e2eDumpRunFiles(t *testing.T, sup *Supervisor, runID string) string {
	t.Helper()
	dir := sup.Manager().Dir(runID)
	var b strings.Builder
	fmt.Fprintf(&b, "e2e dump dir=%s\n", dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(&b, "  readdir: %v", err)
		return b.String()
	}
	for _, e := range entries {
		info, err := e.Info()
		if err == nil {
			fmt.Fprintf(&b, "  %s size=%d\n", e.Name(), info.Size())
		}
	}
	for _, name := range []string{"manifest.json", "read.json"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			fmt.Fprintf(&b, "  %s: %s\n", name, strings.TrimSpace(string(data)))
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "worker-stdout.log")); err == nil && len(data) > 0 {
		tail := data
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		fmt.Fprintf(&b, "  worker-stdout.log tail: %q\n", string(tail))
	}
	return b.String()
}

// e2eReconcile runs one real reconciliation pass with the given server
// in-flight predicate and returns the decisions.
func e2eReconcile(t *testing.T, sup *Supervisor, inFlight func(string) bool) []ReconcileResult {
	t.Helper()
	rec := &Reconciler{Mgr: sup.Manager(), Units: sup.Systemd(), Log: slog.Default(), TaskInFlight: inFlight}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	results, err := rec.Run(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return results
}

func decisionFor(t *testing.T, results []ReconcileResult, runID string) Decision {
	t.Helper()
	for _, res := range results {
		if res.RunID == runID {
			return res.Decision
		}
	}
	t.Fatalf("run %s missing from reconcile results %+v", runID, results)
	return 0
}

func e2eManifest(t *testing.T, sup *Supervisor, runID string) *Manifest {
	t.Helper()
	man, err := sup.Manager().ReadManifest(runID)
	if err != nil {
		t.Fatalf("read manifest %s: %v", runID, err)
	}
	return man
}

func e2eWaitExit(t *testing.T, h agent.WorkerHandle, timeout time.Duration) WorkerExit {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	exit, err := h.Wait(ctx)
	if err != nil {
		t.Fatalf("wait for worker exit: %v", err)
	}
	return exit
}

func e2eAssertUnitGone(t *testing.T, sup *Supervisor, runID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	active, err := sup.Systemd().UnitActive(ctx, UnitName(runID))
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("unit still active after stop")
	}
}

// e2eRunsDir gives the runs dir a SHORT path: control.sock lives inside it
// and unix socket paths cap at 108 bytes — t.TempDir() under a long test
// name overflows and fails connects with EINVAL.
func e2eRunsDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "r349-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// Scenario 1 — daemon restart: the worker keeps running under systemd while
// no daemon exists, the next daemon resumes it (same worker process), output
// continues from the persisted offset (no re-delivery of lines the dead
// daemon already consumed), and an explicit stop afterwards leaves proven
// exit evidence.
func TestE2eSystemdWorkerSurvivesDaemonRestart(t *testing.T) {
	launcher := buildE2eLauncher(t)
	runsDir := e2eRunsDir(t)
	runID := "e2e001a0-cafe-beef-0001"

	sup1 := newE2eSupervisor(t, runsDir, launcher)
	h1 := e2eLaunch(t, sup1, runID)
	firstPID := e2eWaitWorkerPID(t, sup1, runID)
	seen1 := e2eReadUntil(t, sup1, h1, runID, "boot-line-2", 30*time.Second)
	t.Logf("daemon-1 consumed %d bytes up to boot-line-2", seen1)
	// daemon-1's cursor may sit an unknown distance past the boot lines: the
	// reader's bufio buffer swallows whatever the tail had staged by the time
	// it saw boot-line-2. The resume contract asserted on daemon-2 is
	// therefore exactly the persisted-offset one: nothing already consumed
	// comes back, and fresh output flows.

	// --- daemon-1 dies here: no Stop, no Detach, just gone. The worker stays
	// under systemd's cgroup; nothing in this process may tear it down.
	t.Logf("daemon-1 gone; worker pid=%d still under unit %s", firstPID, UnitName(runID))

	sup2 := newE2eSupervisor(t, runsDir, launcher)
	decisions := e2eReconcile(t, sup2, func(taskID string) bool { return taskID == e2eTaskID })
	if got := decisionFor(t, decisions, runID); got != DecisionResume {
		t.Fatalf("reconcile decision = %s, want resume", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h2, err := sup2.Reattach(ctx, runID)
	if err != nil {
		t.Fatalf("reattach: %v", err)
	}
	if got := h2.PID(); got != firstPID {
		t.Fatalf("resumed worker pid = %d, want original %d (worker must survive the restart)", got, firstPID)
	}

	// Output convergence: the resumed daemon sees NEW work lines and never
	// re-delivers the boot lines daemon-1 already consumed.
	stream, err := h2.StdoutStream(ctx)
	if err != nil {
		t.Fatalf("resumed stdout stream: %v", err)
	}
	defer stream.Close()
	deadline := time.Now().Add(30 * time.Second)
	var resumed strings.Builder
	sawNew := false
	reader := bufio.NewReader(stream)
	for time.Now().Before(deadline) && !sawNew {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("resumed stdout read: %v", err)
		}
		resumed.WriteString(line)
		if strings.Contains(line, "boot-line-1") || strings.Contains(line, "boot-line-2") {
			t.Fatalf("boot lines re-delivered after restart (offset resume broken):\n%s", resumed.String())
		}
		if strings.HasPrefix(strings.TrimSpace(line), "work-line-") {
			sawNew = true
		}
	}
	if !sawNew {
		t.Fatalf("no new work line within 30s:\n%s", resumed.String())
	}
	t.Logf("daemon-2 converged output: %q", strings.TrimSpace(resumed.String()))

	// Explicit stop (the task finishing normally under the new daemon) must
	// leave proven exit evidence.
	if err := h2.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	exit := e2eWaitExit(t, h2, 30*time.Second)
	man := e2eManifest(t, sup2, runID)
	if man.State != StateExited || man.Exit == nil {
		t.Fatalf("manifest after stop: state=%s exit=%+v", man.State, man.Exit)
	}
	e2eAssertUnitGone(t, sup2, runID)
	t.Logf("restart scenario exit evidence: exit=%+v record_source=%s", exit, man.Exit.Source)
}

// Scenario 2 — daemon SIGKILL + orphan reap: the worker survives the harshest
// daemon death untouched; when the server no longer wants the task, the next
// daemon's reconciliation kills the cgroup and writes the exit evidence —
// never the reverse order.
func TestE2eSystemdWorkerSurvivesDaemonKillAndOrphanReap(t *testing.T) {
	launcher := buildE2eLauncher(t)
	runsDir := e2eRunsDir(t)
	runID := "e2e001a0-cafe-beef-0002"

	sup1 := newE2eSupervisor(t, runsDir, launcher)
	h1 := e2eLaunch(t, sup1, runID)
	firstPID := e2eWaitWorkerPID(t, sup1, runID)
	e2eReadUntil(t, sup1, h1, runID, "boot-line-1", 30*time.Second)
	// SIGKILL of the daemon process: the handle is abandoned mid-stream.
	_ = h1

	sup2 := newE2eSupervisor(t, runsDir, launcher)

	// Server still wants the task → resume, worker untouched.
	decisions := e2eReconcile(t, sup2, func(string) bool { return true })
	if got := decisionFor(t, decisions, runID); got != DecisionResume {
		t.Fatalf("reconcile (in flight) = %s, want resume", got)
	}
	man := e2eManifest(t, sup2, runID)
	if man.WorkerPID != firstPID || man.Exit != nil || man.State != StateRunning {
		t.Fatalf("manifest after SIGKILL-window reconcile: pid=%d state=%s exit=%+v", man.WorkerPID, man.State, man.Exit)
	}
	if err := syscall.Kill(firstPID, 0); err != nil {
		t.Fatalf("worker pid %d not alive after resume reconcile: %v", firstPID, err)
	}

	// Server released the task (cancel landed server-side while we were
	// dead, or the run was reassigned) → the next daemon reaps the orphan.
	decisions = e2eReconcile(t, sup2, func(string) bool { return false })
	if got := decisionFor(t, decisions, runID); got != DecisionStopOrphan {
		t.Fatalf("reconcile (not in flight) = %s, want stop_orphan", got)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		man = e2eManifest(t, sup2, runID)
		if man.Exit != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no exit evidence after stop_orphan: %+v", man)
		}
		time.Sleep(200 * time.Millisecond)
	}
	e2eAssertUnitGone(t, sup2, runID)
	t.Logf("orphan reap exit evidence: source=%s code=%d signal=%s", man.Exit.Source, man.Exit.Code, man.Exit.Signal)
}

// Scenario 3 — cancel during the restart window, with the negative adjacency
// assertion: while NO daemon exists, nothing may act on the worker (pid
// stable, unit active, manifest byte-stable, no fabricated exit). The cancel
// only lands after the next daemon resumes, and then with evidence.
func TestE2eSystemdCancelDuringRestartWindowLeavesWorkerUntouched(t *testing.T) {
	launcher := buildE2eLauncher(t)
	runsDir := e2eRunsDir(t)
	runID := "e2e001a0-cafe-beef-0003"

	sup1 := newE2eSupervisor(t, runsDir, launcher)
	h1 := e2eLaunch(t, sup1, runID)
	firstPID := e2eWaitWorkerPID(t, sup1, runID)
	e2eReadUntil(t, sup1, h1, runID, "boot-line-2", 30*time.Second)
	manifestBefore, err := os.ReadFile(filepath.Join(runsDir, runID, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	// The restart window with a cancel pending: the request sits on the
	// server; no daemon exists to deliver it. Assert NOTHING in the system
	// moved against the worker during this window.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	windowEnd := time.Now().Add(3 * time.Second)
	checks := 0
	for time.Now().Before(windowEnd) {
		active, err := sup1.Systemd().UnitActive(ctx, UnitName(runID))
		if err != nil {
			t.Fatal(err)
		}
		if !active {
			t.Fatalf("unit went inactive during the daemon-less window (check #%d)", checks+1)
		}
		if err := syscall.Kill(firstPID, 0); err != nil {
			t.Fatalf("worker pid %d gone during the daemon-less window (check #%d): %v", firstPID, checks+1, err)
		}
		manifestNow, err := os.ReadFile(filepath.Join(runsDir, runID, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(manifestBefore, manifestNow) {
			t.Fatalf("manifest mutated during the daemon-less window:\nbefore: %s\nafter:  %s", manifestBefore, manifestNow)
		}
		checks++
		time.Sleep(300 * time.Millisecond)
	}
	t.Logf("negative adjacency held across %d checks over the daemon-less window", checks)

	// The next daemon comes up, sees the task still in flight, resumes — and
	// NOW the pending cancel is delivered as a normal stop with evidence.
	sup2 := newE2eSupervisor(t, runsDir, launcher)
	decisions := e2eReconcile(t, sup2, func(string) bool { return true })
	if got := decisionFor(t, decisions, runID); got != DecisionResume {
		t.Fatalf("reconcile = %s, want resume", got)
	}
	h2, err := sup2.Reattach(ctx, runID)
	if err != nil {
		t.Fatalf("reattach: %v", err)
	}
	if got := h2.PID(); got != firstPID {
		t.Fatalf("resumed pid = %d, want %d", got, firstPID)
	}
	if err := h2.Stop(); err != nil {
		t.Fatalf("deliver pending cancel (stop): %v", err)
	}
	exit := e2eWaitExit(t, h2, 30*time.Second)
	man := e2eManifest(t, sup2, runID)
	if man.Exit == nil {
		t.Fatalf("no exit evidence after delivering the pending cancel: %+v", man)
	}
	if exit.Code == 0 && exit.Signal == "" {
		t.Fatalf("exit record lost code and signal: %+v", exit)
	}
	e2eAssertUnitGone(t, sup2, runID)
	t.Logf("pending cancel delivered after restart: exit=%+v record_source=%s", exit, man.Exit.Source)
}
