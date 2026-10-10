package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// claudePipeCrashText is the error text a resumed claude run leaves when the
// CLI dies before producing any session: the wrapped form withAgentStderr
// builds from the stream finalizer's crash line plus a V8 heap message.
const claudePipeCrashText = "claude exited with error: exit status 1; claude stderr: FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory"

// resumedPipeCrashResult is attempt 1's result: the backend was asked to
// resume, the CLI crashed first, and RUYI-659's backend branch reports that
// as a rejected resume (positive evidence, no session emitted).
func resumedPipeCrashResult() agent.Result {
	return agent.Result{
		Status:         "failed",
		Error:          claudePipeCrashText,
		ResumeRejected: true,
		Usage:          map[string]agent.TokenUsage{"m1": {InputTokens: 5}},
	}
}

// freshPipeCrashResult is a later attempt's result: the retry requested no
// resume, so the backend can claim no rejection — the crash itself is what
// qualifies the next attempt.
func freshPipeCrashResult() agent.Result {
	r := resumedPipeCrashResult()
	r.ResumeRejected = false
	return r
}

// runSelfHealLoop mirrors runTask's shape: the initial (resumed) attempt
// executes outside the loop, then its result, tools and options feed
// freshSessionRetryLoop.
func runSelfHealLoop(t *testing.T, d *Daemon, fb *fakeBackend, priorSession string) (agent.Result, int32, string) {
	t.Helper()
	ctx := context.Background()
	taskLog := slog.Default()
	opts := agent.ExecOptions{ResumeSessionID: priorSession}
	var msgSeq atomic.Int32

	first, tools, err := d.executeAndDrain(ctx, fb, "prompt", opts, taskLog, "task-pipe-crash", "", &msgSeq)
	if err != nil {
		t.Fatalf("initial attempt error: %v", err)
	}
	return d.freshSessionRetryLoop(
		ctx, fb, nil, opts, taskLog,
		&Task{ID: "task-pipe-crash", PriorSessionID: priorSession},
		execenv.TaskContextForEnv{},
		&execenv.Environment{WorkDir: t.TempDir()},
		"brief", &msgSeq,
		first, tools, "claude",
	)
}

// noticeContents returns the text of every text-type message reported while
// the loop ran — the pipe-crash notices under test. The transcript's other
// rows (tool_use and the drained agent output) are not notices.
func noticeContents(t *testing.T, rec *transcriptRecorder) []string {
	t.Helper()
	var out []string
	for _, m := range rec.snapshot() {
		if m.Type == "text" && m.Content != "" {
			out = append(out, m.Content)
		}
	}
	return out
}

// TestPipeCrashSelfHeal_RecoversWithFreshSessionAndRetires pins the happy
// path: a resumed run whose CLI crashes before any session gets a fresh-
// session retry on the same turn, keeps retrying while the crash reproduces,
// and the recovery retires the transcript the CLI kept dying on.
func TestPipeCrashSelfHeal_RecoversWithFreshSessionAndRetires(t *testing.T) {
	t.Parallel()

	d, rec := newTranscriptRecorder(t)
	d.cfg.PipeCrashSelfHealEnabled = true
	fb := &fakeBackend{
		results: []agent.Result{
			resumedPipeCrashResult(),
			freshPipeCrashResult(),
			{Status: "completed", Output: "done", SessionID: "new-sess", Usage: map[string]agent.TokenUsage{
				"m1": {InputTokens: 10, OutputTokens: 20},
			}},
		},
	}

	result, _, retired := runSelfHealLoop(t, d, fb, "sess-old")

	if result.Status != "completed" || result.SessionID != "new-sess" {
		t.Fatalf("expected the fresh attempt's completion, got %+v", result)
	}
	if retired != "sess-old" {
		t.Fatalf("expected the abandoned prior session to be retired, got %q", retired)
	}
	if len(fb.calls) != 3 {
		t.Fatalf("expected initial attempt + 2 crash retries, got %d calls", len(fb.calls))
	}
	for i, want := range []string{"sess-old", "", ""} {
		if fb.calls[i].ResumeSessionID != want {
			t.Fatalf("call %d resumed %q, want %q", i, fb.calls[i].ResumeSessionID, want)
		}
	}
	// Usage survives the crash retries and accumulates across every attempt:
	// the recovery's bill includes both failed attempts' spend (5+5+10).
	if u := result.Usage["m1"]; u.InputTokens != 20 || u.OutputTokens != 20 {
		t.Fatalf("expected accumulated usage {20,20}, got %+v", u)
	}

	notices := noticeContents(t, rec)
	if len(notices) != 2 {
		t.Fatalf("expected one progress notice per crash retry, got %d: %+v", len(notices), notices)
	}
	if !strings.Contains(notices[0], "管道崩溃") || !strings.Contains(notices[0], "第 1 次") {
		t.Fatalf("first notice should name the crash and the attempt, got %q", notices[0])
	}
	if !strings.Contains(notices[1], "第 2 次") {
		t.Fatalf("second notice should count attempt 2, got %q", notices[1])
	}
}

