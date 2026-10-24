package promrules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/promquery"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const workerPollInterval = 15 * time.Second

type InstantQuerier interface {
	QueryInstant(ctx context.Context, query string, at time.Time) ([]promquery.Series, error)
}

type Worker struct {
	pool        *pgxpool.Pool
	prometheus  InstantQuerier
	taskService *service.TaskService
}

type workspaceSettings struct {
	id       pgtype.UUID
	settings []byte
}

type evaluationResult struct {
	rule    Rule
	samples []Sample
	err     error
}

func NewWorker(pool *pgxpool.Pool, prometheus InstantQuerier, taskServices ...*service.TaskService) *Worker {
	var taskService *service.TaskService
	if len(taskServices) > 0 {
		taskService = taskServices[0]
	}
	return &Worker{pool: pool, prometheus: prometheus, taskService: taskService}
}

func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.pool == nil || w.prometheus == nil {
		slog.Info("PromQL rule worker disabled: dependencies are unavailable")
		return
	}
	w.EvaluateDue(ctx)
	ticker := time.NewTicker(workerPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.EvaluateDue(ctx)
		}
	}
}

func (w *Worker) EvaluateDue(ctx context.Context) {
	if w == nil || w.pool == nil || w.prometheus == nil {
		return
	}
	rows, err := w.pool.Query(ctx, "SELECT id, settings FROM workspace WHERE settings ? $1", SettingsKey)
	if err != nil {
		slog.Warn("list workspaces with PromQL rules failed", "error", err)
		return
	}
	workspaces := make([]workspaceSettings, 0)
	for rows.Next() {
		var item workspaceSettings
		if err := rows.Scan(&item.id, &item.settings); err != nil {
			slog.Warn("scan workspace PromQL rules failed", "error", err)
			continue
		}
		workspaces = append(workspaces, item)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		slog.Warn("read workspace PromQL rules failed", "error", rowsErr)
		return
	}
	for _, item := range workspaces {
		if err := w.evaluateWorkspace(ctx, item); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("evaluate workspace PromQL rules failed", "workspace_id", uuidString(item.id), "error", err)
		}
	}
}

