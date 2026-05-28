package lark

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeWSConn is a programmable WSConn driven by tests. ReadMessage
// blocks until either Push delivers a frame or Close is invoked — this
// is how we simulate the "blocked in TCP read" condition the watchdog
// has to break.
type fakeWSConn struct {
	mu          sync.Mutex
	frames      chan []byte
	closeOnce   sync.Once
	closed      chan struct{}
	pongHandler func(string) error
	pings       int32
	writeErr    error // optional injection for WriteControl
}

func newFakeWSConn() *fakeWSConn {
	return &fakeWSConn{
		frames: make(chan []byte, 8),
		closed: make(chan struct{}),
	}
}

func (f *fakeWSConn) Push(b []byte) {
	select {
	case f.frames <- b:
	case <-f.closed:
	}
}

func (f *fakeWSConn) ReadMessage() (int, []byte, error) {
	select {
	case b, ok := <-f.frames:
		if !ok {
			return 0, nil, io.EOF
		}
		return websocket.TextMessage, b, nil
	case <-f.closed:
		return 0, nil, &websocket.CloseError{Code: websocket.CloseAbnormalClosure, Text: "fake closed"}
	}
}

func (f *fakeWSConn) WriteControl(messageType int, data []byte, deadline time.Time) error {
	f.mu.Lock()
	err := f.writeErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if messageType == websocket.PingMessage {
		atomic.AddInt32(&f.pings, 1)
	}
	return nil
}

func (f *fakeWSConn) SetReadDeadline(t time.Time) error { return nil }

func (f *fakeWSConn) SetPongHandler(h func(string) error) {
	f.mu.Lock()
	f.pongHandler = h
	f.mu.Unlock()
}

func (f *fakeWSConn) Close() error {
	f.closeOnce.Do(func() {
		close(f.closed)
	})
	return nil
}

func (f *fakeWSConn) pingCount() int { return int(atomic.LoadInt32(&f.pings)) }

// fakeWSDialer hands back a pre-built fakeWSConn so tests can drive
// frames + observe closes deterministically.
type fakeWSDialer struct {
	conn    *fakeWSConn
	dialErr error
}

func (d *fakeWSDialer) DialContext(ctx context.Context, urlStr string, h http.Header) (WSConn, *http.Response, error) {
	if d.dialErr != nil {
		return nil, nil, d.dialErr
	}
	return d.conn, nil, nil
}

func quietConnector(t *testing.T, conn *fakeWSConn, decoder FrameDecoder) *WSLongConnConnector {
	t.Helper()
	c, err := NewWSLongConnConnector(WSConnectorConfig{
		Dialer:          &fakeWSDialer{conn: conn},
		EndpointFetcher: EndpointFetcherFunc(func(context.Context, InstallationCredentials) (WSEndpoint, error) { return WSEndpoint{URL: "wss://test/ignored"}, nil }),
		FrameDecoder:    decoder,
		CredentialsProvider: CredentialsProviderFunc(func(context.Context, db.LarkInstallation) (InstallationCredentials, error) {
			return InstallationCredentials{AppID: "test_app", AppSecret: "secret"}, nil
		}),
		PingInterval: 10 * time.Millisecond,
		ReadDeadline: time.Second,
		WriteTimeout: time.Second,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewWSLongConnConnector: %v", err)
	}
	return c
}

func TestWSConnectorRunReturnsOnCtxCancelEvenWhenReadIsBlocked(t *testing.T) {
	t.Parallel()
	conn := newFakeWSConn()
	decoder := FrameDecoderFunc(func([]byte, db.LarkInstallation) (InboundMessage, bool, error) {
		return InboundMessage{}, false, nil
	})
	c := quietConnector(t, conn, decoder)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx, db.LarkInstallation{AppID: "test_app"}, func(context.Context, InboundMessage) (DispatchResult, error) {
			t.Errorf("emit unexpectedly called")
			return DispatchResult{}, nil
		})
	}()

	// Give the connector a moment to dial + park in ReadMessage.
	time.Sleep(20 * time.Millisecond)

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on ctx cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of ctx cancel — watchdog broken")
	}
}

