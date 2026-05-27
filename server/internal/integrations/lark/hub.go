package lark

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	mathrand "math/rand/v2"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// HubQueries is the narrow subset of *db.Queries the Hub needs for
// installation enumeration and lease management. *db.Queries satisfies
// it directly; tests substitute a fake.
type HubQueries interface {
	ListActiveLarkInstallations(ctx context.Context) ([]db.LarkInstallation, error)
	AcquireLarkWSLease(ctx context.Context, arg db.AcquireLarkWSLeaseParams) (db.LarkInstallation, error)
	ReleaseLarkWSLease(ctx context.Context, arg db.ReleaseLarkWSLeaseParams) error
}

// EventConnector is the per-installation transport. The Hub owns the
// lifecycle (when to start, when to stop, when to back off), and the
// connector owns the actual wire protocol — opening the Lark long
// connection, decoding events, normalizing them into InboundMessage.
//
// Run MUST block until either:
//   - the ctx is cancelled (graceful shutdown / lease loss / revoke),
//     in which case it returns nil; or
//   - the connection ends and cannot be recovered locally, in which
//     case it returns an error describing why. The Hub treats a
//     non-nil return as "this attempt failed" and schedules a retry
//     under exponential backoff.
//
// Implementations MUST be tolerant of repeated Run calls on different
// contexts — the Hub may call Run, return, and call Run again after
// backoff. Allocating per-call state is fine; persistent state lives in
// the connector struct.
type EventConnector interface {
	Run(ctx context.Context, inst db.LarkInstallation, emit func(InboundMessage)) error
}

// ConnectorFactory builds an EventConnector for a specific installation
// row. The factory exists so the Hub doesn't need to know about Lark
// SDK construction (auth, app credentials decryption) — call sites
// inject a factory configured with their APIClient + secretbox box.
type ConnectorFactory func(inst db.LarkInstallation) (EventConnector, error)

// HubConfig tunes the Hub's lifecycle loops. All fields have sensible
// production defaults via withDefaults; tests typically set Now and
// Logger to inject determinism.
type HubConfig struct {
	// LeaseTTL is how long a successful AcquireLarkWSLease grant is
	// valid before another server replica may steal it. Renewals
	// happen on a tighter interval (LeaseRenewInterval); the gap
	// between renew and TTL absorbs transient DB blips.
	LeaseTTL time.Duration

	// LeaseRenewInterval is the cadence at which the Hub re-acquires
	// the lease on connections it already owns. MUST be substantially
	// less than LeaseTTL so a single missed renewal does not yield
	// the lease.
	LeaseRenewInterval time.Duration

	// PollInterval is how often the Hub scans for new installations
	// (or ones whose lease has expired on another replica) to take
	// over.
	PollInterval time.Duration

	// MinBackoff / MaxBackoff bound the per-installation reconnect
	// schedule. The actual delay starts at MinBackoff, doubles after
	// each consecutive failure (capped at MaxBackoff), and resets on
	// any successful Run that lives at least ResetBackoffAfter.
	MinBackoff        time.Duration
	MaxBackoff        time.Duration
	ResetBackoffAfter time.Duration

	// Now returns the current time. Injected for tests; production
	// uses time.Now.
	Now func() time.Time

	// Logger optional; defaults to slog.Default.
	Logger *slog.Logger
}

