package daemon

import (
	"fmt"
	"time"
)

// Watchdog observation events (RUYI-593, implementation round 2).
//
// The force-stop budgets and their kill semantics are untouched: everything
// in this file is pure observation layered onto the same silence clock the
// kill path already reads. Events ride the existing task message channel as
// type "watchdog" (the server ingest is type-agnostic and redacts every
// column, so no migration and no CLI change), and every knob is a
// durationFromEnv key whose zero disables exactly that mechanism — with all
// knobs at zero the watchdog behaves precisely as before this round.
//
// Two invariants hold across all events:
//   - Self-produced events never touch lastActivityAt. The daemon's own
//     output is not backend activity; writing it back would let each mark or
//     alert defer its own kill forever.
//   - Every event carries checked_at (RFC3339). Ticks land every
//     min(window/2, 5m), so an event can lag its true threshold by up to one
//     tick; checked_at lets post-hoc analysis correct for that.

// toolCallTrack is one unpaired tool_use, as observed by the drain loop.
type toolCallTrack struct {
	Tool       string
	CallID     string
	IssuedAt   time.Time
	LastMarkAt time.Time
	Marks      int
}

// toolCallTracker remembers unpaired tool_use calls so the watchdog can mark
// stalled calls and describe them in the force-stop pre-mortem. The drain
// loop registers on tool_use and retires on tool_result — exact call_id match
// first, then the same same-tool-FIFO fallback the frontend folder uses for
// results that lost their id.
//
// Not safe for concurrent use: every access happens under the drain loop's
// mutex, shared with transcript batch appends so event seqs stay monotonic
// against the message stream.
type toolCallTracker struct {
	anon  int
	order []string
	pend  map[string]*toolCallTrack
}

func newToolCallTracker() *toolCallTracker {
	return &toolCallTracker{pend: map[string]*toolCallTrack{}}
}

// register records a tool_use. A call without an id is tracked under a
// synthetic key (CallID stays empty) so it still shows up in marks and in the
// pre-mortem even though no result can ever match it exactly.
func (t *toolCallTracker) register(callID, tool string, now time.Time) {
	key := callID
	if key == "" {
		t.anon++
		key = fmt.Sprintf("anon:%d", t.anon)
	}
	t.pend[key] = &toolCallTrack{Tool: tool, CallID: callID, IssuedAt: now}
	t.order = append(t.order, key)
}

// take removes the call with exactly this id, if pending. A result naming an
// unknown id retires nothing — the ghost pending call keeps being reported,
// which is the signal we want.
func (t *toolCallTracker) take(callID string) *toolCallTrack {
	if callID == "" {
		return nil
	}
	tr, ok := t.pend[callID]
	if !ok {
		return nil
	}
	t.remove(callID)
	return tr
}

// retireOldest pairs a call-less result with the oldest pending call,
// preferring the same tool name. A stray result still retires one call so the
// pending set cannot drift from what the model actually sees.
func (t *toolCallTracker) retireOldest(preferTool string) *toolCallTrack {
	if len(t.order) == 0 {
		return nil
	}
	key := t.order[0]
	if preferTool != "" {
		for _, k := range t.order {
			if tr := t.pend[k]; tr != nil && tr.Tool == preferTool {
				key = k
				break
			}
		}
	}
	tr := t.pend[key]
	t.remove(key)
	return tr
}

func (t *toolCallTracker) remove(key string) {
	delete(t.pend, key)
	for i, k := range t.order {
		if k == key {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
}

// pendingLocked snapshots the pending calls, oldest first.
func (t *toolCallTracker) pendingLocked() []*toolCallTrack {
	out := make([]*toolCallTrack, 0, len(t.order))
	for _, k := range t.order {
		if tr := t.pend[k]; tr != nil {
			out = append(out, tr)
		}
	}
	return out
}

// markRepeatInterval spaces repeated tool_in_flight marks with exponential
// backoff, capped at 8× the base interval. Review finding: with
// MULTICA_AGENT_TOOL_WATCHDOG=0 (the deliberate long-campaign posture) a
// fixed repeat interval accumulates a mark every half hour for as long as the
// campaign runs, and hours-long runs would drown the transcript. Doubling per
// repeat (30m, 1h, 2h, then a 4h plateau) keeps the stalled call visible
// forever while bounding the noise logarithmically.
func markRepeatInterval(markEvery time.Duration, marksSent int) time.Duration {
	if markEvery <= 0 {
		return 0
	}
	const capMultiple = 8
	interval := markEvery
	for i := 1; i < marksSent && interval < capMultiple*markEvery; i++ {
		interval *= 2
	}
	if interval > capMultiple*markEvery {
		interval = capMultiple * markEvery
	}
	return interval
}

// watchdogEventInput builds the typed payload carried on a "watchdog"
// TaskMessageData's Input. Everything is diagnostic metadata — event name,
// state, durations, tool identity, timestamps; no free text — and the server
// ingest redacts Input regardless of message type, so nothing here can
// smuggle secrets into the transcript.
func watchdogEventInput(event string, checkedAt time.Time, extra map[string]any) map[string]any {
	in := make(map[string]any, len(extra)+2)
	in["event"] = event
	in["checked_at"] = checkedAt.UTC().Format(time.RFC3339)
	for k, v := range extra {
		in[k] = v
	}
	return in
}

// watchdogEvent is one ready-to-report observation event: the typed payload
// for Input and the tool name to surface on the message.
type watchdogEvent struct {
	input map[string]any
	tool  string
}

// watchdogObserver carries the observation knobs and turns watchdog ticks
// into ready-to-report events. Zero-value knobs disable their mechanism;
// with every knob zero the observer emits nothing and the watchdog behaves
// exactly as before this round.
//
// All methods run under the drain loop's mutex — the caller snapshots events
// under the lock and emits them after releasing it, which keeps event seqs
// monotonic against the message stream without holding the lock across I/O.
type watchdogObserver struct {
	tracker *toolCallTracker

	markAfter time.Duration
	markEvery time.Duration
}

func newWatchdogObserver(cfg Config, tracker *toolCallTracker) *watchdogObserver {
	return &watchdogObserver{
		tracker:   tracker,
		markAfter: cfg.AgentToolMarkAfter,
		markEvery: cfg.AgentToolMarkEvery,
	}
}

func (o *watchdogObserver) marksEnabled() bool { return o.markAfter > 0 }

// collectToolMarks snapshots tool_in_flight events for every call that has
// been unpaired for markAfter, then repeats on the backed-off
// markRepeatInterval schedule until the result lands or the run is
// force-stopped. Pure observation: nothing here stops the run or moves the
// silence clock. The caller emits the returned events outside the lock.
func (o *watchdogObserver) collectToolMarks(now time.Time) []watchdogEvent {
	if !o.marksEnabled() {
		return nil
	}
	var events []watchdogEvent
	for _, tr := range o.tracker.pendingLocked() {
		pending := now.Sub(tr.IssuedAt)
		if pending < o.markAfter {
			continue
		}
		if tr.Marks > 0 && (o.markEvery <= 0 || now.Sub(tr.LastMarkAt) < markRepeatInterval(o.markEvery, tr.Marks)) {
			continue
		}
		tr.Marks++
		tr.LastMarkAt = now
		extra := map[string]any{
			"state":      "tool_in_flight",
			"pending_ms": pending.Milliseconds(),
			"mark_count": tr.Marks,
		}
		if tr.CallID != "" {
			extra["call_id"] = tr.CallID
		}
		events = append(events, watchdogEvent{
			input: watchdogEventInput("tool_in_flight", now, extra),
			tool:  tr.Tool,
		})
	}
	return events
}