func TestWSConnectorEmitsDecodedFrames(t *testing.T) {
	t.Parallel()
	conn := newFakeWSConn()
	decoder := FrameDecoderFunc(func(raw []byte, _ db.LarkInstallation) (InboundMessage, bool, error) {
		if string(raw) == "heartbeat" {
			return InboundMessage{}, false, nil
		}
		return InboundMessage{
			EventID:   string(raw),
			AppID:     "test_app",
			MessageID: "msg-" + string(raw),
		}, true, nil
	})
	c := quietConnector(t, conn, decoder)

	var emitted []InboundMessage
	var emitMu sync.Mutex
	emit := func(_ context.Context, msg InboundMessage) (DispatchResult, error) {
		emitMu.Lock()
		emitted = append(emitted, msg)
		emitMu.Unlock()
		return DispatchResult{Outcome: OutcomeIngested}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx, db.LarkInstallation{AppID: "test_app"}, emit)
	}()

	conn.Push([]byte("evt-1"))
	conn.Push([]byte("heartbeat"))
	conn.Push([]byte("evt-2"))

	// Allow the read loop to process all three frames.
	deadline := time.After(2 * time.Second)
	for {
		emitMu.Lock()
		n := len(emitted)
		emitMu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d emits in 2s", n)
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if got := emitted[0].EventID; got != "evt-1" {
		t.Errorf("first emit EventID = %q, want evt-1", got)
	}
	if got := emitted[1].EventID; got != "evt-2" {
		t.Errorf("second emit EventID = %q, want evt-2", got)
	}
}

func TestWSConnectorDecoderErrorDoesNotBreakLoop(t *testing.T) {
	t.Parallel()
	conn := newFakeWSConn()
	decodeCount := int32(0)
	decoder := FrameDecoderFunc(func(raw []byte, _ db.LarkInstallation) (InboundMessage, bool, error) {
		n := atomic.AddInt32(&decodeCount, 1)
		if n == 1 {
			return InboundMessage{}, false, errors.New("synthetic decode failure")
		}
		return InboundMessage{EventID: "good"}, true, nil
	})
	c := quietConnector(t, conn, decoder)

	emits := make(chan InboundMessage, 1)
	emit := func(_ context.Context, msg InboundMessage) (DispatchResult, error) {
		emits <- msg
		return DispatchResult{}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx, db.LarkInstallation{AppID: "test_app"}, emit)
	}()

	conn.Push([]byte("bad"))
	conn.Push([]byte("good"))

	select {
	case msg := <-emits:
		if msg.EventID != "good" {
			t.Errorf("emit EventID = %q, want good", msg.EventID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connector exited after first decode error instead of dropping the frame")
	}

	cancel()
	<-done
}

func TestWSConnectorEmitInfraErrorReturnsFromRun(t *testing.T) {
	t.Parallel()
	conn := newFakeWSConn()
	decoder := FrameDecoderFunc(func([]byte, db.LarkInstallation) (InboundMessage, bool, error) {
		return InboundMessage{EventID: "x"}, true, nil
	})
	c := quietConnector(t, conn, decoder)

	infra := errors.New("dispatcher infra failure")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx, db.LarkInstallation{AppID: "test_app"}, func(context.Context, InboundMessage) (DispatchResult, error) {
			return DispatchResult{}, infra
		})
	}()

	conn.Push([]byte("triggers-infra"))

	select {
	case err := <-done:
		if err == nil || !errors.Is(err, infra) {
			t.Fatalf("expected Run to wrap infra error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after infra error")
	}
}

func TestWSConnectorReadErrorReturnsToHub(t *testing.T) {
	t.Parallel()
	conn := newFakeWSConn()
	decoder := FrameDecoderFunc(func([]byte, db.LarkInstallation) (InboundMessage, bool, error) {
		return InboundMessage{}, false, nil
	})
	c := quietConnector(t, conn, decoder)

	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx, db.LarkInstallation{AppID: "test_app"}, func(context.Context, InboundMessage) (DispatchResult, error) {
			return DispatchResult{}, nil
		})
	}()

	// Close out from under the read loop. Because ctx is NOT cancelled,
	// the connector should treat this as a connection failure and
	// return the wrapped error so the Hub steps up its backoff.
	_ = conn.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run returned nil on read error; expected wrapped err for Hub backoff")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after underlying conn closed")
	}
}

func TestWSConnectorSendsPings(t *testing.T) {
	t.Parallel()
	conn := newFakeWSConn()
	decoder := FrameDecoderFunc(func([]byte, db.LarkInstallation) (InboundMessage, bool, error) {
		return InboundMessage{}, false, nil
	})
	c := quietConnector(t, conn, decoder)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx, db.LarkInstallation{AppID: "test_app"}, func(context.Context, InboundMessage) (DispatchResult, error) {
			return DispatchResult{}, nil
		})
	}()

	// PingInterval was set to 10ms in quietConnector; wait long enough
	// for several pings to fire.
	deadline := time.After(500 * time.Millisecond)
	for {
		if conn.pingCount() >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected >=3 pings, got %d", conn.pingCount())
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	<-done
}

