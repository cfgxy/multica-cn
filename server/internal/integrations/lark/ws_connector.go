package lark

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// WSLongConnConnector is the production EventConnector that holds the
// Lark long-conn WebSocket open and forwards normalized inbound events
// to the Hub's Dispatcher.
//
// It exists to replace NoopConnector once the wire-protocol pieces are
// ready. The Hub's lifecycle invariants do NOT change: the Hub still
// owns lease acquisition / renewal / supervisor backoff, and Run blocks
// until either the ctx is cancelled or the connection ends.
//
// Why this lives as its own file rather than inside hub.go: the Hub is
// transport-agnostic on purpose so unit tests can substitute fakes
// without dragging gorilla/websocket into every test. Keeping the real
// WS plumbing isolated preserves that boundary.
//
// Ownership of the §4.4 invariant
//
// The Lark WS read API blocks on the underlying TCP socket and does NOT
// observe a context. If we simply called conn.ReadMessage in a loop,
// cancelling our ctx would only mark ctx.Done — the goroutine would
// stay parked in the read syscall until the socket finally errored out,
// which could be minutes after lease loss on a healthy network. That is
// the exact "two replicas processing the same installation" window the
// renewLeaseUntil cancel is supposed to close.
//
// We bridge ctx → read interrupt with a watchdog goroutine that calls
// conn.Close once ctx fires. gorilla/websocket's Close causes any
// in-flight Read to return with an error, so the read loop exits in
// bounded time regardless of socket-level traffic. The watchdog also
// runs on a normal read-error exit (so we never leak a goroutine), and
// it is idempotent because conn.Close is safe to call multiple times.
type WSLongConnConnector struct {
	cfg WSConnectorConfig
}

// WSConnectorConfig wires the connector's dependencies. All injected
// interfaces are required; nil dependencies cause NewWSLongConnConnector
// to return an error rather than producing a connector that would panic
// at first use. Time / logger fields default at construction.
type WSConnectorConfig struct {
	// Dialer opens the WebSocket transport. Defaults to gorilla's
	// DefaultDialer with a bounded HandshakeTimeout. Tests inject a
	// fake that points at an httptest server.
	Dialer WSDialer

	// EndpointFetcher resolves the per-installation WS URL + auth
	// headers. The connector calls it once per Run, so a transient
	// failure here causes a Hub-level backoff retry rather than an
	// in-Run reconnect storm.
	EndpointFetcher EndpointFetcher

	// FrameDecoder turns a single inbound WS message into either a
	// normalized InboundMessage (to be emitted upstream) or a
	// "control / heartbeat / unknown" signal that the connector drops
	// silently. Errors from the decoder do NOT exit the loop — they
	// log + drop — because one malformed Lark frame should not tear
	// down the entire connection.
	FrameDecoder FrameDecoder

	// CredentialsProvider returns the InstallationCredentials the
	// EndpointFetcher needs (and, in future, that any outbound calls
	// from the connector would need). Typically wraps
	// InstallationService.DecryptAppSecret so the plaintext secret
	// never sits on the LarkInstallation row in memory.
	CredentialsProvider CredentialsProvider

	// PingInterval is the cadence on which the connector sends a
	// WebSocket ping control frame to keep NATs / load balancers from
	// idling out the connection. Zero defaults to 30s.
	PingInterval time.Duration

	// ReadDeadline bounds a single ReadMessage call. We re-arm the
	// deadline before each read; an expiry yields a transient read
	// error which the connector logs and uses to exit, deferring to
	// the Hub's reconnect backoff. Zero defaults to 75s — comfortably
	// larger than PingInterval so a healthy connection never trips the
	// deadline.
	ReadDeadline time.Duration

	// WriteTimeout bounds a single WriteControl / WriteMessage. Zero
	// defaults to 10s.
	WriteTimeout time.Duration

	// Now is overridable for deterministic tests. Defaults to time.Now.
	Now func() time.Time

	// Logger optional; defaults to slog.Default.
	Logger *slog.Logger
}

