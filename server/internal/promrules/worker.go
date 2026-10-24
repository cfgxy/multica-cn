package promrules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

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
	pool       *pgxpool.Pool
	prometheus InstantQuerier
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

func NewWorker(pool *pgxpool.Pool, prometheus InstantQuerier) *Worker {
	return &Worker{pool: pool, prometheus: prometheus}
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
	if len(results) == 0 {
		return nil
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
	updated := false
	events := make([]service.Event, 0)
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
		state, transition := Evaluate(*rule, rule.State, result.samples, queryErr, now)
		rule.State = state
		updated = true
		if transition != TransitionNone {
			events = append(events, evaluationAuditEvent(item.id, *rule, transition, now))
		}
	}
	if !updated {
		return nil
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("encode updated settings: %w", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE workspace SET settings = jsonb_set(settings, '{promql_rules}', $2::jsonb, true), updated_at = now() WHERE id = $1", item.id, encoded); err != nil {
		return fmt.Errorf("save evaluation state: %w", err)
	}
	if err := service.AppendAuditEvents(ctx, db.New(tx), events...); err != nil {
		return fmt.Errorf("append rule transition audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit evaluation state: %w", err)
	}
	return nil
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