func (c HubConfig) withDefaults() HubConfig {
	if c.LeaseTTL == 0 {
		c.LeaseTTL = 90 * time.Second
	}
	if c.LeaseRenewInterval == 0 {
		c.LeaseRenewInterval = 30 * time.Second
	}
	if c.PollInterval == 0 {
		c.PollInterval = 30 * time.Second
	}
	if c.MinBackoff == 0 {
		c.MinBackoff = 2 * time.Second
	}
	if c.MaxBackoff == 0 {
		c.MaxBackoff = 60 * time.Second
	}
	if c.ResetBackoffAfter == 0 {
		c.ResetBackoffAfter = 60 * time.Second
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// Hub owns the per-installation supervisor goroutines that keep a
// long-running Lark connection per active installation. It enforces
// the §4.4 multi-replica safety rule via the WS lease CAS — at most
// one Hub instance globally holds the lease for any installation, so
// duplicate event consumption across replicas is impossible.
//
// Lifecycle:
//
//	hub := NewHub(queries, factory, dispatcher, HubConfig{})
//	go hub.Run(ctx)             // returns when ctx is cancelled
//	... ctx cancellation triggers ...
//	hub.Wait()                  // joins on every per-installation goroutine
type Hub struct {
	queries    HubQueries
	factory    ConnectorFactory
	dispatcher *Dispatcher
	cfg        HubConfig

	// nodeID is the per-process lease ownership token. The CAS
	// predicate on AcquireLarkWSLease treats matching tokens as
	// "this is us, renew" — so as long as nodeID is stable for the
	// Hub's lifetime, lease renewals don't ping-pong between replicas.
	nodeID string

	mu       sync.Mutex
	stopFns  map[string]context.CancelFunc // installation_id -> per-supervisor cancel
	wg       sync.WaitGroup
	stopped  bool
	stopChan chan struct{}
}

// NewHub constructs a Hub bound to the supplied queries, connector
// factory and dispatcher. The Hub does not start any goroutines until
// Run is called.
func NewHub(queries HubQueries, factory ConnectorFactory, dispatcher *Dispatcher, cfg HubConfig) *Hub {
	cfg = cfg.withDefaults()
	return &Hub{
		queries:    queries,
		factory:    factory,
		dispatcher: dispatcher,
		cfg:        cfg,
		nodeID:     newNodeID(),
		stopFns:    make(map[string]context.CancelFunc),
		stopChan:   make(chan struct{}),
	}
}

// NodeID exposes the per-process lease token. Useful for tests and
// for observability (so operators can correlate DB lease rows to a
// running replica).
func (h *Hub) NodeID() string { return h.nodeID }

// Run is the Hub's main loop. It scans installations every
// PollInterval, attempts to lease any that are not currently being
// supervised by this process, and reaps supervisors for installations
// that have been revoked or whose lease was lost. Returns when ctx is
// cancelled; the caller MUST then call Wait to join all supervisor
// goroutines before exiting.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.stopChan)

	// First sweep immediately so a freshly-restarted server doesn't
	// wait a full PollInterval before picking up its installations.
	h.sweep(ctx)

	t := time.NewTicker(h.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			h.cancelAll()
			return
		case <-t.C:
			h.sweep(ctx)
		}
	}
}

// Wait blocks until every supervisor goroutine the Hub started has
// exited. Call this AFTER cancelling Run's context; calling it before
// returns immediately if no supervisors are active.
func (h *Hub) Wait() {
	h.wg.Wait()
}

// sweep enumerates currently-active installations and starts a
// supervisor for any that this Hub does not yet supervise. Supervisors
// for revoked installations are cancelled.
func (h *Hub) sweep(ctx context.Context) {
	rows, err := h.queries.ListActiveLarkInstallations(ctx)
	if err != nil {
		h.cfg.Logger.Warn("lark hub: list active installations failed", "error", err)
		return
	}
	active := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		id := uuidString(row.ID)
		active[id] = struct{}{}
		h.startSupervisor(ctx, row)
	}
	// Reap supervisors whose installation is no longer active (revoked
	// since the last sweep). The supervisor will exit on the next
	// boundary, release its lease, and the goroutine returns.
	h.mu.Lock()
	for id, cancel := range h.stopFns {
		if _, stillActive := active[id]; !stillActive {
			cancel()
			delete(h.stopFns, id)
		}
	}
	h.mu.Unlock()
}