func (c WSConnectorConfig) withDefaults() WSConnectorConfig {
	if c.PingInterval == 0 {
		c.PingInterval = 30 * time.Second
	}
	if c.ReadDeadline == 0 {
		c.ReadDeadline = 75 * time.Second
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = 10 * time.Second
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// NewWSLongConnConnector validates the supplied config and returns a
// reusable connector. The Hub's ConnectorFactory typically constructs
// one connector per installation, but the returned type is safe to
// share across installations as long as the EndpointFetcher /
// CredentialsProvider are themselves goroutine-safe (the standard
// implementations are).
func NewWSLongConnConnector(cfg WSConnectorConfig) (*WSLongConnConnector, error) {
	if cfg.Dialer == nil {
		return nil, errors.New("lark ws connector: Dialer is required")
	}
	if cfg.EndpointFetcher == nil {
		return nil, errors.New("lark ws connector: EndpointFetcher is required")
	}
	if cfg.FrameDecoder == nil {
		return nil, errors.New("lark ws connector: FrameDecoder is required")
	}
	if cfg.CredentialsProvider == nil {
		return nil, errors.New("lark ws connector: CredentialsProvider is required")
	}
	return &WSLongConnConnector{cfg: cfg.withDefaults()}, nil
}

// Run satisfies EventConnector. It opens one WebSocket session, reads
// frames until either the ctx is cancelled or the connection errors,
// and returns. The Hub treats a nil return as "clean exit" (resets the
// supervisor uptime check) and a non-nil return as "connection failed"
// (steps up the reconnect backoff).
//
// The watchdog goroutine that closes the conn on ctx.Done is essential
// for §4.4: without it, a healthy-but-silent WebSocket would keep the
// goroutine parked in ReadMessage well past lease loss, and the next
// replica would race ours for the same Lark events.
func (c *WSLongConnConnector) Run(ctx context.Context, inst db.LarkInstallation, emit EventEmitter) error {
	log := c.cfg.Logger.With(
		"installation_id", uuidString(inst.ID),
		"app_id", inst.AppID,
	)

	creds, err := c.cfg.CredentialsProvider.Credentials(ctx, inst)
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}

	endpoint, err := c.cfg.EndpointFetcher.Endpoint(ctx, creds)
	if err != nil {
		return fmt.Errorf("resolve ws endpoint: %w", err)
	}

	conn, _, err := c.cfg.Dialer.DialContext(ctx, endpoint.URL, endpoint.Headers)
	if err != nil {
		return fmt.Errorf("dial ws: %w", err)
	}

	// runCtx fans out cancellation to the watchdog + ping goroutines on
	// EVERY Run exit, not just on outer-ctx cancel. Without this, a
	// read error or an emit-side infra failure would unblock the read
	// loop but leave the ping goroutine ticking on the outer ctx — and
	// the deferred "<-pingDone" join would deadlock.
	runCtx, runCancel := context.WithCancel(ctx)

	// closeOnce guards the underlying conn.Close so the watchdog and
	// the normal exit path can both call it without double-Close
	// landing on a recycled FD. gorilla's Close itself is technically
	// idempotent but errors after the first call; we suppress that
	// noise here.
	var closeOnce sync.Once
	closeConn := func() {
		closeOnce.Do(func() {
			_ = conn.Close()
		})
	}

	// Watchdog: when the supervisor cancels our ctx (lease lost,
	// installation revoked, server shutdown) — OR when the deferred
	// runCancel fires on any other Run exit path — close the socket so
	// the blocking ReadMessage below returns immediately instead of
	// waiting for a TCP-level error. The watchdog also exits naturally
	// if the read loop completes first (we close `done` on return), so
	// we never leak a goroutine.
	done := make(chan struct{})
	go func() {
		select {
		case <-runCtx.Done():
			closeConn()
		case <-done:
		}
	}()

	// Ping loop: emits a WebSocket control ping at the configured
	// cadence so NATs and load balancers do not silently drop the
	// connection during long idle stretches. Lark's server responds
	// with a pong; the gorilla read pump consumes those transparently.
	// Bound to runCtx, not the outer ctx, so it shuts down on every
	// Run exit (not only outer-ctx cancellation).
	pingDone := make(chan struct{})
	go c.pingLoop(runCtx, conn, log, pingDone)

	// Exit choreography: cancel runCtx first (signals watchdog + ping
	// loop), close the socket (best-effort; the watchdog usually beat
	// us here), then join the ping goroutine. close(done) lets the
	// watchdog short-circuit if it was still waiting. Order matters —
	// joining pingDone before runCancel would deadlock when Run exits
	// on a read error with the outer ctx still live.
	defer func() {
		runCancel()
		closeConn()
		close(done)
		<-pingDone
	}()

	// Pong handler advances the read deadline whenever the server
	// answers our pings, so a healthy idle connection never trips the
	// deadline. gorilla's default pong handler is a no-op.
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(c.cfg.Now().Add(c.cfg.ReadDeadline))
		return nil
	})

	log.Info("lark ws connector: connected")

	for {
		// Re-arm the read deadline before every Read so a stalled
		// connection eventually unblocks the read syscall and our loop
		// can decide whether to continue. The watchdog above is the
		// primary cancellation lever; the deadline is the secondary
		// defense for "stalled but not yet ctx-cancelled" cases.
		if err := conn.SetReadDeadline(c.cfg.Now().Add(c.cfg.ReadDeadline)); err != nil {
			// Setting a deadline only fails if the conn has been
			// closed already. Treat it the same as a read error.
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("set read deadline: %w", err)
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			// If the ctx is cancelled, the watchdog closed the conn
			// and ReadMessage returns a "use of closed connection"
			// error. That is a clean exit, not a failure.
			if ctx.Err() != nil {
				log.Info("lark ws connector: ctx cancelled, read returned",
					"close_err", err.Error(),
				)
				return nil
			}
			// A normal close from Lark is also a clean exit; the Hub
			// will dial fresh on the next supervisor iteration after
			// backoff.
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Info("lark ws connector: server closed connection", "err", err.Error())
				return nil
			}
			return fmt.Errorf("read message: %w", err)
		}

		msg, ok, derr := c.cfg.FrameDecoder.Decode(raw, inst)
		if derr != nil {
			// Decoder errors are per-frame; logging + drop is the
			// right policy. Tearing down the whole connection because
			// one frame failed to parse would amplify Lark-side
			// schema regressions into a reconnect storm.
			log.Warn("lark ws connector: frame decode failed",
				"err", derr.Error(),
				"raw_len", len(raw),
			)
			continue
		}
		if !ok {
			// Heartbeat / control / not a message event. The decoder
			// is responsible for deciding what counts; we just drop.
			continue
		}

		if _, err := emit(ctx, msg); err != nil {
			// Infra failures from the Dispatcher (e.g. DB down) are
			// surfaced here. Returning the error tears the connection
			// down so the Hub backs off; staying alive would put us
			// in a hot loop swallowing events while the DB is
			// unreachable, which loses messages with no upstream
			// signal.
			log.Error("lark ws connector: emit infra error",
				"event_id", msg.EventID,
				"err", err.Error(),
			)
			return fmt.Errorf("dispatch: %w", err)
		}
	}
}