// TestPipeCrashSelfHeal_StopsAfterThreeConsecutiveCrashes pins the budget:
// while the crash keeps reproducing, the loop stops after three consecutive
// failures and the task fails for a human — with the poisoned session still
// retired so the next task starts clean.
func TestPipeCrashSelfHeal_StopsAfterThreeConsecutiveCrashes(t *testing.T) {
	t.Parallel()

	d, rec := newTranscriptRecorder(t)
	d.cfg.PipeCrashSelfHealEnabled = true
	// A 4th entry must never be consumed; its shape would fail the test
	// loudly if the budget leaked.
	fb := &fakeBackend{
		results: []agent.Result{
			resumedPipeCrashResult(),
			freshPipeCrashResult(),
			freshPipeCrashResult(),
			{Status: "completed", Output: "retry budget leaked", SessionID: "unreachable"},
		},
	}

	result, _, retired := runSelfHealLoop(t, d, fb, "sess-old")

	if len(fb.calls) != 3 {
		t.Fatalf("expected the budget to stop retries after 3 consecutive failures, got %d calls", len(fb.calls))
	}
	// The retry established no session, so the FIRST result stands — the bad
	// session id stays on a failed row for the resume lookups to exclude.
	if result.Status != "failed" || result.SessionID != "" {
		t.Fatalf("expected the first attempt's failure to stand, got %+v", result)
	}
	if retired != "sess-old" {
		t.Fatalf("expected the dying transcript to be retired, got %q", retired)
	}
	if notices := noticeContents(t, rec); len(notices) != 2 {
		t.Fatalf("expected 2 notices (one per automatic retry), got %d", len(notices))
	}
}

// TestPipeCrashSelfHeal_DisabledRestoresLegacyFailFast pins the kill switch:
// with it off, a pipe crash is exactly what pre-RUYI-659 behaviour made of it
// — one failed attempt, no retry, no retirement, no progress message — even
// though the backend still reports the rejection.
func TestPipeCrashSelfHeal_DisabledRestoresLegacyFailFast(t *testing.T) {
	t.Parallel()

	d, rec := newTranscriptRecorder(t)
	d.cfg.PipeCrashSelfHealEnabled = false
	fb := &fakeBackend{
		results: []agent.Result{
			resumedPipeCrashResult(),
			{Status: "completed", Output: "retry fired with the switch off", SessionID: "unreachable"},
		},
	}

	result, _, retired := runSelfHealLoop(t, d, fb, "sess-old")

	if len(fb.calls) != 1 {
		t.Fatalf("expected no retry with the self-heal disabled, got %d calls", len(fb.calls))
	}
	if result.Status != "failed" || result.SessionID != "" {
		t.Fatalf("expected the failed attempt to stand, got %+v", result)
	}
	if retired != "" {
		t.Fatalf("expected no retirement with the self-heal disabled, got %q", retired)
	}
	if notices := noticeContents(t, rec); len(notices) != 0 {
		t.Fatalf("expected no progress notices, got %+v", notices)
	}
}

