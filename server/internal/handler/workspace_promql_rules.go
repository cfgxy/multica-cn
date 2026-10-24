package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/promrules"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type workspacePromQLRulesResponse struct {
	Rules                []promrules.Rule `json:"rules"`
	PrometheusConfigured bool             `json:"prometheus_configured"`
}

type workspacePromQLRulesRequest struct {
	Rules []promrules.Rule `json:"rules"`
}

type workspacePromQLPreviewRequest struct {
	Rule promrules.Rule `json:"rule"`
}

type workspacePromQLPreviewResponse struct {
	Query   string             `json:"query"`
	Samples []promrules.Sample `json:"samples"`
	Matches bool               `json:"matches"`
}

func decodeWorkspacePromQLConfig(settings []byte) (promrules.Config, error) {
	return promrules.DecodeSettings(settings)
}

func stripRuleState(config promrules.Config) promrules.Config {
	out := promrules.Config{Rules: append([]promrules.Rule(nil), config.Rules...)}
	for i := range out.Rules {
		out.Rules[i].State = promrules.RuleState{}
		out.Rules[i].AgentStates = nil
	}
	return out
}

func decodeWorkspacePromQLRequest(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (h *Handler) GetWorkspacePromQLRules(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	workspace, err := h.Queries.GetWorkspace(r.Context(), parseUUID(workspaceID))
	if err != nil {
		slog.Error("read workspace PromQL rules failed", "workspace_id", workspaceID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read PromQL rules")
		return
	}
	config, err := decodeWorkspacePromQLConfig(workspace.Settings)
	if err != nil {
		slog.Error("decode workspace PromQL rules failed", "workspace_id", workspaceID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read PromQL rules")
		return
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	if actorType == "member" && !roleAllowed(member.Role, "owner", "admin") {
		accessible, accessOK := h.accessibleAgentIDs(r.Context(), workspaceID, actorType, actorID, member.Role)
		if !accessOK {
			slog.Error("resolve visible agents for workspace PromQL rules failed", "workspace_id", workspaceID)
			writeError(w, http.StatusInternalServerError, "failed to read PromQL rules")
			return
		}
		config = filterPromQLRulesForVisibleAgents(config, accessible)
	}
	writeJSON(w, http.StatusOK, workspacePromQLRulesResponse{
		Rules: config.Rules, PrometheusConfigured: h.Prometheus != nil && h.Prometheus.Enabled(),
	})
}

// filterPromQLRulesForVisibleAgents removes every inaccessible agent's state
// from an agent-grouped rule. A member can still inspect the selected rule's
// own visible agents, but cannot use workspace-wide rule reads to enumerate a
// private agent or infer its evaluation state from the aggregate state.
func filterPromQLRulesForVisibleAgents(config promrules.Config, visible map[string]struct{}) promrules.Config {
	out := promrules.Config{Rules: append([]promrules.Rule(nil), config.Rules...)}
	for i := range out.Rules {
		rule := &out.Rules[i]
		if rule.Grouping != promrules.GroupAgent {
			continue
		}

		states := make(map[string]promrules.RuleState)
		for agentID, state := range rule.AgentStates {
			if _, allowed := visible[agentID]; !allowed {
				continue
			}
			values := make([]promrules.Sample, 0, len(state.Values))
			for _, sample := range state.Values {
				if sampleAgentID, hasAgentID := sample.Labels["agent_id"]; hasAgentID && sampleAgentID == agentID {
					values = append(values, sample)
				}
			}
			state.Values = values
			states[agentID] = state
		}
		rule.AgentStates = states

		values := make([]promrules.Sample, 0, len(rule.State.Values))
		for _, sample := range rule.State.Values {
			agentID, hasAgentID := sample.Labels["agent_id"]
			if _, allowed := visible[agentID]; hasAgentID && allowed {
				values = append(values, sample)
			}
		}
		rule.State.Values = values
		rule.State.Firing = false
		rule.State.PendingSince = nil
		for _, state := range states {
			if state.Firing {
				rule.State.Firing = true
			}
			if state.PendingSince != nil && (rule.State.PendingSince == nil || state.PendingSince.Before(*rule.State.PendingSince)) {
				pending := *state.PendingSince
				rule.State.PendingSince = &pending
			}
		}
	}
	return out
}

func (h *Handler) PutWorkspacePromQLRules(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
	if !ok {
		return
	}
	var req workspacePromQLRulesRequest
	if err := decodeWorkspacePromQLRequest(r, &req); err != nil || req.Rules == nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := promrules.ValidateRules(req.Rules); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	ctx := r.Context()
	workspaceUUID := parseUUID(workspaceID)
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Error("begin workspace PromQL rules transaction failed", "workspace_id", workspaceID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save PromQL rules")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)
	var settings []byte
	if err := tx.QueryRow(ctx, "SELECT settings FROM workspace WHERE id = $1 FOR UPDATE", workspaceUUID).Scan(&settings); err != nil {
		slog.Error("lock workspace PromQL rules failed", "workspace_id", workspaceID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save PromQL rules")
		return
	}
	oldConfig, err := decodeWorkspacePromQLConfig(settings)
	if err != nil {
		slog.Error("decode existing workspace PromQL rules failed", "workspace_id", workspaceID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save PromQL rules")
		return
	}
	oldByID := make(map[string]promrules.Rule, len(oldConfig.Rules))
	newByID := make(map[string]promrules.Rule, len(req.Rules))
	for _, oldRule := range oldConfig.Rules {
		oldByID[oldRule.ID] = oldRule
	}
	for _, newRule := range req.Rules {
		newByID[newRule.ID] = newRule
	}
	now := time.Now().UTC()
	transitionEvents := make([]service.Event, 0)
	ruleClearCauses := make(map[string]string)
	for _, oldRule := range oldConfig.Rules {
		newRule, exists := newByID[oldRule.ID]
		cause := "rule_deleted"
		if exists {
			if !newRule.Enabled {
				cause = "rule_disabled"
			} else if !promrules.SameDefinition(oldRule, newRule) {
				cause = "rule_reconfigured"
			} else {
				continue
			}
		}
		ruleClearCauses[oldRule.ID] = cause
		if !ruleHasFiringState(oldRule) {
			continue
		}
		transitionEvents = append(transitionEvents, promrules.RuleTransitionAuditEvent(
			workspaceUUID, oldRule, promrules.TransitionResolved, now,
			service.AuditActorMember, member.UserID, cause,
		))
	}
	for i := range req.Rules {
		old, exists := oldByID[req.Rules[i].ID]
		if exists && promrules.SameDefinition(old, req.Rules[i]) {
			req.Rules[i].State = old.State
			req.Rules[i].AgentStates = old.AgentStates
		} else {
			req.Rules[i].State = promrules.RuleState{}
			req.Rules[i].AgentStates = nil
		}
	}
	ruleIDsToClear := make([]pgtype.UUID, 0, len(ruleClearCauses))
	deletedRuleIDs := make([]pgtype.UUID, 0)
	for ruleID := range ruleClearCauses {
		parsed, parseErr := util.ParseUUID(ruleID)
		if parseErr != nil {
			slog.Error("parse stored PromQL rule id failed", "workspace_id", workspaceID, "rule_id", ruleID, "error", parseErr)
			writeError(w, http.StatusInternalServerError, "failed to save PromQL rules")
			return
		}
		ruleIDsToClear = append(ruleIDsToClear, parsed)
		if ruleClearCauses[ruleID] == "rule_deleted" {
			deletedRuleIDs = append(deletedRuleIDs, parsed)
		}
	}
	for _, ruleID := range deletedRuleIDs {
		if err := qtx.RemoveDeletedPromQLRuleIDsFromWorkspacePolicies(ctx, db.RemoveDeletedPromQLRuleIDsFromWorkspacePoliciesParams{
			WorkspaceID: workspaceUUID,
			RuleID:      ruleID,
		}); err != nil {
			slog.Error("remove deleted PromQL rule selections failed", "workspace_id", workspaceID, "rule_id", util.UUIDToString(ruleID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to save PromQL rules")
			return
		}
	}
	releasedPauses := []db.AgentPromqlRulePause{}
	if len(ruleIDsToClear) > 0 {
		releasedPauses, err = qtx.DeleteAgentPromQLRulePausesByWorkspaceRuleIDs(ctx, db.DeleteAgentPromQLRulePausesByWorkspaceRuleIDsParams{
			WorkspaceID: workspaceUUID,
			RuleIds:     ruleIDsToClear,
		})
		if err != nil {
			slog.Error("release changed PromQL rule pauses failed", "workspace_id", workspaceID, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to save PromQL rules")
			return
		}
	}
	releaseEvents := make([]service.Event, 0, len(releasedPauses))
	changedAgents := make(map[string]pgtype.UUID)
	for _, pause := range releasedPauses {
		cause := ruleClearCauses[pause.RuleID.String()]
		if cause == "" {
			cause = "rule_reconfigured"
		}
		events, changed, releaseErr := releasePromQLRulePauses(ctx, qtx, workspaceUUID, []db.AgentPromqlRulePause{pause}, cause)
		if releaseErr != nil {
			slog.Error("record changed PromQL rule pause release failed", "workspace_id", workspaceID, "error", releaseErr)
			writeError(w, http.StatusInternalServerError, "failed to save PromQL rules")
			return
		}
		releaseEvents = append(releaseEvents, events...)
		for agentID, agentUUID := range changed {
			changedAgents[agentID] = agentUUID
		}
	}
	newConfig := promrules.Config{Rules: req.Rules}
	encoded, err := json.Marshal(newConfig)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save PromQL rules")
		return
	}
	if _, err := tx.Exec(ctx, "UPDATE workspace SET settings = jsonb_set(settings, '{promql_rules}', $2::jsonb, true), updated_at = now() WHERE id = $1", workspaceUUID, encoded); err != nil {
		slog.Error("save workspace PromQL rules failed", "workspace_id", workspaceID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save PromQL rules")
		return
	}
	oldForAudit := stripRuleState(oldConfig)
	newForAudit := stripRuleState(newConfig)
	details, _ := json.Marshal(map[string]any{"old": oldForAudit, "new": newForAudit})
	auditEvents := make([]service.Event, 0, 1+len(transitionEvents)+len(releaseEvents))
	auditEvents = append(auditEvents, service.Event{
		ID:          dbid.NewV7(),
		WorkspaceID: workspaceUUID,
		Domain:      service.AuditDomainOps,
		EventType:   service.AuditOpsPromQLRulesUpdated,
		OccurredAt:  now,
		ActorType:   service.AuditActorMember,
		ActorID:     member.UserID,
		Details:     details,
	})
	auditEvents = append(auditEvents, transitionEvents...)
	auditEvents = append(auditEvents, releaseEvents...)
	if err := service.AppendAuditEvents(ctx, qtx, auditEvents...); err != nil {
		slog.Error("audit workspace PromQL rule update failed", "workspace_id", workspaceID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save PromQL rules")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit workspace PromQL rules failed", "workspace_id", workspaceID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save PromQL rules")
		return
	}
	for _, agentID := range changedAgents {
		h.TaskService.PublishPromQLRuleSchedulingChange(ctx, workspaceUUID, agentID)
	}
	writeJSON(w, http.StatusOK, workspacePromQLRulesResponse{
		Rules: newConfig.Rules, PrometheusConfigured: h.Prometheus != nil && h.Prometheus.Enabled(),
	})
}

func ruleHasFiringState(rule promrules.Rule) bool {
	if rule.State.Firing {
		return true
	}
	for _, state := range rule.AgentStates {
		if state.Firing {
			return true
		}
	}
	return false
}

func (h *Handler) PreviewWorkspacePromQLRule(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	if h.Prometheus == nil || !h.Prometheus.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "Prometheus is not configured")
		return
	}
	var req workspacePromQLPreviewRequest
	if err := decodeWorkspacePromQLRequest(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule := req.Rule
	if err := promrules.ValidateRules([]promrules.Rule{rule}); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	query, err := promrules.BuildQuery(workspaceID, rule)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	series, err := h.Prometheus.QueryInstant(r.Context(), query, time.Now())
	if err != nil {
		slog.Warn("workspace PromQL rule preview failed", "workspace_id", workspaceID, "rule_id", rule.ID, "error", err)
		writeError(w, http.StatusBadGateway, "Prometheus query failed")
		return
	}
	samples := make([]promrules.Sample, 0, len(series))
	for _, item := range series {
		if len(item.Points) == 0 {
			continue
		}
		samples = append(samples, promrules.Sample{Labels: item.Labels, Value: item.Points[0].Value})
	}
	previewState, _ := promrules.Evaluate(rule, promrules.RuleState{}, samples, nil, time.Now())
	writeJSON(w, http.StatusOK, workspacePromQLPreviewResponse{
		Query: query, Samples: samples, Matches: previewState.Firing || previewState.PendingSince != nil,
	})
}