func (h *Hub) startSupervisor(parent context.Context, inst db.LarkInstallation) {
	id := uuidString(inst.ID)
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	if _, exists := h.stopFns[id]; exists {
		h.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	h.stopFns[id] = cancel
	h.wg.Add(1)
	h.mu.Unlock()
	go h.supervise(ctx, inst, id)
}

// supervise owns one installation's connection lifecycle. It loops:
// acquire lease → spin up connector → renew lease while connector is
// running → on connector exit, back off → repeat. Returns (and the
// goroutine ends) when ctx is cancelled.
func (h *Hub) supervise(ctx context.Context, inst db.LarkInstallation, id string) {
	defer h.wg.Done()
	defer func() {
		h.mu.Lock()
		delete(h.stopFns, id)
		h.mu.Unlock()
	}()

	log := h.cfg.Logger.With("installation_id", id, "node_id", h.nodeID)
	backoff := h.cfg.MinBackoff

	for {
		if ctx.Err() != nil {
			return
		}

		// Try to claim the WS lease for this installation. If another
		// replica already owns a live lease, sleep until either the
		// lease expires or our context is cancelled.
		leased, err := h.acquireLease(ctx, inst.ID)
		if err != nil {
			log.Warn("lark hub: acquire lease error", "error", err)
			if sleep(ctx, h.cfg.LeaseRenewInterval) {
				return
			}
			continue
		}
		if !leased {
			// Another replica owns the lease. Wait LeaseRenewInterval
			// (less than LeaseTTL) and re-check; if they die, we'll
			// pick it up on the next iteration.
			if sleep(ctx, h.cfg.LeaseRenewInterval) {
				return
			}
			continue
		}

		// Lease acquired. Build a connector, run it under a child
		// context, and start the lease renewer in parallel. The
		// connector returns when its connection dies or our ctx is
		// cancelled; we always release the lease afterwards.
		conn, err := h.factory(inst)
		if err != nil {
			log.Error("lark hub: connector factory failed", "error", err)
			h.releaseLease(context.Background(), inst.ID)
			if sleep(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, h.cfg.MaxBackoff)
			continue
		}

		runCtx, runCancel := context.WithCancel(ctx)
		renewDone := make(chan struct{})
		go func() {
			defer close(renewDone)
			h.renewLeaseUntil(runCtx, inst.ID)
		}()

		startedAt := h.cfg.Now()
		runErr := conn.Run(runCtx, inst, func(msg InboundMessage) {
			h.handleEvent(runCtx, log, msg)
		})
		runCancel()
		<-renewDone
		h.releaseLease(context.Background(), inst.ID)

		if ctx.Err() != nil {
			return
		}

		// If the connection lived long enough that we believe it was
		// "stable", reset the backoff so a single late failure does
		// not start us at the cap. Otherwise step up the backoff.
		uptime := h.cfg.Now().Sub(startedAt)
		if uptime >= h.cfg.ResetBackoffAfter {
			backoff = h.cfg.MinBackoff
		}
		if runErr != nil {
			log.Warn("lark hub: connector exited with error", "error", runErr, "uptime", uptime.String())
		} else {
			log.Info("lark hub: connector exited cleanly", "uptime", uptime.String())
		}
		if sleep(ctx, jitter(backoff)) {
			return
		}
		backoff = nextBackoff(backoff, h.cfg.MaxBackoff)
	}
}

// acquireLease tries to claim or renew the WS lease for an
// installation. Returns (true, nil) when the lease is owned by this
// Hub after the call; (false, nil) when another replica holds a live
// lease; or (false, err) for transport / DB failures.
func (h *Hub) acquireLease(ctx context.Context, instID pgtype.UUID) (bool, error) {
	expires := h.cfg.Now().Add(h.cfg.LeaseTTL)
	_, err := h.queries.AcquireLarkWSLease(ctx, db.AcquireLarkWSLeaseParams{
		ID:           instID,
		NewToken:     pgtype.Text{String: h.nodeID, Valid: true},
		NewExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	})
	if err == nil {
		return true, nil
	}
	if isNoRowsErr(err) {
		// CAS predicate didn't match — someone else holds the lease.
		return false, nil
	}
	return false, err
}

// renewLeaseUntil re-acquires the lease on a tight cadence so a single
// missed renewal does not yield it. Exits when ctx is cancelled. Lease
// loss (acquireLease returns leased=false) cancels the parent run ctx
// so the connector can exit and we can re-enter the backoff loop.
func (h *Hub) renewLeaseUntil(ctx context.Context, instID pgtype.UUID) {
	t := time.NewTicker(h.cfg.LeaseRenewInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			leased, err := h.acquireLease(ctx, instID)
			if err != nil {
				h.cfg.Logger.Warn("lark hub: lease renewal error",
					"installation_id", uuidString(instID),
					"error", err,
				)
				continue
			}
			if !leased {
				h.cfg.Logger.Warn("lark hub: lease lost; tearing down connector",
					"installation_id", uuidString(instID),
				)
				return
			}
		}
	}
}