func (w *Worker) evaluateWorkspace(ctx context.Context, item workspaceSettings) error {
	config, err := DecodeSettings(item.settings)
	if err != nil {
		return fmt.Errorf("decode settings: %w", err)
	}
	if err := validatePersistedConfig(config); err != nil {
		return fmt.Errorf("validate settings: %w", err)
	}
	startedAt := time.Now().UTC()
	results := make(map[string]evaluationResult)
	for _, rule := range config.Rules {
		if !rule.Enabled || !isDue(rule, startedAt) {
			continue
		}
		result := evaluationResult{rule: rule}
		query, queryErr := BuildQuery(uuidString(item.id), rule)
		if queryErr != nil {
			result.err = queryErr
		} else {
			queryCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			series, queryErr := w.prometheus.QueryInstant(queryCtx, query, startedAt)
			cancel()
			if queryErr != nil {
				result.err = queryErr
			} else {
				result.samples = samplesFromSeries(series)
			}
		}
		results[rule.ID] = result
	}
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin evaluation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var settings []byte
	if err := tx.QueryRow(ctx, "SELECT settings FROM workspace WHERE id = $1 FOR UPDATE", item.id).Scan(&settings); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lock workspace: %w", err)
	}
	current, err := DecodeSettings(settings)
	if err != nil {
		return fmt.Errorf("decode locked settings: %w", err)
	}
	if err := validatePersistedConfig(current); err != nil {
		return fmt.Errorf("validate locked settings: %w", err)
	}
	policies, err := db.New(tx).ListAgentBackpressurePoliciesWithPromQLRules(ctx, item.id)
	if err != nil {
		return fmt.Errorf("list selected PromQL policies: %w", err)
	}
	selections := selectedAgentsByRule(policies)

	updated := false
	events := make([]service.Event, 0)
	changedAgents := make(map[string]pgtype.UUID)
	now := time.Now().UTC()
	for i := range current.Rules {
		rule := &current.Rules[i]
		result, exists := results[rule.ID]
		if !exists || !rule.Enabled || !SameDefinition(result.rule, *rule) || !isDue(*rule, now) {
			continue
		}
		queryErr := result.err
		if queryErr != nil {
			slog.Warn("PromQL rule query failed", "workspace_id", uuidString(item.id), "rule_id", rule.ID, "error", queryErr)
			queryErr = errors.New("Prometheus query failed")
		}
		transition := TransitionNone
		if rule.Grouping == GroupAgent {
			transition = evaluateAgentRule(rule, selections[rule.ID], result.samples, queryErr, now)
		} else {
			state, next := Evaluate(*rule, rule.State, result.samples, queryErr, now)
			rule.State = state
			transition = next
		}
		updated = true
		if transition != TransitionNone {
			events = append(events, evaluationAuditEvent(item.id, *rule, transition, now))
		}
	}

	desired := desiredRulePauses(current.Rules, selections)
	qtx := db.New(tx)
	for ruleID, agentIDs := range desired {
		rule, ok := ruleByID(current.Rules, ruleID)
		if !ok {
			continue
		}
		for agentID, agentUUID := range agentIDs {
			inserted, err := qtx.EnsureAgentPromQLRulePause(ctx, db.EnsureAgentPromQLRulePauseParams{
				WorkspaceID: item.id,
				AgentID:     agentUUID,
				RuleID:      parseRuleUUID(rule.ID),
				RuleName:    rule.Name,
				Reason:      "rule_firing",
			})
			if err != nil {
				return fmt.Errorf("ensure PromQL rule pause: %w", err)
			}
			if inserted == 0 {
				continue
			}
			queued, err := qtx.CountQueuedTasksForAgent(ctx, agentUUID)
			if err != nil {
				return fmt.Errorf("count PromQL-held queued tasks: %w", err)
			}
			events = append(events, agentRulePauseAuditEvent(item.id, agentUUID, rule, true, "rule_firing", queued))
			if err := insertRuleSuppressionEvent(ctx, qtx, item.id, agentUUID, rule, service.AgentSuppressionEventEntered, "rule_firing"); err != nil {
				return err
			}
			changedAgents[agentID] = agentUUID
		}
	}

	existingPauses, err := qtx.ListAgentPromQLRulePausesByWorkspace(ctx, item.id)
	if err != nil {
		return fmt.Errorf("list PromQL rule pauses: %w", err)
	}
	for _, pause := range existingPauses {
		ruleID := uuidString(pause.RuleID)
		agentID := uuidString(pause.AgentID)
		if _, keep := desired[ruleID][agentID]; keep {
			continue
		}
		cause := rulePauseClearCause(current.Rules, selections, ruleID, agentID)
		queued, err := qtx.CountQueuedTasksForAgent(ctx, pause.AgentID)
		if err != nil {
			return fmt.Errorf("count released PromQL queue: %w", err)
		}
		deleted, err := qtx.DeleteAgentPromQLRulePausesByRuleIDs(ctx, db.DeleteAgentPromQLRulePausesByRuleIDsParams{
			WorkspaceID: item.id,
			AgentID:     pause.AgentID,
			RuleIds:     []pgtype.UUID{pause.RuleID},
		})
		if err != nil {
			return fmt.Errorf("release PromQL rule pause: %w", err)
		}
		for _, row := range deleted {
			snapshot := Rule{ID: uuidString(row.RuleID), Name: row.RuleName}
			events = append(events, agentRulePauseAuditEvent(item.id, row.AgentID, snapshot, false, cause, queued))
			if err := insertRuleSuppressionEvent(ctx, qtx, item.id, row.AgentID, snapshot, service.AgentSuppressionEventCleared, cause); err != nil {
				return err
			}
			changedAgents[uuidString(row.AgentID)] = row.AgentID
		}
	}
	if updated {
		encoded, err := json.Marshal(current)
		if err != nil {
			return fmt.Errorf("encode updated settings: %w", err)
		}
		if _, err := tx.Exec(ctx, "UPDATE workspace SET settings = jsonb_set(settings, '{promql_rules}', $2::jsonb, true), updated_at = now() WHERE id = $1", item.id, encoded); err != nil {
			return fmt.Errorf("save evaluation state: %w", err)
		}
	}
	if err := service.AppendAuditEvents(ctx, db.New(tx), events...); err != nil {
		return fmt.Errorf("append rule transition audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit evaluation state: %w", err)
	}
	if w.taskService != nil {
		for _, agentID := range changedAgents {
			w.taskService.PublishPromQLRuleSchedulingChange(ctx, item.id, agentID)
		}
	}
	return nil
}

