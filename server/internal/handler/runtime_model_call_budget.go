package handler

// Runtime model-call budget HTTP surface. Unlike daemon host backpressure,
// this is a per-runtime scheduling control calculated entirely from durable
// task_usage rows. Daemon reports and /proc snapshots never carry this usage.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const (
	defaultRuntimeModelCallBudgetMaxCalls      int64 = 1000
	defaultRuntimeModelCallBudgetWindowSeconds int32 = 86400
	minRuntimeModelCallBudgetWindowSeconds     int32 = 60
	maxRuntimeModelCallBudgetWindowSeconds     int32 = 90 * 24 * 60 * 60
	maxRuntimeModelCallBudgetMaxCalls          int64 = 1_000_000_000
)

type runtimeModelCallBudgetConfig struct {
	Enabled       bool  `json:"enabled"`
	MaxCalls      int64 `json:"max_calls"`
	WindowSeconds int32 `json:"window_seconds"`
}

type runtimeModelCallBudgetReport struct {
	KnownCalls       int64  `json:"known_calls"`
	UnknownUsageRows int64  `json:"unknown_usage_rows"`
	Complete         bool   `json:"complete"`
	Active           bool   `json:"active"`
	WindowStartedAt  string `json:"window_started_at"`
}

type runtimeModelCallBudgetResponse struct {
	Config runtimeModelCallBudgetConfig `json:"config"`
	Report runtimeModelCallBudgetReport `json:"report"`
}

func runtimeModelCallBudgetConfigFrom(rt db.AgentRuntime) runtimeModelCallBudgetConfig {
	return runtimeModelCallBudgetConfig{
		Enabled:       rt.ModelCallBudgetEnabled,
		MaxCalls:      rt.ModelCallBudgetMaxCalls,
		WindowSeconds: rt.ModelCallBudgetWindowSeconds,
	}
}

func validateRuntimeModelCallBudgetConfig(config runtimeModelCallBudgetConfig) error {
	if config.MaxCalls < 1 || config.MaxCalls > maxRuntimeModelCallBudgetMaxCalls {
		return fmt.Errorf("max_calls must be between 1 and %d", maxRuntimeModelCallBudgetMaxCalls)
	}
	if config.WindowSeconds < minRuntimeModelCallBudgetWindowSeconds || config.WindowSeconds > maxRuntimeModelCallBudgetWindowSeconds {
		return fmt.Errorf("window_seconds must be between %d and %d", minRuntimeModelCallBudgetWindowSeconds, maxRuntimeModelCallBudgetWindowSeconds)
	}
	return nil
}

func runtimeModelCallBudgetWindowStart(now time.Time, config runtimeModelCallBudgetConfig) time.Time {
	return now.UTC().Add(-time.Duration(config.WindowSeconds) * time.Second)
}

func modelCallBudgetActive(config runtimeModelCallBudgetConfig, knownCalls, unknownUsageRows int64) bool {
	return validateRuntimeModelCallBudgetConfig(config) == nil &&
		config.Enabled && unknownUsageRows == 0 && knownCalls >= config.MaxCalls
}

func (h *Handler) runtimeModelCallBudgetResponse(
	ctx context.Context,
	rt db.AgentRuntime,
	now time.Time,
) (runtimeModelCallBudgetResponse, error) {
	config := runtimeModelCallBudgetConfigFrom(rt)
	windowStart := runtimeModelCallBudgetWindowStart(now, config)
	usage, err := h.Queries.GetRuntimeModelCallBudgetUsage(ctx, db.GetRuntimeModelCallBudgetUsageParams{
		RuntimeID: rt.ID,
		Since:     pgtype.Timestamptz{Time: windowStart, Valid: true},
	})
	if err != nil {
		return runtimeModelCallBudgetResponse{}, err
	}
	return runtimeModelCallBudgetResponse{
		Config: config,
		Report: runtimeModelCallBudgetReport{
			KnownCalls:       usage.KnownCalls,
			UnknownUsageRows: usage.UnknownUsageRows,
			Complete:         usage.UnknownUsageRows == 0,
			Active:           modelCallBudgetActive(config, usage.KnownCalls, usage.UnknownUsageRows),
			WindowStartedAt:  windowStart.Format(time.RFC3339),
		},
	}, nil
}

