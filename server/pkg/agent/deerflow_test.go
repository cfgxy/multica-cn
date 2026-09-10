package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func deerflowTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewReturnsDeerflowBackend(t *testing.T) {
	t.Parallel()
	b, err := New("deerflow", Config{ExecutablePath: "/nonexistent/deerflow-acp"})
	if err != nil {
		t.Fatalf("New(deerflow) error: %v", err)
	}
	if _, ok := b.(*deerflowBackend); !ok {
		t.Fatalf("expected *deerflowBackend, got %T", b)
	}
}

// fakeDeerflowACPScript impersonates `deerflow-acp acp`. It reproduces the
// frames deerflow-acp 0.1.0 was observed to send rather than a generous
// generic ACP fake, because the contract differences are exactly what this
// suite guards:
//
//   - initialize advertises loadSession only when the bridge was started with
//     unstable protocol support; DEERFLOW_NO_LOAD_SESSION drops it.
//   - session/new answers {sessionId} with a df-prefixed thread id and nothing
//     else — no model catalog, no configOptions.
//   - session/resume answers a bare {}, but only after the same parameter
//     binding the bridge does: `cwd` is a required positional of
//     resume_session, so a request without it is answered -32602 the way the
//     real bridge's JSON-RPC dispatcher does.
//   - set_model / set_config_option are NOT routed; the default arm answers
//     -32601 exactly as the bridge does, so any attempt to send them fails the
//     turn visibly.
//   - the private -320xx codes are selectable through env so each one's resume
//     accounting can be pinned separately.
func fakeDeerflowACPScript() string {
	return `#!/bin/sh
while IFS= read -r line; do
  if [ -n "$DEERFLOW_REQUESTS_FILE" ]; then
    printf '%s\n' "$line" >> "$DEERFLOW_REQUESTS_FILE"
  fi
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      if [ -n "$DEERFLOW_NO_LOAD_SESSION" ]; then
        printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentInfo":{"name":"deerflow-acp","version":"0.1.0"},"agentCapabilities":{"loadSession":false,"promptCapabilities":{"image":false,"audio":false,"embeddedContext":false},"mcpCapabilities":{"http":false,"sse":false}}}}\n' "$id"
      else
        printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentInfo":{"name":"deerflow-acp","version":"0.1.0"},"agentCapabilities":{"loadSession":true,"promptCapabilities":{"image":false,"audio":false,"embeddedContext":false},"mcpCapabilities":{"http":false,"sse":false}}}}\n' "$id"
      fi
      ;;
    *'"method":"session/new"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"df-0123456789abcdef"}}\n' "$id"
      ;;
    *'"method":"session/resume"'*)
      case "$line" in
        *'"cwd"'*) ;;
        *)
          printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32602,"message":"missing required argument: cwd"}}\n' "$id"
          continue
          ;;
      esac
      if [ -n "$DEERFLOW_RESUME_ERROR_CODE" ]; then
        printf '{"jsonrpc":"2.0","id":%s,"error":{"code":%s,"message":"deerflow resume refused"}}\n' "$id" "$DEERFLOW_RESUME_ERROR_CODE"
        exit 0
      fi
      if [ -n "$DEERFLOW_STALE_REPLAY" ]; then
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"df-existing","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"STALE PRIOR ANSWER"}}}}\n'
      fi
      printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      ;;
    *'"method":"session/prompt"'*)
      if [ -n "$DEERFLOW_PROMPT_ERROR_CODE" ]; then
        printf '{"jsonrpc":"2.0","id":%s,"error":{"code":%s,"message":"deerflow prompt refused"}}\n' "$id" "$DEERFLOW_PROMPT_ERROR_CODE"
        exit 0
      fi
      printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"df-0123456789abcdef","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"CURRENT ANSWER"}}}}\n'
      printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"%s","usage":{"inputTokens":11,"outputTokens":22}}}\n' "$id" "${DEERFLOW_STOP_REASON:-end_turn}"
      ;;
    *)
      printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id"
      ;;
  esac
done
`
}

