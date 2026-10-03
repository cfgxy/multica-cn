package supervisor

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// Control is the launcher-side control socket: the daemon connects, writes
// stdin bytes / signals, and reads the worker's proven exit. Multiple
// sequential daemon connections are expected across daemon restarts — the
// launcher serves each one and answers with the current truth.
type controlServer struct {
	mu sync.Mutex

	path          string
	ln            net.Listener
	stdinW        ioWriteCloser // worker's stdin
	pid           int           // worker leader pid
	exited        bool          // worker exit proven
	exit          WorkerExit    // the exit evidence
	conns         map[net.Conn]*controlConn
	cond          *sync.Cond // signalled when exit is recorded
	closed        bool
	deliverSignal func(sig syscall.Signal) // installed by the launcher
}

// WorkerExit is the shared proven-exit type: an alias of agent.WorkerExit so
// the control wire and the worker-handle contract carry one vocabulary.
type WorkerExit = agent.WorkerExit

type controlConn struct {
	conn net.Conn
	w    *bufio.Writer
	mu   sync.Mutex
}

type wireFrame struct {
	Op    string `json:"op,omitempty"`
	Ev    string `json:"ev,omitempty"`
	Data  string `json:"data,omitempty"` // base64 stdin bytes
	Sig   int    `json:"sig,omitempty"`
	PID   int    `json:"pid,omitempty"`
	Code  int    `json:"code,omitempty"`
	Names string `json:"signal,omitempty"`
}

// serveControl listens on path and serves connections until the worker exits
// (exit event broadcast to every live connection) or the launcher shuts down.
// deliverSignal is how a "signal" frame reaches the worker's process group —
// the launcher owns that primitive, the protocol only carries the request.
func serveControl(path string, stdinW ioWriteCloser, workerPID int, exited <-chan WorkerExit, deliverSignal func(syscall.Signal)) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("supervisor: clean control socket: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("supervisor: listen control socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return err
	}
	srv := &controlServer{
		path:          path,
		ln:            ln,
		stdinW:        stdinW,
		pid:           workerPID,
		conns:         map[net.Conn]*controlConn{},
		deliverSignal: deliverSignal,
	}
	srv.cond = sync.NewCond(&srv.mu)
	go srv.awaitExit(exited)
	go func() {
		<-srv.done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return nil // listener closed: shutdown
		}
		go srv.serve(conn)
	}
}

func (s *controlServer) done() <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for !s.exited && !s.closed {
			s.cond.Wait()
		}
		close(ch)
	}()
	return ch
}

func (s *controlServer) awaitExit(exited <-chan WorkerExit) {
	exit := <-exited
	s.mu.Lock()
	s.exited = true
	s.exit = exit
	conns := make([]*controlConn, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.send(wireFrame{Ev: "exit", Code: exit.Code, Names: exit.Signal})
		_ = c.conn.Close()
	}
}

func (s *controlServer) serve(conn net.Conn) {
	cc := &controlConn{conn: conn, w: bufio.NewWriter(conn)}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close()
		return
	}
	s.conns[conn] = cc
	exited := s.exited
	pid := s.pid
	s.mu.Unlock()

	if exited {
		_ = cc.send(wireFrame{Ev: "exit", Code: s.exit.Code, Names: s.exit.Signal})
		conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		return
	}
	_ = cc.send(wireFrame{Ev: "ready", PID: pid})

	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		conn.Close()
	}()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), maxControlFrameBytes)
	for sc.Scan() {
		var f wireFrame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			return
		}
		switch f.Op {
		case "stdin":
			data, err := base64.StdEncoding.DecodeString(f.Data)
			if err != nil {
				return
			}
			s.mu.Lock()
			w := s.stdinW
			s.mu.Unlock()
			if w == nil {
				return
			}
			// Blocking write: the worker's stdin consumption is the
			// backpressure boundary — control frames queue in this socket
			// until the worker reads, mirroring the old anonymous-pipe
			// semantics.
			if _, err := w.Write(data); err != nil {
				return
			}
		case "stdin_eof":
			s.mu.Lock()
			w := s.stdinW
			s.mu.Unlock()
			if w == nil {
				return
			}
			_ = w.Close()
		case "signal":
			s.mu.Lock()
			deliver := s.deliverSignal
			s.mu.Unlock()
			if deliver != nil {
				deliver(syscall.Signal(f.Sig))
			}
		default:
			return
		}
	}
}

