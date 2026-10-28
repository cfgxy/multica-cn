package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// workerSession is the daemon-side end of one worker process. Backends talk
// to it exactly where they used to talk to exec.Cmd — StdoutPipe/StdinPipe/
// StderrPipe, Start, PID, Wait, Signal — and it has two implementations:
//
//   - legacy (unsupervised): a byte-for-byte pass-through to the exec.Cmd the
//     backend built. Direct anonymous pipes, direct child, process-group
//     signals — the pre-RUYI-349 semantics, still the only path on hosts
//     without a usable systemd user bus.
//
//   - supervised: the worker runs inside a supervisor-owned lifecycle scope
//     (systemd transient unit). Its stdout/stderr land in per-run append-only
//     log files the session pumps back into the pipes the backend holds, its
//     stdin is bridged through the supervisor's control socket, and its exit
//     is proven by the supervisor's exit record — not by this process being
//     the parent. Daemon death breaks none of it; the next daemon reenters
//     the same session machinery over Reattach.
type workerSession struct {
	cmd  *exec.Cmd
	sup  *Supervision
	mode sessionMode

	// legacy state
	stdinW io.WriteCloser

	// supervised state
	handle      WorkerHandle
	reattaching bool
	stdoutW     io.WriteCloser // feeds the backend's StdoutPipe reader
	stderrW     io.WriteCloser // feeds StderrPipe reader or cmd.Stderr
	stdinR      io.ReadCloser  // drains the backend's StdinPipe writer
	pumpWG      sync.WaitGroup
	startCtx    context.Context
	startCancel context.CancelFunc
	driverOnce  sync.Once
	stopMu      sync.Mutex
	stopStarted bool
	closeOnce   sync.Once
	waitOnce    sync.Once
	waitResult  error
	reapDone    chan struct{} // closed once the worker's exit is proven; past it the cancel driver has no kill target
	exitSeen    chan struct{} // closed once WorkerHandle.Wait reports an exit; the cgroup may still be stopping
	cancelDone  chan struct{} // closed when a context-triggered stop sequence reaches a terminal outcome
	stopDone    chan struct{} // closed after the complete unit cgroup stop reaches a terminal outcome
	stopErr     error
}

type sessionMode int

const (
	sessionLegacy sessionMode = iota
	sessionSupervised
)

// newWorkerSession binds a backend-built command to its execution mode. cmd
// stays the spec carrier (Path/Args/Env/Dir/Stderr/WaitDelay as the backend
// set them); in supervised mode it is never exec'd.
func newWorkerSession(cmd *exec.Cmd, sup *Supervision) *workerSession {
	s := &workerSession{cmd: cmd}
	if sup != nil && sup.active() {
		s.sup = sup
		s.mode = sessionSupervised
		s.reattaching = sup.Reattach
		s.reapDone = make(chan struct{})
		s.exitSeen = make(chan struct{})
		s.cancelDone = make(chan struct{})
		s.stopDone = make(chan struct{})
	}
	return s
}

// Reattaching reports that this execution continues an existing worker:
// backends must not write the initial prompt or protocol handshake.
func (s *workerSession) Reattaching() bool { return s.mode == sessionSupervised && s.reattaching }

func (s *workerSession) StdoutPipe() (io.ReadCloser, error) {
	if s.mode == sessionLegacy {
		return s.cmd.StdoutPipe()
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("supervised stdout pipe: %w", err)
	}
	s.stdoutW = w
	return r, nil
}

func (s *workerSession) StdinPipe() (io.WriteCloser, error) {
	if s.mode == sessionLegacy {
		w, err := s.cmd.StdinPipe()
		if err == nil {
			s.stdinW = w
		}
		return w, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("supervised stdin pipe: %w", err)
	}
	s.stdinR = r
	return w, nil
}

func (s *workerSession) StderrPipe() (io.ReadCloser, error) {
	if s.mode == sessionLegacy {
		return s.cmd.StderrPipe()
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("supervised stderr pipe: %w", err)
	}
	s.stderrW = w
	return r, nil
}

