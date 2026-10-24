package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/multica-ai/multica/server/internal/promrules"
	"github.com/multica-ai/multica/server/internal/service"
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
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
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
	writeJSON(w, http.StatusOK, workspacePromQLRulesResponse{
		Rules: config.Rules, PrometheusConfigured: h.Prometheus != nil && h.Prometheus.Enabled(),
	})
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
	for _, oldRule := range oldConfig.Rules {
		newRule, exists := newByID[oldRule.ID]
		if !oldRule.State.Firing || (exists && newRule.Enabled) {
			continue
		}
		cause := "rule_deleted"
		if exists {
			cause = "rule_disabled"
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
		} else if exists && old.State.Firing && req.Rules[i].Enabled {
			req.Rules[i].State = old.State
			req.Rules[i].State.PendingSince = nil
			req.Rules[i].State.LastEvaluatedAt = nil
			req.Rules[i].State.LastError = ""
			req.Rules[i].State.Values = nil
		} else {
			req.Rules[i].State = promrules.RuleState{}
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
	auditEvents := make([]service.Event, 0, 1+len(transitionEvents))
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
	writeJSON(w, http.StatusOK, workspacePromQLRulesResponse{
		Rules: newConfig.Rules, PrometheusConfigured: h.Prometheus != nil && h.Prometheus.Enabled(),
	})
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
