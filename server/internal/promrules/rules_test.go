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

func TestValidatePersistedConfig_RequiresStableRuleIDs(t *testing.T) {
	config := Config{Rules: []Rule{validRule()}}
	if err := validatePersistedConfig(config); err != nil {
		t.Fatalf("valid persisted config: %v", err)
	}
	config.Rules[0].ID = ""
	if err := validatePersistedConfig(config); err == nil {
		t.Fatal("persisted rule without an ID must be rejected before evaluation")
	}
	config.Rules[0].ID = "not-a-uuid"
	if err := validatePersistedConfig(config); err == nil {
		t.Fatal("persisted rule with an invalid ID must be rejected before evaluation")
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

func TestEvaluateAgentRule_UsesOnlySelectedVerifiedAgentIDs(t *testing.T) {
	rule := validRule()
	rule.Grouping = GroupAgent
	rule.ForSeconds = 0
	selectedID := "11111111-1111-1111-1111-111111111111"
	otherSelectedID := "22222222-2222-2222-2222-222222222222"
	unknownID := "33333333-3333-3333-3333-333333333333"
	selected := map[string]pgtype.UUID{
		selectedID:      parseRuleUUID(selectedID),
		otherSelectedID: parseRuleUUID(otherSelectedID),
	}

	transition := evaluateAgentRule(&rule, selected, []Sample{
		{Labels: map[string]string{"agent_id": selectedID}, Value: 25},
		{Labels: map[string]string{"agent_id": unknownID}, Value: 99},
		{Labels: map[string]string{"agent_id": "not-a-uuid"}, Value: 99},
		{Labels: map[string]string{}, Value: 99},
	}, nil, time.Now().UTC())
	if transition != TransitionFired || !AgentState(rule, selectedID).Firing {
		t.Fatalf("selected agent must fire: state=%+v transition=%q", rule, transition)
	}
	if _, exists := rule.AgentStates[otherSelectedID]; exists {
		t.Fatalf("selected agent without a verified sample must not acquire state: %+v", rule.AgentStates)
	}
	if _, exists := rule.AgentStates[unknownID]; exists {
		t.Fatalf("unknown or cross-workspace label must not acquire state: %+v", rule.AgentStates)
	}
	desired := desiredRulePauses([]Rule{rule}, map[string]map[string]pgtype.UUID{rule.ID: selected})
	if len(desired[rule.ID]) != 1 || !desired[rule.ID][selectedID].Valid {
		t.Fatalf("only the verified selected agent may acquire a pause: %+v", desired)
	}

	transition = evaluateAgentRule(&rule, selected, []Sample{{Labels: map[string]string{"agent_id": unknownID}, Value: 99}}, nil, time.Now().UTC())
	if transition != TransitionResolved || rule.State.Firing || len(rule.AgentStates) != 0 {
		t.Fatalf("mapping loss must resolve only the persisted agent state: state=%+v transition=%q", rule, transition)
	}
	if cause := rulePauseClearCause([]Rule{rule}, map[string]map[string]pgtype.UUID{rule.ID: selected}, rule.ID, selectedID); cause != "mapping_mismatch" {
		t.Fatalf("mapping loss clear cause = %q, want mapping_mismatch", cause)
	}
}

func TestEvaluateAgentRule_KeepsPendingAndDoesNotCreateQueryErrorMappings(t *testing.T) {
	rule := validRule()
	rule.Grouping = GroupAgent
	rule.QueryErrorPolicy = PolicyTrigger
	rule.ForSeconds = 60
	agentID := "11111111-1111-1111-1111-111111111111"
	selected := map[string]pgtype.UUID{agentID: parseRuleUUID(agentID)}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	transition := evaluateAgentRule(&rule, selected, nil, errors.New("prometheus unavailable"), now)
	if transition != TransitionNone || rule.State.Firing || len(rule.AgentStates) != 0 {
		t.Fatalf("query error must not create a mapping or pause: state=%+v transition=%q", rule, transition)
	}
	transition = evaluateAgentRule(&rule, selected, []Sample{{Labels: map[string]string{"agent_id": agentID}, Value: 25}}, nil, now)
	if transition != TransitionNone || AgentState(rule, agentID).PendingSince == nil {
		t.Fatalf("first verified breach should be per-agent pending: state=%+v transition=%q", rule, transition)
	}
	transition = evaluateAgentRule(&rule, selected, []Sample{{Labels: map[string]string{"agent_id": agentID}, Value: 25}}, nil, now.Add(time.Minute))
	if transition != TransitionFired || !AgentState(rule, agentID).Firing {
		t.Fatalf("sustained verified breach should fire that agent: state=%+v transition=%q", rule, transition)
	}
}
