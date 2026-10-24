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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/promrules"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
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
// "no data", not "0% errors". The type is shared with the stage B loop's
// judgments (service.AgentBackpressureWindowReading) so the card and the
// automatic suppression can never disagree on the criteria.
type agentBackpressureWindowReading = service.AgentBackpressureWindowReading

// agentBackpressureSuppressionEvent is one transition row, newest first.
type agentBackpressureSuppressionEvent struct {
	ID              string          `json:"id"`
	EventType       string          `json:"event_type"`
	SampleCount     int             `json:"sample_count"`
	ErrorRatePct    float64         `json:"error_rate_pct"`
	EffectivePolicy json.RawMessage `json:"effective_policy"`
	Source          string          `json:"source,omitempty"`
	RuleID          string          `json:"rule_id,omitempty"`
	RuleName        string          `json:"rule_name,omitempty"`
	Reason          string          `json:"reason,omitempty"`
	OccurredAt      string          `json:"occurred_at"`
}

type agentPromQLRuleStatus struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Grouping        string              `json:"grouping"`
	State           promrules.RuleState `json:"state"`
	Status          string              `json:"status"`
	SourceActive    bool                `json:"source_active"`
	Reason          string              `json:"reason,omitempty"`
	EnteredAt       string              `json:"entered_at,omitempty"`
	MappingMismatch bool                `json:"mapping_mismatch"`
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
	// PromQLRules presents the selected advanced-rule sources independently;
	// a shared scheduling state alone cannot identify which source owns a hold.
	PromQLRules []agentPromQLRuleStatus `json:"promql_rules"`
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
			PromQLRuleIDs:          promQLRuleIDStrings(row.PromqlRuleIds),
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

func promQLRuleIDStrings(ids []pgtype.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id.Valid {
			out = append(out, util.UUIDToString(id))
		}
	}
	return out
}

func promQLRuleUUIDs(ids []string) ([]pgtype.UUID, error) {
	out := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		parsed, err := util.ParseUUID(id)
		if err != nil {
			return nil, err
		}
		out = append(out, parsed)
	}
	return out, nil
}

func selectedPromQLRules(config promrules.Config, agentID pgtype.UUID, selectedIDs []string, pauses []db.AgentPromqlRulePause) []agentPromQLRuleStatus {
	selected := make(map[string]struct{}, len(selectedIDs))
	for _, id := range selectedIDs {
		selected[id] = struct{}{}
	}
	pauseByRule := make(map[string]db.AgentPromqlRulePause, len(pauses))
	for _, pause := range pauses {
		pauseByRule[util.UUIDToString(pause.RuleID)] = pause
	}
	statuses := make([]agentPromQLRuleStatus, 0, len(selected))
	for _, rule := range config.Rules {
		if _, ok := selected[rule.ID]; !ok {
			continue
		}
		state := rule.State
		mapped := true
		if rule.Grouping == promrules.GroupAgent {
			var known bool
			state, known = rule.AgentStates[util.UUIDToString(agentID)]
			mapped = known
		}
		status := "inactive"
		if state.LastError != "" {
			status = "error"
		} else if state.Firing {
			status = "firing"
		} else if state.PendingSince != nil {
			status = "pending"
		} else if rule.Grouping == promrules.GroupAgent && rule.State.LastEvaluatedAt != nil && !mapped {
			status = "mapping_mismatch"
		}
		item := agentPromQLRuleStatus{
			ID:              rule.ID,
			Name:            rule.Name,
			Grouping:        rule.Grouping,
			State:           state,
			Status:          status,
			MappingMismatch: status == "mapping_mismatch",
		}
		if pause, active := pauseByRule[rule.ID]; active {
			item.SourceActive = true
			item.Reason = pause.Reason
			item.EnteredAt = pause.EnteredAt.Time.UTC().Format(time.RFC3339)
		}
		statuses = append(statuses, item)
	}
	return statuses
}