// validatePersistedConfig rejects incomplete historical settings before a
// worker can turn them into durable sources. Request validation may generate a
// new ID for a draft rule, but an evaluated rule must already have its stable
// persisted ID.
func validatePersistedConfig(config Config) error {
	for i, rule := range config.Rules {
		if rule.ID == "" {
			return fmt.Errorf("rule %d is missing a persisted id", i+1)
		}
	}
	return ValidateRules(config.Rules)
}

func isDue(rule Rule, now time.Time) bool {
	return rule.State.LastEvaluatedAt == nil ||
		!now.Before(rule.State.LastEvaluatedAt.Add(time.Duration(rule.EvaluationSeconds)*time.Second))
}

func samplesFromSeries(series []promquery.Series) []Sample {
	samples := make([]Sample, 0, len(series))
	for _, item := range series {
		if len(item.Points) == 0 {
			continue
		}
		samples = append(samples, Sample{Labels: item.Labels, Value: item.Points[0].Value})
	}
	return samples
}

func selectedAgentsByRule(policies []db.AgentBackpressurePolicy) map[string]map[string]pgtype.UUID {
	selected := make(map[string]map[string]pgtype.UUID)
	for _, policy := range policies {
		for _, ruleID := range policy.PromqlRuleIds {
			key := uuidString(ruleID)
			if selected[key] == nil {
				selected[key] = make(map[string]pgtype.UUID)
			}
			selected[key][uuidString(policy.AgentID)] = policy.AgentID
		}
	}
	return selected
}

// evaluateAgentRule accepts only UUID agent_id labels that map exactly to a
// currently selected agent in this workspace. A successful query with a
// missing, malformed, or cross-workspace label drops persisted mapping state;
// reconciliation then releases only this rule's source-owned pause.
func evaluateAgentRule(rule *Rule, selected map[string]pgtype.UUID, samples []Sample, queryErr error, now time.Time) Transition {
	previousFiring := rule.State.Firing
	if rule.AgentStates == nil {
		rule.AgentStates = make(map[string]RuleState)
	}

	if queryErr != nil {
		for agentID := range selected {
			state, known := rule.AgentStates[agentID]
			// A query-error policy cannot create a pause without an already
			// verified mapping. It may only retain or release existing state.
			if !known || (!state.Firing && state.PendingSince == nil) {
				continue
			}
			next, _ := Evaluate(*rule, state, nil, queryErr, now)
			rule.AgentStates[agentID] = next
		}
	} else {
		mapped := verifiedAgentSamples(samples, selected)
		for agentID := range rule.AgentStates {
			if _, current := selected[agentID]; !current {
				delete(rule.AgentStates, agentID)
				continue
			}
			if _, current := mapped[agentID]; !current {
				delete(rule.AgentStates, agentID)
			}
		}
		for agentID, agentSamples := range mapped {
			next, _ := Evaluate(*rule, rule.AgentStates[agentID], agentSamples, nil, now)
			rule.AgentStates[agentID] = next
		}
	}

	aggregate := RuleState{Values: samples}
	evaluatedAt := now.UTC()
	aggregate.LastEvaluatedAt = &evaluatedAt
	if queryErr != nil {
		aggregate.LastError = queryErr.Error()
	}
	for agentID := range selected {
		state, known := rule.AgentStates[agentID]
		if !known {
			continue
		}
		if state.Firing {
			aggregate.Firing = true
		}
		if state.PendingSince != nil && (aggregate.PendingSince == nil || state.PendingSince.Before(*aggregate.PendingSince)) {
			pending := *state.PendingSince
			aggregate.PendingSince = &pending
		}
	}
	rule.State = aggregate
	return firingTransition(previousFiring, aggregate.Firing)
}

func verifiedAgentSamples(samples []Sample, selected map[string]pgtype.UUID) map[string][]Sample {
	mapped := make(map[string][]Sample)
	for _, sample := range samples {
		label, ok := sample.Labels["agent_id"]
		if !ok {
			continue
		}
		id, err := uuid.Parse(label)
		if err != nil {
			continue
		}
		agentID := id.String()
		if _, selectedHere := selected[agentID]; !selectedHere {
			continue
		}
		mapped[agentID] = append(mapped[agentID], sample)
	}
	return mapped
}

