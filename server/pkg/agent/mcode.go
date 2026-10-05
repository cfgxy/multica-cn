package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// mcodeBlockedArgs protect the ACP stdio transport selected by the daemon.
// The public CLI currently exposes no options on `mcode acp`; its only child
// command is the interactive `login` flow, which cannot run inside a claimed
// headless task. Keeping these tokens out of custom_args prevents a profile or
// agent from replacing the protocol process with help or authentication UI.
var mcodeBlockedArgs = map[string]blockedArgMode{
	"acp":      blockedStandalone,
	"login":    blockedStandalone,
	"--region": blockedWithValue,
	"-h":       blockedStandalone,
	"--help":   blockedStandalone,
}

// mcodeBackend runs MiniMax Code as an ACP v1 agent server via `mcode acp`.
// MiniMax Code owns its Runtime, Session, permission, and questionnaire loops;
// this adapter only maps the shared ACP stream into Multica's Backend contract.
//
// The current public ACP surface deliberately declares loadSession:false and
// does not expose session-scoped model selection. Resume therefore returns a
// typed rejection so the daemon can retry on a fresh session, and model choice
// stays managed by MiniMax Code. If a later version advertises loadSession,
// the standard session/load path below begins working without a compatibility
// guess or a version-string gate.
type mcodeBackend struct {
	cfg Config
}

var mcodeReaderDrainGrace = 2 * time.Second
var mcodeSessionStartupReadyDelay = 100 * time.Millisecond

var errMcodeProcessExited = errors.New("mcode process exited")

type mcodeMessageStream struct {
	ch     chan Message
	mu     sync.Mutex
	closed bool
}

func newMcodeMessageStream(size int) *mcodeMessageStream {
	return &mcodeMessageStream{ch: make(chan Message, size)}
}

func (s *mcodeMessageStream) send(message Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	trySend(s.ch, message)
}

