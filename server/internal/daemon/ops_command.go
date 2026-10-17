// RUYI-645: daemon-side executor for the read-only ops command channel.
// Commands arrive on the heartbeat ack (already claimed server-side, with a
// lease epoch as the receipt token); this file builds argv through the shared
// opskind registry, runs the process under a hard timeout, caps captured
// output, and reports a terminal receipt. Nothing here ever re-queues or
// retries a command — the receipt is delivered with transport retries only,
// and a receipt that can't land leaves the row to the server-side lease sweep.
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/opskind"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Per-stream capture cap. Two streams plus receipt framing must stay well
// under the server's 256KB result cap, so 64KB per stream.
const opsStreamCapBytes = 64 * 1024

// opsCommandReceipt mirrors the server's OpsReceipt body (minus the route
// identifiers, which the receipt URL already carries).
type opsCommandReceipt struct {
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	Error      string `json:"error,omitempty"`
	LeaseEpoch int64  `json:"lease_epoch"`
}

func rejectedReceipt(epoch int64, format string, args ...any) opsCommandReceipt {
	return opsCommandReceipt{Status: "rejected", Error: fmt.Sprintf(format, args...), LeaseEpoch: epoch}
}

// opsUnitGate is the daemon-side double check on unit slots: when the local
// service inventory has been collected, a unit it doesn't know is rejected
// without spawning anything. No local inventory yet → the check is skipped
// (the server's inventory gate is the authoritative one; failing closed here
// too would reject legitimate commands on a node whose collector hasn't
// completed its first cycle).
func (d *Daemon) unitInLocalInventory(unit string) (known, ok bool) {
	units, have := lastOpsInventoryUnits()
	if !have {
		return false, true
	}
	_, known = units[unit]
	return known, true
}

// handleOpsCommand executes one claimed command and delivers its receipt.
// Runs in its own goroutine off the heartbeat path.
func (d *Daemon) handleOpsCommand(ctx context.Context, rt Runtime, cmd protocol.DaemonOpsCommandDispatch) {
	d.logger.Info("ops command claimed",
		"runtime_id", rt.ID, "command_id", cmd.CommandID, "kind", cmd.Kind, "timeout_s", cmd.TimeoutS, "lease_epoch", cmd.LeaseEpoch)

	if unit, hasUnit := unitArgOf(cmd.Args); hasUnit {
		if known, checkRan := d.unitInLocalInventory(unit); checkRan && !known {
			d.reportOpsCommandResult(ctx, rt.ID, cmd.CommandID,
				rejectedReceipt(cmd.LeaseEpoch, "unit %q is not present in the local service inventory", unit))
			return
		}
	}

	receipt := executeOpsCommand(ctx, cmd)
	d.reportOpsCommandResult(ctx, rt.ID, cmd.CommandID, receipt)
}

// unitArgOf extracts the unit slot from raw args without re-validating: the
// shared registry already shaped this dispatch, and the executor re-checks
// everything anyway.
func unitArgOf(args []byte) (string, bool) {
	var parsed struct {
		Unit string `json:"unit"`
	}
	if len(args) == 0 || json.Unmarshal(args, &parsed) != nil {
		return "", false
	}
	return parsed.Unit, parsed.Unit != ""
}

// opsDockerBin resolves the docker binary once per call; docker_ps fails
// closed through the shared registry when it is absent.
func opsDockerBin() string {
	p, _ := exec.LookPath("docker")
	return p
}

// executeOpsCommand turns a dispatch into a receipt: whitelist lookup, argv
// build through the shared registry, then the pure executor. Any failure
// before the process spawns is a rejected receipt (the command was invalid,
// not the machine's answer); failures of the process itself are failed.
func executeOpsCommand(ctx context.Context, cmd protocol.DaemonOpsCommandDispatch) opsCommandReceipt {
	spec := opskind.Lookup(opskind.Kind(cmd.Kind))
	if spec == nil {
		return rejectedReceipt(cmd.LeaseEpoch, "command kind %q is not in the read-only whitelist", cmd.Kind)
	}

	var args map[string]any
	if len(cmd.Args) > 0 {
		if err := json.Unmarshal(cmd.Args, &args); err != nil {
			return rejectedReceipt(cmd.LeaseEpoch, "command arguments are not a JSON object")
		}
	}
	if err := opskind.ValidateArgs(opskind.Kind(cmd.Kind), args); err != nil {
		return rejectedReceipt(cmd.LeaseEpoch, "command arguments rejected: %v", err)
	}
	argv, err := opskind.BuildArgv(opskind.Kind(cmd.Kind), args, opsDockerBin())
	if err != nil {
		return rejectedReceipt(cmd.LeaseEpoch, "command argv could not be built: %v", err)
	}

	timeoutS := cmd.TimeoutS
	if timeoutS < 1 || timeoutS > 300 {
		timeoutS = 30
	}
	return runOpsArgv(ctx, argv, time.Duration(timeoutS)*time.Second, cmd.LeaseEpoch)
}

