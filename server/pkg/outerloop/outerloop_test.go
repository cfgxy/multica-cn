package outerloop

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePlan(t *testing.T) {
	mustMeta := func(raw string) map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("bad test metadata: %v", err)
		}
		return m
	}

	t.Run("absent state means no plan", func(t *testing.T) {
		if HasPlan(mustMeta(`{"other":"x"}`)) {
			t.Fatal("metadata without outerloop_state must report no plan")
		}
		if _, ok := ParsePlan(mustMeta(`{"other":"x"}`)); ok {
			t.Fatal("ParsePlan must reject a plan-less bag")
		}
	})

	t.Run("invalid state means no plan", func(t *testing.T) {
		if _, ok := ParsePlan(mustMeta(`{"outerloop_state":"paused"}`)); ok {
			t.Fatal("unknown state must not decode as a plan")
		}
	})

	t.Run("defaults and clamps", func(t *testing.T) {
		plan, ok := ParsePlan(mustMeta(`{"outerloop_state":"running","outerloop_agent":"11111111-1111-1111-1111-111111111111","outerloop_goal":"做完全部验收"}`))
		if !ok {
			t.Fatal("expected a plan")
		}
		if plan.MaxRounds != DefaultMaxRounds {
			t.Fatalf("default max = %d, want %d", plan.MaxRounds, DefaultMaxRounds)
		}
		if plan.Round != 0 || plan.Goal != "做完全部验收" || plan.AgentID == "" {
			t.Fatalf("unexpected plan: %+v", plan)
		}
	})

	t.Run("hard cap clamps oversized max", func(t *testing.T) {
		plan, ok := ParsePlan(mustMeta(`{"outerloop_state":"running","outerloop_max_rounds":99999}`))
		if !ok {
			t.Fatal("expected a plan")
		}
		if plan.MaxRounds != HardMaxRounds {
			t.Fatalf("max = %d, want clamp to %d", plan.MaxRounds, HardMaxRounds)
		}
	})

	t.Run("negative round clamps to zero", func(t *testing.T) {
		plan, _ := ParsePlan(mustMeta(`{"outerloop_state":"waiting_human","outerloop_round":-3,"outerloop_max_rounds":5}`))
		if plan.Round != 0 || plan.MaxRounds != 5 {
			t.Fatalf("unexpected plan: %+v", plan)
		}
	})
}

func TestClampMaxRounds(t *testing.T) {
	if _, ok := ClampMaxRounds(0); ok {
		t.Fatal("zero max must be invalid")
	}
	if _, ok := ClampMaxRounds(HardMaxRounds + 1); ok {
		t.Fatal("beyond hard cap must be invalid")
	}
	if v, ok := ClampMaxRounds(7); !ok || v != 7 {
		t.Fatalf("ClampMaxRounds(7) = %d,%v", v, ok)
	}
	if v, ok := ClampMaxRounds(HardMaxRounds); !ok || v != HardMaxRounds {
		t.Fatalf("hard cap value must be accepted")
	}
}

func TestKeyClassification(t *testing.T) {
	for _, k := range []string{MetaState, MetaMaxRounds, MetaAgent} {
		if !IsControlPlaneKey(k) {
			t.Errorf("%s must be control-plane", k)
		}
		if IsServerOnlyKey(k) {
			t.Errorf("%s must not be server-only", k)
		}
	}
	for _, k := range []string{MetaRound, MetaWaitReason} {
		if !IsServerOnlyKey(k) {
			t.Errorf("%s must be server-only", k)
		}
		if IsControlPlaneKey(k) {
			t.Errorf("%s must not be control-plane", k)
		}
	}
	if IsControlPlaneKey(MetaGoal) || IsServerOnlyKey(MetaGoal) {
		t.Fatal("goal is the agent-writable brief and must be neither")
	}
	if !ValidState(StateRunning) || !ValidState(StateWaitingHuman) || !ValidState(StateStopped) {
		t.Fatal("the three documented states must validate")
	}
	if ValidState("paused") || ValidState("") {
		t.Fatal("unknown/empty states must not validate")
	}
}

func TestRoundDoc(t *testing.T) {
	doc := RoundDoc(3, 20, "跑完外循环验收")
	if doc == "" || !strings.Contains(doc, "3/20") || !strings.Contains(doc, "跑完外循环验收") || !strings.Contains(doc, "STAGE_COMPLETE") {
		t.Fatalf("round doc must carry round, goal and exit convention: %q", doc)
	}
	empty := RoundDoc(1, 5, "")
	if strings.Contains(empty, "任务目标：") {
		t.Fatalf("empty goal must not render an empty 目标 slot: %q", empty)
	}
}