func (s *mcodeMessageStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

func (b *mcodeBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "mcode"
	}
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("mcode executable not found at %q: %w", execPath, err)
	}

	mcpServers, err := buildACPMcpServers(opts.McpConfig, b.cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("mcode: invalid mcp_config: %w", err)
	}

	runCtx, cancel := runContext(ctx, opts.Timeout)
	args := []string{"acp"}
	args = append(args, filterCustomArgs(opts.ExtraArgs, mcodeBlockedArgs, b.cfg.Logger)...)
	args = append(args, filterCustomArgs(opts.CustomArgs, mcodeBlockedArgs, b.cfg.Logger)...)
	cmd := b.cfg.commandAt(execPath).exec(runCtx, args...)
	hideAgentWindow(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			signalProcessGroup(cmd, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = mcodeReaderDrainGrace
	b.cfg.logAgentCommand(cmd, newAgentCommandLogArgs(args, trustAgentCommandPositional(0, "acp")))
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(b.cfg.Env)

	// RUYI-349: session decides at Start between the legacy direct child
	// and the supervised transient unit (daemon injects Supervision for
	// whitelisted providers only).
	sess := newWorkerSession(cmd, opts.Supervision)

	stdout, err := sess.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("mcode stdout pipe: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("mcode stdin pipe: %w", err)
	}
	providerErr := newACPProviderErrorSniffer("mcode")
	stderr, err := sess.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("mcode stderr pipe: %w", err)
	}
	if err := sess.Start(runCtx, b.cfg.Logger); err != nil {
		cancel()
		return nil, fmt.Errorf("start mcode: %w", err)
	}

	stderrSink := io.MultiWriter(newLogWriter(b.cfg.Logger, "[mcode:stderr] "), providerErr)
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(stderrSink, stderr)
	}()

	b.cfg.Logger.Info("mcode acp started", "pid", sess.PID(), "cwd", opts.Cwd)
	msgStream := newMcodeMessageStream(256)
	resCh := make(chan Result, 1)
	var deliverable acpDeliverableTracker
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
		onMessage: func(message Message) {
			if !streamingCurrentTurn.Load() {
				return
			}
			deliverable.observe(message)
			msgStream.send(message)
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
			if line != "" {
				c.handleLine(line)
			}
		}
		c.closeAllPending(errMcodeProcessExited)
	}()

	go func() {
		defer msgStream.close()
		defer close(resCh)
		defer func() {
			// EOF first, bounded natural-exit window; the fallback cancel
			// keeps every early-return path reachable — MCode or a tool
			// descendant that ignores stdin EOF cannot wedge the Wait past
			// the grace.
			finishWorkerStdin(sess, stdin, runCtx, cancel, b.cfg.Logger, "mcode")
			_ = sess.Wait(runCtx)
			releaseProcessGroup(cmd)
		}()

		startTime := time.Now()
		finalStatus := "completed"
		var finalError string
		var sessionID string
		var resumeRejected bool

		// RUYI-390 reattach: the worker consumed its prompt under the
		// previous daemon and its turn is still running. No handshake and
		// no prompt — both would hit a mid-turn session — and the session
		// id rebuilds from the session/update stream. The shared result
		// path below picks the turn tail up from promptDone as usual.
		var promptErr error
		if sess.Reattaching() {
			streamingCurrentTurn.Store(true)
			c.seedReattachIDSpace()
			b.cfg.Logger.Info("mcode reattach: riding the in-flight turn launched by the previous daemon", "pid", sess.PID())
			promptErr = c.waitReattachedTurn(runCtx, "mcode")
			sessionID = c.observedSessionID()
		} else {

			initResult, err := c.request(runCtx, "initialize", map[string]any{
				"protocolVersion": 1,
				"clientInfo": map[string]any{
					"name":    "multica-agent-sdk",
					"version": "0.2.0",
				},
				"clientCapabilities": map[string]any{},
			})
			if err != nil {
				finalStatus, finalError = mcodeRequestFailure(runCtx, opts.Timeout, "initialize", err)
				resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
				return
			}

			mcpServers = filterACPMcpServersByCapability(
				mcpServers,
				extractACPMcpCapabilities(initResult),
				"mcode",
				b.cfg,
			)
			cwd := opts.Cwd
			if cwd == "" {
				cwd = "."
			}

			if opts.ResumeSessionID != "" {
				if !mcodeLoadSessionSupported(initResult) {
					finalStatus = "failed"
					finalError = "mcode ACP does not support session loading; retry with a fresh session"
					resumeRejected = true
					resCh <- Result{
						Status:         finalStatus,
						Error:          finalError,
						DurationMs:     time.Since(startTime).Milliseconds(),
						ResumeRejected: true,
					}
					return
				}
				if err := mcodeWaitForSessionStartup(runCtx); err != nil {
					finalStatus, finalError = mcodeRequestFailure(runCtx, opts.Timeout, "session/load", err)
					resCh <- Result{
						Status:         finalStatus,
						Error:          finalError,
						DurationMs:     time.Since(startTime).Milliseconds(),
						ResumeRejected: resumeRejected,
					}
					return
				}
				result, loadErr := c.request(runCtx, "session/load", map[string]any{
					"cwd":        cwd,
					"sessionId":  opts.ResumeSessionID,
					"mcpServers": mcpServers,
				})
				if loadErr != nil {
					finalStatus, finalError = mcodeRequestFailure(runCtx, opts.Timeout, "session/load", loadErr)
					if finalStatus == "failed" && isACPSessionNotFound(loadErr) {
						resumeRejected = true
					}
					resCh <- Result{
						Status:         finalStatus,
						Error:          finalError,
						DurationMs:     time.Since(startTime).Milliseconds(),
						ResumeRejected: resumeRejected,
					}
					return
				}
				sessionID, _ = resolveResumedSessionID(opts.ResumeSessionID, result)
			} else {
				if err := mcodeWaitForSessionStartup(runCtx); err != nil {
					finalStatus, finalError = mcodeRequestFailure(runCtx, opts.Timeout, "session/new", err)
					resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
					return
				}
				result, newErr := c.request(runCtx, "session/new", map[string]any{
					"cwd":        cwd,
					"mcpServers": mcpServers,
				})
				if newErr != nil {
					finalStatus, finalError = mcodeRequestFailure(runCtx, opts.Timeout, "session/new", newErr)
					resCh <- Result{Status: finalStatus, Error: finalError, DurationMs: time.Since(startTime).Milliseconds()}
					return
				}
				sessionID = extractACPSessionID(result)
				if sessionID == "" {
					resCh <- Result{
						Status:     "failed",
						Error:      "mcode session/new returned no session ID",
						DurationMs: time.Since(startTime).Milliseconds(),
					}
					return
				}
			}

			c.sessionID = sessionID
			if opts.SystemPrompt != "" {
				b.cfg.Logger.Debug("mcode ignoring ExecOptions.SystemPrompt; using cwd-scoped AGENTS.md", "cwd", opts.Cwd)
			}
			streamingCurrentTurn.Store(true)
			_, promptErr = c.request(runCtx, "session/prompt", map[string]any{
				"sessionId": sessionID,
				"prompt": []map[string]any{
					{"type": "text", "text": prompt},
				},
			})
		}
		if promptErr != nil {
			finalStatus, finalError = mcodeRequestFailure(runCtx, opts.Timeout, "session/prompt", promptErr)
			if opts.ResumeSessionID != "" && isACPSessionNotFound(promptErr) {
				resumeRejected = true
				sessionID = ""
			}
		} else {
			select {
			case result := <-promptDone:
				c.mergeUsage(result.usage)
				if result.stopReason == "cancelled" {
					finalStatus = "aborted"
					finalError = "execution cancelled"
				} else if result.stopReason == "max_turn_requests" {
					finalStatus = "failed"
					finalError = "mcode reached its maximum turn requests"
				}
			default:
			}
			waitForACPNotificationQuiescence(runCtx, activity, readerDone, acpNotificationQuietTime, mcodeReaderDrainGrace)
		}
		streamingCurrentTurn.Store(false)

		duration := time.Since(startTime)
		finishWorkerStdin(sess, stdin, runCtx, cancel, b.cfg.Logger, "mcode")
		<-readerDone
		<-stderrDone

		finalOutput, fullOutput := deliverable.result()
		finalStatus, finalError = promoteACPResultOnProviderError(
			finalStatus,
			finalError,
			fullOutput,
			providerErr,
		)
		var usage map[string]TokenUsage
		if accumulated := c.accumulatedUsage(); acpUsagePresent(accumulated) {
			usage = map[string]TokenUsage{"unknown": accumulated}
		}
		resCh <- Result{
			Status:         finalStatus,
			Output:         finalOutput,
			Error:          finalError,
			DurationMs:     duration.Milliseconds(),
			SessionID:      sessionID,
			ResumeRejected: resumeRejected,
			Usage:          usage,
		}
	}()

	return &Session{Messages: msgStream.ch, Result: resCh}, nil
}