func writeFakeDeerflowScript(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "deerflow-acp")
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatalf("write fake deerflow-acp: %v", err)
	}
	return bin
}

// TestDeerflowSessionNew covers the fresh-session happy path and pins the two
// params session/new must carry: the task workdir as `cwd` and an EMPTY
// mcpServers array. A non-empty array is answered with -32602 by the bridge
// rather than ignored.
func TestDeerflowSessionNew(t *testing.T) {
	t.Parallel()
	bin := writeFakeDeerflowScript(t, fakeDeerflowACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("deerflow", Config{
		ExecutablePath: bin,
		Logger:         deerflowTestLogger(),
		Env: map[string]string{
			"DEERFLOW_REQUESTS_FILE": reqFile,
			"DEERFLOW_ACP_MODEL":     "deepseek-chat",
		},
	})
	if err != nil {
		t.Fatalf("New(deerflow) error: %v", err)
	}

	workdir := t.TempDir()
	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{Cwd: workdir})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for range session.Messages {
	}

	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("expected completed, got status=%q error=%q", result.Status, result.Error)
	}
	if result.SessionID != "df-0123456789abcdef" {
		t.Fatalf("expected the bridge thread id as session id, got %q", result.SessionID)
	}
	if !strings.Contains(result.Output, "CURRENT ANSWER") {
		t.Fatalf("expected the streamed answer in Result.Output, got %q", result.Output)
	}
	// DEERFLOW_ACP_MODEL is the only statement of which model ran: the model is
	// process-global and the prompt result carries no per-turn id.
	if _, ok := result.Usage["deepseek-chat"]; !ok {
		t.Fatalf("expected usage attributed to DEERFLOW_ACP_MODEL, got %+v", result.Usage)
	}

	frame := findRecordedFrame(t, reqFile, "session/new")
	params, _ := frame["params"].(map[string]any)
	if params["cwd"] != workdir {
		t.Fatalf("session/new must carry the task workdir as cwd, got %#v", params["cwd"])
	}
	servers, ok := params["mcpServers"].([]any)
	if !ok || len(servers) != 0 {
		t.Fatalf("session/new must carry an empty mcpServers array, got %#v", params["mcpServers"])
	}
}

// TestDeerflowUsageFallsBackToUnknownModel guards attribution when the
// deployment does not pin DEERFLOW_ACP_MODEL: inventing an id would bill the
// turn against a model that may not have run it.
func TestDeerflowUsageFallsBackToUnknownModel(t *testing.T) {
	t.Parallel()
	bin := writeFakeDeerflowScript(t, fakeDeerflowACPScript())

	b, err := New("deerflow", Config{ExecutablePath: bin, Logger: deerflowTestLogger()})
	if err != nil {
		t.Fatalf("New(deerflow) error: %v", err)
	}
	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("expected completed, got status=%q error=%q", result.Status, result.Error)
	}
	if _, ok := result.Usage["unknown"]; !ok {
		t.Fatalf("expected usage under \"unknown\" without DEERFLOW_ACP_MODEL, got %+v", result.Usage)
	}
}

// TestDeerflowNeverNegotiatesModelOrEffort is the regression for treating
// deerflow as a kimi-shaped runtime. Both RPCs answer -32601 on the wire, so
// sending either would hard-fail every task that carries a saved model or
// thinking level. Both values must instead be dropped with a warning.
func TestDeerflowNeverNegotiatesModelOrEffort(t *testing.T) {
	t.Parallel()
	bin := writeFakeDeerflowScript(t, fakeDeerflowACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("deerflow", Config{
		ExecutablePath: bin,
		Logger:         deerflowTestLogger(),
		Env:            map[string]string{"DEERFLOW_REQUESTS_FILE": reqFile},
	})
	if err != nil {
		t.Fatalf("New(deerflow) error: %v", err)
	}

	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd:           t.TempDir(),
		Model:         "deepseek-reasoner",
		ThinkingLevel: "high",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("a saved model/thinking level must not fail the run, got status=%q error=%q", result.Status, result.Error)
	}
	assertNoRecordedFrame(t, reqFile, "session/set_model")
	assertNoRecordedFrame(t, reqFile, "session/set_config_option")
	// A dropped model pick must not be laundered into usage attribution.
	if _, ok := result.Usage["deepseek-reasoner"]; ok {
		t.Fatalf("a model the bridge never applied must not appear in usage, got %+v", result.Usage)
	}
}

