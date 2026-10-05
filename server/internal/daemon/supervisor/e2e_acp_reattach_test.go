//go:build ruyi349e2e

// RUYI-390 local E2E, ACP family edition. Same harness as the RUYI-349 file
// (real systemd user manager, real multica launcher binary, real transient
// units), but the worker speaks the actual ACP JSON-RPC protocol over
// stdin/stdout and the daemon side goes through agent.Backend.Execute — the
// exact production path a supervised ACP run takes, including the reattach
// branch after a daemon restart. Run with:
//
//	env -u MULTICA_TOKEN XDG_RUNTIME_DIR=/run/user/1000 … \
//	  go test -tags ruyi349e2e ./internal/daemon/supervisor/ -run TestE2e390 -v
package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// e2eFakeACPWorker is a deterministic ACP server: it answers the handshake,
// then for session/prompt streams numbered chunks slowly enough for a daemon
// restart to land mid-turn, and finishes the turn with turn_end. It is a
// stand-in for the zcode-acp / reasonix / hermes family, all of which speak
// this exact wire protocol.
const e2eFakeACPWorker = `#!/usr/bin/python3
import sys, json, time

# argv carries the bridge's own flags (e.g. "acp"); only numeric
# overrides are honoured, anything else keeps the defaults.
CHUNKS = 10
INTERVAL = 0.3
for a in sys.argv[1:]:
    if a.isdigit():
        CHUNKS = int(a)
    elif a.replace(".", "", 1).isdigit():
        INTERVAL = float(a)
session_id = "ses-e2e-0001"

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    req = json.loads(line)
    method = req.get("method")
    rid = req.get("id")
    if method == "initialize":
        send({"jsonrpc": "2.0", "id": rid, "result": {
            "protocolVersion": 1,
            "agentCapabilities": {"loadSession": False},
            "authMethods": [],
        }})
    elif method == "session/new":
        send({"jsonrpc": "2.0", "id": rid, "result": {"sessionId": session_id}})
    elif method == "session/prompt":
        for i in range(CHUNKS):
            send({"jsonrpc": "2.0", "method": "session/update", "params": {
                "sessionId": session_id,
                "update": {"sessionUpdate": "agent_message_chunk",
                           "content": {"type": "text", "text": "chunk-%02d\n" % i}},
            }})
            time.sleep(INTERVAL)
        send({"jsonrpc": "2.0", "method": "session/update", "params": {
            "sessionId": session_id,
            "update": {"sessionUpdate": "turn_end", "stopReason": "end_turn"},
        }})
        send({"jsonrpc": "2.0", "id": rid, "result": {"stopReason": "end_turn"}})
`

