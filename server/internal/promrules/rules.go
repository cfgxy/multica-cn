package promrules

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/promquery"
)

const MaxRulesPerWorkspace = 20

const (
	MetricFailures    = "failures"
	MetricFailureRate = "failure_rate"

	GroupWorkspace = "workspace"
	GroupAgent     = "agent"

	ConditionThreshold = "threshold"
	ConditionPresent   = "present"

	PolicyKeep    = "keep"
	PolicyTrigger = "trigger"
	PolicyResolve = "resolve"
)

type Rule struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Enabled           bool      `json:"enabled"`
	Metric            string    `json:"metric"`
	Grouping          string    `json:"grouping"`
	Condition         string    `json:"condition"`
	Comparison        string    `json:"comparison,omitempty"`
	Threshold         float64   `json:"threshold,omitempty"`
	RecoveryThreshold float64   `json:"recovery_threshold,omitempty"`
	LookbackSeconds   int       `json:"lookback_seconds"`
	EvaluationSeconds int       `json:"evaluation_seconds"`
	ForSeconds        int       `json:"for_seconds"`
	NoDataPolicy      string    `json:"no_data_policy"`
	QueryErrorPolicy  string    `json:"query_error_policy"`
	State             RuleState `json:"state"`
}

type RuleState struct {
	Firing          bool       `json:"firing"`
	PendingSince    *time.Time `json:"pending_since,omitempty"`
	LastEvaluatedAt *time.Time `json:"last_evaluated_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	Values          []Sample   `json:"values,omitempty"`
}

type Sample struct {
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value"`
}

const SettingsKey = "promql_rules"

type Config struct {
	Rules []Rule `json:"rules"`
}

func DecodeSettings(settings []byte) (Config, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(settings, &root); err != nil {
		return Config{}, fmt.Errorf("decode workspace settings: %w", err)
	}
	config := Config{Rules: []Rule{}}
	if raw, ok := root[SettingsKey]; ok {
		if err := json.Unmarshal(raw, &config); err != nil {
			return Config{}, fmt.Errorf("decode workspace PromQL rules: %w", err)
		}
	}
	if config.Rules == nil {
		config.Rules = []Rule{}
	}
	return config, nil
}

type Transition string

const (
	TransitionNone     Transition = ""
	TransitionFired    Transition = "fired"
	TransitionResolved Transition = "resolved"
)

func ValidateRules(rules []Rule) error {
	if len(rules) > MaxRulesPerWorkspace {
		return fmt.Errorf("at most %d rules are allowed", MaxRulesPerWorkspace)
	}
	ids := make(map[string]struct{}, len(rules))
	names := make(map[string]struct{}, len(rules))
	for i := range rules {
		rule := &rules[i]
		if rule.ID == "" {
			rule.ID = uuid.NewString()
		}
		if _, err := uuid.Parse(rule.ID); err != nil {
			return fmt.Errorf("rule %d has an invalid id", i+1)
		}
		if _, ok := ids[rule.ID]; ok {
			return fmt.Errorf("rule ids must be unique")
		}
		ids[rule.ID] = struct{}{}

		rule.Name = strings.TrimSpace(rule.Name)
		if rule.Name == "" || len([]rune(rule.Name)) > 80 {
			return fmt.Errorf("rule %d name must contain 1 to 80 characters", i+1)
		}
		nameKey := strings.ToLower(rule.Name)
		if _, ok := names[nameKey]; ok {
			return fmt.Errorf("rule names must be unique")
		}
		names[nameKey] = struct{}{}

		if rule.Metric != MetricFailures && rule.Metric != MetricFailureRate {
			return fmt.Errorf("rule %q has an unsupported metric", rule.Name)
		}
		if rule.Grouping != GroupWorkspace && rule.Grouping != GroupAgent {
			return fmt.Errorf("rule %q has an unsupported grouping", rule.Name)
		}
		if rule.Condition != ConditionThreshold && rule.Condition != ConditionPresent {
			return fmt.Errorf("rule %q has an unsupported condition", rule.Name)
		}
		if rule.Condition == ConditionThreshold {
			if rule.Comparison != ">" && rule.Comparison != ">=" && rule.Comparison != "<" && rule.Comparison != "<=" {
				return fmt.Errorf("rule %q has an unsupported comparison", rule.Name)
			}
			if !finiteNonNegative(rule.Threshold) || !finiteNonNegative(rule.RecoveryThreshold) {
				return fmt.Errorf("rule %q thresholds must be finite and non-negative", rule.Name)
			}
			if rule.Metric == MetricFailureRate && (rule.Threshold > 100 || rule.RecoveryThreshold > 100) {
				return fmt.Errorf("rule %q failure-rate thresholds must not exceed 100", rule.Name)
			}
			if rule.Comparison == ">" || rule.Comparison == ">=" {
				if rule.RecoveryThreshold >= rule.Threshold {
					return fmt.Errorf("rule %q recovery threshold must be below its trigger threshold", rule.Name)
				}
			} else if rule.RecoveryThreshold <= rule.Threshold {
				return fmt.Errorf("rule %q recovery threshold must be above its trigger threshold", rule.Name)
			}
		}
		if rule.LookbackSeconds < 60 || rule.LookbackSeconds > 7*24*60*60 {
			return fmt.Errorf("rule %q lookback must be between 60 and 604800 seconds", rule.Name)
		}
		if rule.EvaluationSeconds < 15 || rule.EvaluationSeconds > 3600 {
			return fmt.Errorf("rule %q evaluation interval must be between 15 and 3600 seconds", rule.Name)
		}
		if rule.ForSeconds < 0 || rule.ForSeconds > 86400 {
			return fmt.Errorf("rule %q duration must be between 0 and 86400 seconds", rule.Name)
		}
		if !validPolicy(rule.NoDataPolicy) || !validPolicy(rule.QueryErrorPolicy) {
			return fmt.Errorf("rule %q has an unsupported no-data or query-error policy", rule.Name)
		}
	}
	return nil
}

