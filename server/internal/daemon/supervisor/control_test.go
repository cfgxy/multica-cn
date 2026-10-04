package supervisor

import (
	"bufio"
	"context"
	"io"
	"net"
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

// TestControlReconnectLearnsExit pins the reconnect contract around the
// worker exit:
//   - a reconnect that lands before the exit is recorded gets the ready
//     snapshot and then the exit broadcast (a valid [ready, exit] order);
//   - the exit broadcast reaches the live reconnect;
//   - the exit ends the launcher lifecycle: the listener shuts down and a
//     late dial fails fast instead of hanging — a daemon that misses that
//     window learns the exit from the manifest (supervisedHandle.Wait).
//
// The previous shape dialed the reconnect after pushing the exit and
// asserted the first frame must be exit. That raced the dial against the
// listener close (backlog conns died unserved and the test hung) and
// outlawed the [ready, exit] order for conns served just before the exit.
func TestControlReconnectLearnsExit(t *testing.T) {
	f := startFakeLauncher(t)
	first, err := DialControl(context.Background(), f.path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	go first.Pump()
	first.Events() // consume

	// Daemon "dies" (without exit observed), then reconnects while the
	// worker is still alive.
	first.Close()
	second, err := DialControl(context.Background(), f.path, 2*time.Second)
	if err != nil {
		t.Fatalf("reconnect before worker exit: %v", err)
	}
	defer second.Close()
	events := second.Events()
	go second.Pump()
	select {
	case ev := <-events:
		if ev.Ev != "ready" || ev.PID != 4242 {
			t.Fatalf("reconnect first event = %+v, want ready/4242", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ready event on reconnect")
	}

	// Observing ready proves the server registered this conn (registration
	// precedes the ready send in serve), so the exit broadcast below is
	// guaranteed to include it.
	f.exitCh <- WorkerExit{Code: 0}
	select {
	case ev := <-events:
		if ev.Ev != "exit" || ev.Code != 0 {
			t.Fatalf("reconnect exit event = %+v, want exit/0", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconnected client did not learn the exit")
	}
	if e := second.ObservedExit(); e == nil || e.Code != 0 {
		t.Fatalf("reconnected ObservedExit = %+v", e)
	}

	select {
	case <-f.srvErr:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish after exit broadcast")
	}
	// The listener is gone with the launcher: a late dial must fail now.
	if c, err := net.Dial("unix", f.path); err == nil {
		c.Close()
		t.Fatal("control listener still answering after worker exit")
	}
}

// TestControlExitReplayToServedConnection pins serve()'s replay path for a
// connection served after the exit was recorded: it gets the exit frame and
// never a ready snapshot. Live, that window is a race against the listener
// close, so it is exercised deterministically over an in-memory pipe.
func TestControlExitReplayToServedConnection(t *testing.T) {
	srv := &controlServer{
		pid:    4242,
		conns:  map[net.Conn]*controlConn{},
		exited: true,
		exit:   WorkerExit{Code: 7, Signal: "SIGKILL"},
	}
	client, server := net.Pipe()
	defer client.Close()
	go srv.serve(server)

	cc := &ControlClient{conn: client, rd: bufio.NewReader(client)}
	events := cc.Events()
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		cc.Pump()
	}()
	select {
	case ev := <-events:
		if ev.Ev != "exit" || ev.Code != 7 || ev.Names != "SIGKILL" {
			t.Fatalf("replay event = %+v, want exit/7/SIGKILL", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("served-after-exit connection did not get the exit replay")
	}
	if e := cc.ObservedExit(); e == nil || e.Code != 7 || e.Signal != "SIGKILL" {
		t.Fatalf("replay ObservedExit = %+v", e)
	}
	// serve() closes the conn after the replay; the pump must finish.
	select {
	case <-pumpDone:
	case <-time.After(2 * time.Second):
		t.Fatal("pump did not finish after the exit replay")
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