func TestWSConnectorRequiresAllDeps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  WSConnectorConfig
	}{
		{"no dialer", WSConnectorConfig{
			EndpointFetcher:     EndpointFetcherFunc(func(context.Context, InstallationCredentials) (WSEndpoint, error) { return WSEndpoint{}, nil }),
			FrameDecoder:        FrameDecoderFunc(func([]byte, db.LarkInstallation) (InboundMessage, bool, error) { return InboundMessage{}, false, nil }),
			CredentialsProvider: CredentialsProviderFunc(func(context.Context, db.LarkInstallation) (InstallationCredentials, error) { return InstallationCredentials{}, nil }),
		}},
		{"no endpoint fetcher", WSConnectorConfig{
			Dialer:              &fakeWSDialer{},
			FrameDecoder:        FrameDecoderFunc(func([]byte, db.LarkInstallation) (InboundMessage, bool, error) { return InboundMessage{}, false, nil }),
			CredentialsProvider: CredentialsProviderFunc(func(context.Context, db.LarkInstallation) (InstallationCredentials, error) { return InstallationCredentials{}, nil }),
		}},
		{"no decoder", WSConnectorConfig{
			Dialer:              &fakeWSDialer{},
			EndpointFetcher:     EndpointFetcherFunc(func(context.Context, InstallationCredentials) (WSEndpoint, error) { return WSEndpoint{}, nil }),
			CredentialsProvider: CredentialsProviderFunc(func(context.Context, db.LarkInstallation) (InstallationCredentials, error) { return InstallationCredentials{}, nil }),
		}},
		{"no credentials provider", WSConnectorConfig{
			Dialer:          &fakeWSDialer{},
			EndpointFetcher: EndpointFetcherFunc(func(context.Context, InstallationCredentials) (WSEndpoint, error) { return WSEndpoint{}, nil }),
			FrameDecoder:    FrameDecoderFunc(func([]byte, db.LarkInstallation) (InboundMessage, bool, error) { return InboundMessage{}, false, nil }),
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewWSLongConnConnector(tc.cfg); err == nil {
				t.Fatalf("expected error for missing dep")
			}
		})
	}
}

func TestWSConnectorDialErrorIsReturned(t *testing.T) {
	t.Parallel()
	dialErr := errors.New("dial blew up")
	c, err := NewWSLongConnConnector(WSConnectorConfig{
		Dialer:          &fakeWSDialer{dialErr: dialErr},
		EndpointFetcher: EndpointFetcherFunc(func(context.Context, InstallationCredentials) (WSEndpoint, error) { return WSEndpoint{URL: "wss://x"}, nil }),
		FrameDecoder:    FrameDecoderFunc(func([]byte, db.LarkInstallation) (InboundMessage, bool, error) { return InboundMessage{}, false, nil }),
		CredentialsProvider: CredentialsProviderFunc(func(context.Context, db.LarkInstallation) (InstallationCredentials, error) {
			return InstallationCredentials{AppID: "a"}, nil
		}),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	err = c.Run(context.Background(), db.LarkInstallation{}, func(context.Context, InboundMessage) (DispatchResult, error) {
		return DispatchResult{}, nil
	})
	if err == nil || !errors.Is(err, dialErr) {
		t.Fatalf("expected wrapped dial error, got %v", err)
	}
}

func TestWSConnectorCredentialsErrorIsReturned(t *testing.T) {
	t.Parallel()
	credsErr := errors.New("decrypt failed")
	c, err := NewWSLongConnConnector(WSConnectorConfig{
		Dialer:          &fakeWSDialer{conn: newFakeWSConn()},
		EndpointFetcher: EndpointFetcherFunc(func(context.Context, InstallationCredentials) (WSEndpoint, error) { return WSEndpoint{URL: "wss://x"}, nil }),
		FrameDecoder:    FrameDecoderFunc(func([]byte, db.LarkInstallation) (InboundMessage, bool, error) { return InboundMessage{}, false, nil }),
		CredentialsProvider: CredentialsProviderFunc(func(context.Context, db.LarkInstallation) (InstallationCredentials, error) {
			return InstallationCredentials{}, credsErr
		}),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	err = c.Run(context.Background(), db.LarkInstallation{}, func(context.Context, InboundMessage) (DispatchResult, error) {
		return DispatchResult{}, nil
	})
	if err == nil || !errors.Is(err, credsErr) {
		t.Fatalf("expected wrapped credentials error, got %v", err)
	}
}