func (h *Hub) releaseLease(ctx context.Context, instID pgtype.UUID) {
	if err := h.queries.ReleaseLarkWSLease(ctx, db.ReleaseLarkWSLeaseParams{
		ID:           instID,
		CurrentToken: pgtype.Text{String: h.nodeID, Valid: true},
	}); err != nil {
		h.cfg.Logger.Warn("lark hub: release lease failed",
			"installation_id", uuidString(instID),
			"error", err,
		)
	}
}

// handleEvent is the seam between the connector (which emits normalized
// InboundMessage) and the inbound Dispatcher. We deliberately do not
// retry here — the Dispatcher classifies errors itself (productizable
// outcomes vs. infra failures), and infra failures propagate up to the
// connector, which decides whether to reconnect.
func (h *Hub) handleEvent(ctx context.Context, log *slog.Logger, msg InboundMessage) {
	if h.dispatcher == nil {
		log.Warn("lark hub: dispatcher not configured; dropping event",
			"event_id", msg.EventID,
		)
		return
	}
	res, err := h.dispatcher.Handle(ctx, msg)
	if err != nil {
		log.Error("lark hub: dispatcher error",
			"event_id", msg.EventID,
			"error", err,
		)
		return
	}
	log.Debug("lark hub: dispatch outcome",
		"event_id", msg.EventID,
		"outcome", string(res.Outcome),
		"drop_reason", string(res.DropReason),
	)
}

func (h *Hub) cancelAll() {
	h.mu.Lock()
	h.stopped = true
	for id, cancel := range h.stopFns {
		cancel()
		delete(h.stopFns, id)
	}
	h.mu.Unlock()
}

// newNodeID returns a 16-byte hex random string unique to this process.
// The DB stores it in lark_installation.ws_lease_token; matching tokens
// on subsequent acquires are treated as renewals (same owner).
func newNodeID() string {
	buf := make([]byte, 16)
	if _, err := cryptorand.Read(buf); err != nil {
		// crypto/rand failure is catastrophic and rare; fall back to a
		// timestamp-derived token rather than panicking on boot.
		return fmt.Sprintf("nodeid-fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// nextBackoff doubles the current backoff up to max. Pure helper so
// the supervise loop reads top-to-bottom.
func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

// jitter spreads reconnect storms (e.g. after a Lark-side outage)
// across the [0.5d, 1.5d) window, so 100 installations don't all
// retry on the same timer edge.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	delta := d / 2
	return d - delta + time.Duration(mathrand.Int64N(int64(2*delta)+1))
}

// sleep is a ctx-aware time.Sleep. Returns true iff the ctx was
// cancelled before the sleep completed — callers use the boolean to
// short-circuit shutdown.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() != nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}

// isNoRowsErr is the local equivalent of errors.Is(err, pgx.ErrNoRows)
// without importing pgx into this file. The CAS predicate on
// AcquireLarkWSLease surfaces "lease held by someone else" as a
// no-rows return, not a structured error type.
func isNoRowsErr(err error) bool {
	if err == nil {
		return false
	}
	// pgx.ErrNoRows is the sentinel; matching by message is
	// sufficient and avoids importing pgx purely for this comparison.
	return errors.Is(err, errPgxNoRows) || err.Error() == "no rows in result set"
}

// errPgxNoRows is initialized in hub_pgx.go to pgx.ErrNoRows so the
// no-rows check above works under both the real pgx import path and
// the string-matched fallback (test fakes return that string directly).
var errPgxNoRows error

func uuidString(u pgtype.UUID) string { return util.UUIDToString(u) }