// TestDeerflowDoesNotForwardMcpServers pins that a stale mcp_config saved
// before the MCP tab was hidden never reaches the wire. The bridge REJECTS a
// non-empty array with -32602 instead of ignoring it, so forwarding would
// brick the task outright.
func TestDeerflowDoesNotForwardMcpServers(t *testing.T) {
	t.Parallel()
	bin := writeFakeDeerflowScript(t, fakeDeerflowACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("deerflow", Config{
		ExecutablePath: bin,
		Logger:         deerflowTestLogger(),
		Env:            map[string]string{"DEERFLOW_REQUESTS_FILE": reqFile},
	})
	if err != nil {
		t.Fatalf("New(deerflow) error: %v", err)
	}

	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd: t.TempDir(),
		McpConfig: json.RawMessage(`{"mcpServers":{"probe-stdio":{"command":"/bin/sh","args":["-c","true"]},` +
			`"probe-http":{"type":"http","url":"http://127.0.0.1:59999/mcp"}}}`),
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for range session.Messages {
	}
	if result := <-session.Result; result.Status != "completed" {
		t.Fatalf("a stale mcp_config must not fail the run, got status=%q error=%q", result.Status, result.Error)
	}

	raw, err := os.ReadFile(reqFile)
	if err != nil {
		t.Fatalf("read requests file: %v", err)
	}
	for _, name := range []string{"probe-stdio", "probe-http"} {
		if strings.Contains(string(raw), name) {
			t.Fatalf("MCP server %q leaked onto the wire:\n%s", name, raw)
		}
	}
}

// TestDeerflowResumeUsesSessionResume covers the resume happy path and pins
// the param set: session/resume carries the session id, the task workdir as
// `cwd`, and an EMPTY mcpServers array. The bridge binds all three
// (resume_session(cwd, session_id, mcp_servers)), so a request missing `cwd`
// fails parameter binding before the session is looked up; a non-empty
// mcpServers is answered with -32602. session/load is forbidden — it replays
// the retained transcript as session/update notifications, which would
// republish the previous answer as this turn's output.
func TestDeerflowResumeUsesSessionResume(t *testing.T) {
	t.Parallel()
	bin := writeFakeDeerflowScript(t, fakeDeerflowACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("deerflow", Config{
		ExecutablePath: bin,
		Logger:         deerflowTestLogger(),
		Env:            map[string]string{"DEERFLOW_REQUESTS_FILE": reqFile},
	})
	if err != nil {
		t.Fatalf("New(deerflow) error: %v", err)
	}

	workdir := t.TempDir()
	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd:             workdir,
		ResumeSessionID: "df-existing",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for range session.Messages {
	}

	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("expected completed, got status=%q error=%q", result.Status, result.Error)
	}
	// The bare {} response leaves resolveResumedSessionID on the requested id.
	if result.SessionID != "df-existing" {
		t.Fatalf("expected sessionID df-existing (fallback from resume), got %q", result.SessionID)
	}
	if result.ResumeRejected || result.ResumeRejectedTransient {
		t.Fatalf("expected no rejection on successful resume, got rejected=%v transient=%v", result.ResumeRejected, result.ResumeRejectedTransient)
	}

	frame := findRecordedFrame(t, reqFile, "session/resume")
	params, _ := frame["params"].(map[string]any)
	if params["sessionId"] != "df-existing" {
		t.Fatalf("session/resume must carry the requested session id, got %#v", params["sessionId"])
	}
	// cwd is a required positional on the bridge's resume_session; without it
	// the request fails parameter binding, not session lookup.
	if params["cwd"] != workdir {
		t.Fatalf("session/resume must carry the task workdir as cwd, got %#v", params["cwd"])
	}
	servers, ok := params["mcpServers"].([]any)
	if !ok || len(servers) != 0 {
		t.Fatalf("session/resume must carry an empty mcpServers array, got %#v", params["mcpServers"])
	}
	if len(params) != 3 {
		t.Fatalf("session/resume must send exactly sessionId, cwd and mcpServers, got %#v", params)
	}
	assertNoRecordedFrame(t, reqFile, "session/load")
	assertNoRecordedFrame(t, reqFile, "session/new")
}

// TestDeerflowResumeDropsReplayedHistory pins the turn gate. Even when the
// bridge flushes a historical agent_message_chunk before answering the resume
// request, it must be swallowed: it belongs to a prior turn.
func TestDeerflowResumeDropsReplayedHistory(t *testing.T) {
	t.Parallel()
	bin := writeFakeDeerflowScript(t, fakeDeerflowACPScript())

	b, err := New("deerflow", Config{
		ExecutablePath: bin,
		Logger:         deerflowTestLogger(),
		Env:            map[string]string{"DEERFLOW_STALE_REPLAY": "1"},
	})
	if err != nil {
		t.Fatalf("New(deerflow) error: %v", err)
	}

	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd:             t.TempDir(),
		ResumeSessionID: "df-existing",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var streamed []string
	for msg := range session.Messages {
		if msg.Type == MessageText {
			streamed = append(streamed, msg.Content)
		}
	}

	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("expected completed, got status=%q error=%q", result.Status, result.Error)
	}
	if strings.Contains(result.Output, "STALE PRIOR ANSWER") {
		t.Fatalf("replayed history leaked into Result.Output: %q", result.Output)
	}
	if !strings.Contains(result.Output, "CURRENT ANSWER") {
		t.Fatalf("expected the current turn's answer in Result.Output, got %q", result.Output)
	}
	for _, content := range streamed {
		if strings.Contains(content, "STALE PRIOR ANSWER") {
			t.Fatalf("replayed history was re-sent to the UI: %v", streamed)
		}
	}
}

