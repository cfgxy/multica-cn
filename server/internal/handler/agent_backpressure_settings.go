package handler

// RUYI-859 stage A: agent-level error-rate backpressure policy — the config
// surface and the read-only visibility on the Agent detail page.
//
// GET/PUT/DELETE /api/agents/{id}/backpressure-policy and
// GET /api/agents/{id}/backpressure-events.
//
// The row is per agent inside a workspace; a missing row means the built-in
// defaults (read-only monitoring, automatic suppression OFF), so the card
// always answers with concrete numbers. Writes are human workspace
// owner/admin only — the same gate as the scheduling freeze (RUYI-608) —
// and every save/delete is audited fail-closed in its transaction
// (agent.suppression_config_updated) with the old→new cards. The automatic
// suppression loop that would act on an armed policy is stage B and is NOT
// wired here: stage A cannot pause or resume anything, and no host-gate
// exemption fields exist for it to act on.

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

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// ---- Read model (GET /api/agents/{id}/backpressure-policy) ----

type agentBackpressurePolicyCard struct {
	AgentID    string                            `json:"agent_id"`
	Configured bool                              `json:"configured"`
	Config     *protocol.AgentBackpressureConfig `json:"config"`
	UpdatedBy  string                            `json:"updated_by,omitempty"`
	UpdatedAt  string                            `json:"updated_at,omitempty"`
}

// agentBackpressureWindowReading is the current-window error-rate feed: the
// terminal-sample counts split by display class, the selected numerator,
// the total denominator, and the derived rate. All fields are present when
// the agent has no samples yet (rate 0, counts 0) — the client renders
// "no data", not "0% errors".
type agentBackpressureWindowReading struct {
	WindowMinutes        int            `json:"window_minutes"`
	TotalTerminal        int            `json:"total_terminal"`
	SelectedFailures     int            `json:"selected_failures"`
	RecoveryThresholdPct float64        `json:"recovery_threshold_pct"`
	ErrorRatePct         *float64       `json:"error_rate_pct,omitempty"`
	ByClass              map[string]int `json:"by_class"`
}

// agentBackpressureSuppressionEvent is one transition row, newest first.
type agentBackpressureSuppressionEvent struct {
	ID              string          `json:"id"`
	EventType       string          `json:"event_type"`
	SampleCount     int             `json:"sample_count"`
	ErrorRatePct    float64         `json:"error_rate_pct"`
	EffectivePolicy json.RawMessage `json:"effective_policy"`
	OccurredAt      string          `json:"occurred_at"`
}

type agentBackpressurePolicyResponse struct {
	AgentID    string                            `json:"agent_id"`
	Configured bool                              `json:"configured"`
	Config     *protocol.AgentBackpressureConfig `json:"config"`
	UpdatedBy  string                            `json:"updated_by,omitempty"`
	UpdatedAt  string                            `json:"updated_at,omitempty"`
	// Window is the live entry-window reading against the effective card.
	Window *agentBackpressureWindowReading `json:"window"`
	// Scheduling is the agent-level pause state (agent or workspace scope),
	// reused from the RUYI-608 reader — it is the "human or auto pause
	// source" the card must surface, alongside the live queued depth.
	Scheduling *service.SchedulingState `json:"scheduling"`
	// Events is the newest-first transition history (stage B writes it;
	// stage A reads and displays it).
	Events []agentBackpressureSuppressionEvent `json:"events"`
}

