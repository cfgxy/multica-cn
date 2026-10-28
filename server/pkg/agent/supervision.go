package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
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

// ErrCancellationUnconfirmed marks a cancellation whose worker exit could not
// be proven. Callers must leave the server-side task in cancel_requested rather
// than acknowledge it as cancelled.
var ErrCancellationUnconfirmed = errors.New("worker cancellation is unconfirmed")

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

	stopMu                   sync.Mutex
	workerExitConfirmed      bool
	cancellationStopInFlight bool
	cancellationFailureCause error
	resolutionDone           chan struct{}
	resolutionClosed         bool
}

// active reports whether this execution must launch through the supervisor.
func (s *Supervision) active() bool { return s != nil && s.Supervisor != nil && s.RunID != "" }

// MarkWorkerExitConfirmed records that WorkerHandle.Wait returned a proven
// exit. It is intentionally separate from a successful signal: delivery of a
// signal never proves the worker or its cgroup has stopped.
func (s *Supervision) MarkWorkerExitConfirmed() {
	if s == nil {
		return
	}
	s.stopMu.Lock()
	s.workerExitConfirmed = true
	s.closeResolutionLocked()
	s.stopMu.Unlock()
}

// BeginCancellationStop prevents an exit record delivered by SIGKILL from
// confirming cancellation until the subsequent whole-cgroup Stop has finished.
// The process leader can die before a descendant, so its exit alone is not
// enough while the cgroup stop is still in flight.
func (s *Supervision) BeginCancellationStop() {
	if s == nil {
		return
	}
	s.stopMu.Lock()
	s.cancellationStopInFlight = true
	s.stopMu.Unlock()
}

// EndCancellationStop records the terminal outcome of a whole-cgroup stop and
// re-enables cancellation confirmation. A failed stop remains sticky.
func (s *Supervision) EndCancellationStop(err error) {
	if s == nil {
		return
	}
	s.stopMu.Lock()
	s.cancellationStopInFlight = false
	if err != nil && s.cancellationFailureCause == nil {
		s.cancellationFailureCause = err
	}
	s.closeResolutionLocked()
	s.stopMu.Unlock()
}

// MarkCancellationUnconfirmed records a failed cancellation action. The
// failure remains sticky even if the worker exits later: the daemon must not
// claim a stop it could not verify through the cancellation path.
func (s *Supervision) MarkCancellationUnconfirmed(err error) {
	if s == nil || err == nil {
		return
	}
	s.stopMu.Lock()
	if s.cancellationFailureCause == nil {
		s.cancellationFailureCause = err
	}
	s.closeResolutionLocked()
	s.stopMu.Unlock()
}

// WaitForWorkerStopResolution blocks until the supervised worker has either
// proven its whole lifecycle scope stopped or recorded a cancellation failure.
// A backend result is not equivalent to either event: several ACP backends
// publish it before their deferred session Wait finishes the cgroup teardown.
func (s *Supervision) WaitForWorkerStopResolution(ctx context.Context) error {
	if !s.active() {
		return nil
	}
	s.stopMu.Lock()
	if err := s.cancellationConfirmationErrorLocked(); err != ErrCancellationUnconfirmed {
		s.stopMu.Unlock()
		return err
	}
	if s.resolutionDone == nil {
		s.resolutionDone = make(chan struct{})
	}
	done := s.resolutionDone
	s.stopMu.Unlock()

	select {
	case <-done:
		return s.CancellationConfirmationError()
	case <-ctx.Done():
		return fmt.Errorf("%w: waiting for worker stop resolution: %v", ErrCancellationUnconfirmed, ctx.Err())
	}
}

func (s *Supervision) closeResolutionLocked() {
	if s.resolutionClosed {
		return
	}
	if s.cancellationFailureCause == nil && (s.cancellationStopInFlight || !s.workerExitConfirmed) {
		return
	}
	if s.resolutionDone == nil {
		s.resolutionDone = make(chan struct{})
	}
	close(s.resolutionDone)
	s.resolutionClosed = true
}

// CancellationFailureError reports a failed cancellation action without
// requiring a worker exit record. It lets a session stop waiting when Kill or
// Stop has already failed, while CancellationConfirmationError remains the
// stricter gate for terminal acknowledgement.
func (s *Supervision) CancellationFailureError() error {
	if !s.active() {
		return nil
	}
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	if s.cancellationFailureCause == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrCancellationUnconfirmed, s.cancellationFailureCause)
}

// CancellationConfirmationError returns nil only after a supervised worker
// has a proven exit and every cancellation action succeeded.
func (s *Supervision) CancellationConfirmationError() error {
	if !s.active() {
		return nil
	}
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	return s.cancellationConfirmationErrorLocked()
}

func (s *Supervision) cancellationConfirmationErrorLocked() error {
	if s.cancellationFailureCause != nil {
		return fmt.Errorf("%w: %v", ErrCancellationUnconfirmed, s.cancellationFailureCause)
	}
	if s.cancellationStopInFlight {
		return ErrCancellationUnconfirmed
	}
	if !s.workerExitConfirmed {
		return ErrCancellationUnconfirmed
	}
	return nil
}

// ShutdownDetaching is the optional supervisor-side switch the daemon flips
// while it is shutting down: a cancellation arriving after this point must
// DETACH (leave the worker running for the next daemon) instead of killing
// it. Sessions consult it so the daemon's root-context teardown does not take
// supervised workers down with it — that teardown is exactly the loss of
// execution context this package exists to prevent.
type ShutdownDetaching interface {
	ShutdownDetaching() bool
}