// Start launches the worker. ctx is the execution context whose cancellation
// the session mirrors onto the worker (supervised mode); legacy mode keeps
// relying on exec.CommandContext's own wiring and ignores ctx.
func (s *workerSession) Start(ctx context.Context, logger *slog.Logger) error {
	if s.mode == sessionLegacy {
		if err := startOwnedProcessTree(s.cmd, logger); err != nil {
			return err
		}
		return nil
	}

	spec := LaunchSpec{
		RunID:   s.sup.RunID,
		TaskID:  s.sup.TaskID,
		Runtime: s.sup.Runtime,
		Path:    s.cmd.Path,
		Args:    s.cmd.Args,
		Env:     s.cmd.Env,
		Dir:     s.cmd.Dir,
	}
	if len(spec.Args) > 0 {
		// exec.Cmd.Args[0] is the argv[0] convention; the launcher rebuilds
		// it from Path itself.
		spec.Args = spec.Args[1:]
	}

	var handle WorkerHandle
	var err error
	if s.reattaching {
		handle, err = s.sup.Supervisor.Reattach(ctx, spec.RunID)
	} else {
		handle, err = s.sup.Supervisor.Launch(ctx, spec)
	}
	if err != nil {
		return fmt.Errorf("supervised launch: %w", err)
	}
	s.handle = handle

	s.startCtx, s.startCancel = context.WithCancel(ctx)
	s.pumpStdout(logger)
	s.pumpStderr(logger)
	s.pumpStdin()
	s.armCancelDriver(logger)

	if s.reattaching {
		logger.Info("reattached supervised worker", "run_id", spec.RunID, "pid", handle.PID())
	} else {
		logger.Info("launched supervised worker", "run_id", spec.RunID, "pid", handle.PID())
	}
	return nil
}

// PID reports the worker leader PID. In supervised mode it is the supervisor's
// last observed value, valid only after Start.
func (s *workerSession) PID() int {
	if s.mode == sessionLegacy {
		if s.cmd.Process != nil {
			return s.cmd.Process.Pid
		}
		return 0
	}
	if s.handle != nil {
		return s.handle.PID()
	}
	return 0
}

// Signal sends sig to the worker's whole process group.
func (s *workerSession) Signal(sig syscall.Signal) error {
	if s.mode == sessionLegacy {
		signalProcessGroup(s.cmd, sig)
		return nil
	}
	if s.handle == nil {
		return errors.New("supervised signal before start")
	}
	return s.handle.Signal(sig)
}

// CloseStdin delivers EOF on the worker's stdin. Idempotent.
func (s *workerSession) CloseStdin() error {
	if s.mode == sessionLegacy {
		if s.stdinW == nil {
			return nil
		}
		err := s.stdinW.Close()
		s.stdinW = nil
		return err
	}
	var err error
	s.closeOnce.Do(func() {
		if s.handle != nil {
			err = s.handle.CloseStdin()
		}
		if s.stdinR != nil {
			// Stop the pump so a later backend Write surfaces as EPIPE
			// instead of hanging on a pipe nobody drains.
			_ = s.stdinR.Close()
		}
	})
	return err
}

// Wait blocks until the worker exits and its output streams have been
// drained. The returned error mirrors os/exec wording ("exit status N" /
// "signal: killed") so failure classification keeps working; nil on exit 0.
func (s *workerSession) Wait(ctx context.Context) error {
	if s.mode == sessionLegacy {
		// cmd.Wait only. The Windows Job Object release stays with the
		// backend: codex confirms whole-tree termination by reading the
		// job's accounting BETWEEN the reap and its own releaseProcessGroup
		// (cleanup_confirmed gates initialize retries), so a release here
		// would drop the job handle and delete the map entry before that
		// confirmation can observe anything, permanently reporting
		// cleanup as unconfirmed on Windows (RUYI-436). Every legacy
		// starter releases on its terminal path; this is the exact
		// ownership split that predated RUYI-349.
		return s.cmd.Wait()
	}
	if s.handle == nil {
		return errors.New("supervised wait before start")
	}
	s.waitOnce.Do(func() {
		// A cancelled execution context asks the cancel driver to stop the
		// worker; it is not exit evidence. Waiting with it would let a caller
		// return and acknowledge cancellation while the systemd unit still runs.
		waitCtx, cancelWait := context.WithCancel(context.Background())
		defer cancelWait()
		type waitOutcome struct {
			exit WorkerExit
			err  error
		}
		waitResult := make(chan waitOutcome, 1)
		go func() {
			exit, err := s.handle.Wait(waitCtx)
			waitResult <- waitOutcome{exit: exit, err: err}
		}()
		var outcome waitOutcome
		select {
		case outcome = <-waitResult:
		case <-s.cancelDone:
			if s.detaching() {
				cancelWait()
				s.waitResult = ErrDetached
				return
			}
			if err := s.sup.CancellationFailureError(); err != nil {
				cancelWait()
				s.waitResult = err
				return
			}
			outcome = <-waitResult
		}
		if outcome.err == nil {
			close(s.exitSeen)
		}
		if s.startCtx != nil && s.startCtx.Err() != nil && !s.detaching() {
			// SIGKILL may yield an exit record before Stop verifies that the
			// complete cgroup is gone. Let the cancellation sequence settle first.
			<-s.cancelDone
			if err := s.sup.CancellationFailureError(); err != nil {
				cancelWait()
				s.waitResult = err
				return
			}
		}
		exit, err := outcome.exit, outcome.err
		if err != nil {
			if !errors.Is(err, ErrDetached) {
				s.sup.MarkCancellationUnconfirmed(fmt.Errorf("supervised worker wait: %w", err))
				s.waitResult = s.sup.CancellationFailureError()
				return
			}
			s.waitResult = err
			return
		}
		if err := s.stopCgroup(nil); err != nil {
			s.waitResult = s.sup.CancellationFailureError()
			return
		}
		// The handle returned a recorded exit, so the cancel driver can no
		// longer have a live target and the daemon may later acknowledge stop.
		close(s.reapDone)
		s.sup.MarkWorkerExitConfirmed()
		if s.startCtx != nil && s.startCtx.Err() != nil && !s.detaching() {
			if err := s.sup.CancellationConfirmationError(); err != nil {
				s.waitResult = err
				return
			}
		}
		if s.startCancel != nil {
			s.startCancel()
		}
		// Normal-exit path: the streams EOF on their own and the pumps
		// finish, carrying every last byte to the backend's readers. Cancel
		// and detach paths: a stream read may be parked on bytes that will
		// never arrive, so the pumps get a bounded grace and are then
		// abandoned — the data that matters there is already lost anyway.
		pumpsDone := make(chan struct{})
		go func() {
			s.pumpWG.Wait()
			close(pumpsDone)
		}()
		select {
		case <-pumpsDone:
		case <-time.After(pumpDrainGrace):
		}
		switch {
		case exit.Code != 0 || exit.Signal != "":
			s.waitResult = &WorkerExitError{Exit: exit}
		default:
			s.waitResult = nil
		}
	})
	return s.waitResult
}