func mcodeLoadSessionSupported(result json.RawMessage) bool {
	var payload struct {
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	return json.Unmarshal(result, &payload) == nil && payload.AgentCapabilities.LoadSession
}

func mcodeWaitForSessionStartup(ctx context.Context) error {
	timer := time.NewTimer(mcodeSessionStartupReadyDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func mcodeRequestFailure(ctx context.Context, timeout time.Duration, operation string, err error) (string, string) {
	if ctx.Err() == context.DeadlineExceeded {
		return "timeout", fmt.Sprintf("mcode timed out during %s after %s", operation, timeout)
	}
	if ctx.Err() == context.Canceled {
		return "aborted", "execution cancelled"
	}
	if mcodeSessionStartupExited(operation, err) {
		return "failed", "mcode session/new failed after initialize: MiniMax Code ACP exited before starting a session; retry agent creation, and if it persists upgrade MiniMax Code or verify `mcode acp` starts successfully"
	}
	return "failed", fmt.Sprintf("mcode %s failed: %v", operation, err)
}

func mcodeSessionStartupExited(operation string, err error) bool {
	if operation != "session/new" {
		return false
	}
	// Only trust transport-level signals. A still-running MCode reports
	// session/new failures as a JSON-RPC error, and session/new is where
	// mcpServers are launched, so text matching on "process exited" or
	// "broken pipe" would rewrite a caller's real MCP startup error into
	// an MCode-exited message and drop its root cause.
	return errors.Is(err, errMcodeProcessExited) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.EPIPE)
}
