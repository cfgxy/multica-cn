package agent

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// acpReattachFrame writes one JSON-RPC line onto the supervised worker's
// stdout, the wire a reattached daemon resumes reading.
func acpReattachFrame(t *testing.T, h *fakeHandle, line string) {
	t.Helper()
	if _, err := h.stdoutW.WriteString(line + "\n"); err != nil {
		t.Fatalf("write wire frame: %v", err)
	}
}

// TestKimiReattachRidesInFlightTurn pins the RUYI-390 reattach contract on
// the ACP family's representative backend: a reattached Execute sends no
// protocol frames, rebuilds the session id off the session/update stream,
// and converges on the turn_end notification the previous daemon never saw.
func TestKimiReattachRidesInFlightTurn(t *testing.T) {
	t.Parallel()

	sup, h := newFakeSupervisor(t)
	// Supervised mode never execs this path, but the backend still resolves
	// the CLI before building the command.
	fakePath := filepath.Join(t.TempDir(), "kimi")
	writeTestExecutable(t, fakePath, []byte("#!/bin/sh\nexit 0\n"))
	backend, err := New("kimi", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new kimi backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "prompt-never-sent", ExecOptions{
		Timeout: 10 * time.Second,
		Supervision: &Supervision{
			Supervisor: sup, RunID: "task-1-1", TaskID: "task-1",
			Runtime: "kimi", Reattach: true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	acpReattachFrame(t, h, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_wire","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"late answer"}}}}`)
	acpReattachFrame(t, h, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_wire","update":{"sessionUpdate":"turn_end","stopReason":"end_turn"}}}`)

	// One-shot CLI semantics: the reattached worker exits once it has
	// delivered its turn. Prove the exit (waitCh) and EOF the streams the
	// way a real supervisor's tailer does — the lifecycle goroutine cannot
	// pass <-readerDone until the wire ends, and the Result is only sent
	// after that point.
	go func() {
		time.Sleep(100 * time.Millisecond)
		h.CloseForExit()
	}()

	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		if result.Status != "completed" {
			t.Fatalf("expected status=completed, got %q (error=%q)", result.Status, result.Error)
		}
		if result.SessionID != "ses_wire" {
			t.Fatalf("expected session id rebuilt from the wire, got %q", result.SessionID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for result")
	}

	if got := h.stdinBuf.String(); got != "" {
		t.Fatalf("reattached Execute must not write protocol frames, recorded: %q", got)
	}
}