// TestPipeCrashSelfHeal_PlainRejectionKeepsLegacySingleRetry pins that the
// feature adds nothing to the pre-existing rejection path: a plain "no
// conversation found" rejection still retries exactly once, recovers, and
// never posts a crash notice — even with the self-heal enabled.
func TestPipeCrashSelfHeal_PlainRejectionKeepsLegacySingleRetry(t *testing.T) {
	t.Parallel()

	d, rec := newTranscriptRecorder(t)
	d.cfg.PipeCrashSelfHealEnabled = true
	fb := &fakeBackend{
		results: []agent.Result{
			{Status: "failed", Error: "no conversation found with session ID: sess-old", ResumeRejected: true, Usage: map[string]agent.TokenUsage{
				"m1": {InputTokens: 5},
			}},
			{Status: "completed", Output: "done", SessionID: "new-sess", Usage: map[string]agent.TokenUsage{
				"m1": {InputTokens: 10, OutputTokens: 20},
			}},
		},
	}

	result, _, retired := runSelfHealLoop(t, d, fb, "sess-old")

	if len(fb.calls) != 2 {
		t.Fatalf("expected the single legacy retry, got %d calls", len(fb.calls))
	}
	if result.Status != "completed" || result.SessionID != "new-sess" {
		t.Fatalf("expected the retry's completion, got %+v", result)
	}
	if retired != "sess-old" {
		t.Fatalf("expected the rejected session to be retired, got %q", retired)
	}
	if notices := noticeContents(t, rec); len(notices) != 0 {
		t.Fatalf("a non-crash rejection posts no crash notice, got %+v", notices)
	}
	if u := result.Usage["m1"]; u.InputTokens != 15 {
		t.Fatalf("expected merged usage, got %+v", u)
	}
}

// crashAfterToolsBackend gets real work done (one tool_use) before the CLI
// dies with the crash text — the one crash shape the self-heal must NOT
// retry, because tool side effects are no longer safe to replay.
type crashAfterToolsBackend struct {
	calls atomic.Int32
}

func (b *crashAfterToolsBackend) Execute(_ context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	b.calls.Add(1)
	msgCh := make(chan agent.Message, 1)
	msgCh <- agent.Message{Type: agent.MessageToolUse, Tool: "bash"}
	close(msgCh)
	resCh := make(chan agent.Result, 1)
	resCh <- agent.Result{
		Status:         "failed",
		Error:          fmt.Sprintf("%s (after tool use)", claudePipeCrashText),
		ResumeRejected: true,
	}
	return &agent.Session{Messages: msgCh, Result: resCh}, nil
}

// TestPipeCrashSelfHeal_DoesNotRetryAfterToolsRun pins the tools gate: a
// crash that lands after the run executed tools stays failed for a human.
func TestPipeCrashSelfHeal_DoesNotRetryAfterToolsRun(t *testing.T) {
	t.Parallel()

	d, rec := newTranscriptRecorder(t)
	d.cfg.PipeCrashSelfHealEnabled = true
	backend := &crashAfterToolsBackend{}
	ctx := context.Background()
	taskLog := slog.Default()
	opts := agent.ExecOptions{ResumeSessionID: "sess-old"}
	var msgSeq atomic.Int32

	first, tools, err := d.executeAndDrain(ctx, backend, "prompt", opts, taskLog, "task-pipe-crash", "", &msgSeq)
	if err != nil {
		t.Fatalf("initial attempt error: %v", err)
	}
	if tools == 0 {
		t.Fatal("expected the drained tool_use to count")
	}

	result, _, retired := d.freshSessionRetryLoop(
		ctx, backend, nil, opts, taskLog,
		&Task{ID: "task-pipe-crash", PriorSessionID: "sess-old"},
		execenv.TaskContextForEnv{},
		&execenv.Environment{WorkDir: t.TempDir()},
		"brief", &msgSeq,
		first, tools, "claude",
	)

	if backend.calls.Load() != 1 {
		t.Fatalf("expected no retry after tools ran, got %d calls", backend.calls.Load())
	}
	if result.Status != "failed" {
		t.Fatalf("expected the failed attempt to stand, got %+v", result)
	}
	if retired != "" {
		t.Fatalf("the prior session stays live for a human retry, got %q retired", retired)
	}
	if notices := noticeContents(t, rec); len(notices) != 0 {
		t.Fatalf("expected no progress notices, got %+v", notices)
	}
}