func finiteNonNegative(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func validPolicy(policy string) bool {
	return policy == PolicyKeep || policy == PolicyTrigger || policy == PolicyResolve
}

// BuildQuery emits only fixed metric expressions and binds every selector to
// the workspace. Rule input never contains executable PromQL.
func BuildQuery(workspaceID string, rule Rule) (string, error) {
	id, err := uuid.Parse(workspaceID)
	if err != nil {
		return "", fmt.Errorf("invalid workspace id")
	}
	workspace := promquery.EscapeLabelValue(id.String())
	group := ""
	if rule.Grouping == GroupAgent {
		group = " by (agent_id)"
	} else if rule.Grouping != GroupWorkspace {
		return "", fmt.Errorf("unsupported grouping")
	}
	window := fmt.Sprintf("%ds", rule.LookbackSeconds)
	failures := fmt.Sprintf("increase(multica_agent_task_failures_total{workspace_id=\"%s\"}[%s])", workspace, window)
	if rule.Metric == MetricFailures {
		return fmt.Sprintf("sum%s(%s)", group, failures), nil
	}
	if rule.Metric != MetricFailureRate {
		return "", fmt.Errorf("unsupported metric")
	}
	runs := fmt.Sprintf("increase(multica_agent_task_runs_total{workspace_id=\"%s\"}[%s])", workspace, window)
	return fmt.Sprintf("(sum%s(%s) / sum%s(%s)) * 100", group, failures, group, runs), nil
}

// Evaluate updates a rule's state from one server-side query result.
func Evaluate(rule Rule, state RuleState, samples []Sample, queryErr error, now time.Time) (RuleState, Transition) {
	previousFiring := state.Firing
	evaluatedAt := now.UTC()
	state.LastEvaluatedAt = &evaluatedAt
	state.Values = samples
	state.LastError = ""

	if queryErr != nil {
		state.LastError = queryErr.Error()
		return applyPolicy(state, rule.QueryErrorPolicy, previousFiring, now)
	}
	if len(samples) == 0 {
		return applyPolicy(state, rule.NoDataPolicy, previousFiring, now)
	}

	triggered, recovered := matches(rule, samples, state.Firing)
	if state.Firing {
		if recovered {
			state.Firing = false
			state.PendingSince = nil
		} else {
			state.PendingSince = nil
		}
	} else if triggered {
		if state.PendingSince == nil {
			pendingSince := now.UTC()
			state.PendingSince = &pendingSince
		}
		if rule.ForSeconds == 0 || !now.Before(state.PendingSince.Add(time.Duration(rule.ForSeconds)*time.Second)) {
			state.Firing = true
			state.PendingSince = nil
		}
	} else {
		state.PendingSince = nil
	}
	return state, firingTransition(previousFiring, state.Firing)
}

func applyPolicy(state RuleState, policy string, previousFiring bool, now time.Time) (RuleState, Transition) {
	switch policy {
	case PolicyTrigger:
		state.Firing = true
		state.PendingSince = nil
	case PolicyResolve:
		state.Firing = false
		state.PendingSince = nil
	case PolicyKeep:
	}
	return state, firingTransition(previousFiring, state.Firing)
}

func firingTransition(previous, current bool) Transition {
	if !previous && current {
		return TransitionFired
	}
	if previous && !current {
		return TransitionResolved
	}
	return TransitionNone
}

func matches(rule Rule, samples []Sample, firing bool) (triggered, recovered bool) {
	if rule.Condition == ConditionPresent {
		return true, false
	}
	if rule.Condition != ConditionThreshold {
		return false, false
	}
	triggered = false
	recovered = true
	for _, sample := range samples {
		if firing {
			if !recoveryMatches(rule, sample.Value) {
				recovered = false
			}
		} else if comparisonMatches(rule.Comparison, sample.Value, rule.Threshold) {
			triggered = true
		}
	}
	return triggered, recovered
}

func comparisonMatches(operator string, value, threshold float64) bool {
	switch operator {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	default:
		return false
	}
}

func recoveryMatches(rule Rule, value float64) bool {
	if rule.Comparison == ">" || rule.Comparison == ">=" {
		return value <= rule.RecoveryThreshold
	}
	return value >= rule.RecoveryThreshold
}

func SameDefinition(a, b Rule) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Enabled == b.Enabled &&
		a.Metric == b.Metric && a.Grouping == b.Grouping && a.Condition == b.Condition &&
		a.Comparison == b.Comparison && a.Threshold == b.Threshold &&
		a.RecoveryThreshold == b.RecoveryThreshold && a.LookbackSeconds == b.LookbackSeconds &&
		a.EvaluationSeconds == b.EvaluationSeconds && a.ForSeconds == b.ForSeconds &&
		a.NoDataPolicy == b.NoDataPolicy && a.QueryErrorPolicy == b.QueryErrorPolicy
}

func Status(state RuleState) string {
	if state.LastError != "" {
		return "error"
	}
	if state.Firing {
		return "firing"
	}
	if state.PendingSince != nil {
		return "pending"
	}
	return "inactive"
}