// TestDeerflowResumeWithoutLoadSessionCapability covers a bridge started
// without unstable protocol support. session/resume is registered as unstable
// there, so it answers -32601 on a perfectly healthy session. The capability
// gate has to catch that before the RPC goes out, and report a rejection so
// the daemon owns the fresh retry instead of surfacing method-not-found.
func TestDeerflowResumeWithoutLoadSessionCapability(t *testing.T) {
	t.Parallel()
	bin := writeFakeDeerflowScript(t, fakeDeerflowACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("deerflow", Config{
		ExecutablePath: bin,
		Logger:         deerflowTestLogger(),
		Env: map[string]string{
			"DEERFLOW_REQUESTS_FILE":   reqFile,
			"DEERFLOW_NO_LOAD_SESSION": "1",
		},
	})
	if err != nil {
		t.Fatalf("New(deerflow) error: %v", err)
	}

	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd:             t.TempDir(),
		ResumeSessionID: "df-existing",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for range session.Messages {
	}

	result := <-session.Result
	if result.Status != "failed" || !result.ResumeRejected {
		t.Fatalf("expected a failed resume rejection, got status=%q rejected=%v error=%q", result.Status, result.ResumeRejected, result.Error)
	}
	if !strings.Contains(result.Error, "session/resume unavailable") {
		t.Fatalf("expected an actionable resume-unavailable error, got %q", result.Error)
	}
	assertNoRecordedFrame(t, reqFile, "session/resume")
	assertNoRecordedFrame(t, reqFile, "session/new")

	// A fresh session must be unaffected by the missing capability: nothing on
	// the first-turn path needs the unstable router.
	fresh, err := b.Execute(context.Background(), "test prompt", ExecOptions{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for range fresh.Messages {
	}
	if result := <-fresh.Result; result.Status != "completed" {
		t.Fatalf("a fresh session must not need loadSession, got status=%q error=%q", result.Status, result.Error)
	}
}

// TestDeerflowResumeErrorCodeAccounting is the load-bearing distinction
// between the bridge's four private codes: whether the resume pointer
// survives. Collapsing them into one failure either throws away a live thread
// on an outage or retries forever against a quarantined one.
func TestDeerflowResumeErrorCodeAccounting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		code          string
		wantRejected  bool
		wantTransient bool
		wantErrorHint string
	}{
		{
			name:         "unknown session is permanent",
			code:         "-32001",
			wantRejected: true,
		},
		{
			// A backend outage says nothing about the session: a fresh one
			// would fail identically, so retiring the pointer would lose the
			// conversation to an outage.
			name:          "backend unavailable keeps the pointer",
			code:          "-32010",
			wantErrorHint: "MULTICA_DEERFLOW_HOME",
		},
		{
			name:          "turn in progress is transient",
			code:          "-32011",
			wantTransient: true,
			wantErrorHint: "another turn is still running",
		},
		{
			name:          "quarantine is irreversible",
			code:          "-32012",
			wantRejected:  true,
			wantErrorHint: "quarantined",
		},
		{
			// An unclassified error must behave like a transient fault, not
			// like a lost session.
			name: "unclassified error keeps the pointer",
			code: "-32603",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bin := writeFakeDeerflowScript(t, fakeDeerflowACPScript())
			b, err := New("deerflow", Config{
				ExecutablePath: bin,
				Logger:         deerflowTestLogger(),
				Env:            map[string]string{"DEERFLOW_RESUME_ERROR_CODE": tc.code},
			})
			if err != nil {
				t.Fatalf("New(deerflow) error: %v", err)
			}

			session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
				Cwd:             t.TempDir(),
				ResumeSessionID: "df-existing",
			})
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			for range session.Messages {
			}

			result := <-session.Result
			if result.Status != "failed" {
				t.Fatalf("expected failed, got status=%q error=%q", result.Status, result.Error)
			}
			if result.ResumeRejected != tc.wantRejected {
				t.Fatalf("code %s: ResumeRejected=%v want %v (error=%q)", tc.code, result.ResumeRejected, tc.wantRejected, result.Error)
			}
			if result.ResumeRejectedTransient != tc.wantTransient {
				t.Fatalf("code %s: ResumeRejectedTransient=%v want %v (error=%q)", tc.code, result.ResumeRejectedTransient, tc.wantTransient, result.Error)
			}
			if !strings.Contains(result.Error, "session/resume failed") {
				t.Fatalf("expected the failing method in the error, got %q", result.Error)
			}
			if tc.wantErrorHint != "" && !strings.Contains(result.Error, tc.wantErrorHint) {
				t.Fatalf("expected the error to explain the code (%q), got %q", tc.wantErrorHint, result.Error)
			}
		})
	}
}

