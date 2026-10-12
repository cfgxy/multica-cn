package handler

// RUYI-729 stage 1: daemon host-backpressure settings — HTTP surface.
//
// GET/PUT/DELETE /api/runtimes/{runtimeId}/backpressure-settings and
// GET /api/runtimes/{runtimeId}/backpressure-events.
//
// The settings row is per daemon host (the daemon_id all of a host's
// runtimes share), but the surface is runtime-shaped because that is what
// the Runtime detail page addresses. The PUT re-resolves the host from the
// runtime row, so saving from any runtime on the same host writes the same
// row — the gate is host-level, never per-runtime.
//
// Authorization mirrors the scheduling freeze (RUYI-608): reads are
// member-visible (the badge and detail page already expose the live
// report), writes are human workspace owner/admin only — an agent must not
// be able to loosen the gate that is throttling it. Every state change is
// fail-closed audited inside its transaction (ops.backpressure_settings_*
// families) with the full old→new cards in details; cards carry thresholds
// and cadence only, never credentials.
//
// Delivery is pull-based: the daemon picks the card up on its next
// heartbeat ack, so a save lands within one beat without a restart.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// backpressureSettingsResponse is the Runtime detail page's read model: the
// effective card (saved or code default), whether a row exists, the current
// live report from the runtime's metadata bag, and the event history anchor
// fields.
type backpressureSettingsResponse struct {
	DaemonID   string                            `json:"daemon_id"`
	Configured bool                              `json:"configured"`
	Config     *protocol.DaemonBackpressureConfig `json:"config"`
	UpdatedBy  string                            `json:"updated_by,omitempty"`
	UpdatedAt  string                            `json:"updated_at,omitempty"`
	Report     *protocol.DaemonBackpressureReport `json:"report,omitempty"`
}

// runtimeBackpressureReport pulls the live report back out of the runtime
// metadata bag — the same loosely-typed bag runtimeHeldByBackpressure reads
// for the claim hold, decoded here through the protocol type the daemon
// serialized.
func runtimeBackpressureReport(rt db.AgentRuntime) *protocol.DaemonBackpressureReport {
	var bag struct {
		Backpressure *protocol.DaemonBackpressureReport `json:"backpressure"`
	}
	if err := json.Unmarshal(rt.Metadata, &bag); err != nil || bag.Backpressure == nil {
		return nil
	}
	return bag.Backpressure
}

func backpressureSettingsResponseFrom(rt db.AgentRuntime, row *db.DaemonBackpressureSetting) backpressureSettingsResponse {
	resp := backpressureSettingsResponse{
		DaemonID:   rt.DaemonID.String,
		Configured: row != nil,
		Report:     runtimeBackpressureReport(rt),
	}
	if row != nil {
		resp.Config = &protocol.DaemonBackpressureConfig{
			Enabled:               row.Enabled,
			MemHighPct:            row.MemHighPct,
			MemRecoveryPct:        row.MemRecoveryPct,
			SwapHighPct:           row.SwapHighPct,
			SwapRecoveryPct:       row.SwapRecoveryPct,
			PSIHighPct:            row.PsiHighPct,
			PSIRecoveryPct:        row.PsiRecoveryPct,
			SampleIntervalSeconds: int(row.SampleIntervalSeconds),
			WindowSize:            int(row.WindowSize),
			ConfigVersion:         row.ConfigVersion,
			Source:                protocol.BackpressureSourceDB,
		}
		if row.UpdatedBy.Valid {
			resp.UpdatedBy = util.UUIDToString(row.UpdatedBy)
		}
		resp.UpdatedAt = row.UpdatedAt.Time.UTC().Format(time.RFC3339)
	} else {
		resp.Config = protocol.DefaultDaemonBackpressureConfig()
	}
	return resp
}

// effectiveBackpressureConfig resolves the card to deliver on a heartbeat
// ack: the saved row when one exists, nil otherwise (the daemon keeps its
// boot config — env or code defaults — so the source of truth stays
// singular). Heartbeat-path best effort: a lookup failure must never fail
// the beat, it just defers delivery to the next one.
func (h *Handler) effectiveBackpressureConfig(ctx context.Context, daemonID string) *protocol.DaemonBackpressureConfig {
	if daemonID == "" {
		return nil
	}
	row, err := h.Queries.GetDaemonBackpressureSettings(ctx, daemonID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Debug("backpressure settings lookup failed; deferring delivery", "error", err)
		}
		return nil
	}
	return &protocol.DaemonBackpressureConfig{
		Enabled:               row.Enabled,
		MemHighPct:            row.MemHighPct,
		MemRecoveryPct:        row.MemRecoveryPct,
		SwapHighPct:           row.SwapHighPct,
		SwapRecoveryPct:       row.SwapRecoveryPct,
		PSIHighPct:            row.PsiHighPct,
		PSIRecoveryPct:        row.PsiRecoveryPct,
		SampleIntervalSeconds: int(row.SampleIntervalSeconds),
		WindowSize:            int(row.WindowSize),
		ConfigVersion:         row.ConfigVersion,
		Source:                protocol.BackpressureSourceDB,
	}
}