func desiredRulePauses(rules []Rule, selections map[string]map[string]pgtype.UUID) map[string]map[string]pgtype.UUID {
	desired := make(map[string]map[string]pgtype.UUID)
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		for agentID, agentUUID := range selections[rule.ID] {
			firing := rule.State.Firing
			if rule.Grouping == GroupAgent {
				firing = AgentState(rule, agentID).Firing
			}
			if !firing {
				continue
			}
			if desired[rule.ID] == nil {
				desired[rule.ID] = make(map[string]pgtype.UUID)
			}
			desired[rule.ID][agentID] = agentUUID
		}
	}
	return desired
}

func ruleByID(rules []Rule, id string) (Rule, bool) {
	for _, rule := range rules {
		if rule.ID == id {
			return rule, true
		}
	}
	return Rule{}, false
}

func rulePauseClearCause(rules []Rule, selections map[string]map[string]pgtype.UUID, ruleID, agentID string) string {
	rule, exists := ruleByID(rules, ruleID)
	if !exists {
		return "rule_deleted"
	}
	if !rule.Enabled {
		return "rule_disabled"
	}
	if _, selected := selections[ruleID][agentID]; !selected {
		return "selection_removed"
	}
	if rule.Grouping == GroupAgent {
		if _, mapped := rule.AgentStates[agentID]; !mapped {
			return "mapping_mismatch"
		}
	}
	return "rule_resolved"
}

func parseRuleUUID(id string) pgtype.UUID {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(parsed), Valid: true}
}

func agentRulePauseAuditEvent(workspaceID, agentID pgtype.UUID, rule Rule, entered bool, reason string, queued int64) service.Event {
	eventType := service.AuditAgentSchedulingResumed
	if entered {
		eventType = service.AuditAgentSchedulingPaused
	}
	details, _ := json.Marshal(map[string]any{
		"source":       "promql_rule",
		"rule_id":      rule.ID,
		"rule_name":    rule.Name,
		"reason":       reason,
		"queued_count": queued,
	})
	return service.AgentEvent(eventType, service.AuditActorSystem, pgtype.UUID{}, workspaceID, agentID, details)
}

func insertRuleSuppressionEvent(ctx context.Context, q *db.Queries, workspaceID, agentID pgtype.UUID, rule Rule, eventType, reason string) error {
	snapshot, err := json.Marshal(map[string]any{
		"rule_id":   rule.ID,
		"rule_name": rule.Name,
		"grouping":  rule.Grouping,
	})
	if err != nil {
		return fmt.Errorf("snapshot PromQL rule: %w", err)
	}
	_, err = q.InsertAgentPromQLRuleSuppressionEvent(ctx, db.InsertAgentPromQLRuleSuppressionEventParams{
		WorkspaceID:     workspaceID,
		AgentID:         agentID,
		EventType:       eventType,
		EffectivePolicy: snapshot,
		RuleID:          parseRuleUUID(rule.ID),
		RuleName:        pgtype.Text{String: rule.Name, Valid: true},
		Reason:          pgtype.Text{String: reason, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("write PromQL suppression event: %w", err)
	}
	return nil
}

func evaluationAuditEvent(workspaceID pgtype.UUID, rule Rule, transition Transition, occurredAt time.Time) service.Event {
	return RuleTransitionAuditEvent(
		workspaceID, rule, transition, occurredAt,
		service.AuditActorSystem, pgtype.UUID{}, "",
	)
}

func RuleTransitionAuditEvent(
	workspaceID pgtype.UUID,
	rule Rule,
	transition Transition,
	occurredAt time.Time,
	actorType string,
	actorID pgtype.UUID,
	resolutionCause string,
) service.Event {
	eventType := service.AuditOpsPromQLRuleFired
	if transition == TransitionResolved {
		eventType = service.AuditOpsPromQLRuleResolved
	}
	detailsFields := map[string]any{
		"rule_id":            rule.ID,
		"rule_name":          rule.Name,
		"metric":             rule.Metric,
		"grouping":           rule.Grouping,
		"condition":          rule.Condition,
		"comparison":         rule.Comparison,
		"threshold":          rule.Threshold,
		"recovery_threshold": rule.RecoveryThreshold,
		"sample_count":       len(rule.State.Values),
	}
	if resolutionCause != "" {
		detailsFields["resolution_cause"] = resolutionCause
	}
	details, _ := json.Marshal(detailsFields)
	return service.Event{
		ID:          dbid.NewV7(),
		WorkspaceID: workspaceID,
		Domain:      service.AuditDomainOps,
		EventType:   eventType,
		OccurredAt:  occurredAt,
		ActorType:   actorType,
		ActorID:     actorID,
		Details:     details,
	}
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", id.Bytes[0:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:16])
}