// TestDeerflowPromptTimeSessionLoss covers the window the resume response
// cannot cover: the bridge echoes the id back without touching DeerFlow, so a
// thread evicted or quarantined between resume and prompt only surfaces at
// prompt time. A permanent loss must clear the session id; an outage must not.
func TestDeerflowPromptTimeSessionLoss(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		code          string
		wantRejected  bool
		wantSessionID string
		wantTransient bool
	}{
		{
			name:          "quarantine clears the pointer",
			code:          "-32012",
			wantRejected:  true,
			wantSessionID: "",
		},
		{
			name:          "concurrent turn keeps the pointer",
			code:          "-32011",
			wantTransient: true,
			wantSessionID: "df-existing",
		},
		{
			name:          "backend outage keeps the pointer",
			code:          "-32010",
			wantSessionID: "df-existing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bin := writeFakeDeerflowScript(t, fakeDeerflowACPScript())
			b, err := New("deerflow", Config{
				ExecutablePath: bin,
				Logger:         deerflowTestLogger(),
				Env:            map[string]string{"DEERFLOW_PROMPT_ERROR_CODE": tc.code},
			})
			if err != nil {
				t.Fatalf("New(deerflow) error: %v", err)
			}

			session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
				Cwd:             t.TempDir(),
				ResumeSessionID: "df-existing",
			})
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			for range session.Messages {
			}

			result := <-session.Result
			if result.Status != "failed" {
				t.Fatalf("expected failed, got status=%q error=%q", result.Status, result.Error)
			}
			if result.ResumeRejected != tc.wantRejected {
				t.Fatalf("code %s: ResumeRejected=%v want %v", tc.code, result.ResumeRejected, tc.wantRejected)
			}
			if result.ResumeRejectedTransient != tc.wantTransient {
				t.Fatalf("code %s: ResumeRejectedTransient=%v want %v", tc.code, result.ResumeRejectedTransient, tc.wantTransient)
			}
			if result.SessionID != tc.wantSessionID {
				t.Fatalf("code %s: SessionID=%q want %q", tc.code, result.SessionID, tc.wantSessionID)
			}
		})
	}
}