// GetRuntimeBackpressureSettings serves the Runtime detail page's settings
// card. Unconfigured hosts answer with the code defaults and
// configured=false, so the form always has concrete numbers to show and
// edit.
func (h *Handler) GetRuntimeBackpressureSettings(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, metrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	row, err := h.Queries.GetDaemonBackpressureSettings(r.Context(), rt.DaemonID.String)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Error("get backpressure settings failed", "runtime_id", util.UUIDToString(rt.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load backpressure settings")
		return
	}
	var rowPtr *db.DaemonBackpressureSetting
	if err == nil {
		rowPtr = &row
	}
	writeJSON(w, http.StatusOK, backpressureSettingsResponseFrom(rt, rowPtr))
}

// decodeBackpressureSettingsRequest reads the save body. The whole card is
// required — partial updates would make "what is in force" ambiguous
// between the row and the omitted fields.
func decodeBackpressureSettingsRequest(w http.ResponseWriter, r *http.Request) (protocol.DaemonBackpressureConfig, bool) {
	var card protocol.DaemonBackpressureConfig
	if err := json.NewDecoder(r.Body).Decode(&card); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return card, false
	}
	// The save path owns source and version: the client does not get to
	// stamp its own audit provenance.
	card.Source = ""
	card.ConfigVersion = 0
	return card, true
}