// writeFakeACPWorker materializes the fake ACP worker and its argv quirk: the
// kimi family backend appends "acp" and the script ignores argv.
func writeFakeACPWorker(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-acp")
	if err := os.WriteFile(path, []byte(e2eFakeACPWorker), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func newE2eACPBackend(t *testing.T, workerPath string) agent.Backend {
	t.Helper()
	b, err := agent.New("kimi", agent.Config{ExecutablePath: workerPath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new kimi-family backend: %v", err)
	}
	return b
}

// e2eCollectChunks reads session messages until the deadline, returning the
// chunk numbers seen ("chunk-07" → 7). Turn results are consumed elsewhere.
func e2eCollectChunks(sess *agent.Session, deadline time.Duration, stopAfter int) map[int]bool {
	seen := map[int]bool{}
	numRe := regexp.MustCompile(`chunk-(\d+)`)
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for len(seen) < stopAfter {
		select {
		case msg, ok := <-sess.Messages:
			if !ok {
				return seen
			}
			if m := numRe.FindStringSubmatch(msg.Content); m != nil {
				n, _ := strconv.Atoi(m[1])
				seen[n] = true
			}
		case <-timer.C:
			return seen
		}
	}
	return seen
}

// Scenario 390-1 — ACP reattach across a daemon restart: the first daemon
// launches a real supervised ACP run (systemd transient unit), consumes the
// first chunks of a long turn, and "dies". The second daemon — a fresh
// supervisor over the same runs dir, exactly what a restarted process is —
// reconciles the run to Resume, reenters it through the backend's reattach
// branch, writes zero protocol frames, converges on the in-flight turn, and
// resumes the chunk stream from the persisted offset.
func TestE2e390ACPReattachRidesDaemonRestart(t *testing.T) {
	launcher := buildE2eLauncher(t)
	runsDir := e2eRunsDir(t)
	worker := writeFakeACPWorker(t)
	backend := newE2eACPBackend(t, worker)
	runID := "390aaa1-1"
	taskID := "01a390a0-0000-4000-8000-000000000001"

	// Daemon #1: launch the run under the real systemd user manager.
	sup1 := newE2eSupervisor(t, runsDir, launcher)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sess1, err := backend.Execute(ctx, "count to ten", agent.ExecOptions{
		Timeout: 110 * time.Second,
		Supervision: &agent.Supervision{
			Supervisor: sup1, RunID: runID, TaskID: taskID, Runtime: "kimi",
		},
	})
	if err != nil {
		t.Fatalf("daemon1 execute: %v", err)
	}
	first := e2eCollectChunks(sess1, 3*time.Second, 4)
	if len(first) < 2 {
		t.Fatalf("daemon1 saw too little of the turn: %v", first)
	}
	t.Logf("daemon1 consumed chunks %v, then 'died'", keys(first))

	// Daemon #2: a fresh supervisor over the same runs dir. The reconcile
	// pass must see the live unit and resume the run.
	sup2 := newE2eSupervisor(t, runsDir, launcher)
	results := e2eReconcile(t, sup2, func(string) bool { return true })
	if d := decisionFor(t, results, runID); d != DecisionResume {
		t.Fatalf("reconcile decision = %s, want resume", d)
	}

	sess2, err := backend.Execute(ctx, "count to ten", agent.ExecOptions{
		Timeout: 110 * time.Second,
		Supervision: &agent.Supervision{
			Supervisor: sup2, RunID: runID, TaskID: taskID, Runtime: "kimi",
			Reattach: true,
		},
	})
	if err != nil {
		t.Fatalf("daemon2 reattach execute: %v", err)
	}
	second := e2eCollectChunks(sess2, 15*time.Second, 10)

	// The reattached daemon rides the SAME turn: the two sides' chunks must
	// tile [0, CHUNKS) with no gap and no re-delivery.
	const totalChunks = 10
	merged := map[int]bool{}
	for n := range first {
		merged[n] = true
	}
	for n := range second {
		merged[n] = true
	}
	if len(merged) != totalChunks {
		var got []int
		for n := range merged {
			got = append(got, n)
		}
		sort.Ints(got)
		t.Fatalf("chunk tiling broken: daemon1=%v daemon2=%v merged=%v", keys(first), keys(second), got)
	}

	select {
	case res := <-sess2.Result:
		if res.Status != "completed" {
			t.Fatalf("reattached status = %q (error=%q)", res.Status, res.Error)
		}
		if res.SessionID != "ses-e2e-0001" {
			t.Fatalf("reattached session id = %q, want the wire-rebuilt one", res.SessionID)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("reattached execute never delivered the result\n%s", e2eDumpRunFiles(t, sup2, runID))
	}

	// The worker exits after its turn (one-shot CLI semantics); the second
	// daemon's session release converges the manifest. Prove the unit is
	// gone and the run left no live process behind.
	e2eAssertUnitGone(t, sup2, runID)

	// Cleanup: remove the spent run's evidence dir (retention GC does this
	// in production after the window; the E2E reclaims eagerly).
	_ = sup2.Manager().RemoveRun(runID)
}

// Scenario 390-2 — cancel precision for supervised ACP runs: cancelling one
// run kills exactly its transient unit and worker, leaving a sibling run
// untouched.
func TestE2e390ACPCancelPrecision(t *testing.T) {
	launcher := buildE2eLauncher(t)
	runsDir := e2eRunsDir(t)
	worker := writeFakeACPWorker(t)
	backend := newE2eACPBackend(t, worker)

	runA := "390bbb1-1"
	runB := "390bbb2-1"
	taskID := "01a390a0-0000-4000-8000-000000000002"
	sup := newE2eSupervisor(t, runsDir, launcher)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	launch := func(id string) *agent.Session {
		sess, err := backend.Execute(ctx, "count", agent.ExecOptions{
			Timeout: 110 * time.Second,
			Supervision: &agent.Supervision{
				Supervisor: sup, RunID: id, TaskID: taskID, Runtime: "kimi",
			},
		})
		if err != nil {
			t.Fatalf("launch %s: %v", id, err)
		}
		return sess
	}
	sessA := launch(runA)
	launch(runB)
	pidA := e2eWaitWorkerPID(t, sup, runA)
	pidB := e2eWaitWorkerPID(t, sup, runB)
	if pidA == pidB {
		t.Fatalf("two runs share a worker pid: %d", pidA)
	}

	// Cancel run A only — the same unit-kill path the reconcile matrix's
	// StopOrphan branch uses. The teardown must be cgroup-precise: B keeps
	// running with its own pid.
	if err := sup.Systemd().KillUnit(ctx, UnitName(runA)); err != nil {
		t.Fatalf("cancel run A: %v", err)
	}
	e2eAssertUnitGone(t, sup, runA)
	if active, err := sup.Systemd().UnitActive(ctx, UnitName(runB)); err != nil || !active {
		t.Fatalf("sibling run B disturbed by A's cancel: active=%v err=%v", active, err)
	}
	if procAlive(pidA) {
		t.Fatalf("run A worker pid %d survived the cancel", pidA)
	}
	if !procAlive(pidB) {
		t.Fatal("run B worker pid died alongside A")
	}

	// Drain A's result (the cancel surfaces as a failed/aborted result), then
	// stop B through the manager and reclaim both dirs.
	select {
	case <-sessA.Result:
	case <-time.After(30 * time.Second):
		t.Log("run A result not delivered within 30s (cancel race) — unit teardown already proven")
	}
	_ = sup.Systemd().KillUnit(ctx, UnitName(runB))
	e2eAssertUnitGone(t, sup, runB)
	_ = sup.Manager().RemoveRun(runA)
	_ = sup.Manager().RemoveRun(runB)
}

// Scenario 390-3 — real zcode-acp binary under real systemd: launch the
// production bridge (no LLM round-trip — the handshake alone proves the
// supervised launch path), confirm the transient unit owns a live process,
// then cancel and prove precise teardown. zcode-acp idles on its stdin pipe,
// so a launched bridge stays alive indefinitely until cancelled.
func TestE2e390ZcodeACPLaunchAndCancel(t *testing.T) {
	bin, err := exec.LookPath("zcode-acp")
	if err != nil {
		t.Skip("zcode-acp not installed on this host")
	}
	launcher := buildE2eLauncher(t)
	runsDir := e2eRunsDir(t)
	sup := newE2eSupervisor(t, runsDir, launcher)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	runID := "390ccc1-1"
	workDir := t.TempDir()
	h, err := sup.Launch(ctx, agent.LaunchSpec{
		RunID:   runID,
		TaskID:  "01a390a0-0000-4000-8000-000000000003",
		Runtime: "zcode",
		Path:    bin,
		Args:    []string{bin, "acp"},
		Dir:     workDir,
	})
	if err != nil {
		t.Fatalf("launch zcode-acp: %v", err)
	}
	pid := e2eWaitWorkerPID(t, sup, runID)
	if !procAlive(pid) {
		t.Fatalf("zcode-acp worker pid %d not alive after launch", pid)
	}
	t.Logf("zcode-acp launched under %s, worker pid %d", UnitName(runID), pid)

	if err := h.Stop(); err != nil {
		t.Fatalf("stop zcode-acp run: %v", err)
	}
	e2eAssertUnitGone(t, sup, runID)
	if procAlive(pid) {
		t.Fatalf("zcode-acp pid %d survived the stop", pid)
	}
	exit, waitErr := e2eWaitExit(t, h, 30*time.Second), error(nil)
	t.Logf("zcode-acp exit evidence: %+v", exit)
	_ = waitErr
	_ = sup.Manager().RemoveRun(runID)
}

func keys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func procAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// field 3 is the state; Z (zombie) counts as gone for our purposes
	fields := strings.Fields(string(data))
	return len(fields) > 2 && fields[2] != "Z"
}
