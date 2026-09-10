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

func zcodeTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewReturnsZcodeBackend(t *testing.T) {
	t.Parallel()
	b, err := New("zcode", Config{ExecutablePath: "/nonexistent/zcode-acp"})
	if err != nil {
		t.Fatalf("New(zcode) error: %v", err)
	}
	if _, ok := b.(*zcodeBackend); !ok {
		t.Fatalf("expected *zcodeBackend, got %T", b)
	}
}

// fakeZcodeACPScript impersonates `zcode-acp acp`. It reproduces the frames
// zcode-acp 0.13.0 sends, because the differences from kimi — the family this
// runtime used to be shelled onto — are what this suite guards:
//
//   - session/new is a LAZY placeholder: {sessionId, modes, configOptions} and
//     no models block. zcode's own session is created on first use, so a
//     backend fault only surfaces at set_model or prompt time.
//   - configOptions advertise the reasoning dial as id `thought` under
//     category `thought_level`, not kimi's `thinking`.
//   - session/resume answers {modes, configOptions} with NO sessionId.
//   - session/set_model is registered under the snake_case spelling.
//   - the prompt result carries usage at the TOP level and can stop with
//     max_turn_requests, which neither kimi nor the shared helpers know.
//   - errors are plain Errors classified by a message prefix
//     (zcode_session_lost / zcode_spawn_failed / zcode_backend_dead_after_retry),
//     not by a private JSON-RPC code.
func fakeZcodeACPScript() string {
	return `#!/bin/sh
while IFS= read -r line; do
  if [ -n "$ZCODE_REQUESTS_FILE" ]; then
    printf '%s\n' "$line" >> "$ZCODE_REQUESTS_FILE"
  fi
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentInfo":{"name":"zcode-acp","version":"0.13.0"},"authMethods":[],"agentCapabilities":{"loadSession":true,"promptCapabilities":{"image":true,"audio":false,"embeddedContext":false},"mcpCapabilities":{"http":false,"sse":false},"sessionCapabilities":{"list":{},"resume":{},"fork":{}}}}}\n' "$id"
      ;;
    *'"method":"session/new"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"zc-placeholder-1","modes":{"currentModeId":"default","availableModes":[{"id":"default","name":"Default"}]},"configOptions":[{"id":"model","category":"model","currentValue":"glm-4.6","options":[{"value":"glm-4.6","name":"GLM 4.6"},{"value":"anthropic\\\\claude-sonnet","name":"Claude Sonnet"}]},{"id":"thought","category":"thought_level","currentValue":"medium","options":[{"value":"low","name":"Low"},{"value":"medium","name":"Medium"},{"value":"high","name":"High"}]}]}}\n' "$id"
      ;;
    *'"method":"session/resume"'*)
      if [ -n "$ZCODE_RESUME_ERROR" ]; then
        printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"%s"}}\n' "$id" "$ZCODE_RESUME_ERROR"
        exit 0
      fi
      if [ -n "$ZCODE_STALE_REPLAY" ]; then
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"zc-existing","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"STALE PRIOR ANSWER"}}}}\n'
      fi
      printf '{"jsonrpc":"2.0","id":%s,"result":{"modes":{"currentModeId":"default","availableModes":[{"id":"default","name":"Default"}]},"configOptions":[{"id":"thought","category":"thought_level","currentValue":"medium","options":[{"value":"low","name":"Low"},{"value":"medium","name":"Medium"},{"value":"high","name":"High"}]}]}}\n' "$id"
      ;;
    *'"method":"session/set_model"'*)
      if [ -n "$ZCODE_SET_MODEL_ERROR" ]; then
        printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"%s"}}\n' "$id" "$ZCODE_SET_MODEL_ERROR"
        exit 0
      fi
      printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      ;;
    *'"method":"session/set_config_option"'*)
      value=$(printf '%s' "$line" | sed -n 's/.*"value":"\([^"]*\)".*/\1/p')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"configOptions":[{"id":"thought","category":"thought_level","currentValue":"%s","options":[{"value":"low","name":"Low"},{"value":"medium","name":"Medium"},{"value":"high","name":"High"}]}]}}\n' "$id" "$value"
      ;;
    *'"method":"session/prompt"'*)
      if [ -n "$ZCODE_PROMPT_ERROR" ]; then
        printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"%s"}}\n' "$id" "$ZCODE_PROMPT_ERROR"
        exit 0
      fi
      printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"zc-placeholder-1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"CURRENT ANSWER"}}}}\n'
      printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"%s","usage":{"inputTokens":31,"outputTokens":42}}}\n' "$id" "${ZCODE_STOP_REASON:-end_turn}"
      ;;
    *)
      printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id"
      ;;
  esac
done
`
}