func (h *Handler) agentPromQLRuleStatuses(ctx context.Context, agent db.Agent, selectedIDs []string) ([]agentPromQLRuleStatus, error) {
	workspace, err := h.Queries.GetWorkspace(ctx, agent.WorkspaceID)
	if err != nil {
		return nil, err
	}
	config, err := decodeWorkspacePromQLConfig(workspace.Settings)
	if err != nil {
		return nil, err
	}
	pauses, err := h.Queries.ListAgentPromQLRulePauses(ctx, db.ListAgentPromQLRulePausesParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
	})
	if err != nil {
		return nil, err
	}
	return selectedPromQLRules(config, agent.ID, selectedIDs, pauses), nil
}

func promQLRulePauseAuditEvent(workspaceID, agentID pgtype.UUID, pause db.AgentPromqlRulePause, reason string, queued int64) service.Event {
	details, _ := json.Marshal(map[string]any{
		"source":       "promql_rule",
		"rule_id":      util.UUIDToString(pause.RuleID),
		"rule_name":    pause.RuleName,
		"reason":       reason,
		"queued_count": queued,
	})
	return service.Event{
		ID:          dbid.NewV7(),
		WorkspaceID: workspaceID,
		Domain:      service.AuditDomainAgent,
		EventType:   service.AuditAgentSchedulingResumed,
		OccurredAt:  time.Now().UTC(),
		ActorType:   service.AuditActorSystem,
		AgentID:     agentID,
		Details:     details,
	}
}

func insertPromQLRuleSuppressionEvent(ctx context.Context, q *db.Queries, workspaceID, agentID pgtype.UUID, pause db.AgentPromqlRulePause, eventType, reason string) error {
	snapshot, err := json.Marshal(map[string]any{
		"rule_id":   util.UUIDToString(pause.RuleID),
		"rule_name": pause.RuleName,
	})
	if err != nil {
		return err
	}
	_, err = q.InsertAgentPromQLRuleSuppressionEvent(ctx, db.InsertAgentPromQLRuleSuppressionEventParams{
		WorkspaceID:     workspaceID,
		AgentID:         agentID,
		EventType:       eventType,
		EffectivePolicy: snapshot,
		RuleID:          pause.RuleID,
		RuleName:        pgtype.Text{String: pause.RuleName, Valid: true},
		Reason:          pgtype.Text{String: reason, Valid: true},
	})
	return err
}

func releasePromQLRulePauses(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, pauses []db.AgentPromqlRulePause, reason string) ([]service.Event, map[string]pgtype.UUID, error) {
	events := make([]service.Event, 0, len(pauses))
	changed := make(map[string]pgtype.UUID, len(pauses))
	for _, pause := range pauses {
		queued, err := q.CountQueuedTasksForAgent(ctx, pause.AgentID)
		if err != nil {
			return nil, nil, err
		}
		events = append(events, promQLRulePauseAuditEvent(workspaceID, pause.AgentID, pause, reason, queued))
		if err := insertPromQLRuleSuppressionEvent(ctx, q, workspaceID, pause.AgentID, pause, service.AgentSuppressionEventCleared, reason); err != nil {
			return nil, nil, err
		}
		changed[util.UUIDToString(pause.AgentID)] = pause.AgentID
	}
	return events, changed, nil
}