// stopCgroup waits for the transient unit's complete cgroup to disappear. A
// launcher exit record only proves the leader exited; a remaining descendant
// can still keep the unit alive. The shared operation also serializes a normal
// exit with cancellation's SIGKILL→Stop path.
func (s *workerSession) stopCgroup(beforeStop func()) error {
	s.stopMu.Lock()
	if s.stopStarted {
		s.stopMu.Unlock()
		<-s.stopDone
		return s.stopErr
	}
	s.stopStarted = true
	s.stopMu.Unlock()

	s.sup.BeginCancellationStop()
	if beforeStop != nil {
		beforeStop()
	}
	s.stopErr = s.handle.Stop()
	s.sup.EndCancellationStop(s.stopErr)
	if s.stopErr != nil {
		s.sup.MarkCancellationUnconfirmed(fmt.Errorf("supervised worker cgroup stop: %w", s.stopErr))
	}
	close(s.stopDone)
	return s.stopErr
}

// pumpDrainGrace bounds Wait's wait for the stream pumps on cancel/detach
// paths. Generous enough that a healthy EOF race never loses trailing output.
const pumpDrainGrace = 2 * time.Second

// WorkerExitError is a supervised worker's non-zero proven exit. ExitCode
// mirrors exec.ExitError for callers that introspect it.
type WorkerExitError struct {
	Exit WorkerExit
}

func (e *WorkerExitError) Error() string { return e.Exit.Error() }

func (e *WorkerExitError) ExitCode() int {
	if e.Exit.Code >= 0 {
		return e.Exit.Code
	}
	return -1
}

// detach ends this daemon's management of a still-running supervised worker
// without stopping it. Legacy mode has nothing to detach.
func (s *workerSession) detach() error {
	if s.mode != sessionSupervised || s.handle == nil {
		return nil
	}
	if s.startCancel != nil {
		s.startCancel()
	}
	return s.handle.Detach()
}