// cappedWriter captures up to limit bytes and flags any overflow, so a
// chatty journal can never blow up the daemon's memory or the receipt size.
type cappedWriter struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.buf.Len() >= w.limit {
		w.truncated = true
		return len(p), nil
	}
	room := w.limit - w.buf.Len()
	if len(p) > room {
		w.buf.Write(p[:room])
		w.truncated = true
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

// runOpsArgv is the actual process runner: one whitelisted argv under a hard
// context timeout, per-stream 64KB capture caps, and a WaitDelay so pipes
// close even when the child leaves grandchildren behind after the kill.
func runOpsArgv(ctx context.Context, argv []string, timeout time.Duration, epoch int64) opsCommandReceipt {
	if len(argv) == 0 {
		return rejectedReceipt(epoch, "command argv is empty")
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(execCtx, argv[0], argv[1:]...)
	c.WaitDelay = 2 * time.Second

	stdout := &cappedWriter{limit: opsStreamCapBytes}
	stderr := &cappedWriter{limit: opsStreamCapBytes}
	c.Stdout = stdout
	c.Stderr = stderr

	spawnErr := c.Run()
	receipt := opsCommandReceipt{
		Stdout:     stdout.buf.String(),
		Stderr:     stderr.buf.String(),
		Truncated:  stdout.truncated || stderr.truncated,
		LeaseEpoch: epoch,
	}

	switch {
	case execCtx.Err() != nil && errors.Is(execCtx.Err(), context.DeadlineExceeded):
		receipt.Status = "failed"
		receipt.Error = fmt.Sprintf("command timed out after %s", timeout)
		return receipt
	case spawnErr != nil:
		receipt.Status = "failed"
		var exitErr *exec.ExitError
		if errors.As(spawnErr, &exitErr) {
			code := exitErr.ExitCode()
			receipt.ExitCode = &code
		} else {
			// Binary missing/not executable — the machine's answer is that
			// the command cannot run here.
			receipt.Error = fmt.Sprintf("command could not be executed: %v", spawnErr)
		}
		return receipt
	default:
		receipt.Status = "succeeded"
		code := 0
		receipt.ExitCode = &code
		return receipt
	}
}

// reportOpsCommandResult delivers a terminal receipt with transport retries.
// The receipt itself is never re-generated: retries re-POST the identical
// body, and a stale epoch on the server side is a definitive rejection.
func (d *Daemon) reportOpsCommandResult(ctx context.Context, runtimeID, commandID string, receipt opsCommandReceipt) {
	var lastErr error
	for i, backoff := range runtimeReportBackoffs {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}
		err := d.client.ReportOpsCommandResult(ctx, runtimeID, commandID, receipt)
		if err == nil {
			return
		}
		lastErr = err
		if !isTransientError(err) {
			break
		}
	}
	d.logger.Warn("ops command receipt delivery failed",
		"runtime_id", runtimeID, "command_id", commandID, "attempts", len(runtimeReportBackoffs), "error", lastErr)
}

// opsInventoryUnits is the collector's latest successful snapshot, shared
// with the executor's unit gate. Guarded by a mutex — one writer (the
// inventory loop), two readers (executor goroutines, tests).
var (
	opsInventoryMu       sync.RWMutex
	opsInventoryUnitSet  map[string]bool
	opsInventoryHasUnits bool
)

func storeOpsInventoryUnits(units []opsServiceEntry) {
	set := make(map[string]bool, len(units))
	for _, u := range units {
		set[u.Name] = true
	}
	opsInventoryMu.Lock()
	opsInventoryUnitSet = set
	opsInventoryHasUnits = true
	opsInventoryMu.Unlock()
}

func lastOpsInventoryUnits() (map[string]bool, bool) {
	opsInventoryMu.RLock()
	defer opsInventoryMu.RUnlock()
	return opsInventoryUnitSet, opsInventoryHasUnits
}
