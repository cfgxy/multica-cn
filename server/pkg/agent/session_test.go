package agent

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeSupervisor is a scripted WorkerSupervisor: Launch reports a scripted
// exit and exposes controllable streams, so session behavior is driven
// without a real supervisor implementation.
type fakeSupervisor struct {
	mu sync.Mutex

	launches   []LaunchSpec
	launchGate chan struct{} // closed → Launch returns
	handle     *fakeHandle

	detaching bool
}

type fakeHandle struct {
	mu sync.Mutex

	spec      LaunchSpec
	pid       int
	stdinBuf  strings.Builder
	stdinCl   bool
	stdoutR   *os.File // reader side handed to StdoutStream
	stdoutW   *os.File
	stderrR   *os.File
	stderrW   *os.File
	exit      *WorkerExit
	exitErr   error
	waitCh    chan struct{}
	detached  bool
	stopped   bool
	signalled []syscall.Signal
}

func (h *fakeHandle) PID() int { return h.pid }

func (h *fakeHandle) Stdin() io.Writer { return writerFuncs{h} }

func (h *fakeHandle) CloseStdin() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stdinCl = true
	return nil
}

func (h *fakeHandle) StdoutStream(ctx context.Context) (io.ReadCloser, error) {
	h.mu.Lock()
	r := h.stdoutR
	h.mu.Unlock()
	if r == nil {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return r, nil
}

func (h *fakeHandle) StderrStream(ctx context.Context) (io.ReadCloser, error) {
	h.mu.Lock()
	r := h.stderrR
	h.mu.Unlock()
	if r == nil {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return r, nil
}

func (h *fakeHandle) Wait(ctx context.Context) (WorkerExit, error) {
	<-h.waitCh
	// A real supervisor's tailer ends its stream with EOF once the exit is
	// proven; mimic that so pumps unblock the way they do in production.
	h.closeStreams()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.exitErr != nil {
		return WorkerExit{}, h.exitErr
	}
	if h.exit != nil {
		return *h.exit, nil
	}
	return WorkerExit{}, ErrDetached
}

func (h *fakeHandle) closeStreams() {
	h.mu.Lock()
	outR, errR := h.stdoutR, h.stderrR
	h.stdoutR, h.stderrR = nil, nil
	h.mu.Unlock()
	if outR != nil {
		outR.Close()
	}
	if errR != nil {
		errR.Close()
	}
}

func (h *fakeHandle) Signal(sig syscall.Signal) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.signalled = append(h.signalled, sig)
	return nil
}

func (h *fakeHandle) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopped = true
	return nil
}

func (h *fakeHandle) Detach() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.detached = true
	select {
	case <-h.waitCh:
	default:
		close(h.waitCh)
	}
	return nil
}

type writerFuncs struct{ h *fakeHandle }

func (w writerFuncs) Write(p []byte) (int, error) {
	w.h.mu.Lock()
	defer w.h.mu.Unlock()
	if w.h.stdinCl {
		return 0, syscall.EPIPE
	}
	return w.h.stdinBuf.Write(p)
}

func (f *fakeSupervisor) ShutdownDetaching() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.detaching
}

func (f *fakeSupervisor) Launch(ctx context.Context, spec LaunchSpec) (WorkerHandle, error) {
	f.mu.Lock()
	f.launches = append(f.launches, spec)
	gate := f.launchGate
	h := f.handle
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return h, nil
}

func (f *fakeSupervisor) Reattach(ctx context.Context, runID string) (WorkerHandle, error) {
	return f.handle, nil
}

func newFakeSupervisor(t *testing.T) (*fakeSupervisor, *fakeHandle) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
	})
	sup := &fakeSupervisor{launchGate: make(chan struct{})}
	close(sup.launchGate)
	h := &fakeHandle{
		pid:     4242,
		stdoutR: outR,
		stdoutW: outW,
		stderrR: errR,
		stderrW: errW,
		waitCh:  make(chan struct{}),
	}
	sup.handle = h
	return sup, h
}