// pingLoop sends a periodic WebSocket ping. Exits when ctx fires; the
// done channel lets the parent join cleanly so we never leak this
// goroutine past Run.
func (c *WSLongConnConnector) pingLoop(ctx context.Context, conn WSConn, log *slog.Logger, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(c.cfg.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			deadline := c.cfg.Now().Add(c.cfg.WriteTimeout)
			if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				// A write failure typically means the conn has died;
				// the read loop will exit on its next iteration. We
				// just stop pinging — closing the conn here would
				// race with the read loop's own cleanup.
				log.Warn("lark ws connector: ping write failed", "err", err.Error())
				return
			}
		}
	}
}

// WSDialer is the dialer surface this connector consumes. *websocket.Dialer
// satisfies it directly (via DialContext); tests inject a fake that
// returns a programmable WSConn.
type WSDialer interface {
	DialContext(ctx context.Context, urlStr string, requestHeader http.Header) (WSConn, *http.Response, error)
}

// WSConn is the subset of *websocket.Conn this connector uses. Pulled
// out so the connector can be tested without a live socket — the test
// double implements only the methods we touch.
type WSConn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteControl(messageType int, data []byte, deadline time.Time) error
	SetReadDeadline(t time.Time) error
	SetPongHandler(h func(appData string) error)
	Close() error
}