// TestDeerflowStopReasonRefusalFails pins refusal as a failed turn. The bridge
// maps a DeerFlow stream() exception to refusal and redacts the detail, so
// reporting it as completed would present an empty answer as the final one.
func TestDeerflowStopReasonRefusalFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		stopReason string
		wantStatus string
	}{
		{stopReason: "end_turn", wantStatus: "completed"},
		{stopReason: "cancelled", wantStatus: "aborted"},
		{stopReason: "refusal", wantStatus: "failed"},
	} {
		t.Run(tc.stopReason, func(t *testing.T) {
			t.Parallel()
			bin := writeFakeDeerflowScript(t, fakeDeerflowACPScript())
			b, err := New("deerflow", Config{
				ExecutablePath: bin,
				Logger:         deerflowTestLogger(),
				Env:            map[string]string{"DEERFLOW_STOP_REASON": tc.stopReason},
			})
			if err != nil {
				t.Fatalf("New(deerflow) error: %v", err)
			}
			session, err := b.Execute(context.Background(), "test prompt", ExecOptions{Cwd: t.TempDir()})
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			for range session.Messages {
			}
			result := <-session.Result
			if result.Status != tc.wantStatus {
				t.Fatalf("stopReason %q: got status=%q want %q (error=%q)", tc.stopReason, result.Status, tc.wantStatus, result.Error)
			}
			// The session id survives every terminal reason so the next turn
			// can continue the same thread.
			if result.SessionID != "df-0123456789abcdef" {
				t.Fatalf("stopReason %q: expected the session id to survive, got %q", tc.stopReason, result.SessionID)
			}
		})
	}
}

