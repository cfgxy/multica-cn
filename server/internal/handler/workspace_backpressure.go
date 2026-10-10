package handler

// Workspace-level host-memory backpressure settings (RUYI-618). One card per
// workspace: the settings UI (owner-only writes) saves it, the heartbeat ack
// path delivers the effective card to the workspace's daemons, and the
// daemons hot-apply it to the admission gate. A workspace that never saved
// the form gets the code defaults — no row, no drift.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// backpressureSettingsResponse is the effective card for a workspace. The
// embedded wire shape matches what the daemon receives on heartbeat acks, so
// the settings form round-trips the exact delivery payload; Custom flags
// whether an owner ever saved the form (false = the values are the code
// defaults).
type backpressureSettingsResponse struct {
	protocol.DaemonBackpressureConfig
	Custom bool `json:"custom"`
}

func backpressureConfigFromRow(row db.WorkspaceBackpressureSetting) *protocol.DaemonBackpressureConfig {
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
	}
}

// backpressureSettings resolves the effective card: the saved row when
// present, the code defaults otherwise. Storage failures degrade to defaults
// with a log — settings must never break a read or a heartbeat.
func (h *Handler) backpressureSettings(ctx context.Context, workspaceID pgtype.UUID) backpressureSettingsResponse {
	row, err := h.Queries.GetWorkspaceBackpressureSettings(ctx, workspaceID)
	if err == nil {
		return backpressureSettingsResponse{
			DaemonBackpressureConfig: *backpressureConfigFromRow(row),
			Custom:                   true,
		}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("read workspace backpressure settings", "workspace_id", uuidToString(workspaceID), "error", err)
	}
	return backpressureSettingsResponse{
		DaemonBackpressureConfig: *protocol.DefaultDaemonBackpressureConfig(),
		Custom:                   false,
	}
}

// GetWorkspaceBackpressureSettings — GET /api/workspace/backpressure-settings
func (h *Handler) GetWorkspaceBackpressureSettings(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.backpressureSettings(r.Context(), parseUUID(workspaceID)))
}

// backpressureSettingsSaveRequest is the settings-form payload. Field
// validation is delegated to the wire config's Validate so the UI cannot
// save a card the daemons would refuse at hot-apply time.
type backpressureSettingsSaveRequest struct {
	Enabled               bool    `json:"enabled"`
	MemHighPct            float64 `json:"mem_high_pct"`
	MemRecoveryPct        float64 `json:"mem_recovery_pct"`
	SwapHighPct           float64 `json:"swap_high_pct"`
	SwapRecoveryPct       float64 `json:"swap_recovery_pct"`
	PSIHighPct            float64 `json:"psi_high_pct"`
	PSIRecoveryPct        float64 `json:"psi_recovery_pct"`
	SampleIntervalSeconds int     `json:"sample_interval_seconds"`
	WindowSize            int     `json:"window_size"`
}

// PutWorkspaceBackpressureSettings — PUT /api/workspace/backpressure-settings
// Owner-only at the route layer: the watermarks pause task claiming for
// every daemon in the workspace, which is a whole-workspace operational
// decision. Invalid cards are refused with 422 and persist nothing; the
// error strings are the shared protocol validation messages the daemon logs
// on hot-apply, so what the UI shows and what a daemon would have said are
// the same sentence.
func (h *Handler) PutWorkspaceBackpressureSettings(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	var req backpressureSettingsSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cfg := protocol.DaemonBackpressureConfig{
		Enabled:               req.Enabled,
		MemHighPct:            req.MemHighPct,
		MemRecoveryPct:        req.MemRecoveryPct,
		SwapHighPct:           req.SwapHighPct,
		SwapRecoveryPct:       req.SwapRecoveryPct,
		PSIHighPct:            req.PSIHighPct,
		PSIRecoveryPct:        req.PSIRecoveryPct,
		SampleIntervalSeconds: req.SampleIntervalSeconds,
		WindowSize:            req.WindowSize,
	}
	if err := cfg.Validate(); err != nil {
		// Standard error shape {"error": ...} — the web client's
		// parseErrorBody reads `error` into ApiError.message, so the card
		// surfaces the exact validation sentence.
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if _, err := h.Queries.UpsertWorkspaceBackpressureSettings(r.Context(), db.UpsertWorkspaceBackpressureSettingsParams{
		WorkspaceID:           parseUUID(workspaceID),
		Enabled:               req.Enabled,
		MemHighPct:            req.MemHighPct,
		MemRecoveryPct:        req.MemRecoveryPct,
		SwapHighPct:           req.SwapHighPct,
		SwapRecoveryPct:       req.SwapRecoveryPct,
		PsiHighPct:            req.PSIHighPct,
		PsiRecoveryPct:        req.PSIRecoveryPct,
		SampleIntervalSeconds: int32(req.SampleIntervalSeconds),
		WindowSize:            int32(req.WindowSize),
		UpdatedBy:             member.UserID,
	}); err != nil {
		slog.Warn("save workspace backpressure settings", "workspace_id", workspaceID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure settings")
		return
	}
	writeJSON(w, http.StatusOK, h.backpressureSettings(r.Context(), parseUUID(workspaceID)))
}

// backpressureWireConfig is the heartbeat-side read of the effective card
// (RUYI-618): every ack carries it so running daemons converge on the
// workspace value within one beat. Best-effort like the rest of the ack —
// a settings read failure degrades to defaults, never fails the heartbeat.
func (h *Handler) backpressureWireConfig(ctx context.Context, workspaceID pgtype.UUID) *protocol.DaemonBackpressureConfig {
	resp := h.backpressureSettings(ctx, workspaceID)
	cfg := resp.DaemonBackpressureConfig
	return &cfg
}
