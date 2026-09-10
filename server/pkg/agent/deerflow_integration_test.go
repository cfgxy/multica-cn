//go:build agentintegration

package agent

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// deerflowSmokeCommand resolves the bridge for a real-binary run. The
// executable is not on PATH in any deployment we control (it lives in the
// bridge's own virtualenv), so MULTICA_DEERFLOW_PATH is the normal way in and
// mirrors what a runtime profile's per-machine command override does.
func deerflowSmokeCommand(t *testing.T) string {
	t.Helper()
	if path := strings.TrimSpace(os.Getenv("MULTICA_DEERFLOW_PATH")); path != "" {
		return path
	}
	path, err := exec.LookPath("deerflow-acp")
	if err != nil {
		t.Skip("deerflow-acp not found; set MULTICA_DEERFLOW_PATH to the bridge executable")
	}
	return path
}

// deerflowSmokeHome is the DeerFlow deployment root. The bridge resolves
// config.yaml relative to its own working directory, so a run started in a
// task workdir reports backend-unavailable no matter how healthy DeerFlow is —
// which is exactly what MULTICA_DEERFLOW_HOME exists to prevent.
func deerflowSmokeHome(t *testing.T) string {
	t.Helper()
	home := strings.TrimSpace(os.Getenv("MULTICA_DEERFLOW_HOME"))
	if home == "" {
		t.Skip("set MULTICA_DEERFLOW_HOME to the DeerFlow deployment root")
	}
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		t.Skipf("MULTICA_DEERFLOW_HOME=%q is not a directory", home)
	}
	return home
}

// TestDeerflowRealACPSmoke drives the real deerflow-acp bridge end-to-end.
// The unit suite can only assert the contract as read off the bridge's source;
// this is what proves a DeerFlow profile completes a task under its own
// runtime identity, streams text, and returns a terminal stopReason.
func TestDeerflowRealACPSmoke(t *testing.T) {
	requireRealAgentSmoke(t)
	if testing.Short() {
		t.Skip("skipping real-binary smoke test in -short mode")
	}
	path := deerflowSmokeCommand(t)
	home := deerflowSmokeHome(t)
	if version, err := exec.Command(path, "--version").CombinedOutput(); len(version) > 0 {
		// deerflow-acp 0.1.0 prints its version and then exits non-zero, so
		// the output is the signal here, not the exit status.
		t.Logf("deerflow-acp version: %s (err=%v)", strings.TrimSpace(string(version)), err)
	}

	backend, err := New("deerflow", Config{
		ExecutablePath: path,
		Logger:         slog.Default(),
		Env:            map[string]string{deerflowHomeEnv: home},
	})
	if err != nil {
		t.Fatalf("new deerflow backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	session, err := backend.Execute(ctx, "Reply with exactly one word: DEERFLOW_SMOKE_OK", ExecOptions{
		Timeout: 7 * time.Minute,
		Cwd:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var streamed int
	go func() {
		for range session.Messages {
			streamed++
		}
	}()

	result := <-session.Result
	t.Logf("status=%q error=%q sessionID=%q output=%q", result.Status, result.Error, result.SessionID, result.Output)
	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed (error=%q)", result.Status, result.Error)
	}
	if strings.TrimSpace(result.Output) == "" {
		t.Error("no text was streamed back; the prompt path returned nothing")
	}
	// session/load replays history and cancel addresses the session by id, so
	// a run that reports no id cannot be resumed or cancelled afterwards.
	if result.SessionID == "" {
		t.Error("no session id returned")
	}
}

// TestDeerflowRealCancelSmoke covers the other terminal path an operator can
// reach from the UI. The bridge maps a cancelled turn to stopReason
// `cancelled`, which the backend has to report as aborted rather than failed —
// a cancelled task shown as failed sends people looking for a defect that is
// not there.
func TestDeerflowRealCancelSmoke(t *testing.T) {
	requireRealAgentSmoke(t)
	if testing.Short() {
		t.Skip("skipping real-binary smoke test in -short mode")
	}
	path := deerflowSmokeCommand(t)
	home := deerflowSmokeHome(t)

	backend, err := New("deerflow", Config{
		ExecutablePath: path,
		Logger:         slog.Default(),
		Env:            map[string]string{deerflowHomeEnv: home},
	})
	if err != nil {
		t.Fatalf("new deerflow backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	session, err := backend.Execute(ctx,
		"Research the history of the printing press in exhaustive detail.",
		ExecOptions{Timeout: 4 * time.Minute, Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	// Cancellation reaches a running turn by cancelling the execution
	// context, which is what the daemon does when a task is stopped. Let the
	// turn get underway first: cancelling before the bridge has a turn in
	// flight is a different path.
	time.Sleep(20 * time.Second)
	cancel()

	result := <-session.Result
	t.Logf("status=%q error=%q output=%q", result.Status, result.Error, result.Output)
	if result.Status != "aborted" {
		t.Fatalf("status = %q, want aborted for a cancelled turn (error=%q)", result.Status, result.Error)
	}
}