// runtimeHeldByModelCallBudget is deliberately fail-open. A query problem,
// invalid persisted configuration, or even one provider row without original
// evidence must never turn a partial count into an admission hold.
func (h *Handler) runtimeHeldByModelCallBudget(ctx context.Context, rt db.AgentRuntime, now time.Time) bool {
	config := runtimeModelCallBudgetConfigFrom(rt)
	if !config.Enabled {
		return false
	}
	if err := validateRuntimeModelCallBudgetConfig(config); err != nil {
		slog.Debug("model-call budget hold: invalid persisted config releases runtime",
			"runtime_id", uuidToString(rt.ID), "error", err)
		return false
	}
	usage, err := h.Queries.GetRuntimeModelCallBudgetUsage(ctx, db.GetRuntimeModelCallBudgetUsageParams{
		RuntimeID: rt.ID,
		Since: pgtype.Timestamptz{
			Time:  runtimeModelCallBudgetWindowStart(now, config),
			Valid: true,
		},
	})
	if err != nil {
		slog.Debug("model-call budget hold: usage lookup releases runtime",
			"runtime_id", uuidToString(rt.ID), "error", err)
		return false
	}
	return modelCallBudgetActive(config, usage.KnownCalls, usage.UnknownUsageRows)
}

// runtimeModelCallBudgetHolds batch-loads usage for queue decoration and the
// multi-runtime claim endpoint. Missing rows and failed reads leave the result
// false, matching the single-runtime hold's fail-open behavior.
func (h *Handler) runtimeModelCallBudgetHolds(ctx context.Context, runtimes []db.AgentRuntime, now time.Time) map[string]bool {
	holds := make(map[string]bool, len(runtimes))
	ids := make([]pgtype.UUID, 0, len(runtimes))
	eligible := make(map[string]runtimeModelCallBudgetConfig, len(runtimes))
	for _, rt := range runtimes {
		config := runtimeModelCallBudgetConfigFrom(rt)
		if !config.Enabled || validateRuntimeModelCallBudgetConfig(config) != nil {
			continue
		}
		id := uuidToString(rt.ID)
		ids = append(ids, rt.ID)
		eligible[id] = config
	}
	if len(ids) == 0 {
		return holds
	}
	rows, err := h.Queries.ListRuntimeModelCallBudgetUsage(ctx, db.ListRuntimeModelCallBudgetUsageParams{
		Now:        pgtype.Timestamptz{Time: now.UTC(), Valid: true},
		RuntimeIds: ids,
	})
	if err != nil {
		slog.Debug("model-call budget holds: usage lookup releases runtimes", "error", err)
		return holds
	}
	for _, row := range rows {
		id := uuidToString(row.RuntimeID)
		config, ok := eligible[id]
		if !ok {
			continue
		}
		holds[id] = modelCallBudgetActive(config, row.KnownCalls, row.UnknownUsageRows)
	}
	return holds
}

func (h *Handler) GetRuntimeModelCallBudget(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, metrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	resp, err := h.runtimeModelCallBudgetResponse(r.Context(), rt, time.Now())
	if err != nil {
		slog.Error("get runtime model-call budget failed", "runtime_id", uuidToString(rt.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load model-call budget")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func decodeRuntimeModelCallBudgetConfig(w http.ResponseWriter, r *http.Request) (runtimeModelCallBudgetConfig, bool) {
	var config runtimeModelCallBudgetConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return config, false
	}
	if err := validateRuntimeModelCallBudgetConfig(config); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return config, false
	}
	return config, true
}

func (h *Handler) PutRuntimeModelCallBudget(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, metrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, util.UUIDToString(rt.WorkspaceID))
	if !ok {
		return
	}
	config, ok := decodeRuntimeModelCallBudgetConfig(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Error("begin runtime model-call budget tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save model-call budget")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	updated, err := qtx.UpdateAgentRuntimeModelCallBudget(ctx, db.UpdateAgentRuntimeModelCallBudgetParams{
		ModelCallBudgetEnabled:       config.Enabled,
		ModelCallBudgetMaxCalls:      config.MaxCalls,
		ModelCallBudgetWindowSeconds: config.WindowSeconds,
		ID:                           rt.ID,
	})
	if err != nil {
		slog.Error("save runtime model-call budget failed", "runtime_id", uuidToString(rt.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save model-call budget")
		return
	}
	details, _ := json.Marshal(map[string]any{
		"runtime_id": uuidToString(rt.ID),
		"old":        runtimeModelCallBudgetConfigFrom(rt),
		"new":        config,
	})
	if err := service.AppendAuditEvents(ctx, qtx, service.Event{
		ID:          dbid.NewV7(),
		WorkspaceID: rt.WorkspaceID,
		Domain:      service.AuditDomainOps,
		EventType:   service.AuditOpsRuntimeModelCallBudgetUpdated,
		OccurredAt:  time.Now().UTC(),
		ActorType:   service.AuditActorMember,
		ActorID:     member.UserID,
		Details:     details,
	}); err != nil {
		slog.Error("audit runtime model-call budget update failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save model-call budget")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit runtime model-call budget tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save model-call budget")
		return
	}
	resp, err := h.runtimeModelCallBudgetResponse(ctx, updated, time.Now())
	if err != nil {
		slog.Error("read saved runtime model-call budget failed", "runtime_id", uuidToString(rt.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "saved model-call budget but failed to load usage")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
