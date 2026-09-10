package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// deerflowBlockedArgs are flags hardcoded by the daemon that must not be
// overridden by user-configured custom_args. `acp` is the protocol subcommand
// that drives the ACP JSON-RPC transport; overriding it would break the
// daemon↔bridge contract. `doctor` is the bridge's own readiness probe — it
// prints a report and exits without ever starting the ACP server.
var deerflowBlockedArgs = map[string]blockedArgMode{
	"acp":       blockedStandalone,
	"doctor":    blockedStandalone,
	"--help":    blockedStandalone,
	"-h":        blockedStandalone,
	"--version": blockedStandalone,
}

// deerflowHomeEnv names the directory the bridge process must be started in.
//
// DeerFlow locates its own config.yaml relative to the process working
// directory: DeerFlowClient(config_path=…) does not move that lookup root, so
// a bridge launched anywhere else constructs a client against a missing
// configuration and every turn fails at backend construction (-32010). The
// daemon's per-task workdir is therefore the wrong cwd for the process, while
// still being the right value for the session's `cwd` param — the two are
// separate concerns and this backend keeps them separate.
//
// Reading one env key is deliberately all this does. The bridge owns its own
// DEERFLOW_ACP_* configuration surface and the daemon passes the runtime's
// env through untouched; re-deriving or overriding that surface here would
// rebuild the env layer the bridge already owns.
const deerflowHomeEnv = "MULTICA_DEERFLOW_HOME"

// DeerFlow's bridge answers unknown sessions and its own refusals with
// private codes. They are all -320xx, which the shared ACP helpers do not
// recognise, so each one is classified here rather than through
// isACPSessionNotFound.
const (
	// deerflowCodeUnknownSession: the sessionId exists in neither this bridge
	// process nor the DeerFlow checkpointer. Only a fresh session can cure it.
	deerflowCodeUnknownSession = -32001
	// deerflowCodeBackendUnavailable: DeerFlowClient construction or a
	// checkpointer read failed. The session is not implicated — a fresh one
	// would fail identically — so the resume pointer must be preserved.
	deerflowCodeBackendUnavailable = -32010
	// deerflowCodeTurnInProgress: another turn already runs on this session.
	// The session stays healthy, so this is a transient resume refusal.
	deerflowCodeTurnInProgress = -32011
	// deerflowCodeSessionQuarantined: the previous turn's worker process group
	// could not be confirmed reaped, so the bridge permanently refuses to open
	// another turn on that thread. Irreversible by design: only session/new
	// moves forward.
	deerflowCodeSessionQuarantined = -32012
)

var (
	deerflowReaderDrainGrace      = 2 * time.Second
	deerflowNotificationQuietTime = 250 * time.Millisecond
)

// deerflowRPCCode returns the JSON-RPC error code carried by err, or 0 when
// err is not an ACP RPC error.
func deerflowRPCCode(err error) int {
	var rpcErr *acpRPCError
	if !errors.As(err, &rpcErr) {
		return 0
	}
	return rpcErr.Code
}

// deerflowSessionPermanentlyLost reports whether err means the requested
// session can never be resumed again, so the daemon should retire the pointer
// and retry from a fresh session.
func deerflowSessionPermanentlyLost(err error) bool {
	switch deerflowRPCCode(err) {
	case deerflowCodeUnknownSession, deerflowCodeSessionQuarantined:
		return true
	default:
		return false
	}
}

// deerflowSessionTemporarilyBusy reports whether err means the session is
// healthy but cannot accept a turn right now. The daemon may run this turn on
// a fresh session without retiring the requested one.
func deerflowSessionTemporarilyBusy(err error) bool {
	return deerflowRPCCode(err) == deerflowCodeTurnInProgress
}

// deerflowRequestErrorMessage turns the bridge's private codes into something
// an operator can act on. The bridge redacts its own payloads — -32603 carries
// only {sessionId, errorType} — so the text added here is the only place the
// meaning of a code is stated.
func deerflowRequestErrorMessage(method string, err error) string {
	base := fmt.Sprintf("deerflow %s failed: %v", method, err)
	switch deerflowRPCCode(err) {
	case deerflowCodeBackendUnavailable:
		return base + " — the bridge could not reach DeerFlow." +
			" Check that the DeerFlow deployment is running and that " + deerflowHomeEnv +
			" points at the deployment root holding its config.yaml."
	case deerflowCodeSessionQuarantined:
		return base + " — the bridge quarantined this session because a previous turn's" +
			" worker process group could not be confirmed reaped. The quarantine is" +
			" irreversible; this task continues on a fresh session."
	case deerflowCodeTurnInProgress:
		return base + " — another turn is still running on this session."
	default:
		return base
	}
}