// TestKimiReattachWorkerExitFailsTask pins the failure leg: the worker dies
// before delivering a turn result, the reattached wait maps stdout EOF onto
// the same "process exited" failure shape a sent prompt would have produced.
func TestKimiReattachWorkerExitFailsTask(t *testing.T) {
	t.Parallel()

	sup, h := newFakeSupervisor(t)
	// Supervised mode never execs this path, but the backend still resolves
	// the CLI before building the command.
	fakePath := filepath.Join(t.TempDir(), "kimi")
	writeTestExecutable(t, fakePath, []byte("#!/bin/sh\nexit 0\n"))
	backend, err := New("kimi", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new kimi backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "prompt-never-sent", ExecOptions{
		Timeout: 10 * time.Second,
		Supervision: &Supervision{
			Supervisor: sup, RunID: "task-1-1", TaskID: "task-1",
			Runtime: "kimi", Reattach: true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	// The worker dies without a turn result: Wait proves the exit and the
	// supervisor's tailer ends the streams with EOF, exactly as in
	// production (fakeHandle.Wait calls closeStreams).
	h.mu.Lock()
	h.exit = &WorkerExit{Code: 1}
	h.mu.Unlock()
	h.CloseForExit()

	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		if result.Status != "failed" {
			t.Fatalf("expected status=failed, got %q (error=%q)", result.Status, result.Error)
		}
		if !strings.Contains(result.Error, "process exited") {
			t.Fatalf("expected the exit to be named, got %q", result.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

// TestReattachWireSignals pins the latch ordering rules underneath the
// wait: EOF after a delivered turn result is not a failure, EOF before one
// is, and ctx cancellation surfaces the caller's classification unchanged.
func TestReattachWireSignals(t *testing.T) {
	t.Parallel()

	t.Run("turn result first, EOF is not a failure", func(t *testing.T) {
		t.Parallel()
		c := &hermesClient{cfg: Config{Logger: slog.Default()}}
		c.markTurnDone()
		c.markStreamClosed()
		if err := c.waitReattachedTurn(context.Background(), "kimi"); err != nil {
			t.Fatalf("want nil after a delivered turn, got %v", err)
		}
	})
	t.Run("EOF without a turn result fails", func(t *testing.T) {
		t.Parallel()
		c := &hermesClient{cfg: Config{Logger: slog.Default()}}
		c.markStreamClosed()
		err := c.waitReattachedTurn(context.Background(), "kimi")
		if err == nil || !strings.Contains(err.Error(), "process exited") {
			t.Fatalf("want process-exited failure, got %v", err)
		}
	})
	t.Run("cancellation surfaces ctx error", func(t *testing.T) {
		t.Parallel()
		c := &hermesClient{cfg: Config{Logger: slog.Default()}}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := c.waitReattachedTurn(ctx, "kimi")
		if err == nil {
			t.Fatal("want an error after cancellation")
		}
	})
	t.Run("late frames rebuild the session id", func(t *testing.T) {
		t.Parallel()
		c := &hermesClient{cfg: Config{Logger: slog.Default()}}
		if got := c.observedSessionID(); got != "" {
			t.Fatalf("want empty before any frame, got %q", got)
		}
		c.observeSessionID("ses_1")
		c.observeSessionID("ses_2")
		if got := c.observedSessionID(); got != "ses_2" {
			t.Fatalf("want the latest id, got %q", got)
		}
	})
}

// TestKimiNormalFinishForwardsStdinEOFWithoutKill pins the RUYI-390 cancel
// timing convergence on the normal-completion path: once the turn's result
// is in, the backend closes stdin — the supervised bridge forwards that EOF
// to the worker's stdin (RUYI-424) — and gives the worker a bounded window
// to exit on its own. The cancel driver's kill is only the fallback for a
// worker that ignores EOF. The old wiring cancelled unconditionally at this
// boundary, SIGKILLing a finishing worker and leaving the launcher a kill
// record where the daemon task layer's completed convergence needs the
// worker's own natural-exit record.
func TestKimiNormalFinishForwardsStdinEOFWithoutKill(t *testing.T) {
	t.Parallel()

	sup, h := newFakeSupervisor(t)
	// Supervised mode never execs this path, but the backend still resolves
	// the CLI before building the command.
	fakePath := filepath.Join(t.TempDir(), "kimi")
	writeTestExecutable(t, fakePath, []byte("#!/bin/sh\nexit 0\n"))
	backend, err := New("kimi", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new kimi backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "count to ten", ExecOptions{
		Timeout: 30 * time.Second,
		Supervision: &Supervision{
			Supervisor: sup, RunID: "task-2-1", TaskID: "task-2",
			Runtime: "kimi",
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	// Feed the wire in lockstep with what the backend actually sends: a
	// response is only deliverable once its request is on the worker's
	// stdin, so poll stdinBuf before answering each request.
	waitForStdin := func(substr string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			h.mu.Lock()
			buf := h.stdinBuf.String()
			h.mu.Unlock()
			if strings.Contains(buf, substr) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("worker never received %q; stdin so far: %q", substr, buf)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	waitForStdin(`"initialize"`)
	acpReattachFrame(t, h, `{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":false},"authMethods":[]}}`)
	waitForStdin(`"session/new"`)
	acpReattachFrame(t, h, `{"jsonrpc":"2.0","id":1,"result":{"sessionId":"ses_finish"}}`)
	waitForStdin(`"session/prompt"`)
	acpReattachFrame(t, h, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_finish","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"1 2 3"}}}}`)
	acpReattachFrame(t, h, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_finish","update":{"sessionUpdate":"turn_end","stopReason":"end_turn"}}}`)
	acpReattachFrame(t, h, `{"jsonrpc":"2.0","id":2,"result":{"stopReason":"end_turn"}}`)

	// One-shot CLI semantics: the worker exits on stdin EOF and nothing
	// else. Prove the exit only after the EOF was actually delivered —
	// never before, mirroring what a real one-shot worker does.
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			h.mu.Lock()
			cl := h.stdinCl
			h.mu.Unlock()
			if cl {
				h.mu.Lock()
				h.exit = &WorkerExit{Code: 0}
				h.mu.Unlock()
				h.CloseForExit()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		if result.Status != "completed" {
			t.Fatalf("expected status=completed, got %q (error=%q)", result.Status, result.Error)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timeout waiting for result")
	}

	h.mu.Lock()
	forwarded, signals := h.stdinCl, h.signalled
	h.mu.Unlock()
	if !forwarded {
		t.Fatal("backend never forwarded stdin EOF to the worker")
	}
	if len(signals) != 0 {
		t.Fatalf("cancel driver signalled a finishing worker: %v", signals)
	}
}
