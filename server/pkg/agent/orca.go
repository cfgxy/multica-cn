package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// orcaBackend implements Backend by spawning the Stably Orca CLI
// (`orca agent launch ... --json`) and reading assistant output via
// `orca agent read ... --json`.
//
// In current Stably Orca CLI (v1.4.280), session resumption with a new prompt
// is not exposed on the CLI argument surface. Like copilot, antigravity, cursor,
// deveco, and codearts, orca is registered in resumeRejectionUndetectable so
// Multica manages fresh session fallbacks rather than failing silently on an
// unavailable resume.
type orcaBackend struct {
	cfg Config
}

// IsGnomeScreenReader checks if the given path points to the GNOME desktop screen reader
// binary or python script instead of the Stably Orca CLI.
func IsGnomeScreenReader(path string) bool {
	clean := filepath.Clean(path)
	if clean == "/usr/bin/orca" {
		return true
	}
	if data, err := os.ReadFile(clean); err == nil {
		content := string(data)
		if strings.Contains(content, "gnome") || strings.Contains(content, "screen reader") || strings.Contains(content, "orca.orca") {
			return true
		}
	}
	return false
}

func resolveOrcaExecutablePath(cfg Config) (string, error) {
	execPath := strings.TrimSpace(cfg.ExecutablePath)
	if execPath == "" {
		if envPath := strings.TrimSpace(os.Getenv("MULTICA_ORCA_PATH")); envPath != "" {
			execPath = envPath
		}
	}
	if execPath == "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			candidate := filepath.Join(home, ".orca", "cli", "bin", "orca")
			if _, err := os.Stat(candidate); err == nil {
				execPath = candidate
			}
		}
	}
	if execPath == "" {
		execPath = "orca"
	}
	resolved, err := exec.LookPath(execPath)
	if err != nil {
		return "", fmt.Errorf("orca executable not found at %q: %w", execPath, err)
	}
	if IsGnomeScreenReader(resolved) {
		return "", fmt.Errorf("refusing to execute %q: detected GNOME screen reader; configure MULTICA_ORCA_PATH to the Stably Orca CLI (e.g. /home/guxy/.orca/cli/bin/orca)", resolved)
	}
	return resolved, nil
}

func buildOrcaLaunchArgs(prompt string, opts ExecOptions) []string {
	agentName := "pi"
	if opts.Model == "claude" || opts.Model == "codex" || opts.Model == "pi" || opts.Model == "antigravity" {
		agentName = opts.Model
	}
	args := []string{"agent", "launch", "--agent", agentName, "--prompt", prompt}
	if opts.Cwd != "" {
		args = append(args, "--worktree", "path:"+filepath.Clean(opts.Cwd))
	}
	if opts.Model != "" && opts.Model != agentName {
		args = append(args, "--model", opts.Model)
	}
	args = append(args, "--json")
	return args
}

type orcaLaunchOutput struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result struct {
		Outcome struct {
			Kind      string `json:"kind"`
			SessionID string `json:"sessionId"`
			Handle    string `json:"handle"`
		} `json:"outcome"`
		WorktreeID string `json:"worktreeId"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type orcaReadOutput struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result struct {
		OrcaSessionID string `json:"orcaSessionId"`
		State         string `json:"state"`
		Entries       []struct {
			Kind string `json:"kind"`
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"entries"`
	} `json:"result"`
}

func (b *orcaBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	execPath, err := resolveOrcaExecutablePath(b.cfg)
	if err != nil {
		return nil, err
	}

	timeout := opts.Timeout
	runCtx, cancel := runContext(ctx, timeout)

	launchArgs := buildOrcaLaunchArgs(prompt, opts)
	cmd := b.cfg.commandAt(execPath).exec(runCtx, launchArgs...)
	hideAgentWindow(cmd)
	b.cfg.logAgentCommand(cmd, newAgentCommandLogArgs(launchArgs))
	cmd.WaitDelay = 10 * time.Second
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(b.cfg.Env)

	sess := newWorkerSession(cmd, opts.Supervision)
	stdout, err := sess.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("orca stdout pipe: %w", err)
	}

	stderrBuf := newStderrTail(newLogWriter(b.cfg.Logger, "[orca:stderr] "), agentStderrTailBytes)
	cmd.Stderr = stderrBuf

	if err := sess.Start(runCtx, b.cfg.Logger); err != nil {
		cancel()
		return nil, fmt.Errorf("start orca agent launch: %w", err)
	}

	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)

	go func() {
		defer cancel()
		defer close(msgCh)
		defer close(resCh)

		startTime := time.Now()
		var launchOut orcaLaunchOutput
		decErr := json.NewDecoder(stdout).Decode(&launchOut)
		waitErr := sess.Wait(runCtx)

		if decErr != nil {
			errText := string(stderrBuf.Tail())
			if errText == "" {
				errText = fmt.Sprintf("decode launch json: %v", decErr)
			}
			resCh <- Result{
				Status:     "failed",
				Error:      errText,
				DurationMs: time.Since(startTime).Milliseconds(),
			}
			return
		}

		if !launchOut.OK || launchOut.Error != nil {
			msg := "orca launch failed"
			if launchOut.Error != nil {
				msg = fmt.Sprintf("orca launch failed (%s): %s", launchOut.Error.Code, launchOut.Error.Message)
			} else if waitErr != nil {
				msg = fmt.Sprintf("orca launch failed: %v", waitErr)
			}
			resCh <- Result{
				Status:     "failed",
				Error:      msg,
				DurationMs: time.Since(startTime).Milliseconds(),
			}
			return
		}

		sessionID := launchOut.Result.Outcome.SessionID
		if sessionID != "" {
			trySend(msgCh, Message{
				Type:      MessageStatus,
				Status:    "running",
				SessionID: sessionID,
			})
		}

		// Read output using orca agent read
		var finalOutput string
		readInterval := 500 * time.Millisecond
		maxPolls := 120 // up to 60s
		for i := 0; i < maxPolls; i++ {
			select {
			case <-runCtx.Done():
				resCh <- Result{
					Status:     "cancelled",
					SessionID:  sessionID,
					Output:     finalOutput,
					DurationMs: time.Since(startTime).Milliseconds(),
				}
				return
			case <-time.After(readInterval):
			}

			readCmd := b.cfg.commandAt(execPath).exec(runCtx, "agent", "read", "orca_session_id:"+sessionID, "--json")
			readBytes, err := outputOwned(readCmd, b.cfg.Logger)
			if err != nil {
				continue
			}

			var readOut orcaReadOutput
			if err := json.Unmarshal(readBytes, &readOut); err != nil || !readOut.OK {
				continue
			}

			var assistantText string
			for _, entry := range readOut.Result.Entries {
				if entry.Role == "assistant" && entry.Text != "" {
					assistantText = entry.Text
				}
			}

			if assistantText != "" && assistantText != finalOutput {
				finalOutput = assistantText
				trySend(msgCh, Message{
					Type:    MessageText,
					Content: finalOutput,
				})
			}

			if readOut.Result.State == "done" || (finalOutput != "" && i > 3) {
				break
			}
		}

		resCh <- Result{
			Status:     "completed",
			Output:     finalOutput,
			SessionID:  sessionID,
			DurationMs: time.Since(startTime).Milliseconds(),
		}
	}()

	return &Session{
		Messages: msgCh,
		Result:   resCh,
	}, nil
}
