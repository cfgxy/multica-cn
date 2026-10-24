package promrules

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
)

func validRule() Rule {
	return Rule{
		ID:                "8da35391-fd75-4b80-b4ec-436d563c5348",
		Name:              "Failure rate",
		Enabled:           true,
		Metric:            MetricFailureRate,
		Grouping:          GroupWorkspace,
		Condition:         ConditionThreshold,
		Comparison:        ">=",
		Threshold:         20,
		RecoveryThreshold: 10,
		LookbackSeconds:   300,
		EvaluationSeconds: 30,
		ForSeconds:        60,
		NoDataPolicy:      PolicyKeep,
		QueryErrorPolicy:  PolicyKeep,
	}
}

func TestValidateRules(t *testing.T) {
	t.Run("normalizes and fills id", func(t *testing.T) {
		rules := []Rule{validRule()}
		rules[0].ID = ""
		rules[0].Name = "  Rate  "
		if err := ValidateRules(rules); err != nil {
			t.Fatal(err)
		}
		if rules[0].ID == "" || rules[0].Name != "Rate" {
			t.Fatalf("rule was not normalized: %+v", rules[0])
		}
	})

	tests := []struct {
		name string
		edit func([]Rule) []Rule
	}{
		{"duplicate id", func(r []Rule) []Rule { return append(r, r[0]) }},
		{"duplicate name", func(r []Rule) []Rule {
			other := r[0]
			other.ID = "ec5b5f3d-6ad1-4815-83f7-3f87d3b65f8f"
			return append(r, other)
		}},
		{"unsupported metric", func(r []Rule) []Rule { r[0].Metric = "up"; return r }},
		{"invalid recovery hysteresis", func(r []Rule) []Rule { r[0].RecoveryThreshold = r[0].Threshold; return r }},
		{"unbounded lookback", func(r []Rule) []Rule { r[0].LookbackSeconds = 604801; return r }},
		{"unsafe error policy", func(r []Rule) []Rule { r[0].QueryErrorPolicy = "ignore"; return r }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := tt.edit([]Rule{validRule()})
			if err := ValidateRules(rules); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestBuildQueryBindsWorkspaceAndOnlyUsesFixedMetrics(t *testing.T) {
	rule := validRule()
	query, err := BuildQuery("6e331e93-e134-40b6-8868-cb42e22fc947", rule)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"multica_agent_task_failures_total{workspace_id=\"6e331e93-e134-40b6-8868-cb42e22fc947\"}",
		"multica_agent_task_runs_total{workspace_id=\"6e331e93-e134-40b6-8868-cb42e22fc947\"}",
		"[300s]",
		"* 100",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("query %q missing %q", query, want)
		}
	}
	if _, err := BuildQuery("workspace\"} or up", rule); err == nil {
		t.Fatal("invalid workspace id must be rejected")
	}

	rule.Grouping = GroupAgent
	query, err = BuildQuery("6e331e93-e134-40b6-8868-cb42e22fc947", rule)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(query, "by (agent_id)") != 2 {
		t.Fatalf("agent ratio must aggregate numerator and denominator by agent: %s", query)
	}
}

func TestEvaluatePendingFiringAndRecovery(t *testing.T) {
	rule := validRule()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	state, transition := Evaluate(rule, RuleState{}, []Sample{{Value: 21}}, nil, now)
	if transition != TransitionNone || Status(state) != "pending" {
		t.Fatalf("first breach should become pending: state=%+v transition=%q", state, transition)
	}
	state, transition = Evaluate(rule, state, []Sample{{Value: 22}}, nil, now.Add(59*time.Second))
	if transition != TransitionNone || state.Firing {
		t.Fatalf("breach before duration should remain pending: state=%+v", state)
	}
	state, transition = Evaluate(rule, state, []Sample{{Value: 25}}, nil, now.Add(60*time.Second))
	if transition != TransitionFired || !state.Firing {
		t.Fatalf("sustained breach should fire: state=%+v transition=%q", state, transition)
	}
	state, transition = Evaluate(rule, state, []Sample{{Value: 12}}, nil, now.Add(90*time.Second))
	if transition != TransitionNone || !state.Firing {
		t.Fatalf("hysteresis band must remain firing: state=%+v", state)
	}
	state, transition = Evaluate(rule, state, []Sample{{Value: 10}}, nil, now.Add(120*time.Second))
	if transition != TransitionResolved || state.Firing {
		t.Fatalf("recovery threshold should resolve: state=%+v transition=%q", state, transition)
	}
}

func TestEvaluateExplicitNoDataAndQueryErrorPolicies(t *testing.T) {
	rule := validRule()
	state := RuleState{Firing: true}
	rule.NoDataPolicy = PolicyKeep
	kept, transition := Evaluate(rule, state, nil, nil, time.Now())
	if transition != TransitionNone || !kept.Firing {
		t.Fatalf("keep policy changed firing state: %+v", kept)
	}
	rule.NoDataPolicy = PolicyResolve
	resolved, transition := Evaluate(rule, state, nil, nil, time.Now())
	if transition != TransitionResolved || resolved.Firing {
		t.Fatalf("resolve policy did not clear state: %+v", resolved)
	}
	rule.QueryErrorPolicy = PolicyTrigger
	triggered, transition := Evaluate(rule, RuleState{}, nil, errors.New("prometheus unavailable"), time.Now())
	if transition != TransitionFired || !triggered.Firing || Status(triggered) != "error" {
		t.Fatalf("query error policy not reflected: %+v transition=%q", triggered, transition)
	}
}

func TestRuleTransitionAuditEvent_ValidatesMemberResolution(t *testing.T) {
	workspaceID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	memberID := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	rule := validRule()
	rule.State = RuleState{Firing: true, Values: []Sample{{Labels: map[string]string{"agent_id": "private-label"}, Value: 21}}}

	event := RuleTransitionAuditEvent(
		workspaceID, rule, TransitionResolved, time.Now().UTC(),
		service.AuditActorMember, memberID, "rule_disabled",
	)
	if err := event.Validate(); err != nil {
		t.Fatalf("member resolution event must satisfy the audit contract: %v", err)
	}
	if event.EventType != service.AuditOpsPromQLRuleResolved {
		t.Fatalf("unexpected event type %q", event.EventType)
	}
	var details map[string]any
	if err := json.Unmarshal(event.Details, &details); err != nil {
		t.Fatalf("decode resolution details: %v", err)
	}
	if details["resolution_cause"] != "rule_disabled" {
		t.Fatalf("resolution cause missing from details: %v", details)
	}
	if _, leaked := details["labels"]; leaked {
		t.Fatalf("audit event must not include sample labels: %v", details)
	}
}

func TestEvaluatePresenceRuleUsesNoDataPolicyForRecovery(t *testing.T) {
	rule := validRule()
	rule.Condition = ConditionPresent
	rule.ForSeconds = 0
	rule.NoDataPolicy = PolicyResolve
	state, transition := Evaluate(rule, RuleState{}, []Sample{{Value: 0}}, nil, time.Now())
	if transition != TransitionFired || !state.Firing {
		t.Fatalf("present vector should fire: %+v", state)
	}
	state, transition = Evaluate(rule, state, nil, nil, time.Now().Add(time.Second))
	if transition != TransitionResolved || state.Firing {
		t.Fatalf("absence should follow the configured no-data policy: %+v", state)
	}
}
