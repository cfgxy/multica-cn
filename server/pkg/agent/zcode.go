package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// zcodeBlockedArgs are flags hardcoded by the daemon that must not be
// overridden by user-configured custom_args. Every entry here is a zcode-acp
// CLI subcommand that would replace the stdio ACP bridge with something else:
// `acp`/`server` is the bridge itself (the daemon appends it), `repl` needs a
// TTY, `quota` prints a usage card and exits, `hub` runs the remote-access
// daemon, and `profile` captures a desktop profile.
var zcodeBlockedArgs = map[string]blockedArgMode{
	"acp":       blockedStandalone,
	"server":    blockedStandalone,
	"repl":      blockedStandalone,
	"quota":     blockedStandalone,
	"hub":       blockedStandalone,
	"profile":   blockedStandalone,
	"--help":    blockedStandalone,
	"-h":        blockedStandalone,
	"--version": blockedStandalone,
}

// zcodeReaderDrainGrace bounds how long the turn waits for trailing ACP
// notifications after the session/prompt response. A var, not a const, so
// tests can shorten it.
var zcodeReaderDrainGrace = 2 * time.Second

// zcodeSessionLostMarker is the classification prefix zcode-acp puts in front
// of an error whose root cause is a session file that no longer exists
// (backend/supervise.ts ERR_SESSION_LOST). The bridge raises it as a plain
// Error, so it arrives as a JSON-RPC internal error whose *message* carries
// the marker — the code alone cannot distinguish it from a transient backend
// fault.
const zcodeSessionLostMarker = "zcode_session_lost"

// zcodeBackendDeadMarkers are the bridge's classification prefixes for
// infrastructure failures that leave the session itself intact: the zcode
// binary would not start, or supervised restarts were exhausted. These must
// NOT retire the resume pointer — a fresh session would fail identically, and
// dropping the pointer would lose the conversation to an outage.
var zcodeBackendDeadMarkers = []string{
	"zcode_spawn_failed",
	"zcode_backend_dead_after_retry",
}

// zcodeUnknownSessionPattern matches the bridge's own unknown-session wording,
// `session <id> not found` (src/handlers/session.ts). The shared
// isACPSessionNotFound matcher cannot see it: every runtime it was written for
// puts the id after the phrase, so it looks for the contiguous "session not
// found", and zcode's id sits in the middle. Without this the non-supervised
// path — a resume against an id the bridge has no mapping for — would keep the
// pointer and loop every later dispatch on the dead session.
var zcodeUnknownSessionPattern = regexp.MustCompile(`session\s+\S+\s+not found`)

// zcodeSessionLost reports whether err means the requested session is gone on
// the zcode side, so the daemon should retire the pointer and retry fresh.
//
// Three shapes are matched. The bridge's own supervised paths prefix the
// message with zcodeSessionLostMarker; its non-supervised paths surface the raw
// unknown-session wording, either in the contiguous form isACPSessionNotFound
// already recognises or in zcode's own id-infixed form.
func zcodeSessionLost(err error) bool {
	if isACPSessionNotFound(err) {
		return true
	}
	var rpcErr *acpRPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	text := strings.ToLower(rpcErr.Message + " " + rpcErr.Data)
	return strings.Contains(text, zcodeSessionLostMarker) ||
		zcodeUnknownSessionPattern.MatchString(text)
}

