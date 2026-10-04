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
const deerflowHomeEnv = DeerflowHomeEnv

// DeerflowHomeEnv is the process-env var carrying the DeerFlow deployment
// root. Exported because internal/daemon injects it at startup from
// backends.deerflow.home (RUYI-283 QA P1); package-local call sites keep
// the unexported spelling above.
const DeerflowHomeEnv = "MULTICA_DEERFLOW_HOME"

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
//   - session/set_model is session-scoped and validated against the
//     bridge's own model list (unknown ids are refused, not silently
//     remapped). The backend re-sends the pick before every prompt: it
//     spawns a fresh bridge process per turn, and a session resumed into a
//     new process carries no override, so re-sending is what makes the
//     pick stick. ModelSelectionSupported and the ListModels discovery in
//     models.go expose the catalog from the session/new models block.
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
// process. The deployment root is looked up in tiers, most specific first:
//
//  1. the task env's MULTICA_DEERFLOW_HOME (deployment-level injection via
//     agent.Config.Env) — the historical spelling;
//  2. the task env's DEERFLOW_HOME — the agent custom_env spelling. The
//     MULTICA_ prefix is deliberately stripped from custom_env by
//     isBlockedEnvKey, so this non-namespaced key is the one user-reachable
//     per-agent configuration surface (RUYI-283 QA);
//  3. the DAEMON process environment's MULTICA_DEERFLOW_HOME — set by
//     backends.deerflow.home in config.json (applyDeerflowOverride) or a
//     deployment-level export. The task env map never contains the daemon's
//     process env, but this function runs inside the daemon, so the lookup
//     is direct.
//
// Anything else falls back to the task workdir with a warning that says what
// breaks, because a wrong cwd surfaces later as an opaque -32010.
func (b *deerflowBackend) resolveDeerflowProcessDir(taskCwd string) string {
	home := strings.TrimSpace(b.cfg.Env[deerflowHomeEnv])
	if home == "" {
		home = strings.TrimSpace(b.cfg.Env["DEERFLOW_HOME"])
	}
	if home == "" {
		home = strings.TrimSpace(os.Getenv(deerflowHomeEnv))
	}
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
	// The model pick is applied per turn after the session is established
	// (below): the bridge validates the id against its own list and refuses
	// unknown ones, which fails the turn visibly instead of silently running
	// it on the default model.
	//
	// The thinking switch rides the same per-turn discipline: the bridge
	// broadcasts a thinking config option (id `thinking`, category
	// `thought_level`, options on/off) on BOTH session/new and
	// session/resume and applies session/set_config_option to a per-session
	// override threaded into DeerFlowClient.stream as thinking_enabled, so
	// applyACPEffortOption below delivers opts.ThinkingLevel where this
	// backend used to drop it with a warning.

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

	// RUYI-349: session decides at Start between the legacy direct child
	// and the supervised transient unit (daemon injects Supervision for
	// whitelisted providers only).
	sess := newWorkerSession(cmd, opts.Supervision)

	stdout, err := sess.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("deerflow stdout pipe: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("deerflow stdin pipe: %w", err)
	}
	// StderrPipe + an explicit copier give us a join point (`stderrDone`) that
	// fires before the failure-promotion decision; see hermes.go for why the
	// io.MultiWriter form races with stopReason=end_turn under load.
	providerErr := newACPProviderErrorSniffer("deerflow")
	stderr, err := sess.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("deerflow stderr pipe: %w", err)
	}

	if err := sess.Start(runCtx, b.cfg.Logger); err != nil {
		cancel()
		return nil, fmt.Errorf("start deerflow-acp: %w", err)
	}

	stderrSink := io.MultiWriter(newLogWriter(b.cfg.Logger, "[deerflow:stderr] "), providerErr)
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(stderrSink, stderr)
	}()

	b.cfg.Logger.Info("deerflow-acp started", "pid", sess.PID(), "process_dir", cmd.Dir, "session_cwd", taskCwd)

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
	// RUYI-390: a reattached Execute opens the turn gate immediately —
	// the in-flight turn may deliver its turn_end on the very first
	// frames the stdout reader sees, before the lifecycle goroutine
	// would have opened it, and a dropped turn_end hangs the reattach.
	if opts.Supervision != nil && opts.Supervision.Reattach {
		streamingCurrentTurn.Store(true)
	}

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
			_ = sess.Wait(runCtx)
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

		// RUYI-390 reattach: the worker consumed its prompt under the
		// previous daemon and its turn is still running. No handshake and
		// no prompt — both would hit a mid-turn session — and the session
		// id rebuilds from the session/update stream. The shared result
		// path below picks the turn tail up from promptDone as usual.
		var resumeRejectedTransient bool
		var promptErr error
		if sess.Reattaching() {
			streamingCurrentTurn.Store(true)
			c.seedReattachIDSpace()
			b.cfg.Logger.Info("deerflow reattach: riding the in-flight turn launched by the previous daemon", "pid", sess.PID())
			promptErr = c.waitReattachedTurn(runCtx, "deerflow")
			sessionID = c.observedSessionID()
		} else {
			// Set when the session stays healthy but cannot take a turn now.

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

			// sessionResult is whichever of session/new or session/resume
			// produced this turn's session — the response the thinking switch is
			// read from.
			var sessionResult json.RawMessage
			if opts.ResumeSessionID != "" {
				// session/resume, not session/load. Both restore the DeerFlow
				// thread, but load also replays the retained transcript back as
				// session/update notifications, so a resumed turn would re-emit
				// the previous answer as its own output.
				//
				// The param set mirrors session/new: the bridge binds `cwd` to the
				// restored session (resume_session(cwd, session_id, mcp_servers)),
				// so omitting it fails parameter binding before the session is
				// even looked up. mcpServers stays an empty array for the same
				// reason it does on session/new — a non-empty list is rejected
				// with -32602 rather than ignored.
				result, err := c.request(runCtx, "session/resume", map[string]any{
					"sessionId":  opts.ResumeSessionID,
					"cwd":        taskCwd,
					"mcpServers": []any{},
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
				sessionResult = result
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
				sessionResult = result
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

			// Apply the model pick on BOTH fresh and resumed sessions: this
			// backend spawns a fresh bridge process per turn and a session
			// resumed into it carries no override, so re-sending before every
			// prompt is what makes the pick stick. The bridge validates the id
			// and refuses unknown ones; the turn then fails visibly while the
			// session stays healthy, so the resume pointer is preserved.
			if opts.Model != "" {
				if _, err := c.request(runCtx, "session/set_model", map[string]any{
					"sessionId": sessionID,
					"modelId":   opts.Model,
				}); err != nil {
					b.cfg.Logger.Warn("deerflow set_model failed; failing the turn instead of running on the default model",
						"backend", "deerflow",
						"session_id", sessionID,
						"requested_model", opts.Model,
					)
					resCh <- Result{
						Status:         "failed",
						Error:          deerflowRequestErrorMessage("session/set_model", err),
						DurationMs:     time.Since(startTime).Milliseconds(),
						SessionID:      sessionID,
						ResumeRejected: resumeRejected,
					}
					return
				}
				b.cfg.Logger.Info("deerflow session model set", "session_id", sessionID, "model", opts.Model)
			}

			// Apply the persisted thinking switch on BOTH fresh and resumed
			// sessions — the same per-turn replay the model pick above follows,
			// for the same reason: this backend spawns a fresh bridge process per
			// turn, so a resumed session carries no override. The option id and
			// on/off vocabulary are read off sessionResult, and stateIsCurrent
			// stays true because the bridge's thinking option does not depend on
			// the session's model (the engine switch is global), so the set_model
			// above cannot stale it. A configuration failure never blocks the
			// task; see the helper for what the warnings mean.
			applyACPEffortOption(runCtx, c.request, "deerflow", b.cfg.Logger, sessionID, sessionResult, opts.ThinkingLevel, true)

			userText := prompt
			if opts.SystemPrompt != "" {
				userText = opts.SystemPrompt + "\n\n---\n\n" + prompt
			}

			streamingCurrentTurn.Store(true)
			_, promptErr = c.request(runCtx, "session/prompt", map[string]any{
				"sessionId": sessionID,
				"prompt": []map[string]any{
					{"type": "text", "text": userText},
				},
			})
		}
		if promptErr != nil {
			switch {
			case runCtx.Err() == context.DeadlineExceeded:
				finalStatus = "timeout"
				finalError = fmt.Sprintf("deerflow timed out after %s", timeout)
			case runCtx.Err() == context.Canceled:
				finalStatus = "aborted"
				finalError = "execution cancelled"
			default:
				finalStatus = "failed"
				finalError = deerflowRequestErrorMessage("session/prompt", promptErr)
				if opts.ResumeSessionID != "" {
					// A resumed session can die between resume and prompt: the
					// bridge echoes the id back on resume, so a quarantine or
					// an eviction only surfaces here.
					if deerflowSessionPermanentlyLost(promptErr) {
						b.cfg.Logger.Warn("deerflow refused the resumed session at prompt time; clearing session id so the daemon retries fresh",
							"backend", "deerflow",
							"session_id", sessionID,
						)
						sessionID = ""
						resumeRejected = true
					} else if deerflowSessionTemporarilyBusy(promptErr) {
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
		b.cfg.Logger.Info("deerflow finished", "pid", sess.PID(), "status", finalStatus, "duration", duration.Round(time.Millisecond).String())

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
		// model id in the response either, so attribute under the applied
		// pick (opts.Model) when one was sent, the configured process default
		// next, and "unknown" otherwise rather than inventing an id.
		var usageMap map[string]TokenUsage
		if acpUsagePresent(u) {
			model := strings.TrimSpace(opts.Model)
			if model == "" {
				model = strings.TrimSpace(b.cfg.Env["DEERFLOW_ACP_MODEL"])
			}
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