// agentBackpressureWindowReading computes the current-window error-rate feed
// for the effective card's entry window through the single shared source of
// the criteria: service.AgentBackpressureReadingFor (stage B's entry and
// recovery judgments classify through the same function, so the card and
// the loop can never disagree on the numerator). Queued rows — including
// runtime-backpressure holds — are structurally outside the terminal set
// and never count here; their depth is read separately through the
// Scheduling block.
func (h *Handler) agentBackpressureWindowReading(ctx context.Context, agent db.Agent, cfg *protocol.AgentBackpressureConfig) (*agentBackpressureWindowReading, error) {
	return service.AgentBackpressureReadingFor(ctx, h.Queries, agent.WorkspaceID, agent.ID, cfg, cfg.WindowMinutes)
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
	promQLRules, err := h.agentPromQLRuleStatuses(r.Context(), agent, card.Config.PromQLRuleIDs)
	if err != nil {
		slog.Error("read agent PromQL rule statuses failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read PromQL rule statuses")
		return
	}
	writeJSON(w, http.StatusOK, agentBackpressurePolicyResponse{
		AgentID:     card.AgentID,
		Configured:  card.Configured,
		Config:      card.Config,
		UpdatedBy:   card.UpdatedBy,
		UpdatedAt:   card.UpdatedAt,
		Window:      reading,
		Scheduling:  &state,
		Events:      events,
		PromQLRules: promQLRules,
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
			Source:          row.Source,
			RuleID:          util.UUIDToString(row.RuleID),
			RuleName:        row.RuleName.String,
			Reason:          row.Reason.String,
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
	ruleIDs, err := promQLRuleUUIDs(card.PromQLRuleIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid PromQL rule id")
		return
	}
	card.PromQLRuleIDs = promQLRuleIDStrings(ruleIDs)

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Error("begin agent backpressure policy tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	var settings []byte
	if err := tx.QueryRow(ctx, "SELECT settings FROM workspace WHERE id = $1 FOR UPDATE", agent.WorkspaceID).Scan(&settings); err != nil {
		slog.Error("lock workspace for agent backpressure policy failed", "workspace_id", util.UUIDToString(agent.WorkspaceID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}
	config, err := decodeWorkspacePromQLConfig(settings)
	if err != nil {
		slog.Error("decode workspace PromQL rules for policy save failed", "workspace_id", util.UUIDToString(agent.WorkspaceID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}
	available := make(map[string]struct{}, len(config.Rules))
	for _, rule := range config.Rules {
		id, parseErr := util.ParseUUID(rule.ID)
		if parseErr == nil {
			available[util.UUIDToString(id)] = struct{}{}
		}
	}
	for _, ruleID := range card.PromQLRuleIDs {
		if _, exists := available[ruleID]; !exists {
			writeError(w, http.StatusUnprocessableEntity, "selected PromQL rule does not belong to this workspace")
			return
		}
	}

	prev, prevErr := qtx.GetAgentBackpressurePolicy(ctx, db.GetAgentBackpressurePolicyParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
	})
	hasPrev := prevErr == nil
	if prevErr != nil && !errors.Is(prevErr, pgx.ErrNoRows) {
		slog.Error("load previous agent backpressure policy failed", "agent_id", util.UUIDToString(agent.ID), "error", prevErr)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}

	removedRuleIDs := make([]pgtype.UUID, 0)
	if hasPrev {
		newIDs := make(map[string]struct{}, len(ruleIDs))
		for _, id := range ruleIDs {
			newIDs[util.UUIDToString(id)] = struct{}{}
		}
		for _, id := range prev.PromqlRuleIds {
			if _, retained := newIDs[util.UUIDToString(id)]; !retained {
				removedRuleIDs = append(removedRuleIDs, id)
			}
		}
	}
	removedPauses := []db.AgentPromqlRulePause{}
	if len(removedRuleIDs) > 0 {
		removedPauses, err = qtx.DeleteAgentPromQLRulePausesByRuleIDs(ctx, db.DeleteAgentPromQLRulePausesByRuleIDsParams{
			WorkspaceID: agent.WorkspaceID,
			AgentID:     agent.ID,
			RuleIds:     removedRuleIDs,
		})
		if err != nil {
			slog.Error("release removed PromQL rule pauses failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
			return
		}
	}
	releaseEvents, changedAgents, err := releasePromQLRulePauses(ctx, qtx, agent.WorkspaceID, removedPauses, "selection_removed")
	if err != nil {
		slog.Error("record removed PromQL rule pauses failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}

	saved, err := qtx.UpsertAgentBackpressurePolicy(ctx, db.UpsertAgentBackpressurePolicyParams{
		WorkspaceID:            agent.WorkspaceID,
		AgentID:                agent.ID,
		WindowMinutes:          int32(card.WindowMinutes),
		ErrorRateThresholdPct:  card.ErrorRateThresholdPct,
		MinSamples:             int32(card.MinSamples),
		RecoveryWindowMinutes:  int32(card.RecoveryWindowMinutes),
		SuppressOnErrorRate:    card.SuppressOnErrorRate,
		CountModelApiFailures:  card.CountModelAPIFailures,
		CountExecutionFailures: card.CountExecutionFailures,
		PromqlRuleIds:          ruleIDs,
		UpdatedBy:              member.UserID,
	})
	if err != nil {
		slog.Error("save agent backpressure policy failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}

	var oldCard *protocol.AgentBackpressureConfig
	if hasPrev {
		oldCard = agentBackpressurePolicyFromCard(agent, &prev).Config
	}
	details, _ := json.Marshal(map[string]any{
		"agent_id": util.UUIDToString(agent.ID),
		"old":      oldCard,
		"new":      card,
	})
	auditEvents := append(releaseEvents, service.Event{
		ID:          dbid.NewV7(),
		WorkspaceID: agent.WorkspaceID,
		Domain:      service.AuditDomainAgent,
		EventType:   service.AuditAgentSuppressionConfigUpdated,
		OccurredAt:  time.Now().UTC(),
		ActorType:   service.AuditActorMember,
		ActorID:     member.UserID,
		AgentID:     agent.ID,
		Details:     details,
	})
	if err := service.AppendAuditEvents(ctx, qtx, auditEvents...); err != nil {
		slog.Error("audit agent backpressure policy save failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit agent backpressure policy tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save backpressure policy")
		return
	}
	for _, changedAgentID := range changedAgents {
		h.TaskService.PublishPromQLRuleSchedulingChange(ctx, agent.WorkspaceID, changedAgentID)
	}
	slog.Info("agent backpressure policy saved",
		"agent_id", util.UUIDToString(agent.ID),
		"suppress_on_error_rate", card.SuppressOnErrorRate,
		"actor", util.UUIDToString(member.UserID))
	writeJSON(w, http.StatusOK, agentBackpressurePolicyFromCard(agent, &saved))
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
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Error("begin agent backpressure policy delete tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	var ignoredSettings []byte
	if err := tx.QueryRow(ctx, "SELECT settings FROM workspace WHERE id = $1 FOR UPDATE", agent.WorkspaceID).Scan(&ignoredSettings); err != nil {
		slog.Error("lock workspace for agent backpressure policy delete failed", "workspace_id", util.UUIDToString(agent.WorkspaceID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	prev, prevErr := qtx.GetAgentBackpressurePolicy(ctx, db.GetAgentBackpressurePolicyParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
	})
	if prevErr != nil && !errors.Is(prevErr, pgx.ErrNoRows) {
		slog.Error("load agent backpressure policy for delete failed", "agent_id", util.UUIDToString(agent.ID), "error", prevErr)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	removedPauses, err := qtx.DeleteAgentPromQLRulePauses(ctx, db.DeleteAgentPromQLRulePausesParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
	})
	if err != nil {
		slog.Error("release deleted policy PromQL pauses failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	releaseEvents, changedAgents, err := releasePromQLRulePauses(ctx, qtx, agent.WorkspaceID, removedPauses, "policy_deleted")
	if err != nil {
		slog.Error("record deleted policy PromQL pauses failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	if err := qtx.DeleteAgentBackpressurePolicy(ctx, db.DeleteAgentBackpressurePolicyParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
	}); err != nil {
		slog.Error("delete agent backpressure policy failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	auditEvents := releaseEvents
	if prevErr == nil {
		details, _ := json.Marshal(map[string]any{
			"agent_id": util.UUIDToString(agent.ID),
			"old":      agentBackpressurePolicyFromCard(agent, &prev).Config,
			"new":      nil,
		})
		auditEvents = append(auditEvents, service.Event{
			ID:          dbid.NewV7(),
			WorkspaceID: agent.WorkspaceID,
			Domain:      service.AuditDomainAgent,
			EventType:   service.AuditAgentSuppressionConfigUpdated,
			OccurredAt:  time.Now().UTC(),
			ActorType:   service.AuditActorMember,
			ActorID:     member.UserID,
			AgentID:     agent.ID,
			Details:     details,
		})
	}
	if err := service.AppendAuditEvents(ctx, qtx, auditEvents...); err != nil {
		slog.Error("audit agent backpressure policy delete failed", "agent_id", util.UUIDToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit agent backpressure policy delete tx failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove backpressure policy")
		return
	}
	for _, changedAgentID := range changedAgents {
		h.TaskService.PublishPromQLRuleSchedulingChange(ctx, agent.WorkspaceID, changedAgentID)
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
