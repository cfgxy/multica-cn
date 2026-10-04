package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// RUYI-390 reattach support for the ACP family: when a supervised worker
// outlives its daemon, the next daemon's Execute reenters the worker with
// Reattach set. The worker already ran initialize and consumed its prompt —
// its turn is in flight — so the reattached side issues no requests at all.
// It rebuilds what it needs from the wire:
//
//   - the session id rides every session/update notification
//     (params.sessionId), so observedSessionID picks it up from the first
//     frame the resumed stdout stream delivers (zcode's lazy placeholder id
//     included — the real id replaces it on the same wire);
//   - the turn's end is the same turn_end notification a sent prompt would
//     have waited on (extractPromptResult fires onPromptDone either way);
//   - requests the dead daemon sent but never consumed come back as
//     responses with unknown ids, which handleResponse already drops
//     silently, and tool_call updates for unknown callIds degrade the same
//     way — no state to repair;
//   - requests the worker blocked on mid-restart (session/request_permission,
//     terminal calls) are answered by the reader goroutine exactly as they
//     would be in a fresh session.
//
// Provisional (RUYI-349, Owner directive 2026-10-04): this reattach judgment
// extends the Phase 1 pattern to the ACP family while the stuck-running root
// cause is open. Do not replicate it to further runtimes; it converges into
// the corrected unified lifecycle plan RUYI-349 will deliver.

const (
	// reattachIDBase re-seeds the JSON-RPC id counter after a reattach so
	// requests the new daemon sends cannot collide with an in-flight id the
	// previous daemon left behind — a late response to the old id must not
	// be deliverable to the new request's pending call.
	reattachIDBase = 1_000_000_000
)

// wireSignals are the two latches a reattached wait resolves on. They are
// lazily built under wireMu: hermesClient values are struct literals at a
// dozen backend call sites, and keeping the zero value usable means none of
// those literals change.
type wireSignals struct {
	wireMu       sync.Mutex
	turnDone     chan struct{} // closed once the in-flight turn has a result
	streamClosed chan struct{} // closed on stdout EOF without a turn result

	// observedSession records the last session id seen on the wire. It
	// lives here because handleNotification (reader goroutine) writes it
	// and the lifecycle goroutine reads it after the wait resolves.
	observedSession atomic.Value // string
}

// reattachWaits lazily creates and returns the wait channels.
func (c *hermesClient) reattachWaits() (<-chan struct{}, <-chan struct{}) {
	c.wireMu.Lock()
	defer c.wireMu.Unlock()
	if c.turnDone == nil {
		c.turnDone = make(chan struct{})
		c.streamClosed = make(chan struct{})
	}
	return c.turnDone, c.streamClosed
}

// markTurnDone records that the in-flight turn has produced its result.
// Runs before onPromptDone so a waiter that wakes on turnDone finds the
// prompt result already queued (or queued within the same scheduling beat —
// the send is buffered and non-blocking).
func (c *hermesClient) markTurnDone() {
	c.wireMu.Lock()
	defer c.wireMu.Unlock()
	if c.turnDone == nil {
		c.turnDone = make(chan struct{})
		c.streamClosed = make(chan struct{})
	}
	select {
	case <-c.turnDone:
	default:
		close(c.turnDone)
	}
}

// markStreamClosed records stdout EOF. When the turn already delivered its
// result the EOF is just the worker exiting after a finished turn — not a
// failure signal — so streamClosed stays open and a waiter already resolved
// on turnDone.
func (c *hermesClient) markStreamClosed() {
	c.wireMu.Lock()
	defer c.wireMu.Unlock()
	if c.turnDone == nil {
		c.turnDone = make(chan struct{})
		c.streamClosed = make(chan struct{})
	}
	select {
	case <-c.turnDone:
		return
	default:
	}
	select {
	case <-c.streamClosed:
	default:
		close(c.streamClosed)
	}
}

// observeSessionID records the session id carried by an ACP notification.
// Called for every session/update, before any accept gate: a reattached
// client gates updates it cannot attribute yet, but the id itself is valid
// no matter which turn emitted it.
func (c *hermesClient) observeSessionID(id string) {
	if id != "" {
		c.observedSession.Store(id)
	}
}

// observedSessionID returns the last session id seen on the wire, or ""
// before the first frame with one arrives.
func (c *hermesClient) observedSessionID() string {
	s, _ := c.observedSession.Load().(string)
	return s
}

// waitReattachedTurn blocks until the turn the previous daemon delivered
// ends. It is the reattach-side twin of request("session/prompt"): a
// nil error means the turn result is queued for the caller's promptDone
// channel; a closed stream maps to the same "process exited" failure shape
// request() would return via closeAllPending; ctx cancellation surfaces the
// caller's timeout/abort classification unchanged.
func (c *hermesClient) waitReattachedTurn(ctx context.Context, backend string) error {
	turnDone, streamClosed := c.reattachWaits()
	select {
	case <-turnDone:
		return nil
	case <-streamClosed:
		return fmt.Errorf("%s process exited before delivering the turn result", backend)
	case <-ctx.Done():
		return fmt.Errorf("%s reattach wait interrupted: %w", backend, ctx.Err())
	}
}

// seedReattachIDSpace moves the request id counter past anything the
// previous daemon could have allocated. Defensive: the reattached Execute
// sends no protocol requests of its own, but terminal-call handling and
// future callers would otherwise reuse the id range whose late responses
// are streaming in from the resumed log.
func (c *hermesClient) seedReattachIDSpace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nextID < reattachIDBase {
		c.nextID = reattachIDBase
	}
}