// loadAgentBackpressureRow fetches the policy row; a miss returns (nil, nil)
// — the defaults path, not an error.
func (h *Handler) loadAgentBackpressureRow(ctx context.Context, agent db.Agent) (*db.AgentBackpressurePolicy, error) {
	row, err := h.Queries.GetAgentBackpressurePolicy(ctx, db.GetAgentBackpressurePolicyParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// agentBackpressurePolicyFromCard builds the read model: the saved row, or
// the built-in defaults when the agent has never been configured.
func agentBackpressurePolicyFromCard(agent db.Agent, row *db.AgentBackpressurePolicy) agentBackpressurePolicyCard {
	card := agentBackpressurePolicyCard{AgentID: util.UUIDToString(agent.ID), Configured: row != nil}
	if row != nil {
		card.Config = &protocol.AgentBackpressureConfig{
			WindowMinutes:          int(row.WindowMinutes),
			ErrorRateThresholdPct:  row.ErrorRateThresholdPct,
			MinSamples:             int(row.MinSamples),
			RecoveryWindowMinutes:  int(row.RecoveryWindowMinutes),
			SuppressOnErrorRate:    row.SuppressOnErrorRate,
			CountModelAPIFailures:  row.CountModelApiFailures,
			CountExecutionFailures: row.CountExecutionFailures,
		}
		if row.UpdatedBy.Valid {
			card.UpdatedBy = util.UUIDToString(row.UpdatedBy)
		}
		card.UpdatedAt = row.UpdatedAt.Time.UTC().Format(time.RFC3339)
	} else {
		card.Config = protocol.DefaultAgentBackpressureConfig()
	}
	return card
}

// agentBackpressureWindowReading computes the current-window error-rate feed
// for the effective card's entry window: terminal samples by display class
// (taskfailure folds both selected groups), the numerator the two class
// switches select, the denominator, and the derived rate (nil when no
// samples). Queued rows — including runtime-backpressure holds — are
// structurally outside the terminal set and never count here; their depth
// is read separately through the Scheduling block.
func (h *Handler) agentBackpressureWindowReading(ctx context.Context, agent db.Agent, cfg *protocol.AgentBackpressureConfig) (*agentBackpressureWindowReading, error) {
	rows, err := h.Queries.CountAgentTerminalTasksByReasonInWindow(ctx, db.CountAgentTerminalTasksByReasonInWindowParams{
		WorkspaceID:   agent.WorkspaceID,
		AgentID:       agent.ID,
		WindowMinutes: int32(cfg.WindowMinutes),
	})
	if err != nil {
		return nil, err
	}
	reading := &agentBackpressureWindowReading{
		WindowMinutes:        cfg.WindowMinutes,
		RecoveryThresholdPct: cfg.ErrorRateThresholdPct * protocol.AgentBackpressureRecoveryFactor,
		ByClass:              map[string]int{},
	}
	for _, row := range rows {
		// reason '' is the succeeded bucket; taskfailure folds real
		// reasons (including 'unclassified') into the display classes the
		// card's two switches select over.
		cls := taskfailure.ClassOther
		if row.Reason != "" {
			cls = taskfailure.DisplayClass(taskfailure.Reason(row.Reason))
		}
		reading.ByClass[cls] += int(row.TaskCount)
		if row.Reason == "" {
			continue // succeeded: denominator only
		}
		if (cls == taskfailure.ClassAuth || cls == taskfailure.ClassRateLimit ||
			cls == taskfailure.ClassTimeout || cls == taskfailure.ClassProvider) && cfg.CountModelAPIFailures {
			reading.SelectedFailures += int(row.TaskCount)
		}
		if (cls == taskfailure.ClassRuntime || cls == taskfailure.ClassAgent || cls == taskfailure.ClassOther) && cfg.CountExecutionFailures {
			reading.SelectedFailures += int(row.TaskCount)
		}
	}
	for _, row := range rows {
		reading.TotalTerminal += int(row.TaskCount)
	}
	if reading.TotalTerminal > 0 {
		rate := float64(reading.SelectedFailures) / float64(reading.TotalTerminal) * 100
		reading.ErrorRatePct = &rate
	}
	return reading, nil
}

// GetAgentBackpressurePolicy serves the Agent detail page's backpressure
// settings card: the effective config, the current-window error-rate
// reading, the class criteria, the pause source, and the recent events.
// Reads follow the Agent's visibility (loadAgentForUser + the private
// gate, exactly as GetAgent does); only the writes are admin-restricted.
func (h *Handler) GetAgentBackpressurePolicy(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	workspaceID := util.UUIDToString(agent.WorkspaceID)
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	if !h.canAccessPrivateAgent(r.Context(), agent, actorType, actorID, workspaceID) {
		writeError(w, http.StatusForbidden, "you do not have access to this agent")
		return
	}
	row, err := h.loadAgentBackpressureRow(r.Context(), agent)
	if err != nil {
		slog.Error("load agent backpressure policy failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load backpressure policy")
		return
	}
	card := agentBackpressurePolicyFromCard(agent, row)
	reading, err := h.agentBackpressureWindowReading(r.Context(), agent, card.Config)
	if err != nil {
		slog.Error("compute agent backpressure window reading failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read backpressure window")
		return
	}
	state, err := h.TaskService.AgentSchedulingState(r.Context(), agent.WorkspaceID, agent.ID)
	if err != nil {
		slog.Error("read agent scheduling state failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read scheduling state")
		return
	}
	events, err := h.listAgentBackpressureEvents(r.Context(), agent, 10)
	if err != nil {
		slog.Error("list agent suppression events failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list suppression events")
		return
	}
	writeJSON(w, http.StatusOK, agentBackpressurePolicyResponse{
		AgentID:    card.AgentID,
		Configured: card.Configured,
		Config:     card.Config,
		UpdatedBy:  card.UpdatedBy,
		UpdatedAt:  card.UpdatedAt,
		Window:     reading,
		Scheduling: &state,
		Events:     events,
	})
}

func (h *Handler) listAgentBackpressureEvents(ctx context.Context, agent db.Agent, limit int32) ([]agentBackpressureSuppressionEvent, error) {
	rows, err := h.Queries.ListAgentSuppressionEvents(ctx, db.ListAgentSuppressionEventsParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
		MaxEvents:   limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]agentBackpressureSuppressionEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, agentBackpressureSuppressionEvent{
			ID:              util.UUIDToString(row.ID),
			EventType:       row.EventType,
			SampleCount:     int(row.SampleCount),
			ErrorRatePct:    row.ErrorRatePct,
			EffectivePolicy: row.EffectivePolicy,
			OccurredAt:      row.OccurredAt.Time.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

// ---- Writes (PUT/DELETE /api/agents/{id}/backpressure-policy) ----

// decodeAgentBackpressureSettingsRequest reads the save body. The whole card
// is required — a partial update would make "what is in force" ambiguous
// between the row and the omitted fields, the same rule as the daemon-host
// settings surface.
func decodeAgentBackpressureSettingsRequest(w http.ResponseWriter, r *http.Request) (protocol.AgentBackpressureConfig, bool) {
	var card protocol.AgentBackpressureConfig
	if err := json.NewDecoder(r.Body).Decode(&card); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return card, false
	}
	return card, true
}

// PutAgentBackpressurePolicy saves the agent's policy card, boundary-checked
// and audited fail-closed in the same transaction. Saving is idempotent per
// agent (composite primary key), and a save never creates anything that
// acts: stage A has no automatic loop to arm, so an armed policy stays
// inert until stage B lands.
func (h *Handler) PutAgentBackpressurePolicy(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, util.UUIDToString(agent.WorkspaceID))
	if !ok {
		return
	}
	card, ok := decodeAgentBackpressureSettingsRequest(w, r)
	if !ok {
		return
	}
	if err := protocol.ValidateAgentBackpressureConfig(&card); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	prev, prevErr := h.Queries.GetAgentBackpressurePolicy(ctx, db.GetAgentBackpressurePolicyParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
	})
	hasPrev := prevErr == nil
	if prevErr != nil && !errors.Is(prevErr, pgx.ErrNoRows) {
		slog.Error("load previous agent backpressure policy failed", "agent_id", util.UUIDToString(agent.ID), "error", prevErr)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Error("begin agent backpressure policy tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	_, err = qtx.UpsertAgentBackpressurePolicy(ctx, db.UpsertAgentBackpressurePolicyParams{
		WorkspaceID:            agent.WorkspaceID,
		AgentID:                agent.ID,
		WindowMinutes:          int32(card.WindowMinutes),
		ErrorRateThresholdPct:  card.ErrorRateThresholdPct,
		MinSamples:             int32(card.MinSamples),
		RecoveryWindowMinutes:  int32(card.RecoveryWindowMinutes),
		SuppressOnErrorRate:    card.SuppressOnErrorRate,
		CountModelApiFailures:  card.CountModelAPIFailures,
		CountExecutionFailures: card.CountExecutionFailures,
		UpdatedBy:              member.UserID,
	})
	if err != nil {
		slog.Error("save agent backpressure policy failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}

	oldCard := (*protocol.AgentBackpressureConfig)(nil)
	if hasPrev {
		oldCard = &protocol.AgentBackpressureConfig{
			WindowMinutes:          int(prev.WindowMinutes),
			ErrorRateThresholdPct:  prev.ErrorRateThresholdPct,
			MinSamples:             int(prev.MinSamples),
			RecoveryWindowMinutes:  int(prev.RecoveryWindowMinutes),
			SuppressOnErrorRate:    prev.SuppressOnErrorRate,
			CountModelAPIFailures:  prev.CountModelApiFailures,
			CountExecutionFailures: prev.CountExecutionFailures,
		}
	}
	details, _ := json.Marshal(map[string]any{
		"agent_id": util.UUIDToString(agent.ID),
		"old":      oldCard,
		"new":      card,
	})
	if err := service.AppendAuditEvents(ctx, qtx, service.Event{
		ID:          dbid.NewV7(),
		WorkspaceID: agent.WorkspaceID,
		Domain:      service.AuditDomainAgent,
		EventType:   service.AuditAgentSuppressionConfigUpdated,
		OccurredAt:  time.Now().UTC(),
		ActorType:   service.AuditActorMember,
		ActorID:     member.UserID,
		AgentID:     agent.ID,
		Details:     details,
	}); err != nil {
		slog.Error("audit agent backpressure policy save failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit agent backpressure policy tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}
	slog.Info("agent backpressure policy saved",
		"agent_id", util.UUIDToString(agent.ID),
		"suppress_on_error_rate", card.SuppressOnErrorRate,
		"actor", util.UUIDToString(member.UserID))

	out, err := h.loadAgentBackpressureRow(ctx, agent)
	if err != nil {
		slog.Error("reload agent backpressure policy failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}
	writeJSON(w, http.StatusOK, agentBackpressurePolicyFromCard(agent, out))
}

// DeleteAgentBackpressurePolicy restores the agent to the built-in defaults
// by removing its row. Idempotent: a missing row is a no-op that still
// answers with the default card, audited like a save (old card, null new).
func (h *Handler) DeleteAgentBackpressurePolicy(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, util.UUIDToString(agent.WorkspaceID))
	if !ok {
		return
	}

	ctx := r.Context()
	prev, prevErr := h.Queries.GetAgentBackpressurePolicy(ctx, db.GetAgentBackpressurePolicyParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
	})
	if errors.Is(prevErr, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, agentBackpressurePolicyFromCard(agent, nil))
		return
	}
	if prevErr != nil {
		slog.Error("load agent backpressure policy for delete failed", "agent_id", util.UUIDToString(agent.ID), "error", prevErr)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Error("begin agent backpressure policy delete tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	if err := qtx.DeleteAgentBackpressurePolicy(ctx, db.DeleteAgentBackpressurePolicyParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
	}); err != nil {
		slog.Error("delete agent backpressure policy failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	oldCard := &protocol.AgentBackpressureConfig{
		WindowMinutes:          int(prev.WindowMinutes),
		ErrorRateThresholdPct:  prev.ErrorRateThresholdPct,
		MinSamples:             int(prev.MinSamples),
		RecoveryWindowMinutes:  int(prev.RecoveryWindowMinutes),
		SuppressOnErrorRate:    prev.SuppressOnErrorRate,
		CountModelAPIFailures:  prev.CountModelApiFailures,
		CountExecutionFailures: prev.CountExecutionFailures,
	}
	details, _ := json.Marshal(map[string]any{
		"agent_id": util.UUIDToString(agent.ID),
		"old":      oldCard,
		"new":      nil,
	})
	if err := service.AppendAuditEvents(ctx, qtx, service.Event{
		ID:          dbid.NewV7(),
		WorkspaceID: agent.WorkspaceID,
		Domain:      service.AuditDomainAgent,
		EventType:   service.AuditAgentSuppressionConfigUpdated,
		OccurredAt:  time.Now().UTC(),
		ActorType:   service.AuditActorMember,
		ActorID:     member.UserID,
		AgentID:     agent.ID,
		Details:     details,
	}); err != nil {
		slog.Error("audit agent backpressure policy delete failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit agent backpressure policy delete tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	writeJSON(w, http.StatusOK, agentBackpressurePolicyFromCard(agent, nil))
}

// ListAgentBackpressureEvents serves the newest-first transition history
// for one agent; limit is bounded the same way the daemon-host event
// timeline is.
func (h *Handler) ListAgentBackpressureEvents(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	workspaceID := util.UUIDToString(agent.WorkspaceID)
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	if !h.canAccessPrivateAgent(r.Context(), agent, actorType, actorID, workspaceID) {
		writeError(w, http.StatusForbidden, "you do not have access to this agent")
		return
	}
	limit := int32(50)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = int32(n)
		}
	}
	events, err := h.listAgentBackpressureEvents(r.Context(), agent, limit)
	if err != nil {
		slog.Error("list agent backpressure events failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list backpressure events")
		return
	}
	writeJSON(w, http.StatusOK, events)
}