// armCancelDriver mirrors os/exec's context semantics onto the supervised
// worker: when the execution context is cancelled the worker is stopped —
// graceful first (SIGTERM to the group, bounded by cmd.WaitDelay when the
// backend set one), then SIGKILL. When the daemon itself is shutting down the
// supervisor's detaching switch wins: the worker is left running and the next
// daemon reconciles it.
func (s *workerSession) armCancelDriver(logger *slog.Logger) {
	ctx := s.startCtx
	if ctx == nil {
		return
	}
	grace := s.cmd.WaitDelay
	go func() {
		<-ctx.Done()
		s.driverOnce.Do(func() {
			defer close(s.cancelDone)
			if s.detaching() {
				_ = s.detach()
				return
			}
			markFailure := func(action string, err error) {
				if err == nil {
					return
				}
				failure := fmt.Errorf("supervised worker %s: %w", action, err)
				s.sup.MarkCancellationUnconfirmed(failure)
				logger.Error("supervised worker cancellation is unconfirmed", "action", action, "error", err)
			}
			forceStop := func() {
				// Signal retains the backend's established process-group contract;
				// Stop then kills the complete transient-unit cgroup and waits for
				// its disappearance before recording an exit.
				stopErr := s.stopCgroup(func() {
					markFailure("SIGKILL", s.Signal(syscall.SIGKILL))
				})
				markFailure("cgroup stop", stopErr)
			}
			// A reaped worker is never a kill target: the natural-exit
			// path proves the exit before any cancellation fan-out. Past
			// this point the check is best-effort — a worker exiting in
			// the race window just receives a no-op signal, as before.
			select {
			case <-s.reapDone:
				return
			default:
			}
			if grace <= 0 {
				forceStop()
				return
			}
			if err := s.Signal(syscall.SIGTERM); err != nil {
				markFailure("SIGTERM", err)
				forceStop()
				return
			}
			timer := time.NewTimer(grace)
			defer timer.Stop()
			select {
			case <-s.reapDone:
			case <-s.exitSeen:
			case <-timer.C:
				forceStop()
			}
		})
	}()
}

func (s *workerSession) detaching() bool {
	sd, ok := s.sup.Supervisor.(ShutdownDetaching)
	return ok && sd.ShutdownDetaching()
}

func (s *workerSession) pumpStdout(logger *slog.Logger) {
	if s.stdoutW == nil {
		// Backend never asked for stdout; drain the stream so the worker's
		// append-only log never doubles as a blocking queue. Only THIS
		// stream: each pump owns exactly one stream, so a missing stderr
		// sink can never steal stdout bytes (and vice versa).
		s.pumpWG.Add(1)
		go func() {
			defer s.pumpWG.Done()
			s.drainOne(s.handle.StdoutStream)
		}()
		return
	}
	w := s.stdoutW
	s.pumpWG.Add(1)
	go func() {
		defer s.pumpWG.Done()
		defer w.Close()
		stream, err := s.handle.StdoutStream(s.startCtx)
		if err != nil {
			logger.Error("supervised stdout stream unavailable", "error", err)
			return
		}
		defer stream.Close()
		_, _ = io.Copy(w, stream)
	}()
}

func (s *workerSession) pumpStderr(logger *slog.Logger) {
	// Two stderr sinks exist across backends: an explicit StderrPipe reader
	// (stderrW) or a writer planted on cmd.Stderr before Start. Route the
	// worker's stderr stream to whichever the backend wired; a backend that
	// wired neither still gets its stderr drained.
	if s.stderrW == nil && s.cmd.Stderr == nil {
		s.pumpWG.Add(1)
		go func() {
			defer s.pumpWG.Done()
			s.drainOne(s.handle.StderrStream)
		}()
		return
	}
	s.pumpWG.Add(1)
	go func() {
		defer s.pumpWG.Done()
		stream, err := s.handle.StderrStream(s.startCtx)
		if err != nil {
			logger.Error("supervised stderr stream unavailable", "error", err)
			return
		}
		defer stream.Close()
		if s.stderrW != nil {
			defer s.stderrW.Close()
			_, _ = io.Copy(s.stderrW, stream)
			return
		}
		_, _ = io.Copy(s.cmd.Stderr, stream)
	}()
}

func (s *workerSession) pumpStdin() {
	if s.stdinR == nil {
		return
	}
	r := s.stdinR
	s.pumpWG.Add(1)
	go func() {
		defer s.pumpWG.Done()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := r.Read(buf)
			if n > 0 && s.handle != nil {
				if _, werr := s.handle.Stdin().Write(buf[:n]); werr != nil && rerr == nil {
					rerr = werr
				}
			}
			if rerr != nil {
				// EOF here is the BACKEND closing its end of the bridge
				// pipe (claude.go closes its StdinPipe the moment it sees
				// the final result). Legacy mode delivers EOF to the worker
				// for free from the closed anonymous pipe; supervised mode
				// must forward it through the supervisor, or stream-JSON
				// CLIs that idle after their result never exit and the run
				// strands in running (RUYI-424). Idempotent: an explicit
				// session CloseStdin has already fired closeOnce, making
				// this a no-op.
				if errors.Is(rerr, io.EOF) {
					_ = s.CloseStdin()
				}
				return
			}
		}
	}()
}

// drainOne consumes one supervised stream that no sink wants, keeping the
// worker's log from doubling as a blocking queue. Strictly one stream per
// call: a second opener on the same stream (fake handles hand out the same
// fd; log tails re-read the same file) would race the stream's real reader.
func (s *workerSession) drainOne(open func(context.Context) (io.ReadCloser, error)) {
	if s.handle == nil {
		return
	}
	stream, err := open(s.startCtx)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, stream)
	stream.Close()
}
