package supervisor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// startFakeLauncher runs a control server the way the launcher does: worker
// stdin is an os.Pipe, signals land in a recorded slice, exit is push-fed.
type fakeLauncher struct {
	stdinR  *os.File
	stdinW  io.WriteCloser
	signals []syscall.Signal
	sigMu   sync.Mutex
	exitCh  chan WorkerExit
	srvErr  chan error
	path    string
}

func startFakeLauncher(t *testing.T) *fakeLauncher {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeLauncher{
		stdinR: r,
		stdinW: w,
		exitCh: make(chan WorkerExit, 1),
		srvErr: make(chan error, 1),
		path:   filepath.Join(t.TempDir(), "control.sock"),
	}
	go func() {
		f.srvErr <- serveControl(f.path, f.stdinW, 4242, f.exitCh, func(sig syscall.Signal) {
			f.sigMu.Lock()
			f.signals = append(f.signals, sig)
			f.sigMu.Unlock()
		})
	}()
	t.Cleanup(func() {
		// Make sure awaitExit always has a value (a real one if the test sent
		// it, a zero one from the closed channel otherwise), then tear down.
		select {
		case f.exitCh <- WorkerExit{}:
		default:
		}
		close(f.exitCh)
		f.stdinR.Close()
		f.stdinW.Close()
	})
	return f
}

func (f *fakeLauncher) recordedSignals() []syscall.Signal {
	f.sigMu.Lock()
	defer f.sigMu.Unlock()
	return append([]syscall.Signal(nil), f.signals...)
}

func readPipeTimeout(t *testing.T, r *os.File, n int) string {
	t.Helper()
	type res struct {
		s   string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		buf := make([]byte, n)
		got, err := r.Read(buf)
		ch <- res{string(buf[:got]), err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("stdin read: %v", r.err)
		}
		return r.s
	case <-time.After(2 * time.Second):
		t.Fatal("stdin read timed out")
		return ""
	}
}

func TestControlStdinSignalExitRoundTrip(t *testing.T) {
	f := startFakeLauncher(t)
	client, err := DialControl(context.Background(), f.path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	events := client.Events()
	go client.Pump()

	// ready frame arrives first.
	select {
	case ev := <-events:
		if ev.Ev != "ready" || ev.PID != 4242 {
			t.Fatalf("first event = %+v, want ready/4242", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ready event")
	}

	if _, err := client.WriteStdin(context.Background(), []byte("hello prompt")); err != nil {
		t.Fatal(err)
	}
	if got := readPipeTimeout(t, f.stdinR, 5); got != "hello" {
		t.Fatalf("stdin bridge got %q", got)
	}

	if err := client.Signal(int(syscall.SIGTERM)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sigs := f.recordedSignals(); len(sigs) == 1 && sigs[0] == syscall.SIGTERM {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sigs := f.recordedSignals(); len(sigs) != 1 || sigs[0] != syscall.SIGTERM {
		t.Fatalf("signals = %v, want [TERM]", sigs)
	}

	f.exitCh <- WorkerExit{Code: 3}
	select {
	case ev := <-events:
		if ev.Ev != "exit" || ev.Code != 3 {
			t.Fatalf("exit event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no exit event")
	}
	if e := client.ObservedExit(); e == nil || e.Code != 3 {
		t.Fatalf("ObservedExit = %+v", e)
	}
	select {
	case err := <-f.srvErr:
		if err != nil {
			t.Fatalf("serveControl: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down after exit")
	}
}

func TestControlReconnectLearnsExit(t *testing.T) {
	f := startFakeLauncher(t)
	first, err := DialControl(context.Background(), f.path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	go first.Pump()
	first.Events() // consume

	// Daemon "dies" (without exit observed), worker exits, daemon restarts.
	first.Close()
	f.exitCh <- WorkerExit{Code: 0}

	select {
	case <-f.srvErr:
		t.Fatal("server must keep serving late connections after exit")
	default:
	}

	second, err := DialControl(context.Background(), f.path, 2*time.Second)
	if err != nil {
		t.Fatalf("reconnect after worker exit: %v", err)
	}
	defer second.Close()
	events := second.Events()
	go second.Pump()
	select {
	case ev := <-events:
		if ev.Ev != "exit" || ev.Code != 0 {
			t.Fatalf("replayed event = %+v, want exit/0", ev)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("reconnected client did not learn the exit")
	}
	if e := second.ObservedExit(); e == nil || e.Code != 0 {
		t.Fatalf("reconnected ObservedExit = %+v", e)
	}
	select {
	case <-f.srvErr:
	case <-time.After(30 * time.Second):
		t.Fatal("server did not finish after exit replay")
	}
}

func TestControlStdinEOF(t *testing.T) {
	f := startFakeLauncher(t)
	client, err := DialControl(context.Background(), f.path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	go client.Pump()
	client.Events()

	if err := client.CloseStdin(); err != nil {
		t.Fatal(err)
	}
	// EOF on the write end: a pending reader gets 0, io.EOF.
	buf := make([]byte, 8)
	errCh := make(chan error, 1)
	go func() {
		_, err := f.stdinR.Read(buf)
		errCh <- err
	}()
	select {
	case err := <-errCh:
		if err != io.EOF {
			t.Fatalf("after stdin_eof read err = %v, want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stdin_eof did not reach the pipe")
	}
}
