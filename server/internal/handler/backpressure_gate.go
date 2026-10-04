package handler

// RUYI-397 multi-runtime preference routing: the server passes over a
// runtime whose host reported memory backpressure when handing out new task
// claims, so a backpressured machine is not handed more work while healthy
// ones are available. The runtime's queued tasks stay queued — the hold
// delays claims, it never fails or reassigns them. This is the server-side
// half of the RUYI-393 design: the daemon already refuses to claim while its
// own gate is active, and this hold covers what that client-side gate cannot
// see — an outdated daemon that keeps polling through backpressure, and
// claim paths that arrive without a fresh machine report.

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/dispatch"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// runtimeBackpressureHoldTTL bounds how long a stored ACTIVE report may hold
// a runtime. A held runtime re-reports on every heartbeat while the gate is
// active, so a live hold keeps itself fresh; the TTL only has to outlive
// heartbeat jitter, not an outage. When reports stop arriving the hold decays
// and the server claims again — fail-open, because a queue starved by a stale
// hold is worse than a claim politely raced with a machine that stopped
// reporting. The dedupe guard in SetAgentRuntimeBackpressure does not bump
// recorded_at for an unchanged report, so the TTL is also what eventually
// releases a long-sitting identical report.
const runtimeBackpressureHoldTTL = 2 * time.Minute

type storedBackpressureReport struct {
	Active     bool   `json:"active"`
	RecordedAt string `json:"recorded_at"`
}

// runtimeHeldByBackpressure reports whether the runtime's metadata bag
// carries a fresh ACTIVE backpressure report. A missing key, an inactive
// report, malformed metadata, or an unreadable recorded_at all release the
// runtime — the report is advisory state merged from heartbeats, and the
// fail-open stance mirrors the frontend's readRuntimeBackpressure.
func runtimeHeldByBackpressure(rt db.AgentRuntime, now time.Time) bool {
	var bag struct {
		Backpressure *storedBackpressureReport `json:"backpressure"`
	}
	if err := json.Unmarshal(rt.Metadata, &bag); err != nil || bag.Backpressure == nil {
		return false
	}
	report := bag.Backpressure
	if !report.Active {
		return false
	}
	recorded, err := time.Parse(time.RFC3339, report.RecordedAt)
	if err != nil {
		slog.Debug("backpressure hold: unreadable recorded_at releases the runtime",
			"runtime_id", uuidToString(rt.ID), "recorded_at", report.RecordedAt)
		return false
	}
	return now.Sub(recorded) < runtimeBackpressureHoldTTL
}

// hydrateQueuedBackpressureReasons stamps QueuedReason on queued rows whose
// agent's CURRENT runtime is held by a fresh backpressure report (RUYI-397).
// The agent row — not the task's enqueued runtime_id — is the binding
// authority (RUYI-224), so the resolution goes agent -> runtime in two batch
// queries bounded by the response page. Best-effort decoration: any failure
// leaves the field omitted and the list still renders, the same fail-open
// stance as the hold itself.
func (h *Handler) hydrateQueuedBackpressureReasons(ctx context.Context, resp []AgentTaskResponse) {
	seen := make(map[string]struct{})
	agentIDs := make([]pgtype.UUID, 0, len(resp))
	for i := range resp {
		if resp[i].Status != "queued" || resp[i].AgentID == "" {
			continue
		}
		if _, ok := seen[resp[i].AgentID]; ok {
			continue
		}
		seen[resp[i].AgentID] = struct{}{}
		if id, err := util.ParseUUID(resp[i].AgentID); err == nil {
			agentIDs = append(agentIDs, id)
		}
	}
	if len(agentIDs) == 0 {
		return
	}
	agents, err := h.Queries.GetAgentRuntimeBindings(ctx, agentIDs)
	if err != nil {
		slog.Debug("queued-reason hydration: agent bindings unavailable", "error", err)
		return
	}
	rtIDs := make([]pgtype.UUID, 0, len(agents))
	binding := make(map[string]string, len(agents))
	for _, a := range agents {
		if !a.RuntimeID.Valid {
			continue
		}
		binding[uuidToString(a.ID)] = uuidToString(a.RuntimeID)
		rtIDs = append(rtIDs, a.RuntimeID)
	}
	if len(rtIDs) == 0 {
		return
	}
	runtimes, err := h.Queries.GetAgentRuntimes(ctx, rtIDs)
	if err != nil {
		slog.Debug("queued-reason hydration: runtimes unavailable", "error", err)
		return
	}
	now := time.Now()
	held := make(map[string]bool, len(runtimes))
	for _, rt := range runtimes {
		held[uuidToString(rt.ID)] = runtimeHeldByBackpressure(rt, now)
	}
	for i := range resp {
		if resp[i].Status != "queued" {
			continue
		}
		if held[binding[resp[i].AgentID]] {
			resp[i].QueuedReason = string(dispatch.ReasonRuntimeBackpressure)
		}
	}
}