// TestDeerflowProcessDirComesFromHomeEnv pins the cwd split. DeerFlow resolves
// its own config.yaml relative to the PROCESS working directory, so the bridge
// must start in the deployment root while the session's `cwd` param stays the
// task workdir. Collapsing the two fails every turn with an opaque -32010.
func TestDeerflowProcessDirComesFromHomeEnv(t *testing.T) {
	t.Parallel()
	deployRoot := t.TempDir()
	taskDir := t.TempDir()

	b := &deerflowBackend{cfg: Config{
		Logger: deerflowTestLogger(),
		Env:    map[string]string{deerflowHomeEnv: deployRoot},
	}}
	if got := b.resolveDeerflowProcessDir(taskDir); got != deployRoot {
		t.Fatalf("expected the configured deployment root, got %q", got)
	}

	// Unset falls back to the task workdir with a warning rather than refusing
	// to launch: an operator whose deployment root IS the workdir stays working.
	unset := &deerflowBackend{cfg: Config{Logger: deerflowTestLogger()}}
	if got := unset.resolveDeerflowProcessDir(taskDir); got != taskDir {
		t.Fatalf("expected the task workdir when %s is unset, got %q", deerflowHomeEnv, got)
	}

	// A path that is not a directory must not become cmd.Dir: exec would fail
	// with a bare chdir error instead of the warning that says what to fix.
	notADir := filepath.Join(deployRoot, "config.yaml")
	if err := os.WriteFile(notADir, []byte("{}"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	bad := &deerflowBackend{cfg: Config{
		Logger: deerflowTestLogger(),
		Env:    map[string]string{deerflowHomeEnv: notADir},
	}}
	if got := bad.resolveDeerflowProcessDir(taskDir); got != taskDir {
		t.Fatalf("expected the task workdir when %s names a file, got %q", deerflowHomeEnv, got)
	}
	missing := &deerflowBackend{cfg: Config{
		Logger: deerflowTestLogger(),
		Env:    map[string]string{deerflowHomeEnv: filepath.Join(deployRoot, "nope")},
	}}
	if got := missing.resolveDeerflowProcessDir(taskDir); got != taskDir {
		t.Fatalf("expected the task workdir when %s is missing, got %q", deerflowHomeEnv, got)
	}
}

// TestDeerflowSessionCwdIsTaskDirNotDeploymentRoot is the wire-level half of
// the split above.
func TestDeerflowSessionCwdIsTaskDirNotDeploymentRoot(t *testing.T) {
	t.Parallel()
	bin := writeFakeDeerflowScript(t, fakeDeerflowACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")
	deployRoot := t.TempDir()
	taskDir := t.TempDir()

	b, err := New("deerflow", Config{
		ExecutablePath: bin,
		Logger:         deerflowTestLogger(),
		Env: map[string]string{
			"DEERFLOW_REQUESTS_FILE": reqFile,
			deerflowHomeEnv:          deployRoot,
		},
	})
	if err != nil {
		t.Fatalf("New(deerflow) error: %v", err)
	}
	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{Cwd: taskDir})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for range session.Messages {
	}
	if result := <-session.Result; result.Status != "completed" {
		t.Fatalf("expected completed, got status=%q error=%q", result.Status, result.Error)
	}

	frame := findRecordedFrame(t, reqFile, "session/new")
	params, _ := frame["params"].(map[string]any)
	if params["cwd"] != taskDir {
		t.Fatalf("session/new cwd must stay the task workdir (%q), got %#v", taskDir, params["cwd"])
	}
	if params["cwd"] == deployRoot {
		t.Fatal("session/new cwd must not be replaced by the deployment root")
	}
}

func TestDeerflowBlockedArgs(t *testing.T) {
	t.Parallel()
	// `acp` is appended by the backend; `doctor` prints a readiness report and
	// exits without ever starting the ACP server.
	for _, flag := range []string{"acp", "doctor", "--help", "-h", "--version"} {
		mode, ok := deerflowBlockedArgs[flag]
		if !ok {
			t.Fatalf("expected %s to be in deerflowBlockedArgs", flag)
		}
		if mode != blockedStandalone {
			t.Fatalf("expected %s to be blockedStandalone, got %v", flag, mode)
		}
	}
	kept := filterCustomArgs([]string{"acp", "--log-level", "debug", "doctor"}, deerflowBlockedArgs, deerflowTestLogger())
	if strings.Join(kept, " ") != "--log-level debug" {
		t.Fatalf("expected only the passthrough flags to survive, got %v", kept)
	}
}

func TestDeerflowLoadSessionSupported(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{name: "advertised", raw: `{"agentCapabilities":{"loadSession":true}}`, want: true},
		{name: "explicitly false", raw: `{"agentCapabilities":{"loadSession":false}}`},
		{name: "absent", raw: `{"agentCapabilities":{}}`},
		{name: "no capabilities block", raw: `{"protocolVersion":1}`},
		{name: "malformed", raw: `not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := deerflowLoadSessionSupported(json.RawMessage(tc.raw)); got != tc.want {
				t.Fatalf("deerflowLoadSessionSupported(%s) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestDeerflowErrorClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		code          int
		wantPermanent bool
		wantTransient bool
	}{
		{code: deerflowCodeUnknownSession, wantPermanent: true},
		{code: deerflowCodeSessionQuarantined, wantPermanent: true},
		{code: deerflowCodeTurnInProgress, wantTransient: true},
		{code: deerflowCodeBackendUnavailable},
		{code: -32603},
	} {
		err := &acpRPCError{Method: "session/resume", Code: tc.code, Message: "refused"}
		if got := deerflowRPCCode(err); got != tc.code {
			t.Fatalf("deerflowRPCCode = %d, want %d", got, tc.code)
		}
		if got := deerflowSessionPermanentlyLost(err); got != tc.wantPermanent {
			t.Fatalf("code %d: deerflowSessionPermanentlyLost = %v, want %v", tc.code, got, tc.wantPermanent)
		}
		if got := deerflowSessionTemporarilyBusy(err); got != tc.wantTransient {
			t.Fatalf("code %d: deerflowSessionTemporarilyBusy = %v, want %v", tc.code, got, tc.wantTransient)
		}
	}

	// A non-RPC error carries no code and must classify as neither, so a
	// transport failure never retires a live session.
	plain := errors.New("pipe closed")
	if deerflowRPCCode(plain) != 0 || deerflowSessionPermanentlyLost(plain) || deerflowSessionTemporarilyBusy(plain) {
		t.Fatal("a non-RPC error must classify as neither permanent nor transient")
	}
}

// TestDeerflowListModels pins that the family reports no catalog WITHOUT
// spawning a discovery subprocess: the model is process-global
// (DEERFLOW_ACP_MODEL) and session/set_model answers -32601, so there is
// nothing to discover and nothing that could consume a catalog.
func TestDeerflowListModels(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked")
	bin := writeFakeDeerflowScript(t, "#!/bin/sh\ntouch '"+marker+"'\nexit 0\n")

	cat, err := ListModels(context.Background(), "deerflow", Command{Path: bin})
	if err != nil {
		t.Fatalf("deerflow ListModels should not error, got: %v", err)
	}
	if len(cat.Models) != 0 {
		t.Fatalf("deerflow ListModels should return an empty catalog, got %d models", len(cat.Models))
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("deerflow ListModels executed the CLI; it must return an empty catalog without spawning a discovery subprocess")
	}
}

func TestDeerflowModelSelectionUnsupported(t *testing.T) {
	t.Parallel()
	if ModelSelectionSupported("deerflow") {
		t.Fatal("ModelSelectionSupported(deerflow) should be false — the bridge answers session/set_model with -32601 and pins the model at startup via DEERFLOW_ACP_MODEL")
	}
}

// TestDeerflowTimeout pins that a stalled session/new is reported as a
// timeout rather than a generic failure.
func TestDeerflowTimeout(t *testing.T) {
	t.Parallel()
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true}}}\n' "$id"
      ;;
    *'"method":"session/new"'*)
      sleep 30
      printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"df-late"}}\n' "$id"
      ;;
    *)
      printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id"
      ;;
  esac
done
`
	bin := writeFakeDeerflowScript(t, script)
	b, err := New("deerflow", Config{ExecutablePath: bin, Logger: deerflowTestLogger()})
	if err != nil {
		t.Fatalf("New(deerflow) error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := b.Execute(ctx, "test prompt", ExecOptions{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for range session.Messages {
	}
	if result := <-session.Result; result.Status != "timeout" {
		t.Fatalf("expected timeout, got status=%q error=%q", result.Status, result.Error)
	}
}