// deerflowLoadSessionSupported reads the loadSession capability the bridge
// advertises from initialize. It is the bridge's own statement that
// session/resume is routed, which requires it to have been started with
// use_unstable_protocol: without that the resume RPC answers -32601 and a
// resumed task would fail on a healthy session.
func deerflowLoadSessionSupported(result json.RawMessage) bool {
	var r struct {
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	return json.Unmarshal(result, &r) == nil && r.AgentCapabilities.LoadSession
}

// deerflowBackend implements Backend by spawning `deerflow-acp acp` and
// speaking standard ACP JSON-RPC 2.0 over stdin/stdout through the shared
// hermesClient transport.
//
// DeerFlow is a multi-agent research runtime; deerflow-acp is a read-only
// bridge in front of it (see RUYI-118). This is its own protocol family
// rather than a runtime profile on top of another one, because its dispatch
// table departs from every existing family in ways a shell would have to
// paper over:
//
//   - session/set_model answers -32601. The model is fixed for the process
//     lifetime by DEERFLOW_ACP_MODEL, so ModelSelectionSupported opts the
//     family out and this backend never sends the RPC. A kimi-shaped shell
//     would send it and hard-fail every model-pinned task.
//   - session/set_config_option answers -32601, so no thinking level is
//     negotiated here. DEERFLOW_ACP_THINKING owns that switch.
//   - A non-empty mcpServers list answers -32602 rather than being ignored.
//     providerSupportsMcpConfig excludes the family and this backend always
//     sends an empty list.
//   - Resume goes through session/resume, which restores the thread WITHOUT
//     replaying history. session/load exists and does replay, which would
//     re-emit prior turns as this turn's output.
//   - Unknown sessions, backend outages, concurrent turns and quarantined
//     threads each get their own private code (-32001 / -32010 / -32011 /
//     -32012). They differ in whether the resume pointer survives, so they
//     are classified rather than collapsed into one failure.
//   - The process must start in the DeerFlow deployment root; see
//     deerflowHomeEnv.
type deerflowBackend struct {
	cfg Config
}

// deerflowMessageStream serializes sends and the final close so a late stdout
// reader cannot send on a closed channel. Mirrors zeroclaw/dim/grok/traecli.
type deerflowMessageStream struct {
	ch     chan Message
	mu     sync.Mutex
	closed bool
}

func newDeerflowMessageStream(size int) *deerflowMessageStream {
	return &deerflowMessageStream{ch: make(chan Message, size)}
}

func (s *deerflowMessageStream) send(msg Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	trySend(s.ch, msg)
}

func (s *deerflowMessageStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

// resolveDeerflowProcessDir picks the working directory for the bridge
// process. A configured deerflowHomeEnv wins when it names a real directory;
// anything else falls back to the task workdir with a warning that says what
// breaks, because a wrong cwd surfaces later as an opaque -32010.
func (b *deerflowBackend) resolveDeerflowProcessDir(taskCwd string) string {
	home := strings.TrimSpace(b.cfg.Env[deerflowHomeEnv])
	if home == "" {
		b.cfg.Logger.Warn("deerflow: "+deerflowHomeEnv+" is unset; starting the bridge in the task workdir",
			"backend", "deerflow",
			"process_dir", taskCwd,
			"impact", "DeerFlow resolves config.yaml relative to the process working directory; turns fail with backend-unavailable when it is not the deployment root",
		)
		return taskCwd
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		b.cfg.Logger.Warn("deerflow: "+deerflowHomeEnv+" does not name a directory; starting the bridge in the task workdir",
			"backend", "deerflow",
			"configured_dir", home,
			"process_dir", taskCwd,
		)
		return taskCwd
	}
	return home
}

func (b *deerflowBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "deerflow-acp"
	}
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("deerflow-acp executable not found at %q: %w", execPath, err)
	}

	// The bridge rejects a non-empty mcpServers list with -32602 instead of
	// ignoring it: it proxies no MCP at all, and DeerFlow's own tool chain is
	// configured on the DeerFlow side. providerSupportsMcpConfig hides the MCP
	// tab for this family, so reaching here means a value saved before that —
	// warn and send an empty list rather than bricking the task on config we
	// cannot honour.
	if len(opts.McpConfig) > 0 {
		b.cfg.Logger.Warn("deerflow ignores MCP servers supplied by Multica; the bridge proxies no MCP and DeerFlow configures its own tools",
			"backend", "deerflow",
		)
	}
	// Same shape for the model: the bridge answers session/set_model with
	// -32601 because the model is pinned at process start by
	// DEERFLOW_ACP_MODEL. Sending it would fail the task; silently dropping it
	// without a record would let the UI claim a pick that never applied.
	if opts.Model != "" {
		b.cfg.Logger.Warn("deerflow cannot switch models per session; the model is fixed at bridge startup by DEERFLOW_ACP_MODEL",
			"backend", "deerflow",
			"requested_model", opts.Model,
		)
	}
	if opts.ThinkingLevel != "" {
		b.cfg.Logger.Warn("deerflow cannot set a thinking level per session; DEERFLOW_ACP_THINKING owns that switch",
			"backend", "deerflow",
			"requested_level", opts.ThinkingLevel,
		)
	}

	timeout := opts.Timeout
	runCtx, cancel := runContext(ctx, timeout)

	deerflowArgs := append([]string{"acp"}, filterCustomArgs(opts.CustomArgs, deerflowBlockedArgs, b.cfg.Logger)...)

	cmd := b.cfg.commandAt(execPath).exec(runCtx, deerflowArgs...)
	hideAgentWindow(cmd)
	b.cfg.logAgentCommand(cmd, newAgentCommandLogArgs(deerflowArgs, trustAgentCommandPositional(0, "acp")))
	taskCwd := opts.Cwd
	if taskCwd == "" {
		taskCwd = "."
	}
	cmd.Dir = b.resolveDeerflowProcessDir(taskCwd)
	cmd.Env = buildEnv(b.cfg.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("deerflow stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("deerflow stdin pipe: %w", err)
	}
	// StderrPipe + an explicit copier give us a join point (`stderrDone`) that
	// fires before the failure-promotion decision; see hermes.go for why the
	// io.MultiWriter form races with stopReason=end_turn under load.
	providerErr := newACPProviderErrorSniffer("deerflow")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("deerflow stderr pipe: %w", err)
	}

	if err := startOwnedProcessTree(cmd, b.cfg.Logger); err != nil {
		cancel()
		return nil, fmt.Errorf("start deerflow-acp: %w", err)
	}

	stderrSink := io.MultiWriter(newLogWriter(b.cfg.Logger, "[deerflow:stderr] "), providerErr)
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(stderrSink, stderr)
	}()

	b.cfg.Logger.Info("deerflow-acp started", "pid", cmd.Process.Pid, "process_dir", cmd.Dir, "session_cwd", taskCwd)

	msgStream := newDeerflowMessageStream(256)
	resCh := make(chan Result, 1)

	// The bridge streams narration and the final answer as the same
	// agent_message_chunk type; the tracker keeps only the post-tool-call
	// block for Result.Output while retaining the full text for error
	// detection.
	var deliverable acpDeliverableTracker
	// streamingCurrentTurn gates every session update so anything pushed
	// outside our turn is dropped instead of landing in the deliverable. It
	// must default to false — session/load replays a transcript, and even on
	// the resume path the bridge may flush frames before answering the
	// request that triggered them.
	var streamingCurrentTurn atomic.Bool

	promptDone := make(chan hermesPromptResult, 1)
	activity := make(chan struct{}, 1)

	c := &hermesClient{
		cfg:          b.cfg,
		stdin:        stdin,
		pending:      make(map[int]*pendingRPC),
		pendingTools: make(map[string]*pendingToolCall),
		acceptNotification: func(string) bool {
			return streamingCurrentTurn.Load()
		},
		onActivity: func() {
			select {
			case activity <- struct{}{}:
			default:
			}
		},
		onMessage: func(msg Message) {
			if !streamingCurrentTurn.Load() {
				return
			}
			if msg.Type == MessageToolUse {
				// Re-normalise tool titles the same way kimi/traecli/grok/dim
				// do so the UI sees consistent snake_case names.
				msg.Tool = kimiToolNameFromTitle(msg.Tool)
			}
			deliverable.observe(msg)
			msgStream.send(msg)
		},
		onPromptDone: func(result hermesPromptResult) {
			if !streamingCurrentTurn.Load() {
				return
			}
			select {
			case promptDone <- result:
			default:
			}
		},
	}

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := newAgentStreamScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			c.handleLine(line)
		}
		c.closeAllPending(fmt.Errorf("deerflow-acp process exited"))
	}()

	go func() {
		defer cancel()
		defer msgStream.close()
		defer close(resCh)
		defer func() {
			stdin.Close()
			_ = cmd.Wait()
			releaseProcessGroup(cmd)
		}()

		startTime := time.Now()
		finalStatus := "completed"
		var finalError string
		var sessionID string
		// Set when the bridge refuses the requested session permanently. Only
		// that is curable by starting over, so backend outages and concurrent
		// turns below must leave it false.
		var resumeRejected bool
		// Set when the session stays healthy but cannot take a turn now.
		var resumeRejectedTransient bool

		// No terminal and no elicitation capability: the bridge reports no
		// permission requests (DeerFlow has no approval loop) and this
		// headless client cannot answer a form.
		initResult, err := c.request(runCtx, "initialize", map[string]any{
			"protocolVersion": 1,
			"clientInfo": map[string]any{
				"name":    "multica-agent-sdk",
				"version": "0.2.0",
			},
			"clientCapabilities": map[string]any{},
		})
		if err != nil {
			finalStatus = "failed"
			finalError = fmt.Sprintf("deerflow initialize failed: %v", err)
			resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
			return
		}

		if opts.ResumeSessionID != "" && !deerflowLoadSessionSupported(initResult) {
			// The resume RPC is registered as unstable in the bridge's ACP
			// router, so a bridge started without use_unstable_protocol
			// answers -32601 on a perfectly healthy session. Detect that from
			// the advertised capability instead of failing the turn on an
			// opaque method-not-found.
			b.cfg.Logger.Warn("deerflow did not advertise loadSession; the bridge is running without unstable protocol support and cannot resume",
				"backend", "deerflow",
				"requested_session", opts.ResumeSessionID,
			)
			resumeRejected = true
			resCh <- Result{
				Status:         "failed",
				Error:          "deerflow session/resume unavailable: initialize did not advertise agentCapabilities.loadSession (start the bridge with unstable protocol support)",
				DurationMs:     time.Since(startTime).Milliseconds(),
				ResumeRejected: resumeRejected,
			}
			return
		}

		if opts.ResumeSessionID != "" {
			// session/resume, not session/load. Both restore the DeerFlow
			// thread, but load also replays the retained transcript back as
			// session/update notifications, so a resumed turn would re-emit
			// the previous answer as its own output.
			result, err := c.request(runCtx, "session/resume", map[string]any{
				"sessionId": opts.ResumeSessionID,
			})
			if err != nil {
				resumeRejected = deerflowSessionPermanentlyLost(err)
				resumeRejectedTransient = deerflowSessionTemporarilyBusy(err)
				if resumeRejected || resumeRejectedTransient {
					b.cfg.Logger.Warn("deerflow refused the resumed session; the daemon will retry on a fresh session",
						"backend", "deerflow",
						"requested_session", opts.ResumeSessionID,
						"permanent", resumeRejected,
					)
				}
				resCh <- Result{
					Status:                  "failed",
					Error:                   deerflowRequestErrorMessage("session/resume", err),
					DurationMs:              time.Since(startTime).Milliseconds(),
					ResumeRejected:          resumeRejected,
					ResumeRejectedTransient: resumeRejectedTransient,
				}
				return
			}
			var changed bool
			sessionID, changed = resolveResumedSessionID(opts.ResumeSessionID, result)
			if changed {
				b.cfg.Logger.Warn("deerflow returned a different session id on resume — original was likely lost; continuing with the new id",
					"backend", "deerflow",
					"requested", opts.ResumeSessionID,
					"actual", sessionID,
				)
			}
		} else {
			// mcpServers stays empty on purpose: a non-empty list is rejected
			// with -32602 rather than ignored.
			result, err := c.request(runCtx, "session/new", map[string]any{
				"cwd":        taskCwd,
				"mcpServers": []any{},
			})
			if err != nil {
				switch {
				case runCtx.Err() == context.DeadlineExceeded:
					finalStatus = "timeout"
					finalError = fmt.Sprintf("deerflow timed out during session/new: %v", timeout)
				case runCtx.Err() == context.Canceled:
					finalStatus = "aborted"
					finalError = fmt.Sprintf("deerflow aborted: %v", err)
				default:
					finalStatus = "failed"
					finalError = deerflowRequestErrorMessage("session/new", err)
				}
				resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
				return
			}
			sessionID = extractACPSessionID(result)
			if sessionID == "" {
				finalStatus = "failed"
				finalError = "deerflow session/new returned no session ID"
				resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
				return
			}
		}

		c.sessionID = sessionID
		// Early session pin so a cancelled run still preserves the resume
		// pointer.
		msgStream.send(Message{Type: MessageStatus, Status: "running", SessionID: sessionID})
		b.cfg.Logger.Info("deerflow session ready", "session_id", sessionID)

		userText := prompt
		if opts.SystemPrompt != "" {
			userText = opts.SystemPrompt + "\n\n---\n\n" + prompt
		}

		streamingCurrentTurn.Store(true)
		_, err = c.request(runCtx, "session/prompt", map[string]any{
			"sessionId": sessionID,
			"prompt": []map[string]any{
				{"type": "text", "text": userText},
			},
		})
		if err != nil {
			switch {
			case runCtx.Err() == context.DeadlineExceeded:
				finalStatus = "timeout"
				finalError = fmt.Sprintf("deerflow timed out after %s", timeout)
			case runCtx.Err() == context.Canceled:
				finalStatus = "aborted"
				finalError = "execution cancelled"
			default:
				finalStatus = "failed"
				finalError = deerflowRequestErrorMessage("session/prompt", err)
				if opts.ResumeSessionID != "" {
					// A resumed session can die between resume and prompt: the
					// bridge echoes the id back on resume, so a quarantine or
					// an eviction only surfaces here.
					if deerflowSessionPermanentlyLost(err) {
						b.cfg.Logger.Warn("deerflow refused the resumed session at prompt time; clearing session id so the daemon retries fresh",
							"backend", "deerflow",
							"session_id", sessionID,
						)
						sessionID = ""
						resumeRejected = true
					} else if deerflowSessionTemporarilyBusy(err) {
						// Keep sessionID: the session is healthy and must stay
						// reachable for the next turn.
						resumeRejectedTransient = true
					}
				}
			}
		} else {
			select {
			case pr := <-promptDone:
				switch pr.stopReason {
				case "cancelled":
					finalStatus = "aborted"
					finalError = "deerflow cancelled the prompt"
				case "refusal":
					// The bridge maps a backend stream() exception to refusal
					// and redacts the detail, so this is the only statement of
					// what happened. A refusal is a failed turn, not a clean
					// empty answer.
					finalStatus = "failed"
					finalError = "deerflow refused the turn: the DeerFlow backend raised while streaming (see the bridge log for the redacted error type)"
				}
				c.mergeUsage(pr.usage)
			default:
			}
			// Give the stdout reader a bounded chance to consume notifications
			// that land just after session/prompt returns. Closing stdin at
			// the response boundary otherwise races the reader and truncates
			// the final text — the same race fixed for hermes/dim/grok.
			waitForACPNotificationQuiescence(runCtx, activity, readerDone, deerflowNotificationQuietTime, deerflowReaderDrainGrace)
		}

		duration := time.Since(startTime)
		b.cfg.Logger.Info("deerflow finished", "pid", cmd.Process.Pid, "status", finalStatus, "duration", duration.Round(time.Millisecond).String())

		stdin.Close()
		cancel()

		// The bridge reaps its worker process group before exiting, so the
		// pipes can stay open briefly after session/prompt returns. Bound the
		// drain.
		drainCtx, drainCancel := context.WithTimeout(context.Background(), deerflowReaderDrainGrace)
		select {
		case <-readerDone:
		case <-drainCtx.Done():
		}
		select {
		case <-stderrDone:
		case <-drainCtx.Done():
		}
		drainCancel()
		providerErr.Finalize()
		streamingCurrentTurn.Store(false)

		finalOutput, providerErrorOutput := deliverable.result()

		// Promote completed→failed when stderr or the agent text stream show a
		// terminal upstream-LLM failure (auth / rate-limit / HTTP 4xx). It
		// reads the full text stream, not the deliverable, so a give-up turn
		// that lands before a tool call stays visible.
		finalStatus, finalError = promoteACPResultOnProviderError(finalStatus, finalError, providerErrorOutput, providerErr)

		u := c.accumulatedUsage()

		// The bridge reports token counts on PromptResponse.usage and does not
		// emit usage_update by default (its size/used fields express context
		// occupancy, which DeerFlow does not provide). There is no per-turn
		// model id in the response either — the model is process-global — so
		// attribute under the configured model when one is known and "unknown"
		// otherwise rather than inventing an id.
		var usageMap map[string]TokenUsage
		if acpUsagePresent(u) {
			model := strings.TrimSpace(b.cfg.Env["DEERFLOW_ACP_MODEL"])
			if model == "" {
				model = "unknown"
			}
			usageMap = map[string]TokenUsage{model: u}
		}

		resCh <- Result{
			Status:                  finalStatus,
			Output:                  finalOutput,
			Error:                   finalError,
			DurationMs:              duration.Milliseconds(),
			SessionID:               sessionID,
			ResumeRejected:          resumeRejected,
			ResumeRejectedTransient: resumeRejectedTransient,
			Usage:                   usageMap,
		}
	}()

	return &Session{Messages: msgStream.ch, Result: resCh}, nil
}
