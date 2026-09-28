package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestClaudeExecuteBackfillsContextTokensOnBudgetStop reproduces the gap behind
// RUYI-148's first production trigger (2026-09-18 00:15): a run the context
// ceiling force-stopped has to publish the poller's final reading on
// usage.ContextTokens, or the claim-time session gate reads the earlier and
// smaller reading the stream folded in, judges the session still usable, and
// the retry resumes the same oversized session straight back into the ceiling.
//
// The fixture pulls the two readings apart deliberately: the stream's assistant
// event reports 50,000 (which fold writes onto usage) while the transcript has
// already grown to 204,887 (which is what trips the hard line). The transcript
// fill only touches entries still at zero, so it will not override the folded
// 50,000 — the budget-stop backfill is the only path that can close the gap.
//
// The poller ticks every 15s, so this test is deliberately slow rather than
// reaching into production code for a shorter interval.
func TestClaudeExecuteBackfillsContextTokensOnBudgetStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	const (
		sessionID        = "6f1d0f3e-9a1c-4f0b-8f2d-2b7a5c3e91aa"
		model            = "claude-sonnet-5"
		hardCeiling      = int64(200000)
		transcriptAtStop = int64(204887)
		streamReading    = int64(50000)
	)

	configDir := t.TempDir()
	cwd := t.TempDir()
	// claudeTranscriptRoot reads this process's environment, so the fixture
	// root has to be exported here; t.Setenv rules out t.Parallel.
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	transcriptDir := filepath.Join(configDir, "projects", claudeProjectSlug(cwd))
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	// input 100 + cache_read 204787 + output 0 = 204,887, the live reading the
	// poller sees.
	transcriptLine := `{"type":"assistant","isSidechain":false,"message":{"id":"msg-last","model":"` + model +
		`","usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":204787,"cache_creation_input_tokens":0}}}`
	if err := os.WriteFile(filepath.Join(transcriptDir, sessionID+".jsonl"), []byte(transcriptLine+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	fakePath := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\n" +
		"IFS= read -r _\n" +
		`echo '{"type":"system","subtype":"init","session_id":"` + sessionID + `"}'` + "\n" +
		`echo '{"type":"assistant","message":{"model":"` + model +
		`","usage":{"input_tokens":49000,"output_tokens":1000,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[{"type":"text","text":"working"}]}}'` + "\n" +
		// The stop comes from the poller: the process stays alive until the
		// hard-line reading tears it down.
		"sleep 120\n"
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("claude", Config{
		ExecutablePath: fakePath,
		Env:            map[string]string{"IS_SANDBOX": "1"},
		Logger:         slog.Default(),
	})
	if err != nil {
		t.Fatalf("new claude backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	session, err := backend.Execute(ctx, "prompt", ExecOptions{
		Cwd:                  cwd,
		Timeout:              80 * time.Second,
		MaxContextHardTokens: hardCeiling,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	var result Result
	select {
	case r, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		result = r
	case <-time.After(80 * time.Second):
		t.Fatal("timeout waiting for result")
	}

	if result.Status != "context_budget" {
		t.Fatalf("a budget stop must end as context_budget, got %q (error %q)", result.Status, result.Error)
	}
	u, ok := result.Usage[model]
	if !ok {
		t.Fatalf("usage carries no entry for model %q: %#v", model, result.Usage)
	}
	if u.ContextTokens != transcriptAtStop {
		t.Fatalf("stopped run reports ContextTokens = %d, want %d (the poller's final reading); "+
			"the folded stream value is %d, and a gate reading that keeps resuming this oversized session",
			u.ContextTokens, transcriptAtStop, streamReading)
	}
}
