// Package outerloop holds the pure decision core of the explicit
// continuation outer loop (RUYI-660): the end-marker predicate over an
// agent's terminal report, the member-reply intent classifier, and the
// pipeline-class boundary that decides whether a failed run escalates to a
// human or is left for the stall sweep to pull back.
//
// Everything in this package is side-effect free so the server-side loop
// wiring (service/handler/scheduler) can be reviewed as pure plumbing. The
// failure taxonomy it reads lives in pkg/taskfailure; this package never
// imports it — reasons arrive as plain strings so the classification table
// stays decoupled from taxonomy evolution.
package outerloop

import (
	"strconv"
	"strings"
)

// StageCompleteMarker is the canonical end marker an agent's stage-closing
// report must carry. It maps the workspace's own reporting convention
// (阶段状态：STAGE_COMPLETE as the stage-end signal), so a plan's loop
// semantics and the humans reading the thread share one token.
const StageCompleteMarker = "STAGE_COMPLETE"

// Metadata keys carrying the per-issue plan in issues.metadata. The plan
// exists iff outerloop_state is present.
//
// Control-plane keys are member-writable (through the metadata handler's
// transition validation) and agent-forbidden; server-only keys are written
// exclusively by server logic via the single-key atomic SQL path — neither
// actors nor members may write them directly.
const (
	MetaState     = "outerloop_state"      // control-plane: running | waiting_human | stopped
	MetaGoal      = "outerloop_goal"       // brief; the only agent-writable key
	MetaMaxRounds = "outerloop_max_rounds" // control-plane; int, monotonic up
	MetaRound     = "outerloop_round"      // server-only: auto continuation rounds dispatched so far
	MetaAgent     = "outerloop_agent"      // control-plane: driving agent UUID, pinned at open
	MetaWaitReason = "outerloop_wait_reason" // server-only: why the loop is waiting on a human
)

// Plan states. Empty/absent means "no plan".
const (
	StateRunning      = "running"
	StateWaitingHuman = "waiting_human"
	StateStopped      = "stopped"
)

// DefaultMaxRounds bounds a plan when outerloop_max_rounds was never set.
const DefaultMaxRounds = 20

// HardMaxRounds is the ceiling any member-supplied max is clamped to. The
// max only ever rises: a write that lowers it keeps the stored value.
const HardMaxRounds = 1000

// ValidState reports whether s names a plan state.
func ValidState(s string) bool {
	switch s {
	case StateRunning, StateWaitingHuman, StateStopped:
		return true
	}
	return false
}

// IsControlPlaneKey reports whether key is member-writable (with transition
// validation) and agent-forbidden. Server-only keys are neither.
func IsControlPlaneKey(key string) bool {
	switch key {
	case MetaState, MetaMaxRounds, MetaAgent:
		return true
	}
	return false
}

// IsServerOnlyKey reports whether key is written exclusively by server
// logic: agents get 403 and members get 403.
func IsServerOnlyKey(key string) bool {
	switch key {
	case MetaRound, MetaWaitReason:
		return true
	}
	return false
}

// Plan is the decoded view of an issue's outer-loop plan.
type Plan struct {
	State     string
	Goal      string
	MaxRounds int
	Round     int
	AgentID   string // empty when the plan never pinned an agent
}

// HasPlan reports whether the metadata bag carries a plan. The bag is the
// parsed issues.metadata map (values are string / float64 / bool by the
// handler's write contract).
func HasPlan(metadata map[string]any) bool {
	s, _ := metadata[MetaState].(string)
	return ValidState(s)
}

// ParsePlan decodes the plan from a metadata bag, applying defaults. ok is
// false when no valid plan state is present — callers must treat the issue
// as plan-less in that case, even if stray keys remain.
func ParsePlan(metadata map[string]any) (Plan, bool) {
	state, _ := metadata[MetaState].(string)
	if !ValidState(state) {
		return Plan{}, false
	}
	plan := Plan{
		State:     state,
		Goal:      stringField(metadata[MetaGoal]),
		MaxRounds: DefaultMaxRounds,
		Round:     intField(metadata[MetaRound]),
		AgentID:   stringField(metadata[MetaAgent]),
	}
	if max := intField(metadata[MetaMaxRounds]); max > 0 {
		plan.MaxRounds = max
	}
	if plan.MaxRounds > HardMaxRounds {
		plan.MaxRounds = HardMaxRounds
	}
	if plan.Round < 0 {
		plan.Round = 0
	}
	return plan, true
}

// ClampMaxRounds validates a member-supplied max: it must be a positive
// integer within the hard cap. Monotonicity (only up) is the caller's job —
// it needs the stored value, which this package does not see.
func ClampMaxRounds(value int) (int, bool) {
	if value < 1 || value > HardMaxRounds {
		return 0, false
	}
	return value, true
}

// RoundDoc is the per-round guidance injected into every auto-continuation
// handoff so the woken run knows where it stands in the loop and how it is
// expected to end (or escalate). goal is the plan brief; both may be empty.
func RoundDoc(round, max int, goal string) string {
	var b strings.Builder
	b.WriteString("【外循环续跑】第 ")
	b.WriteString(strconv.Itoa(round))
	b.WriteString("/")
	b.WriteString(strconv.Itoa(max))
	b.WriteString(" 轮。")
	if goal != "" {
		b.WriteString("任务目标：")
		b.WriteString(goal)
		b.WriteString("。")
	}
	b.WriteString("退出约定：阶段完成时发布以 STAGE_COMPLETE 结尾的阶段结束回报，并把 Issue 置 in_review；未完成则继续推进，不要提前关闭 Issue；需要人工输入时，说明阻塞点后结束本轮等待回复。")
	return b.String()
}

func stringField(v any) string {
	s, _ := v.(string)
	return s
}

func intField(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