// EndpointFetcher resolves the per-installation WS URL + auth headers.
// Splitting this from the Dialer keeps the dialer dumb (just opens a
// URL) while the fetcher owns the Lark-specific connection-token
// dance against open.feishu.cn.
type EndpointFetcher interface {
	Endpoint(ctx context.Context, creds InstallationCredentials) (WSEndpoint, error)
}

// WSEndpoint is the resolved transport target.
type WSEndpoint struct {
	URL     string
	Headers http.Header
}

// FrameDecoder turns one raw WebSocket message into either an
// InboundMessage (ok=true) or a no-op (ok=false). The connector treats
// a decoder error as per-frame: log + drop, do not tear down the
// connection. The Lark wire format is currently in flux (the
// open-platform protocol mixes JSON envelopes and pb payloads
// depending on api version), so the decoder is kept behind this
// interface to insulate the transport from the wire shape.
type FrameDecoder interface {
	Decode(raw []byte, inst db.LarkInstallation) (msg InboundMessage, ok bool, err error)
}

// CredentialsProvider supplies the plaintext InstallationCredentials a
// connector needs for its EndpointFetcher call. The default
// implementation wraps InstallationService.DecryptAppSecret so the
// plaintext app_secret lives in memory only while a Run is in flight.
type CredentialsProvider interface {
	Credentials(ctx context.Context, inst db.LarkInstallation) (InstallationCredentials, error)
}

// CredentialsProviderFunc adapts a free function to the
// CredentialsProvider interface — useful at wiring sites where the
// dependency graph (InstallationService → secretbox.Box) is already
// established.
type CredentialsProviderFunc func(ctx context.Context, inst db.LarkInstallation) (InstallationCredentials, error)

// Credentials implements CredentialsProvider.
func (f CredentialsProviderFunc) Credentials(ctx context.Context, inst db.LarkInstallation) (InstallationCredentials, error) {
	return f(ctx, inst)
}

// EndpointFetcherFunc adapts a plain function to EndpointFetcher.
type EndpointFetcherFunc func(ctx context.Context, creds InstallationCredentials) (WSEndpoint, error)

// Endpoint implements EndpointFetcher.
func (f EndpointFetcherFunc) Endpoint(ctx context.Context, creds InstallationCredentials) (WSEndpoint, error) {
	return f(ctx, creds)
}

// FrameDecoderFunc adapts a plain function to FrameDecoder.
type FrameDecoderFunc func(raw []byte, inst db.LarkInstallation) (InboundMessage, bool, error)

// Decode implements FrameDecoder.
func (f FrameDecoderFunc) Decode(raw []byte, inst db.LarkInstallation) (InboundMessage, bool, error) {
	return f(raw, inst)
}

// GorillaDialer is the production WSDialer. It wraps the supplied
// *websocket.Dialer (defaulting to a fresh dialer with a bounded
// HandshakeTimeout if nil) and converts the returned *websocket.Conn
// into the WSConn interface this package uses for testability.
type GorillaDialer struct {
	Dialer *websocket.Dialer
}

// NewGorillaDialer returns a WSDialer with sensible production
// defaults. HandshakeTimeout is bounded so a slow / unreachable Lark
// host does not block the supervisor for the full TCP timeout.
func NewGorillaDialer() *GorillaDialer {
	return &GorillaDialer{
		Dialer: &websocket.Dialer{
			HandshakeTimeout: 15 * time.Second,
		},
	}
}

// DialContext implements WSDialer.
func (g *GorillaDialer) DialContext(ctx context.Context, urlStr string, requestHeader http.Header) (WSConn, *http.Response, error) {
	d := g.Dialer
	if d == nil {
		d = websocket.DefaultDialer
	}
	c, resp, err := d.DialContext(ctx, urlStr, requestHeader)
	if err != nil {
		return nil, resp, err
	}
	return c, resp, nil
}