func writeFakeZcodeScript(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "zcode-acp")
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatalf("write fake zcode-acp: %v", err)
	}
	return bin
}

// TestZcodeSessionNew covers the fresh-session happy path and pins usage
// attribution to the prompt result's top-level `usage` object. Kimi's disk
// scan must NOT be inherited: there is no zcode wire log to scan, so a
// fallback would bill every turn as zero tokens.
func TestZcodeSessionNew(t *testing.T) {
	t.Parallel()
	bin := writeFakeZcodeScript(t, fakeZcodeACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("zcode", Config{
		ExecutablePath: bin,
		Logger:         zcodeTestLogger(),
		Env:            map[string]string{"ZCODE_REQUESTS_FILE": reqFile},
	})
	if err != nil {
		t.Fatalf("New(zcode) error: %v", err)
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
	if result.SessionID != "zc-placeholder-1" {
		t.Fatalf("expected the placeholder session id, got %q", result.SessionID)
	}
	if !strings.Contains(result.Output, "CURRENT ANSWER") {
		t.Fatalf("expected the streamed answer in Result.Output, got %q", result.Output)
	}
	usage, ok := result.Usage["unknown"]
	if !ok {
		t.Fatalf("expected top-level prompt usage attributed to \"unknown\" without a model pick, got %+v", result.Usage)
	}
	if usage.InputTokens != 31 || usage.OutputTokens != 42 {
		t.Fatalf("expected the prompt result's own token counts, got %+v", usage)
	}

	frame := findRecordedFrame(t, reqFile, "session/new")
	params, _ := frame["params"].(map[string]any)
	if params["cwd"] != workdir {
		t.Fatalf("session/new must carry the task workdir as cwd, got %#v", params["cwd"])
	}
}

// TestZcodeForwardsMcpServersOnNewAndResume pins the MCP contract. The bridge
// forwards mcpServers to its ZCode backend on BOTH entry points, so dropping
// them on resume would silently strip a resumed task's tools.
func TestZcodeForwardsMcpServersOnNewAndResume(t *testing.T) {
	t.Parallel()
	mcp := json.RawMessage(`{"mcpServers":{"probe-stdio":{"command":"/bin/sh","args":["-c","true"]}}}`)

	for _, tc := range []struct {
		name   string
		method string
		opts   ExecOptions
	}{
		{name: "session/new", method: "session/new", opts: ExecOptions{McpConfig: mcp}},
		{name: "session/resume", method: "session/resume", opts: ExecOptions{McpConfig: mcp, ResumeSessionID: "zc-existing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bin := writeFakeZcodeScript(t, fakeZcodeACPScript())
			reqFile := filepath.Join(t.TempDir(), "requests.txt")

			b, err := New("zcode", Config{
				ExecutablePath: bin,
				Logger:         zcodeTestLogger(),
				Env:            map[string]string{"ZCODE_REQUESTS_FILE": reqFile},
			})
			if err != nil {
				t.Fatalf("New(zcode) error: %v", err)
			}
			opts := tc.opts
			opts.Cwd = t.TempDir()
			session, err := b.Execute(context.Background(), "test prompt", opts)
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			for range session.Messages {
			}
			if result := <-session.Result; result.Status != "completed" {
				t.Fatalf("expected completed, got status=%q error=%q", result.Status, result.Error)
			}

			frame := findRecordedFrame(t, reqFile, tc.method)
			params, _ := frame["params"].(map[string]any)
			servers, ok := params["mcpServers"].([]any)
			if !ok || len(servers) != 1 {
				t.Fatalf("%s must forward the configured MCP servers, got %#v", tc.method, params["mcpServers"])
			}
			entry, _ := servers[0].(map[string]any)
			if entry["name"] != "probe-stdio" {
				t.Fatalf("%s carried an unexpected MCP entry: %#v", tc.method, entry)
			}
		})
	}
}

// TestZcodeDropsRemoteMcpTransports covers the capability filter. zcode-acp
// advertises mcpCapabilities {http:false, sse:false}, so an http/sse entry has
// to be dropped rather than tanking session/new for the stdio servers that do
// work.
func TestZcodeDropsRemoteMcpTransports(t *testing.T) {
	t.Parallel()
	bin := writeFakeZcodeScript(t, fakeZcodeACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("zcode", Config{
		ExecutablePath: bin,
		Logger:         zcodeTestLogger(),
		Env:            map[string]string{"ZCODE_REQUESTS_FILE": reqFile},
	})
	if err != nil {
		t.Fatalf("New(zcode) error: %v", err)
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
		t.Fatalf("an unsupported MCP transport must not fail the run, got status=%q error=%q", result.Status, result.Error)
	}

	frame := findRecordedFrame(t, reqFile, "session/new")
	params, _ := frame["params"].(map[string]any)
	servers, _ := params["mcpServers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("expected only the stdio server to survive the capability filter, got %#v", servers)
	}
	raw, err := os.ReadFile(reqFile)
	if err != nil {
		t.Fatalf("read requests file: %v", err)
	}
	if strings.Contains(string(raw), "probe-http") {
		t.Fatalf("an http MCP entry reached a bridge that advertises no http transport:\n%s", raw)
	}
}

// TestZcodeSetsModelWithSnakeCaseMethod pins the method spelling. The bridge
// registers `session/set_model` specifically for this client; the camelCase
// spelling falls through to -32601, which would hard-fail every model-pinned
// task.
func TestZcodeSetsModelWithSnakeCaseMethod(t *testing.T) {
	t.Parallel()
	bin := writeFakeZcodeScript(t, fakeZcodeACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("zcode", Config{
		ExecutablePath: bin,
		Logger:         zcodeTestLogger(),
		Env:            map[string]string{"ZCODE_REQUESTS_FILE": reqFile},
	})
	if err != nil {
		t.Fatalf("New(zcode) error: %v", err)
	}
	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd:   t.TempDir(),
		Model: "glm-4.6",
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
	if _, ok := result.Usage["glm-4.6"]; !ok {
		t.Fatalf("an applied model pick must own the turn's usage, got %+v", result.Usage)
	}

	frame := findRecordedFrame(t, reqFile, "session/set_model")
	params, _ := frame["params"].(map[string]any)
	if params["modelId"] != "glm-4.6" || params["sessionId"] != "zc-placeholder-1" {
		t.Fatalf("session/set_model carried unexpected params: %#v", params)
	}
	assertNoRecordedFrame(t, reqFile, "session/setModel")
}

// TestZcodeSetModelFailureFailsTheTask is deliberate: running on zcode's
// default model after the user pinned another one would present the answer as
// if their pick had been honoured.
func TestZcodeSetModelFailureFailsTheTask(t *testing.T) {
	t.Parallel()
	bin := writeFakeZcodeScript(t, fakeZcodeACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("zcode", Config{
		ExecutablePath: bin,
		Logger:         zcodeTestLogger(),
		Env: map[string]string{
			"ZCODE_REQUESTS_FILE":   reqFile,
			"ZCODE_SET_MODEL_ERROR": "unknown model",
		},
	})
	if err != nil {
		t.Fatalf("New(zcode) error: %v", err)
	}
	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd:   t.TempDir(),
		Model: "nope",
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
	if !strings.Contains(result.Error, "could not switch to model") {
		t.Fatalf("expected an actionable model error, got %q", result.Error)
	}
	// A fresh session that failed on set_model keeps its id: the session
	// exists and nothing about it is lost.
	if result.SessionID != "zc-placeholder-1" || result.ResumeRejected {
		t.Fatalf("a model error on a fresh session must not retire it, got session=%q rejected=%v", result.SessionID, result.ResumeRejected)
	}
	assertNoRecordedFrame(t, reqFile, "session/prompt")
}

// TestZcodeAppliesThoughtConfigOption pins the reasoning dial. zcode-acp
// addresses it as config id `thought` (category `thought_level`) with a
// per-model vocabulary; kimi's hard-coded `thinking` id sets nothing at all,
// which is one of the four reasons this is its own family.
func TestZcodeAppliesThoughtConfigOption(t *testing.T) {
	t.Parallel()
	bin := writeFakeZcodeScript(t, fakeZcodeACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("zcode", Config{
		ExecutablePath: bin,
		Logger:         zcodeTestLogger(),
		Env:            map[string]string{"ZCODE_REQUESTS_FILE": reqFile},
	})
	if err != nil {
		t.Fatalf("New(zcode) error: %v", err)
	}
	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd:           t.TempDir(),
		ThinkingLevel: "high",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for range session.Messages {
	}
	if result := <-session.Result; result.Status != "completed" {
		t.Fatalf("expected completed, got status=%q error=%q", result.Status, result.Error)
	}

	frame := findRecordedFrame(t, reqFile, "session/set_config_option")
	params, _ := frame["params"].(map[string]any)
	if params["configId"] != "thought" {
		t.Fatalf("the effort request must address the id the session advertised (thought), got %#v", params["configId"])
	}
	if params["value"] != "high" {
		t.Fatalf("expected the persisted level to be sent verbatim, got %#v", params["value"])
	}
}

// TestZcodeUnadvertisedThoughtLevelIsSkipped covers the other half: a level
// this session's model does not offer must not be sent. The vocabulary is
// per-model, so a level saved against another model would draw a hard error on
// a call whose failure is deliberately swallowed.
func TestZcodeUnadvertisedThoughtLevelIsSkipped(t *testing.T) {
	t.Parallel()
	bin := writeFakeZcodeScript(t, fakeZcodeACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("zcode", Config{
		ExecutablePath: bin,
		Logger:         zcodeTestLogger(),
		Env:            map[string]string{"ZCODE_REQUESTS_FILE": reqFile},
	})
	if err != nil {
		t.Fatalf("New(zcode) error: %v", err)
	}
	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd:           t.TempDir(),
		ThinkingLevel: "ultra",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for range session.Messages {
	}
	if result := <-session.Result; result.Status != "completed" {
		t.Fatalf("an unsupported level must not fail the run, got status=%q error=%q", result.Status, result.Error)
	}
	assertNoRecordedFrame(t, reqFile, "session/set_config_option")
}

// TestZcodeResumeKeepsRequestedSessionID pins the resume response shape: the
// bridge answers {modes, configOptions} with NO sessionId, so the client keeps
// addressing the session with the id it sent.
func TestZcodeResumeKeepsRequestedSessionID(t *testing.T) {
	t.Parallel()
	bin := writeFakeZcodeScript(t, fakeZcodeACPScript())
	reqFile := filepath.Join(t.TempDir(), "requests.txt")

	b, err := New("zcode", Config{
		ExecutablePath: bin,
		Logger:         zcodeTestLogger(),
		Env:            map[string]string{"ZCODE_REQUESTS_FILE": reqFile},
	})
	if err != nil {
		t.Fatalf("New(zcode) error: %v", err)
	}
	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd:             t.TempDir(),
		ResumeSessionID: "zc-existing",
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
	if result.SessionID != "zc-existing" {
		t.Fatalf("expected the requested id to survive a resume response without one, got %q", result.SessionID)
	}
	if result.ResumeRejected {
		t.Fatal("expected ResumeRejected=false on a successful resume")
	}
	// session/load replays the stored transcript as notifications, which would
	// republish the previous answer as this turn's output.
	assertNoRecordedFrame(t, reqFile, "session/load")
	assertNoRecordedFrame(t, reqFile, "session/new")

	promptFrame := findRecordedFrame(t, reqFile, "session/prompt")
	promptParams, _ := promptFrame["params"].(map[string]any)
	if promptParams["sessionId"] != "zc-existing" {
		t.Fatalf("the prompt must address the resumed session, got %#v", promptParams["sessionId"])
	}
}

// TestZcodeResumeDropsReplayedHistory pins the turn gate for zcode: the bridge
// can flush frames before answering the resume request, and they belong to a
// prior turn.
func TestZcodeResumeDropsReplayedHistory(t *testing.T) {
	t.Parallel()
	bin := writeFakeZcodeScript(t, fakeZcodeACPScript())

	b, err := New("zcode", Config{
		ExecutablePath: bin,
		Logger:         zcodeTestLogger(),
		Env:            map[string]string{"ZCODE_STALE_REPLAY": "1"},
	})
	if err != nil {
		t.Fatalf("New(zcode) error: %v", err)
	}
	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd:             t.TempDir(),
		ResumeSessionID: "zc-existing",
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

// TestZcodeResumeErrorAccounting pins the marker-based classification. The
// bridge raises plain Errors, so the distinction between "your session is
// gone" and "my backend is down" lives in the message prefix — and only the
// first may retire the resume pointer. Getting this wrong loses a live
// conversation to a transient outage.
func TestZcodeResumeErrorAccounting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		message      string
		wantRejected bool
		wantHint     string
	}{
		{
			name:         "supervised session-lost marker",
			message:      "zcode_session_lost: session file removed",
			wantRejected: true,
			wantHint:     "no longer has this session",
		},
		{
			// The bridge's non-supervised paths surface the raw backend
			// wording instead of the marker.
			name:         "raw backend wording",
			message:      "session zc-existing not found",
			wantRejected: true,
			wantHint:     "no longer has this session",
		},
		{
			name:     "spawn failure keeps the pointer",
			message:  "zcode_spawn_failed: ENOENT",
			wantHint: "could not keep its ZCode backend running",
		},
		{
			name:     "dead after retry keeps the pointer",
			message:  "zcode_backend_dead_after_retry: backend reader exited",
			wantHint: "could not keep its ZCode backend running",
		},
		{
			name:    "unclassified error keeps the pointer",
			message: "rate limit exceeded",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bin := writeFakeZcodeScript(t, fakeZcodeACPScript())
			b, err := New("zcode", Config{
				ExecutablePath: bin,
				Logger:         zcodeTestLogger(),
				Env:            map[string]string{"ZCODE_RESUME_ERROR": tc.message},
			})
			if err != nil {
				t.Fatalf("New(zcode) error: %v", err)
			}
			session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
				Cwd:             t.TempDir(),
				ResumeSessionID: "zc-existing",
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
				t.Fatalf("%q: ResumeRejected=%v want %v (error=%q)", tc.message, result.ResumeRejected, tc.wantRejected, result.Error)
			}
			if !strings.Contains(result.Error, "session/resume failed") {
				t.Fatalf("expected the failing method in the error, got %q", result.Error)
			}
			if tc.wantHint != "" && !strings.Contains(result.Error, tc.wantHint) {
				t.Fatalf("expected the error to explain the marker (%q), got %q", tc.wantHint, result.Error)
			}
		})
	}
}

// TestZcodePromptTimeSessionLoss is the consequence of the lazy placeholder:
// session/resume echoes the id back without touching zcode, so a session the
// bridge cannot materialise only fails at the first call that needs the
// backend. Clearing the id there is what lets the daemon retry fresh; keeping
// it on an outage is what stops an outage from discarding the conversation.
func TestZcodePromptTimeSessionLoss(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		message       string
		wantRejected  bool
		wantSessionID string
	}{
		{
			name:          "session lost clears the pointer",
			message:       "zcode_session_lost: session file removed",
			wantRejected:  true,
			wantSessionID: "",
		},
		{
			name:          "backend dead keeps the pointer",
			message:       "zcode_backend_dead_after_retry: backend reader exited",
			wantSessionID: "zc-existing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bin := writeFakeZcodeScript(t, fakeZcodeACPScript())
			b, err := New("zcode", Config{
				ExecutablePath: bin,
				Logger:         zcodeTestLogger(),
				Env:            map[string]string{"ZCODE_PROMPT_ERROR": tc.message},
			})
			if err != nil {
				t.Fatalf("New(zcode) error: %v", err)
			}
			session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
				Cwd:             t.TempDir(),
				ResumeSessionID: "zc-existing",
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
				t.Fatalf("%q: ResumeRejected=%v want %v", tc.message, result.ResumeRejected, tc.wantRejected)
			}
			if result.SessionID != tc.wantSessionID {
				t.Fatalf("%q: SessionID=%q want %q", tc.message, result.SessionID, tc.wantSessionID)
			}
		})
	}
}

// TestZcodeSetModelTimeSessionLoss covers the same window one call earlier: a
// pinned model is the first thing that needs the real backend.
func TestZcodeSetModelTimeSessionLoss(t *testing.T) {
	t.Parallel()
	bin := writeFakeZcodeScript(t, fakeZcodeACPScript())

	b, err := New("zcode", Config{
		ExecutablePath: bin,
		Logger:         zcodeTestLogger(),
		Env:            map[string]string{"ZCODE_SET_MODEL_ERROR": "zcode_session_lost: session file removed"},
	})
	if err != nil {
		t.Fatalf("New(zcode) error: %v", err)
	}
	session, err := b.Execute(context.Background(), "test prompt", ExecOptions{
		Cwd:             t.TempDir(),
		ResumeSessionID: "zc-existing",
		Model:           "glm-4.6",
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
	if !result.ResumeRejected || result.SessionID != "" {
		t.Fatalf("a session lost at set_model time must be retired, got session=%q rejected=%v", result.SessionID, result.ResumeRejected)
	}
}

// TestZcodeStopReasons pins the third terminal reason. max_turn_requests is
// the bridge's own per-turn request budget, not a finished answer: reporting it
// as completed would present a truncated result as the final one.
func TestZcodeStopReasons(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		stopReason string
		wantStatus string
		wantError  string
	}{
		{stopReason: "end_turn", wantStatus: "completed"},
		{stopReason: "cancelled", wantStatus: "aborted"},
		{stopReason: "max_turn_requests", wantStatus: "failed", wantError: "per-turn request limit"},
	} {
		t.Run(tc.stopReason, func(t *testing.T) {
			t.Parallel()
			bin := writeFakeZcodeScript(t, fakeZcodeACPScript())
			b, err := New("zcode", Config{
				ExecutablePath: bin,
				Logger:         zcodeTestLogger(),
				Env:            map[string]string{"ZCODE_STOP_REASON": tc.stopReason},
			})
			if err != nil {
				t.Fatalf("New(zcode) error: %v", err)
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
			if tc.wantError != "" && !strings.Contains(result.Error, tc.wantError) {
				t.Fatalf("stopReason %q: expected the error to explain the stop, got %q", tc.stopReason, result.Error)
			}
			// The session id survives every terminal reason so the next turn
			// continues the same conversation.
			if result.SessionID != "zc-placeholder-1" {
				t.Fatalf("stopReason %q: expected the session id to survive, got %q", tc.stopReason, result.SessionID)
			}
		})
	}
}

func TestZcodeBlockedArgs(t *testing.T) {
	t.Parallel()
	// Every entry is a zcode-acp subcommand that would replace the stdio ACP
	// bridge with something else.
	for _, flag := range []string{"acp", "server", "repl", "quota", "hub", "profile", "--help", "-h", "--version"} {
		mode, ok := zcodeBlockedArgs[flag]
		if !ok {
			t.Fatalf("expected %s to be in zcodeBlockedArgs", flag)
		}
		if mode != blockedStandalone {
			t.Fatalf("expected %s to be blockedStandalone, got %v", flag, mode)
		}
	}
	kept := filterCustomArgs([]string{"server", "--log-level", "debug", "repl"}, zcodeBlockedArgs, zcodeTestLogger())
	if strings.Join(kept, " ") != "--log-level debug" {
		t.Fatalf("expected only the passthrough flags to survive, got %v", kept)
	}
}

func TestZcodeErrorClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		message         string
		wantSessionLost bool
		wantBackendDead bool
	}{
		{message: "zcode_session_lost: gone", wantSessionLost: true},
		{message: "Session not found: zc-1", wantSessionLost: true},
		{message: "zcode_spawn_failed: ENOENT", wantBackendDead: true},
		{message: "zcode_backend_dead_after_retry: backend reader exited", wantBackendDead: true},
		{message: "rate limit exceeded"},
	} {
		// isACPSessionNotFound only recognises the wording under a code it
		// knows, so the fixture uses one the bridge actually returns.
		err := &acpRPCError{Method: "session/resume", Code: -32603, Message: tc.message}
		if got := zcodeSessionLost(err); got != tc.wantSessionLost {
			t.Fatalf("%q: zcodeSessionLost = %v, want %v", tc.message, got, tc.wantSessionLost)
		}
		if got := zcodeBackendUnavailable(err); got != tc.wantBackendDead {
			t.Fatalf("%q: zcodeBackendUnavailable = %v, want %v", tc.message, got, tc.wantBackendDead)
		}
	}

	// The marker can also arrive in `data` rather than `message`.
	inData := &acpRPCError{Method: "session/prompt", Code: -32603, Message: "internal error", Data: "zcode_session_lost: gone"}
	if !zcodeSessionLost(inData) {
		t.Fatal("expected the session-lost marker to be recognised in the error data")
	}

	// A non-RPC error carries no marker and must classify as neither, so a
	// transport failure never retires a live session.
	plain := errors.New("pipe closed")
	if zcodeSessionLost(plain) || zcodeBackendUnavailable(plain) {
		t.Fatal("a non-RPC error must classify as neither session-lost nor backend-dead")
	}

	// The backend-dead annotation must win over the session-lost one when a
	// message carries both prefixes: the session is intact.
	both := &acpRPCError{Method: "session/prompt", Code: -32603, Message: "zcode_backend_dead_after_retry: session not found while restarting"}
	if !strings.Contains(zcodeRequestErrorMessage("session/prompt", both), "could not keep its ZCode backend running") {
		t.Fatalf("expected the backend-dead annotation to win, got %q", zcodeRequestErrorMessage("session/prompt", both))
	}
}

// TestZcodeModelSelectionSupported is the counterpart to deerflow: the bridge
// registers session/set_model for exactly this client, so the family must
// offer the picker.
func TestZcodeModelSelectionSupported(t *testing.T) {
	t.Parallel()
	if !ModelSelectionSupported("zcode") {
		t.Fatal("ModelSelectionSupported(zcode) should be true — zcode-acp registers session/set_model under the snake_case spelling this client sends")
	}
}

// TestZcodeListModels covers discovery through the shared ACP probe. zcode's
// session/new carries no `models` block, so the catalog has to come from the
// `model` configOption — the fallback shape parseACPSessionNewModels drops to.
func TestZcodeListModels(t *testing.T) {
	t.Parallel()
	bin := writeFakeZcodeScript(t, fakeZcodeACPScript())

	cat, err := ListModels(context.Background(), "zcode", Command{Path: bin})
	if err != nil {
		t.Fatalf("zcode ListModels error: %v", err)
	}
	if len(cat.Models) != 2 {
		t.Fatalf("expected the two advertised models, got %+v", cat.Models)
	}
	byID := map[string]Model{}
	for _, m := range cat.Models {
		byID[m.ID] = m
	}
	current, ok := byID["glm-4.6"]
	if !ok {
		t.Fatalf("expected the built-in model id verbatim, got %+v", cat.Models)
	}
	if !current.Default {
		t.Fatalf("expected the session's currentValue to be the default model, got %+v", current)
	}
	// Third-party ids are `provider\model` — a BACKSLASH, not a colon — so the
	// shared colon-based provider split leaves them ungrouped rather than
	// inventing a provider from the wrong separator.
	third, ok := byID[`anthropic\claude-sonnet`]
	if !ok {
		t.Fatalf("expected the backslash-qualified third-party id verbatim, got %+v", cat.Models)
	}
	if third.Provider != "" {
		t.Fatalf("expected no provider inferred from a backslash-qualified id, got %q", third.Provider)
	}
}

// TestZcodeListModelsFallsBackWhenDiscoveryFails pins that an unreachable or
// broken CLI leaves manual entry available instead of erroring the settings
// screen.
func TestZcodeListModelsFallsBackWhenDiscoveryFails(t *testing.T) {
	t.Parallel()
	bin := writeFakeZcodeScript(t, "#!/bin/sh\nexit 1\n")

	cat, err := ListModels(context.Background(), "zcode", Command{Path: bin})
	if err != nil {
		t.Fatalf("zcode ListModels should not error on a broken CLI, got: %v", err)
	}
	if len(cat.Models) != 0 || !cat.Fallback {
		t.Fatalf("expected an empty fallback catalog, got %d models fallback=%v", len(cat.Models), cat.Fallback)
	}
}

// TestZcodeTimeout pins that a stalled session/new is reported as a timeout
// rather than a generic failure.
func TestZcodeTimeout(t *testing.T) {
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
      printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"zc-late"}}\n' "$id"
      ;;
    *)
      printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id"
      ;;
  esac
done
`
	bin := writeFakeZcodeScript(t, script)
	b, err := New("zcode", Config{ExecutablePath: bin, Logger: zcodeTestLogger()})
	if err != nil {
		t.Fatalf("New(zcode) error: %v", err)
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
