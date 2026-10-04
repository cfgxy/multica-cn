package supervisor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// Supervisor is the systemd-backed agent.WorkerSupervisor: it launches each
// task worker inside a transient user unit and hands the daemon a handle
// whose I/O and lifecycle survive daemon death.
type Supervisor struct {
	mgr     *Manager
	sys     *systemdCtl
	binPath string
	log     *slog.Logger

	detaching atomic.Bool
}

// New wires a Supervisor. binPath is the daemon binary re-executed as the
// launcher (os.Executable() of the daemon process).
func New(mgr *Manager, sys *systemdCtl, binPath string, log *slog.Logger) (*Supervisor, error) {
	if binPath == "" {
		return nil, fmt.Errorf("supervisor: empty daemon binary path")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Supervisor{mgr: mgr, sys: sys, binPath: binPath, log: log}, nil
}

// Manager exposes the run registry (reconciliation, cleanup, tests).
func (s *Supervisor) Manager() *Manager { return s.mgr }

// Systemd exposes the systemd wrapper (reconciliation, tests).
func (s *Supervisor) Systemd() *systemdCtl { return s.sys }

// SetDetaching flips the graceful-shutdown switch: from now on, session
// cancellation detaches instead of killing (daemon is going away).
func (s *Supervisor) SetDetaching() { s.detaching.Store(true) }

// ShutdownDetaching implements agent.ShutdownDetaching.
func (s *Supervisor) ShutdownDetaching() bool { return s.detaching.Load() }

// Launch starts the worker under unit multica-run-<run_id>.service.
func (s *Supervisor) Launch(ctx context.Context, spec agent.LaunchSpec) (agent.WorkerHandle, error) {
	if err := ValidateRunID(spec.RunID); err != nil {
		return nil, err
	}
	dir := s.mgr.Dir(spec.RunID)
	man := &Manifest{
		Version:       1,
		RunID:         spec.RunID,
		TaskID:        spec.TaskID,
		Runtime:       spec.Runtime,
		Unit:          UnitName(spec.RunID),
		State:         StateRunning,
		ControlSocket: controlSocketPath(dir),
		StdoutLog:     stdoutLogPath(dir),
		StderrLog:     stderrLogPath(dir),
		StartedAt:     time.Now().UTC(),
	}
	if err := s.mgr.WriteManifest(man); err != nil {
		return nil, fmt.Errorf("supervisor: write manifest: %w", err)
	}
	lspec := &launchSpec{
		Version:   1,
		RunID:     spec.RunID,
		Path:      spec.Path,
		Args:      spec.Args,
		Env:       spec.Env,
		Dir:       spec.Dir,
		StdoutLog: man.StdoutLog,
		StderrLog: man.StderrLog,
		Socket:    man.ControlSocket,
		Manifest:  manifestPath(dir),
	}
	specPath, err := s.mgr.WriteSpec(spec.RunID, lspec)
	if err != nil {
		return nil, fmt.Errorf("supervisor: write spec: %w", err)
	}
	if err := s.sys.startUnit(ctx, man.Unit, specPath, s.binPath); err != nil {
		return nil, err
	}
	client, err := DialControl(ctx, man.ControlSocket, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("supervisor: control dial after start %s: %w", spec.RunID, err)
	}
	return s.newHandle(man, client), nil
}

// Reattach binds to a previously launched run. It serves both reconciliation
// targets: a still-running worker (control socket live) and a worker that
// exited while the daemon was down (nil client; output and exit come from the
// manifest and log files).
func (s *Supervisor) Reattach(ctx context.Context, runID string) (agent.WorkerHandle, error) {
	man, err := s.mgr.ReadManifest(runID)
	if err != nil {
		return nil, fmt.Errorf("supervisor: reattach read manifest %s: %w", runID, err)
	}
	if man.State == StateExited || man.Exit != nil {
		return s.newHandle(man, nil), nil
	}
	client, err := DialControl(ctx, man.ControlSocket, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("supervisor: reattach dial %s (unit dead without exit evidence): %w", runID, err)
	}
	return s.newHandle(man, client), nil
}

func (s *Supervisor) newHandle(man *Manifest, client *ControlClient) *supervisedHandle {
	h := &supervisedHandle{
		sup:      s,
		man:      *man,
		client:   client,
		pumpDone: make(chan struct{}),
	}
	if client != nil {
		go func() {
			client.Pump()
			close(h.pumpDone)
		}()
	} else {
		close(h.pumpDone)
	}
	return h
}

// supervisedHandle is the daemon-side end of one supervised worker.
type supervisedHandle struct {
	sup    *Supervisor
	man    Manifest
	client *ControlClient

	pumpDone chan struct{}

	mu           sync.Mutex
	detached     bool
	manifestExit *WorkerExit
}

var _ agent.WorkerHandle = (*supervisedHandle)(nil)

func (h *supervisedHandle) PID() int { return h.man.WorkerPID }

func (h *supervisedHandle) Stdin() io.Writer { return stdinWriter{h} }

type stdinWriter struct{ h *supervisedHandle }

func (w stdinWriter) Write(p []byte) (int, error) {
	if w.h.client == nil {
		return 0, fmt.Errorf("supervisor: run %s has no live control connection", w.h.man.RunID)
	}
	return w.h.client.WriteStdin(context.Background(), p)
}

func (h *supervisedHandle) CloseStdin() error {
	if h.client == nil {
		return nil // a dead worker's stdin is trivially at EOF
	}
	return h.client.CloseStdin()
}

func (h *supervisedHandle) StdoutStream(ctx context.Context) (io.ReadCloser, error) {
	return newResumableTail(h.sup.mgr, h.man.RunID, h.man.StdoutLog, true, h.provenExit), nil
}

func (h *supervisedHandle) StderrStream(ctx context.Context) (io.ReadCloser, error) {
	return newResumableTail(h.sup.mgr, h.man.RunID, h.man.StderrLog, false, h.provenExit), nil
}

// provenExit resolves terminal evidence in source order: the live control
// connection first (the launcher's own observation), then the manifest (also
// covers supervisor-initiated stops and post-mortem convergence).
func (h *supervisedHandle) provenExit() *WorkerExit {
	if h.client != nil {
		if e := h.client.ObservedExit(); e != nil {
			return e
		}
	}
	h.mu.Lock()
	if h.manifestExit != nil {
		e := *h.manifestExit
		h.mu.Unlock()
		return &e
	}
	h.mu.Unlock()
	man, err := readManifestQuiet(manifestPath(h.sup.mgr.Dir(h.man.RunID)))
	if err != nil || man.Exit == nil {
		return nil
	}
	e := WorkerExit{Code: man.Exit.Code, Signal: man.Exit.Signal}
	h.mu.Lock()
	h.manifestExit = &e
	h.mu.Unlock()
	return &e
}

func (h *supervisedHandle) Wait(ctx context.Context) (WorkerExit, error) {
	if h.client == nil {
		if e := h.provenExit(); e != nil {
			return *e, nil
		}
		return WorkerExit{}, fmt.Errorf("supervisor: run %s reattached without control or exit evidence", h.man.RunID)
	}
	select {
	case <-h.pumpDone:
	case <-ctx.Done():
		return WorkerExit{}, ctx.Err()
	}
	if h.isDetached() {
		return WorkerExit{}, agent.ErrDetached
	}
	if e := h.client.ObservedExit(); e != nil {
		return *e, nil
	}
	// The connection died without an exit frame: either the supervisor
	// stopped the unit (launcher killed mid-flight) or the launcher itself
	// crashed. Terminal evidence is then the manifest record the stop path
	// writes — poll for it.
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if e := h.provenExit(); e != nil {
			return *e, nil
		}
		select {
		case <-ctx.Done():
			return WorkerExit{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (h *supervisedHandle) Signal(sig syscall.Signal) error {
	if h.client == nil {
		return fmt.Errorf("supervisor: run %s has no live control connection", h.man.RunID)
	}
	return h.client.Signal(int(sig))
}

// Stop hard-kills the unit's whole cgroup (the precise cancel primitive) and
// records the exit evidence the launcher can no longer write — its own death
// IS the evidence that a stop happened.
func (h *supervisedHandle) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := h.sup.sys.KillUnit(ctx, h.man.Unit); err != nil {
		return err
	}
	// Wait until systemd reports the unit gone, so a racing launcher cannot
	// resurrect state after we record the exit.
	deadline := time.Now().Add(10 * time.Second)
	for {
		active, err := h.sup.sys.UnitActive(ctx, h.man.Unit)
		if err == nil && !active {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	man, err := h.sup.mgr.ReadManifest(h.man.RunID)
	if err != nil {
		return fmt.Errorf("supervisor: post-stop manifest %s: %w", h.man.RunID, err)
	}
	if man.Exit == nil {
		man.State = StateExited
		man.Exit = &ExitRecord{
			Code:   -1,
			Signal: "killed",
			At:     time.Now().UTC(),
			Source: ExitSourceSupervisor,
		}
		if err := h.sup.mgr.WriteManifest(man); err != nil {
			return fmt.Errorf("supervisor: post-stop exit record %s: %w", h.man.RunID, err)
		}
	}
	return nil
}

// Detach leaves the worker running for the next daemon (graceful shutdown).
func (h *supervisedHandle) Detach() error {
	h.mu.Lock()
	h.detached = true
	h.mu.Unlock()
	if h.client != nil {
		h.client.Close()
	}
	return nil
}

func (h *supervisedHandle) isDetached() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.detached
}

// Paths inside a run directory. Shared with manifest.go constants implicitly:
// every name is derived from the run dir, never from user input.
func manifestPath(dir string) string      { return dir + "/manifest.json" }
func controlSocketPath(dir string) string { return dir + "/control.sock" }
func stdoutLogPath(dir string) string     { return dir + "/worker-stdout.log" }
func stderrLogPath(dir string) string     { return dir + "/worker-stderr.log" }
