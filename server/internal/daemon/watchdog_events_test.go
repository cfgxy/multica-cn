package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// ─── Harness ────────────────────────────────────────────────────────────────

// recordingServer stands in for the Multica server: it 200s everything and
// records request bodies per path, so tests assert on the transcript events
// and comment calls the daemon actually sent.
type recordingServer struct {
	mu     sync.Mutex
	server *httptest.Server
	hits   map[string][]recordedHit
}

type recordedHit struct {
	at   time.Time
	body []byte
}

func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	rs := &recordingServer{hits: map[string][]recordedHit{}}
	rs.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rs.mu.Lock()
		rs.hits[r.URL.Path] = append(rs.hits[r.URL.Path], recordedHit{at: time.Now(), body: body})
		rs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(rs.server.Close)
	return rs
}

func (rs *recordingServer) hitsBySuffix(suffix string) []recordedHit {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var out []recordedHit
	for p, hits := range rs.hits {
		if strings.HasSuffix(p, suffix) {
			out = append(out, hits...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out
}

// reportedMessages decodes every /messages batch into one seq-sorted slice.
func (rs *recordingServer) reportedMessages(t *testing.T) []TaskMessageData {
	t.Helper()
	var out []TaskMessageData
	for _, hit := range rs.hitsBySuffix("/messages") {
		var req struct {
			Messages []TaskMessageData `json:"messages"`
		}
		if err := json.Unmarshal(hit.body, &req); err != nil {
			t.Fatalf("decode reported messages: %v", err)
		}
		out = append(out, req.Messages...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

func watchdogEvents(t *testing.T, msgs []TaskMessageData) []TaskMessageData {
	t.Helper()
	var out []TaskMessageData
	for _, m := range msgs {
		if m.Type == "watchdog" {
			out = append(out, m)
		}
	}
	return out
}

func eventField(t *testing.T, m TaskMessageData, key string) any {
	t.Helper()
	v, ok := m.Input[key]
	if !ok {
		t.Fatalf("watchdog event seq=%d missing input field %q (input=%v)", m.Seq, key, m.Input)
	}
	return v
}

// commentBodies decodes every /comment POST body.
func (rs *recordingServer) commentBodies(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, hit := range rs.hitsBySuffix("/comment") {
		var req struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(hit.body, &req); err != nil {
			t.Fatalf("decode comment body: %v", err)
		}
		out = append(out, req.Content)
	}
	return out
}

// scriptedBackend plays its script onto the message channel on a wall-clock
// timeline. hang=true models the MUL-2225 stall: the subprocess never
// returns, so neither the message channel closes nor a Result arrives.
type scriptedBackend struct {
	script []scriptedEvent
	hang   bool
}

type scriptedEvent struct {
	after time.Duration
	msg   agent.Message
}

func (b scriptedBackend) Execute(_ context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	msgCh := make(chan agent.Message, len(b.script)+1)
	resCh := make(chan agent.Result, 1)
	go func() {
		start := time.Now()
		for _, ev := range b.script {
			if d := ev.after - time.Since(start); d > 0 {
				time.Sleep(d)
			}
			msgCh <- ev.msg
		}
		if b.hang {
			return
		}
		close(msgCh)
		resCh <- agent.Result{Status: "completed", Output: "done"}
	}()
	return &agent.Session{Messages: msgCh, Result: resCh}, nil
}

func toolUse(tool, callID string) agent.Message {
	return agent.Message{Type: agent.MessageToolUse, Tool: tool, CallID: callID}
}

// ─── Unit: toolCallTracker ──────────────────────────────────────────────────

func TestToolCallTracker(t *testing.T) {
	tr := newToolCallTracker()
	now := time.Now()

	tr.register("c1", "Bash", now)
	tr.register("c2", "Read", now.Add(time.Minute))
	tr.register("", "Bash", now.Add(2*time.Minute)) // call-less tool_use

	// An unknown call_id resolves nothing and retires nothing: a pending
	// call whose result went missing stays pending, which is exactly what
	// marks should keep reporting.
	if tr.take("missing") != nil {
		t.Fatal("unknown call_id must not resolve")
	}
	// Exact call_id pairing wins.
	if got := tr.take("c2"); got == nil || got.Tool != "Read" {
		t.Fatalf("take(c2) = %+v, want the Read call", got)
	}
	// Same-tool FIFO for a call-less result, matching the frontend folder:
	// the oldest Bash, not the newest.
	got := tr.retireOldest("Bash")
	if got == nil || got.CallID != "c1" {
		t.Fatalf("retireOldest(Bash) = %+v, want c1 (oldest Bash)", got)
	}
	// Global FIFO when the result carries no tool hint either.
	got = tr.retireOldest("")
	if got == nil || got.CallID != "" {
		t.Fatalf("retireOldest(no hint) = %+v, want the call-less entry", got)
	}
	if got := tr.pendingLocked(); len(got) != 0 {
		t.Fatalf("pending = %+v, want empty", got)
	}
}

// ─── Unit: mark backoff ─────────────────────────────────────────────────────

func TestMarkRepeatIntervalBacksOffAndCaps(t *testing.T) {
	base := 30 * time.Minute
	want := map[int]time.Duration{
		0: base,     // first repeat waits the base interval
		1: base,     // after one mark, still the base interval
		2: 2 * base, // then doubling…
		3: 4 * base, //
		4: 8 * base, // …up to the 8× cap
		9: 8 * base, // and the cap holds forever
	}
	for marks, interval := range want {
		if got := markRepeatInterval(base, marks); got != interval {
			t.Fatalf("markRepeatInterval(%s, %d) = %s, want %s", base, marks, got, interval)
		}
	}
}

// ─── Integration: tool_in_flight marks ──────────────────────────────────────

// A tool_use whose result never arrives must surface on the transcript well
// before the kill budget — that visibility is the entire point of ADR-1 — and
// the mark must carry the tool name, call id, and true pending duration.
func TestExecuteAndDrain_ToolInFlight_MarksBeforeKill(t *testing.T) {
	rs := newRecordingServer(t)
	d := &Daemon{client: NewClient(rs.server.URL), logger: slog.Default()}
	d.cfg.AgentIdleWatchdog = 400 * time.Millisecond
	d.cfg.AgentToolWatchdog = 400 * time.Millisecond
	d.cfg.AgentToolMarkAfter = 60 * time.Millisecond
	d.cfg.AgentToolMarkEvery = 0 // first mark only; repeats are covered below

	result, _, err := d.executeAndDrain(context.Background(),
		scriptedBackend{script: []scriptedEvent{
			{after: 10 * time.Millisecond, msg: toolUse("Bash", "call-1")},
		}, hang: true},
		"p", agent.ExecOptions{}, slog.Default(), "t-mark", "", "test", new(atomic.Int32))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "idle_watchdog" {
		t.Fatalf("expected idle_watchdog kill, got %q (%q)", result.Status, result.Error)
	}

	killAt := time.Now()
	marks := toolInFlightMarks(t, rs)
	if len(marks) != 1 {
		t.Fatalf("expected exactly 1 tool_in_flight mark, got %d", len(marks))
	}
	mark := marks[0]
	if mark.Tool != "Bash" {
		t.Fatalf("mark tool = %q, want Bash", mark.Tool)
	}
	if got := eventField(t, mark, "call_id"); got != "call-1" {
		t.Fatalf("mark call_id = %v, want call-1", got)
	}
	if got := eventField(t, mark, "state"); got != "tool_in_flight" {
		t.Fatalf("mark state = %v, want tool_in_flight", got)
	}
	pendingMs, ok := eventField(t, mark, "pending_ms").(float64)
	if !ok || pendingMs < 40 {
		t.Fatalf("mark pending_ms = %v, want a true pending duration ≥40ms", eventField(t, mark, "pending_ms"))
	}
	if got := eventField(t, mark, "mark_count"); got != float64(1) {
		t.Fatalf("mark_count = %v, want 1", got)
	}
	// The mark was generated strictly before the kill: checked_at is stamped
	// when the watchdog decided to emit, while the kill fired on a later tick.
	checkedAt, err := time.Parse(time.RFC3339, eventField(t, mark, "checked_at").(string))
	if err != nil {
		t.Fatalf("parse checked_at: %v", err)
	}
	if !checkedAt.Before(killAt) {
		t.Fatalf("mark checked_at %v not before kill %v", checkedAt, killAt)
	}
	// Silence grading is off in this test: no warn/alert may appear.
	for _, m := range watchdogEvents(t, rs.reportedMessages(t)) {
		if ev := eventField(t, m, "event").(string); ev != "tool_in_flight" {
			t.Fatalf("unexpected watchdog event %q with grading keys off", ev)
		}
	}
}

func toolInFlightMarks(t *testing.T, rs *recordingServer) []TaskMessageData {
	t.Helper()
	var out []TaskMessageData
	for _, m := range watchdogEvents(t, rs.reportedMessages(t)) {
		if eventField(t, m, "event") == "tool_in_flight" {
			out = append(out, m)
		}
	}
	return out
}

// With the tool budget disabled entirely (MULTICA_AGENT_TOOL_WATCHDOG=0, the
// long-campaign posture) marks keep arriving but must back off instead of
// accumulating linearly — the review's noise-ceiling finding. Over a fixed
// observation window the linear count would be ~3× the backed-off count, so
// both bounds have real margin.
func TestExecuteAndDrain_ToolInFlight_MarkBacksOffWhileToolWindowDisabled(t *testing.T) {
	rs := newRecordingServer(t)
	d := &Daemon{client: NewClient(rs.server.URL), logger: slog.Default()}
	d.cfg.AgentIdleWatchdog = 100 * time.Millisecond // tick = 50ms: a fine observation clock
	d.cfg.AgentToolWatchdog = 0                      // never force-stop while a tool is in flight
	d.cfg.AgentToolMarkAfter = 40 * time.Millisecond
	d.cfg.AgentToolMarkEvery = 60 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = d.executeAndDrain(ctx,
			scriptedBackend{script: []scriptedEvent{
				{after: 5 * time.Millisecond, msg: toolUse("Bash", "call-1")},
			}, hang: true},
			"p", agent.ExecOptions{}, slog.Default(), "t-backoff", "", "test", new(atomic.Int32))
	}()

	// Watch for 700ms of wall clock: backoff schedules marks at
	// ~50/150/300/550ms (intervals 60→120→240) and the next would be due at
	// ~1030ms, so ≤4 marks; a linear schedule would fire ~11.
	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = toolInFlightMarks(t, rs)
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	<-done

	marks := toolInFlightMarks(t, rs)
	if len(marks) < 2 {
		t.Fatalf("expected ≥2 marks over the window, got %d", len(marks))
	}
	if len(marks) > 4 {
		t.Fatalf("expected backoff to hold marks ≤4 over the window (linear would be ~11), got %d", len(marks))
	}
	for i, m := range marks {
		if got := eventField(t, m, "mark_count"); got != float64(i+1) {
			t.Fatalf("mark %d mark_count = %v, want %d", i, got, i+1)
		}
	}
}