// registerSignalSender is gone: the launcher passes its signal delivery
// straight into serveControl.

func (cc *controlConn) send(f wireFrame) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if _, err := cc.w.Write(append(data, '\n')); err != nil {
		return err
	}
	return cc.w.Flush()
}

// ControlClient is the daemon-side end: reconnecting, queueing stdin writes
// while the socket is down (bounded), and reporting the exit it observes.
type ControlClient struct {
	path string

	mu     sync.Mutex
	conn   net.Conn
	rd     *bufio.Reader
	exit   *WorkerExit
	events []wireFrame
	subs   []chan wireFrame
}

const maxControlFrameBytes = 8 * 1024 * 1024

// DialControl connects to the launcher's control socket, retrying until the
// timeout (the launcher may still be starting).
func DialControl(ctx context.Context, path string, timeout time.Duration) (*ControlClient, error) {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.Dial("unix", path)
		if err == nil {
			return &ControlClient{path: path, conn: conn, rd: bufio.NewReader(conn)}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("supervisor: control dial %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Events returns the channel every wire event is broadcast on.
func (c *ControlClient) Events() <-chan wireFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan wireFrame, 16)
	c.subs = append(c.subs, ch)
	// Replay observed events (a reconnected client must learn the exit that
	// happened before it reconnected).
	for _, ev := range c.events {
		select {
		case ch <- ev:
		default:
		}
	}
	return ch
}

func (c *ControlClient) send(f wireFrame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return errors.New("supervisor: control client closed")
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	c.conn.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
	_, err = c.conn.Write(append(data, '\n'))
	return err
}

const controlWriteTimeout = 30 * time.Second

// WriteStdin forwards bytes to the worker's stdin through the launcher.
// Backpressure is the worker's own read pace: this blocks (bounded by the
// write deadline) exactly like the anonymous pipe it replaces.
func (c *ControlClient) WriteStdin(ctx context.Context, p []byte) (int, error) {
	enc := base64.StdEncoding.EncodeToString(p)
	if err := c.send(wireFrame{Op: "stdin", Data: enc}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// CloseStdin delivers EOF on the worker's stdin.
func (c *ControlClient) CloseStdin() error { return c.send(wireFrame{Op: "stdin_eof"}) }

// Signal asks the launcher to signal the worker's group.
func (c *ControlClient) Signal(sig int) error { return c.send(wireFrame{Op: "signal", Sig: sig}) }

// Close drops the connection. Observed events stay available to new
// subscribers.
func (c *ControlClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// Pump reads wire events until the connection dies, recording the exit event
// if one arrives. Safe to call from one goroutine; a client that was already
// (or concurrently) closed simply finishes.
func (c *ControlClient) Pump() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), maxControlFrameBytes)
	for sc.Scan() {
		var f wireFrame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			return
		}
		c.mu.Lock()
		if f.Ev == "exit" {
			c.exit = &WorkerExit{Code: f.Code, Signal: f.Names}
		}
		c.events = append(c.events, f)
		subs := append([]chan wireFrame(nil), c.subs...)
		c.mu.Unlock()
		for _, ch := range subs {
			select {
			case ch <- f:
			default:
			}
		}
		if f.Ev == "exit" {
			return
		}
	}
}

// ObservedExit returns the exit event if one has been observed.
func (c *ControlClient) ObservedExit() *WorkerExit {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exit == nil {
		return nil
	}
	cp := *c.exit
	return &cp
}

type ioWriteCloser interface {
	Write(p []byte) (int, error)
	Close() error
}