// PutRuntimeBackpressureSettings validates and saves the host's gate card,
// audited fail-closed in the same transaction (an administrative
// control-plane write without its trail is treated as not having happened).
// The daemon picks the new card up on its next heartbeat ack.
func (h *Handler) PutRuntimeBackpressureSettings(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, metrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, util.UUIDToString(rt.WorkspaceID))
	if !ok {
		return
	}
	card, ok := decodeBackpressureSettingsRequest(w, r)
	if !ok {
		return
	}
	if err := protocol.ValidateBackpressureConfig(&card); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Old card for the audit trail (nil when this is the host's first save).
	prev, prevErr := h.Queries.GetDaemonBackpressureSettings(r.Context(), rt.DaemonID.String)
	hasPrev := prevErr == nil
	if prevErr != nil && !errors.Is(prevErr, pgx.ErrNoRows) {
		slog.Error("load previous backpressure settings failed", "runtime_id", util.UUIDToString(rt.ID), "error", prevErr)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure settings")
		return
	}

	ctx := r.Context()
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Error("begin backpressure settings tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure settings")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	row, err := qtx.UpsertDaemonBackpressureSettings(ctx, db.UpsertDaemonBackpressureSettingsParams{
		DaemonID:              rt.DaemonID.String,
		WorkspaceID:           rt.WorkspaceID,
		Enabled:               card.Enabled,
		MemHighPct:            card.MemHighPct,
		MemRecoveryPct:        card.MemRecoveryPct,
		SwapHighPct:           card.SwapHighPct,
		SwapRecoveryPct:       card.SwapRecoveryPct,
		PsiHighPct:            card.PSIHighPct,
		PsiRecoveryPct:        card.PSIRecoveryPct,
		SampleIntervalSeconds: int32(card.SampleIntervalSeconds),
		WindowSize:            int32(card.WindowSize),
		UpdatedBy:             member.UserID,
	})
	if err != nil {
		slog.Error("save backpressure settings failed", "runtime_id", util.UUIDToString(rt.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure settings")
		return
	}

	oldCard := (*protocol.DaemonBackpressureConfig)(nil)
	if hasPrev {
		oldCard = &protocol.DaemonBackpressureConfig{
			Enabled:               prev.Enabled,
			MemHighPct:            prev.MemHighPct,
			MemRecoveryPct:        prev.MemRecoveryPct,
			SwapHighPct:           prev.SwapHighPct,
			SwapRecoveryPct:       prev.SwapRecoveryPct,
			PSIHighPct:            prev.PsiHighPct,
			PSIRecoveryPct:        prev.PsiRecoveryPct,
			SampleIntervalSeconds: int(prev.SampleIntervalSeconds),
			WindowSize:            int(prev.WindowSize),
			ConfigVersion:         prev.ConfigVersion,
			Source:                protocol.BackpressureSourceDB,
		}
	}
	details, _ := json.Marshal(map[string]any{
		"daemon_id": rt.DaemonID.String,
		"runtime_id": util.UUIDToString(rt.ID),
		"old":       oldCard,
		"new":       card,
	})
	if err := service.AppendAuditEvents(ctx, qtx, service.Event{
		ID:          dbid.NewV7(),
		WorkspaceID: rt.WorkspaceID,
		Domain:      service.AuditDomainOps,
		EventType:   service.AuditOpsBackpressureSettingsUpdated,
		OccurredAt:  time.Now().UTC(),
		ActorType:   service.AuditActorMember,
		ActorID:     member.UserID,
		Details:     details,
	}); err != nil {
		slog.Error("audit backpressure settings update failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure settings")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit backpressure settings tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure settings")
		return
	}
	slog.Info("backpressure settings saved",
		"daemon_id", rt.DaemonID.String,
		"runtime_id", util.UUIDToString(rt.ID),
		"config_version", row.ConfigVersion,
		"actor", util.UUIDToString(member.UserID))
	writeJSON(w, http.StatusOK, backpressureSettingsResponseFrom(rt, &row))
}

// DeleteRuntimeBackpressureSettings restores the host to code/env defaults
// by removing the row; the next heartbeat ack delivers nothing and the
// daemon keeps its boot configuration. Idempotent: no row is a no-op that
// still answers with the default card. Removal is audited like the save.
func (h *Handler) DeleteRuntimeBackpressureSettings(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, metrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, util.UUIDToString(rt.WorkspaceID))
	if !ok {
		return
	}

	ctx := r.Context()
	prev, prevErr := h.Queries.GetDaemonBackpressureSettings(ctx, rt.DaemonID.String)
	if errors.Is(prevErr, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, backpressureSettingsResponseFrom(rt, nil))
		return
	}
	if prevErr != nil {
		slog.Error("load backpressure settings for delete failed", "runtime_id", util.UUIDToString(rt.ID), "error", prevErr)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure settings")
		return
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Error("begin backpressure settings delete tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure settings")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	if err := qtx.DeleteDaemonBackpressureSettings(ctx, rt.DaemonID.String); err != nil {
		slog.Error("delete backpressure settings failed", "runtime_id", util.UUIDToString(rt.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure settings")
		return
	}
	details, _ := json.Marshal(map[string]any{
		"daemon_id":  rt.DaemonID.String,
		"runtime_id": util.UUIDToString(rt.ID),
		"old": protocol.DaemonBackpressureConfig{
			Enabled:               prev.Enabled,
			MemHighPct:            prev.MemHighPct,
			MemRecoveryPct:        prev.MemRecoveryPct,
			SwapHighPct:           prev.SwapHighPct,
			SwapRecoveryPct:       prev.SwapRecoveryPct,
			PSIHighPct:            prev.PsiHighPct,
			PSIRecoveryPct:        prev.PsiRecoveryPct,
			SampleIntervalSeconds: int(prev.SampleIntervalSeconds),
			WindowSize:            int(prev.WindowSize),
			ConfigVersion:         prev.ConfigVersion,
			Source:                protocol.BackpressureSourceDB,
		},
	})
	if err := service.AppendAuditEvents(ctx, qtx, service.Event{
		ID:          dbid.NewV7(),
		WorkspaceID: rt.WorkspaceID,
		Domain:      service.AuditDomainOps,
		EventType:   service.AuditOpsBackpressureSettingsRemoved,
		OccurredAt:  time.Now().UTC(),
		ActorType:   service.AuditActorMember,
		ActorID:     member.UserID,
		Details:     details,
	}); err != nil {
		slog.Error("audit backpressure settings removal failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure settings")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit backpressure settings delete tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure settings")
		return
	}
	writeJSON(w, http.StatusOK, backpressureSettingsResponseFrom(rt, nil))
}

// backpressureEventResponse is one timeline row of the Runtime detail
// page's trigger/recovery history.
type backpressureEventResponse struct {
	ID                string                              `json:"id"`
	EventType         string                              `json:"event_type"`
	Reason            string                              `json:"reason"`
	MemAvailablePct   float64                             `json:"mem_available_pct"`
	SwapUsedPct       float64                             `json:"swap_used_pct"`
	PsiSomeAvg10      float64                             `json:"psi_some_avg10"`
	PsiReadOK         bool                                `json:"psi_read_ok"`
	DeferredClaims    int64                               `json:"deferred_claims"`
	EffectiveConfig   protocol.DaemonBackpressureConfig   `json:"effective_config"`
	Hits              []protocol.DaemonBackpressureCondition `json:"hits"`
	OccurredAt        string                              `json:"occurred_at"`
}

// ListRuntimeBackpressureEvents serves the newest-first trigger/recovery
// timeline; limit is bounded so a generous client cannot read the table
// whole.
func (h *Handler) ListRuntimeBackpressureEvents(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, metrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	limit := int32(50)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = int32(n)
		}
	}
	rows, err := h.Queries.ListDaemonBackpressureEvents(r.Context(), db.ListDaemonBackpressureEventsParams{
		RuntimeID: rt.ID,
		Limit:     limit,
	})
	if err != nil {
		slog.Error("list backpressure events failed", "runtime_id", util.UUIDToString(rt.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list backpressure events")
		return
	}
	resp := make([]backpressureEventResponse, 0, len(rows))
	for _, row := range rows {
		event := backpressureEventResponse{
			ID:              util.UUIDToString(row.ID),
			EventType:       row.EventType,
			Reason:          row.Reason,
			MemAvailablePct: row.MemAvailablePct,
			SwapUsedPct:     row.SwapUsedPct,
			PsiSomeAvg10:    row.PsiSomeAvg10,
			PsiReadOK:       row.PsiReadOk,
			DeferredClaims:  row.DeferredClaims,
			OccurredAt:      row.OccurredAt.Time.UTC().Format(time.RFC3339),
		}
		// The config snapshot was validated at write time; a decode failure
		// means an ancient/corrupt row — degrade to an empty card rather
		// than failing the whole timeline.
		_ = json.Unmarshal(row.EffectiveConfig, &event.EffectiveConfig)
		_ = json.Unmarshal(row.Hits, &event.Hits)
		resp = append(resp, event)
	}
	// Bare array, like every other list endpoint: the frontend consumes this
	// through a z.array(...) schema whose parseWithFallback silently degrades
	// a shape mismatch to an empty list (the D3 defect — the wrapper object
	// made the timeline never render).
	writeJSON(w, http.StatusOK, resp)
}

// recordBackpressureEvent appends the transition history row (RUYI-729 ④).
// The effective card comes from the report itself when the daemon echoes
// one; for pre-stage-1 daemons it falls back to the server's view of the
// host config (saved row or defaults) so the history stays interpretable.
// Best-effort by design: a history write failure must never fail the
// heartbeat that delivered the report.
func (h *Handler) recordBackpressureEvent(ctx context.Context, rt db.AgentRuntime, report *protocol.DaemonBackpressureReport) {
	card := report.Config
	if card == nil {
		card = h.effectiveBackpressureConfig(ctx, rt.DaemonID.String)
		if card == nil {
			card = protocol.DefaultDaemonBackpressureConfig()
		}
	}
	configJSON, err := json.Marshal(card)
	if err != nil {
		slog.Warn("marshal backpressure event config", "runtime_id", util.UUIDToString(rt.ID), "error", err)
		return
	}
	hits := report.Conditions
	if hits == nil {
		hits = []protocol.DaemonBackpressureCondition{}
	}
	hitsJSON, err := json.Marshal(hits)
	if err != nil {
		slog.Warn("marshal backpressure event hits", "runtime_id", util.UUIDToString(rt.ID), "error", err)
		return
	}
	eventType := "cleared"
	if report.Active {
		eventType = "entered"
	}
	_, err = h.Queries.InsertDaemonBackpressureEvent(ctx, db.InsertDaemonBackpressureEventParams{
		RuntimeID:        rt.ID,
		DaemonID:         rt.DaemonID.String,
		WorkspaceID:      rt.WorkspaceID,
		EventType:        eventType,
		Reason:           report.Reason,
		MemAvailablePct:  report.MemAvailablePct,
		SwapUsedPct:      report.SwapUsedPct,
		PsiSomeAvg10:     report.PSIMemorySomeAvg10,
		PsiReadOk:        report.PSIReadOK,
		DeferredClaims:   report.DeferredClaims,
		EffectiveConfig:  configJSON,
		Hits:             hitsJSON,
	})
	if err != nil {
		slog.Warn("record backpressure event failed", "runtime_id", util.UUIDToString(rt.ID), "error", err)
	}
}