// zcodeBackendUnavailable reports whether err is the bridge's own
// infrastructure failure rather than a statement about the session.
func zcodeBackendUnavailable(err error) bool {
	var rpcErr *acpRPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	text := strings.ToLower(rpcErr.Message + " " + rpcErr.Data)
	for _, marker := range zcodeBackendDeadMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// zcodeRequestErrorMessage annotates the bridge's classification markers so an
// operator can tell an outage apart from a lost session without reading the
// bridge log.
func zcodeRequestErrorMessage(method string, err error) string {
	base := fmt.Sprintf("zcode %s failed: %v", method, err)
	switch {
	case zcodeBackendUnavailable(err):
		return base + " — the zcode-acp bridge could not keep its ZCode backend running." +
			" Check the ZCode installation and credentials; the session itself is intact."
	case zcodeSessionLost(err):
		return base + " — zcode no longer has this session; the task continues on a fresh one."
	default:
		return base
	}
}

// zcodeBackend implements Backend by spawning `zcode-acp acp` and speaking
// standard ACP JSON-RPC 2.0 over stdin/stdout through the shared hermesClient
// transport.
//
// This is its own protocol family, not a runtime profile over `kimi`. The two
// were previously conflated, which made every zcode task report itself as Kimi
// in the UI, the daemon log and the runtime metrics while also inheriting kimi
// behaviour zcode does not share:
//
//   - Usage. Kimi exports no token counters over ACP, so kimi.go falls back to
//     scanning kimi's own on-disk wire log under KIMI_CODE_HOME. zcode-acp does
//     report counters, on the prompt result's top-level `usage` object
//     (attachTurnUsage, added for exactly this client). Running the kimi
//     fallback against a zcode session scans a directory that does not exist
//     and bills the turn as zero tokens.
//   - Reasoning effort. Kimi addresses it as config id `thinking`; zcode-acp
//     advertises id `thought` (category `thought_level`) with a per-model
//     vocabulary read from the enabled provider's own config. Sending
//     `thinking` gets nothing set. This backend therefore uses the shared
//     applyACPEffortOption, which reads the id and the advertised levels off
//     the session instead of hard-coding either.
//   - Terminal. Kimi drives the client's ACP terminal (terminal/create and
//     friends); zcode-acp never issues those requests — it streams Bash output
//     as `terminal_output` only when the client advertises
//     `_meta.terminal_output`, which this headless client does not. Advertising
//     the capability would promise a surface nothing uses.
//   - Session ids. `session/new` returns a lazy placeholder and defers zcode's
//     own session/create to first use, so the id is real to the bridge but not
//     yet backed by a zcode session.
//
// What it does share with kimi is the plain ACP transport, MCP forwarding
// (session/new and session/resume both pass mcpServers through to the backend)
// and per-session model switching through session/set_model, which the bridge
// registers as a snake_case alias for exactly this client.
type zcodeBackend struct {
	cfg Config
}

// zcodeMessageStream serializes sends and the final close so a late stdout
// reader cannot send on a closed channel.
type zcodeMessageStream struct {
	ch     chan Message
	mu     sync.Mutex
	closed bool
}

func newZcodeMessageStream(size int) *zcodeMessageStream {
	return &zcodeMessageStream{ch: make(chan Message, size)}
}

func (s *zcodeMessageStream) send(msg Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	trySend(s.ch, msg)
}

func (s *zcodeMessageStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

func (b *zcodeBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "zcode-acp"
	}
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("zcode-acp executable not found at %q: %w", execPath, err)
	}

	// Translate the agent's mcp_config (Claude-style object of objects) into
	// the array shape ACP session/new expects. Fail closed on malformed JSON so
	// the launch surfaces the real error instead of silently dropping every MCP
	// server.
	mcpServers, err := buildACPMcpServers(opts.McpConfig, b.cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("zcode: invalid mcp_config: %w", err)
	}

	timeout := opts.Timeout
	runCtx, cancel := runContext(ctx, timeout)

	zcodeArgs := append([]string{"acp"}, filterCustomArgs(opts.CustomArgs, zcodeBlockedArgs, b.cfg.Logger)...)
	cmd := b.cfg.commandAt(execPath).exec(runCtx, zcodeArgs...)
	hideAgentWindow(cmd)
	b.cfg.logAgentCommand(cmd, newAgentCommandLogArgs(zcodeArgs, trustAgentCommandPositional(0, "acp")))
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(b.cfg.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("zcode stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("zcode stdin pipe: %w", err)
	}
	// StderrPipe + an explicit copier give us a join point (`stderrDone`) that
	// fires before the failure-promotion decision; see hermes.go for why the
	// io.MultiWriter form races with stopReason=end_turn under load.
	providerErr := newACPProviderErrorSniffer("zcode")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("zcode stderr pipe: %w", err)
	}

	if err := startOwnedProcessTree(cmd, b.cfg.Logger); err != nil {
		cancel()
		return nil, fmt.Errorf("start zcode-acp: %w", err)
	}

	stderrSink := io.MultiWriter(newLogWriter(b.cfg.Logger, "[zcode:stderr] "), providerErr)
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(stderrSink, stderr)
	}()

	b.cfg.Logger.Info("zcode-acp started", "pid", cmd.Process.Pid, "cwd", opts.Cwd)

	msgStream := newZcodeMessageStream(256)
	resCh := make(chan Result, 1)

	// zcode streams interim narration and the final answer as the same
	// agent_message_chunk type; the tracker keeps only the post-tool-call block
	// for Result.Output while retaining the full text for error detection.
	var deliverable acpDeliverableTracker
	// streamingCurrentTurn gates every session update so anything pushed
	// outside our turn is dropped instead of landing in the deliverable. It
	// must default to false: the bridge emits an initial usage_update on
	// resume, and may flush queued frames before our session/prompt response
	// streams.
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
				// The bridge titles tool calls after ZCode's own tool names
				// ("Bash", "Read", "Edit"); normalise them to the snake_case
				// identifiers the UI expects, the same way the other ACP
				// backends do.
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
		c.closeAllPending(fmt.Errorf("zcode-acp process exited"))
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
		// Set when zcode refuses the session we asked to resume. Only that is
		// curable by starting fresh, so a bridge outage below must leave it
		// false.
		var resumeRejected bool

		// No terminal capability: the bridge never issues terminal/* requests
		// (see the type comment).
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
			finalError = fmt.Sprintf("zcode initialize failed: %v", err)
			resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
			return
		}

		// Drop MCP entries whose remote transport the bridge didn't advertise.
		// zcode-acp reports mcpCapabilities {http:false, sse:false} — it
		// forwards stdio entries to the ZCode backend and proxies no remote
		// transport — so an http/sse entry has to be dropped here rather than
		// tanking session/new.
		mcpServers = filterACPMcpServersByCapability(mcpServers, extractACPMcpCapabilities(initResult), "zcode", b.cfg)

		cwd := opts.Cwd
		if cwd == "" {
			cwd = "."
		}

		// sessionResult is the response the effort selector is read from
		// below; both branches produce one.
		var sessionResult json.RawMessage

		if opts.ResumeSessionID != "" {
			// session/resume, not session/load. Both restore the thread, but
			// load also replays the stored transcript back as session/update
			// notifications, so a resumed turn would re-emit the previous
			// answer as its own output.
			//
			// mcpServers rides along: the bridge forwards them on resume too,
			// and without it a resumed task loses the MCP tools a fresh task
			// on the same agent would have.
			result, err := c.request(runCtx, "session/resume", map[string]any{
				"cwd":        cwd,
				"sessionId":  opts.ResumeSessionID,
				"mcpServers": mcpServers,
			})
			if err != nil {
				resumeRejected = zcodeSessionLost(err)
				if resumeRejected {
					b.cfg.Logger.Warn("zcode no longer has the resumed session; the daemon will retry on a fresh session",
						"backend", "zcode",
						"requested_session", opts.ResumeSessionID,
					)
				}
				resCh <- Result{
					Status:         "failed",
					Error:          zcodeRequestErrorMessage("session/resume", err),
					DurationMs:     time.Since(startTime).Milliseconds(),
					ResumeRejected: resumeRejected,
				}
				return
			}
			sessionResult = result
			// The bridge's resume response carries modes and configOptions but
			// no sessionId — the client keeps addressing the session with the
			// id it sent. resolveResumedSessionID handles both shapes, so a
			// future response that does echo an id is still honoured.
			var changed bool
			sessionID, changed = resolveResumedSessionID(opts.ResumeSessionID, result)
			if changed {
				b.cfg.Logger.Warn("zcode returned a different session id on resume — original was likely lost; continuing with the new id",
					"backend", "zcode",
					"requested", opts.ResumeSessionID,
					"actual", sessionID,
				)
			}
		} else {
			result, err := c.request(runCtx, "session/new", map[string]any{
				"cwd":        cwd,
				"mcpServers": mcpServers,
			})
			if err != nil {
				switch {
				case runCtx.Err() == context.DeadlineExceeded:
					finalStatus = "timeout"
					finalError = fmt.Sprintf("zcode timed out during session/new: %v", timeout)
				case runCtx.Err() == context.Canceled:
					finalStatus = "aborted"
					finalError = fmt.Sprintf("zcode aborted: %v", err)
				default:
					finalStatus = "failed"
					finalError = zcodeRequestErrorMessage("session/new", err)
				}
				resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
				return
			}
			sessionResult = result
			sessionID = extractACPSessionID(result)
			if sessionID == "" {
				finalStatus = "failed"
				finalError = "zcode session/new returned no session ID"
				resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
				return
			}
		}

		c.sessionID = sessionID
		// Early session pin so a cancelled run still preserves the resume
		// pointer.
		msgStream.send(Message{Type: MessageStatus, Status: "running", SessionID: sessionID})
		b.cfg.Logger.Info("zcode session ready", "session_id", sessionID)

		// Apply a model pick before prompting. The bridge routes
		// session/set_model to its own model switch, which fails when the
		// provider is not configured or the id is unknown. This MUST fail the
		// task: silently running on zcode's default model would let the user
		// believe their pick was honoured.
		modelSwitched := false
		if opts.Model != "" {
			if _, err := c.request(runCtx, "session/set_model", map[string]any{
				"sessionId": sessionID,
				"modelId":   opts.Model,
			}); err != nil {
				b.cfg.Logger.Warn("zcode set_model failed", "error", err, "requested_model", opts.Model)
				finalStatus = "failed"
				finalError = fmt.Sprintf("zcode could not switch to model %q: %v", opts.Model, err)
				if opts.ResumeSessionID != "" && zcodeSessionLost(err) {
					// A lazy placeholder materialises its zcode session on
					// first use, so a session the bridge lost surfaces at the
					// first call that needs the backend — here, when a model
					// is pinned. Clear the id so the daemon retries fresh.
					b.cfg.Logger.Warn("resumed session lost at set_model time; clearing session id so the daemon retries fresh",
						"backend", "zcode",
						"session_id", sessionID,
					)
					sessionID = ""
					resumeRejected = true
				}
				resCh <- Result{
					Status:         finalStatus,
					Error:          finalError,
					DurationMs:     time.Since(startTime).Milliseconds(),
					SessionID:      sessionID,
					ResumeRejected: resumeRejected,
				}
				return
			}
			modelSwitched = true
			b.cfg.Logger.Info("zcode session model set", "model", opts.Model)
		}

		// Apply a persisted reasoning-effort level through whichever selector
		// this session advertised. zcode-acp addresses it as config id
		// `thought` under category `thought_level`, and its vocabulary is
		// per-model (read from the enabled provider's own reasoning variants),
		// so the id and the level list are read off the session rather than
		// hard-coded. A configuration failure never blocks the task; see the
		// helper for what the warnings mean.
		//
		// stateIsCurrent is false after a model switch: the advertised effort
		// vocabulary belongs to the model the session opened on, and the
		// refreshed list arrives only as a config_option_update notification
		// the shared client drops.
		applyACPEffortOption(runCtx, c.request, "zcode", b.cfg.Logger, sessionID, sessionResult, opts.ThinkingLevel, !modelSwitched)

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
				finalError = fmt.Sprintf("zcode timed out after %s", timeout)
			case runCtx.Err() == context.Canceled:
				finalStatus = "aborted"
				finalError = "execution cancelled"
			default:
				finalStatus = "failed"
				finalError = zcodeRequestErrorMessage("session/prompt", err)
				if opts.ResumeSessionID != "" && zcodeSessionLost(err) {
					// The bridge echoes the requested id back from resume
					// without touching the backend, so a session it cannot
					// materialise only fails here, at prompt time. An empty
					// SessionID lets the daemon's resume-failure fallback
					// retry fresh and store the replacement id.
					b.cfg.Logger.Warn("resumed session lost at prompt time; clearing session id so the daemon retries fresh",
						"backend", "zcode",
						"session_id", sessionID,
					)
					sessionID = ""
					resumeRejected = true
				}
			}
		} else {
			select {
			case pr := <-promptDone:
				switch pr.stopReason {
				case "cancelled":
					finalStatus = "aborted"
					finalError = "zcode cancelled the prompt"
				case "max_turn_requests":
					// The bridge's own per-turn request budget, not a model
					// refusal: the turn stopped with work outstanding, so
					// reporting it as completed would present a truncated
					// answer as the final one.
					finalStatus = "failed"
					finalError = "zcode stopped the turn at its per-turn request limit before the agent finished"
				}
				c.mergeUsage(pr.usage)
			default:
			}
			waitForACPNotificationQuiescence(runCtx, activity, readerDone, acpNotificationQuietTime, zcodeReaderDrainGrace)
		}

		duration := time.Since(startTime)
		b.cfg.Logger.Info("zcode finished", "pid", cmd.Process.Pid, "status", finalStatus, "duration", duration.Round(time.Millisecond).String())

		stdin.Close()
		cancel()

		drainCtx, drainCancel := context.WithTimeout(context.Background(), zcodeReaderDrainGrace)
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
		// terminal upstream-LLM failure (auth / rate-limit / HTTP 4xx).
		finalStatus, finalError = promoteACPResultOnProviderError(finalStatus, finalError, providerErrorOutput, providerErr)

		u := c.accumulatedUsage()

		// zcode-acp reports the turn's token buckets on the prompt result's
		// top-level `usage` object (attachTurnUsage), which the shared client
		// has already parsed. No disk fallback: unlike kimi there is no wire
		// log to scan, and a turn that genuinely produced no usage must report
		// none rather than a zero-valued row.
		var usageMap map[string]TokenUsage
		if acpUsagePresent(u) {
			model := opts.Model
			if model == "" {
				// The session's own model is whatever the enabled provider
				// defaults to; the prompt result carries no model id, so
				// attribute to "unknown" rather than inventing one.
				model = "unknown"
			}
			usageMap = map[string]TokenUsage{model: u}
		}

		resCh <- Result{
			Status:         finalStatus,
			Output:         finalOutput,
			Error:          finalError,
			DurationMs:     duration.Milliseconds(),
			SessionID:      sessionID,
			ResumeRejected: resumeRejected,
			Usage:          usageMap,
		}
	}()

	return &Session{Messages: msgStream.ch, Result: resCh}, nil
}
