package agent

import (
	"context"
	"fmt"
	"io"
	"syscall"
)

// WorkerSupervisor owns the lifecycle of task workers OUTSIDE the daemon
// process (RUYI-349 Phase 1). The daemon's control plane dies and restarts
// freely; a supervised worker keeps running inside its own lifecycle scope
// (a systemd transient unit) and the next daemon process rediscovers it via
// Reattach.
//
// The daemon-side implementation lives in internal/daemon/supervisor. This
// package only defines the contract the backends launch through, so the
// provider code never learns whether the worker is a direct child (legacy)
// or a supervised unit.
type WorkerSupervisor interface {
	// Launch starts the program described in spec under the supervisor's
	// ownership. The worker is NOT a child of the daemon: daemon death must
	// not deliver it a signal, and its output must keep landing somewhere a
	// future daemon can read.
	Launch(ctx context.Context, spec LaunchSpec) (WorkerHandle, error)

	// Reattach binds to a worker this supervisor previously launched, using
	// the identity the supervisor persisted at launch time. It is called on
	// daemon startup reconciliation (worker still alive) and on post-mortem
	// convergence (worker exited while the daemon was down; the returned
	// handle serves the recorded output stream and the recorded exit).
	Reattach(ctx context.Context, runID string) (WorkerHandle, error)
}

// LaunchSpec is everything the supervisor needs to start a worker. It is the
// same information an exec.Cmd carries, minus the pipes: the supervisor owns
// the wire format for I/O (append-only per-run log files plus a reconnectable
// control socket), so the daemon never holds an anonymous pipe into the
// worker.
type LaunchSpec struct {
	RunID   string   // unique per launch attempt; unit/directory identity
	TaskID  string   // server-side task this attempt belongs to
	Runtime string   // provider/runtime identity, for audit only
	Path    string   // worker program
	Args    []string // worker argv (after Path)
	Env     []string // full worker environment (may carry credentials; the supervisor stores it 0600, same trust domain as the daemon process)
	Dir     string   // worker working directory
}

// WorkerHandle is the daemon-side end of one supervised worker.
type WorkerHandle interface {
	// PID reports the worker's leader PID as most recently observed. It is
	// advisory (the launcher refreshes it at spawn); lifecycle truth comes
	// from Wait/Done.
	PID() int

	// Stdin returns the writer whose bytes the supervisor forwards to the
	// worker's standard input. Closing it delivers EOF to the worker.
	Stdin() io.Writer

	// CloseStdin delivers EOF on the worker's stdin. Safe to call twice.
	CloseStdin() error

	// StdoutStream returns a reader over the worker's stdout from the run's
	// persisted read offset onwards, keeping that offset persisted as data is
	// drained. The stream ends when the worker's output reaches EOF for good
	// (worker exit recorded). A fresh daemon re-reading the same run resumes
	// from the persisted offset — that is the resumable-continuation
	// contract.
	StdoutStream(ctx context.Context) (io.ReadCloser, error)

	// StderrStream is StdoutStream for the worker's stderr.
	StderrStream(ctx context.Context) (io.ReadCloser, error)

	// Wait blocks until the worker's exit is proven (exit record from the
	// launcher, or the supervisor having stopped the unit itself) and returns
	// it. It must return after Stop() or Detach() even though the worker may
	// still be running.
	Wait(ctx context.Context) (WorkerExit, error)

	// Signal sends sig to the worker's whole process group (the graceful
	// shutdown primitive backends drive SIGTERM→grace→SIGKILL with).
	Signal(sig syscall.Signal) error

	// Stop terminates the worker's whole lifecycle scope (the transient
	// unit's cgroup): the precise, target-only cancel primitive.
	Stop() error

	// Detach disconnects the daemon from the worker WITHOUT stopping it:
	// daemon shutdown path. After Detach the worker keeps running and a
	// future daemon reattaches via Reattach.
	Detach() error
}

// WorkerExit is a supervised worker's proven terminal state. The Error
// wording deliberately mirrors os/exec's ("exit status N" / "signal: killed")
// so failure classification that greps exit text keeps working.
type WorkerExit struct {
	Code   int    // process exit code; -1 when signalled
	Signal string // "" or e.g. "killed" (lowercase signal name, exec-style)
}

// ErrDetached is returned by WorkerHandle.Wait when the daemon detached
// without waiting for the worker (graceful daemon shutdown). The run is not
// over — the next daemon reconciles it.
var ErrDetached = fmt.Errorf("worker detached: daemon shutdown leaves it running")

func (e WorkerExit) Exited() bool { return e.Code >= 0 }

func (e WorkerExit) Error() string {
	if e.Signal != "" {
		return "signal: " + e.Signal
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

// Supervision is the per-execution binding a backend reads off its Config to
// decide how the worker is launched. Zero value (nil Config field) means the
// legacy direct-child semantics — byte-for-byte what backends did before
// RUYI-349.
type Supervision struct {
	Supervisor WorkerSupervisor
	RunID      string
	// TaskID is the server-side task this run belongs to (audit + reconcile
	// joins). Runtime is the provider identity, audit only.
	TaskID  string
	Runtime string
	// Reattach marks this Execute as a continuation of an already-launched
	// worker (daemon restart reconciliation). Backends must not write the
	// initial prompt/handshake to a reattached worker — it consumed its
	// prompt before the previous daemon died.
	Reattach bool
}

// active reports whether this execution must launch through the supervisor.
func (s *Supervision) active() bool { return s != nil && s.Supervisor != nil && s.RunID != "" }

// ShutdownDetaching is the optional supervisor-side switch the daemon flips
// while it is shutting down: a cancellation arriving after this point must
// DETACH (leave the worker running for the next daemon) instead of killing
// it. Sessions consult it so the daemon's root-context teardown does not take
// supervised workers down with it — that teardown is exactly the loss of
// execution context this package exists to prevent.
type ShutdownDetaching interface {
	ShutdownDetaching() bool
}