func TestWorkerSessionLegacyPassthroughRunsRealProcess(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", "echo hello-legacy; exit 0")
	sess := newWorkerSession(cmd, nil)
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Start(context.Background(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "hello-legacy" {
		t.Fatalf("legacy stdout = %q, want hello-legacy", got)
	}
	if err := sess.Wait(context.Background()); err != nil {
		t.Fatalf("legacy wait: %v", err)
	}
	if sess.Reattaching() {
		t.Fatal("legacy session must never report reattaching")
	}
}

func TestWorkerSessionLegacyNonZeroExitMirrorsExec(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", "exit 3")
	sess := newWorkerSession(cmd, nil)
	if err := sess.Start(context.Background(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	err := sess.Wait(context.Background())
	if err == nil {
		t.Fatal("want error for exit 3")
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("error wording drifted from exec: %v", err)
	}
}

func TestWorkerSessionSupervisedLaunchWiresStreams(t *testing.T) {
	sup, h := newFakeSupervisor(t)
	cmd := exec.Command("fake-cli", "--flag", "value")
	cmd.Dir = "/tmp"
	cmd.Env = []string{"A=1"}
	sess := newWorkerSession(cmd, &Supervision{
		Supervisor: sup,
		RunID:      "run-1",
		TaskID:     "task-1",
		Runtime:    "claude",
	})

	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.StderrPipe(); err != nil {
		t.Fatal(err)
	}
	if err := sess.Start(context.Background(), slog.Default()); err != nil {
		t.Fatal(err)
	}

	// Launch spec carries the backend-built command, argv[0] stripped.
	if len(sup.launches) != 1 {
		t.Fatalf("launches = %d, want 1", len(sup.launches))
	}
	spec := sup.launches[0]
	if spec.Path != "fake-cli" || len(spec.Args) != 2 || spec.Args[0] != "--flag" {
		t.Fatalf("spec argv = %q %q", spec.Path, spec.Args)
	}
	if spec.Dir != "/tmp" || spec.RunID != "run-1" || spec.TaskID != "task-1" {
		t.Fatalf("spec identity = %+v", spec)
	}
	if sess.PID() != 4242 {
		t.Fatalf("PID = %d, want 4242", sess.PID())
	}

	// Worker output flows back through the backend's pipe.
	if _, err := h.stdoutW.WriteString("frame-1\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := stdout.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("stdout read = %d, %v", n, err)
	}
	if !strings.HasPrefix(string(buf[:n]), "frame-1") {
		t.Fatalf("stdout = %q", buf[:n])
	}

	// Backend stdin writes reach the supervisor bridge.
	if _, err := io.WriteString(stdin, "prompt-bytes"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		got := h.stdinBuf.String()
		h.mu.Unlock()
		if got == "prompt-bytes" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.mu.Lock()
	got := h.stdinBuf.String()
	h.mu.Unlock()
	if got != "prompt-bytes" {
		t.Fatalf("stdin bridge = %q", got)
	}

	// Exit 0 → Wait nil, stdout EOF, streams torn down.
	h.mu.Lock()
	h.exit = &WorkerExit{Code: 0}
	close(h.waitCh)
	h.mu.Unlock()
	if err := sess.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if _, err := stdout.Read(buf); err == nil {
		t.Fatal("stdout should be EOF after worker exit")
	}
}

func TestWorkerSessionSupervisedExitCodeSurfaces(t *testing.T) {
	sup, h := newFakeSupervisor(t)
	cmd := exec.Command("fake-cli")
	sess := newWorkerSession(cmd, &Supervision{Supervisor: sup, RunID: "run-1", TaskID: "t", Runtime: "claude"})
	if _, err := sess.StdoutPipe(); err != nil {
		t.Fatal(err)
	}
	if err := sess.Start(context.Background(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.exit = &WorkerExit{Code: 3}
	close(h.waitCh)
	h.mu.Unlock()
	err := sess.Wait(context.Background())
	if err == nil {
		t.Fatal("want error for exit 3")
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("exit wording = %v", err)
	}
	var exitErr *WorkerExitError
	if !asWorkerExitError(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("WorkerExitError not introspectable: %v", err)
	}
}

func asWorkerExitError(err error, target **WorkerExitError) bool {
	for err != nil {
		if e, ok := err.(*WorkerExitError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestWorkerSessionCancelDriverKillsWithoutGrace(t *testing.T) {
	sup, h := newFakeSupervisor(t)
	cmd := exec.Command("fake-cli") // no WaitDelay → immediate SIGKILL on cancel
	sess := newWorkerSession(cmd, &Supervision{Supervisor: sup, RunID: "run-1", TaskID: "t", Runtime: "claude"})
	if _, err := sess.StdoutPipe(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := sess.Start(ctx, slog.Default()); err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.After(2 * time.Second)
	for {
		h.mu.Lock()
		got := append([]syscall.Signal(nil), h.signalled...)
		h.mu.Unlock()
		if len(got) > 0 && got[len(got)-1] == syscall.SIGKILL {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("SIGKILL never delivered; got %v", got)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestWorkerSessionCancelDriverGracefulThenKill(t *testing.T) {
	sup, h := newFakeSupervisor(t)
	cmd := exec.Command("fake-cli")
	cmd.WaitDelay = 50 * time.Millisecond
	sess := newWorkerSession(cmd, &Supervision{Supervisor: sup, RunID: "run-1", TaskID: "t", Runtime: "claude"})
	if _, err := sess.StdoutPipe(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := sess.Start(ctx, slog.Default()); err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.After(3 * time.Second)
	sawTERM, sawKILL := false, false
	for {
		h.mu.Lock()
		for _, sig := range h.signalled {
			if sig == syscall.SIGTERM {
				sawTERM = true
			}
			if sig == syscall.SIGKILL {
				sawKILL = true
			}
		}
		h.mu.Unlock()
		if sawTERM && sawKILL {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("graceful sequence incomplete: TERM=%v KILL=%v", sawTERM, sawKILL)
		case <-time.After(5 * time.Millisecond):
		}
	}
	if !sawTERM || !sawKILL {
		t.Fatalf("TERM=%v KILL=%v", sawTERM, sawKILL)
	}
	// Grace honored the worker exiting: if the fake reports exit quickly the
	// driver must not have needed the full wait — implied by both signals
	// being the only escalation path asserted above.
}

func TestWorkerSessionShutdownDetachLeavesWorkerAlone(t *testing.T) {
	sup, h := newFakeSupervisor(t)
	cmd := exec.Command("fake-cli")
	sess := newWorkerSession(cmd, &Supervision{Supervisor: sup, RunID: "run-1", TaskID: "t", Runtime: "claude"})
	if _, err := sess.StdoutPipe(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := sess.Start(ctx, slog.Default()); err != nil {
		t.Fatal(err)
	}
	sup.mu.Lock()
	sup.detaching = true
	sup.mu.Unlock()
	cancel()
	if err := sess.Wait(context.Background()); !errIs(err, ErrDetached) {
		t.Fatalf("wait err = %v, want ErrDetached", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.detached {
		t.Fatal("handle not detached")
	}
	if len(h.signalled) != 0 {
		t.Fatalf("worker signalled during detach: %v", h.signalled)
	}
}

func errIs(err, target error) bool {
	if err == nil {
		return false
	}
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestWorkerSessionCloseStdinDeliversEOFOnce(t *testing.T) {
	sup, h := newFakeSupervisor(t)
	cmd := exec.Command("fake-cli")
	sess := newWorkerSession(cmd, &Supervision{Supervisor: sup, RunID: "run-1", TaskID: "t", Runtime: "claude"})
	if _, err := sess.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	if err := sess.Start(context.Background(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	if err := sess.CloseStdin(); err != nil {
		t.Fatal(err)
	}
	// Idempotent.
	if err := sess.CloseStdin(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		closed := h.stdinCl
		h.mu.Unlock()
		if closed {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("stdin EOF never reached the supervisor")
}

// TestWorkerSessionBackendStdinCloseDeliversWorkerEOF pins the RUYI-424
// regression: the backend closing ITS end of the supervised stdin bridge
// must deliver EOF to the worker through the supervisor. The claude CLI's
// stream-json input mode idles after emitting its final result and only
// exits on stdin EOF — a dropped EOF strands the run in "running" forever.
func TestWorkerSessionBackendStdinCloseDeliversWorkerEOF(t *testing.T) {
	sup, h := newFakeSupervisor(t)
	cmd := exec.Command("fake-cli")
	sess := newWorkerSession(cmd, &Supervision{Supervisor: sup, RunID: "run-1", TaskID: "t", Runtime: "claude"})
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Start(context.Background(), slog.Default()); err != nil {
		t.Fatal(err)
	}

	// claude stream-json protocol: the worker emits its final result while
	// staying alive, waiting for the next input frame or stdin EOF.
	if _, err := h.stdoutW.WriteString(`{"type":"result","result":"done"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	resultSeen := make(chan struct{})
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := stdout.Read(buf)
			if n > 0 && strings.Contains(string(buf[:n]), `"type":"result"`) {
				close(resultSeen)
				return
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-resultSeen:
	case <-time.After(10 * time.Second):
		t.Fatal("result frame never reached the backend scanner")
	}

	// The backend closes its stdin pipe the moment it sees the result —
	// exactly what claude.go's closeStdin does. The pump must forward this
	// as stdin EOF; the worker never writes another byte to the bridge.
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}

	// The worker exits only after stdin EOF reaches it (claude protocol),
	// so poll for delivery before releasing the scripted exit.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		closed := h.stdinCl
		h.mu.Unlock()
		if closed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.mu.Lock()
	closed := h.stdinCl
	h.mu.Unlock()
	if !closed {
		t.Fatal("backend stdin close never reached the worker — claude would idle after result forever (RUYI-424)")
	}
	h.mu.Lock()
	h.exit = &WorkerExit{Code: 0}
	close(h.waitCh)
	h.mu.Unlock()

	waitDone := make(chan error, 1)
	go func() { waitDone <- sess.Wait(context.Background()) }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("wait after clean exit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("sess.Wait never returned — the zombie-run shape this test exists to prevent")
	}
}

func TestWorkerSessionReattachSkipsLaunch(t *testing.T) {
	sup, _ := newFakeSupervisor(t)
	cmd := exec.Command("fake-cli", "--resume", "abc")
	sess := newWorkerSession(cmd, &Supervision{
		Supervisor: sup,
		RunID:      "run-9",
		TaskID:     "task-9",
		Runtime:    "claude",
		Reattach:   true,
	})
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Start(context.Background(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	if !sess.Reattaching() {
		t.Fatal("reattaching flag lost")
	}
	if len(sup.launches) != 0 {
		t.Fatalf("reattach launched a new worker: %+v", sup.launches)
	}
	// Stream still reachable (parse loop reads the tail).
	if _, err := sup.handle.stdoutW.WriteString("tail-frame\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	done := make(chan error, 1)
	go func() {
		_, err := stdout.Read(buf)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reattach stream read: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reattach stream read timed out")
	}
}
