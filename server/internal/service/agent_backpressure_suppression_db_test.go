package service

// RUYI-859 stage B, DB-gated lifecycle tests for the automatic error-rate
// suppression loop. Every test drives TaskService.TickAgentBackpressureSuppression
// over a real database (skipped when DATABASE_URL is unreachable) and asserts
// the full transition trail: the scheduling_pause row itself, the audit row
// (system actor), the entered/cleared suppression-event snapshot, and the
// realtime broadcast — each owed exactly once, on the actual insert/delete
// only.
//
// The entry and recovery windows both read from agent_task_queue terminal
// rows completed inside them; the fixtures timestamp every sample at
// now() so it falls inside both the 60-minute entry and the 30-minute
// recovery window, and the mix is rewritten between ticks to model the
// trailing windows sliding.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// seedBPFixture builds the pause-fixture plus the stage A/B tables' cleanup:
// the fixture owns workspace/runtime/agent rows; the policy and suppression
// event rows belong to this test set and are removed first (no FKs by repo
// rule, so order only keeps the shared database tidy between runs).
func seedBPFixture(t *testing.T) (pool *pgxpool.Pool, workspaceID, agentID, ownerID, runtimeID string) {
	t.Helper()
	ctx := context.Background()
	pool = newTaskClaimRacePool(t)
	workspaceID, agentID, runtimeID, ownerID = createSchedulingPauseFixture(t, ctx, pool)
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		pool.Exec(cleanupCtx, `DELETE FROM agent_suppression_event WHERE agent_id = $1`, agentID)
		pool.Exec(cleanupCtx, `DELETE FROM agent_backpressure_policy WHERE agent_id = $1`, agentID)
	})
	return
}

// requireStageBTables skips the whole test when the database predates the
// stage A tables: the loop cannot select armed policies without them, and
// asserting against a missing table would be a false red.
func requireStageBTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var policyTable, eventTable string
	if err := pool.QueryRow(context.Background(),
		`SELECT to_regclass('agent_backpressure_policy')::text, to_regclass('agent_suppression_event')::text`).
		Scan(&policyTable, &eventTable); err != nil {
		t.Skipf("probe stage A tables: %v", err)
	}
	if policyTable == "" || eventTable == "" {
		t.Skipf("database not migrated through the stage A backpressure tables (%q, %q): stage B lifecycle not applicable",
			policyTable, eventTable)
	}
}

// seedBPPolicy saves the armed policy card the loop judges against: a 60
// minute entry window, 30% threshold, 5 sample floor, 30 minute recovery
// window, both failure-class switches on.
func seedBPPolicy(t *testing.T, pool *pgxpool.Pool, workspaceID, agentID, ownerID string) {
	t.Helper()
	_, err := db.New(pool).UpsertAgentBackpressurePolicy(context.Background(), db.UpsertAgentBackpressurePolicyParams{
		WorkspaceID:            util.MustParseUUID(workspaceID),
		AgentID:                util.MustParseUUID(agentID),
		WindowMinutes:          60,
		ErrorRateThresholdPct:  30,
		MinSamples:             5,
		RecoveryWindowMinutes:  30,
		SuppressOnErrorRate:    true,
		CountModelApiFailures:  true,
		CountExecutionFailures: true,
		UpdatedBy:              util.MustParseUUID(ownerID),
	})
	if err != nil {
		t.Fatalf("seed armed policy: %v", err)
	}
}

// insertBPTerminalSamples adds succeeded and failed terminal rows for the
// agent; every row is completed "now" so it sits inside both trailing
// windows. The fixture's reverse-order cleanup removes them with the
// agent_task_queue row sweep.
func insertBPTerminalSamples(t *testing.T, pool *pgxpool.Pool, agentID, runtimeID string, succeeded int, failedReasons []string) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < succeeded; i++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, completed_at)
			VALUES ($1, $2, 'completed', 0, now())
		`, agentID, runtimeID); err != nil {
			t.Fatalf("insert succeeded sample: %v", err)
		}
	}
	for _, reason := range failedReasons {
		if _, err := pool.Exec(ctx, `
			INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, failure_reason, completed_at)
			VALUES ($1, $2, 'failed', 0, $3, now())
		`, agentID, runtimeID, reason); err != nil {
			t.Fatalf("insert failed sample: %v", err)
		}
	}
}

// deleteBPTerminalSamples drops the fixture agent's terminal rows by
// failure_reason match: the way the trailing window slides once the
// failures age out.
func deleteBPTerminalSamples(t *testing.T, pool *pgxpool.Pool, agentID string, reason string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM agent_task_queue WHERE agent_id = $1 AND failure_reason = $2`,
		agentID, reason); err != nil {
		t.Fatalf("delete terminal samples: %v", err)
	}
}

// countPauseRows returns how many scheduling_pause rows sit in one slot
// (workspace-level and agent-level rows are distinct slots).
func countPauseRows(t *testing.T, pool *pgxpool.Pool, workspaceID string, agentID *string) int {
	t.Helper()
	var n int
	if agentID == nil {
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM scheduling_pause WHERE workspace_id = $1 AND agent_id IS NULL`,
			workspaceID).Scan(&n); err != nil {
			t.Fatalf("count workspace pause rows: %v", err)
		}
		return n
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scheduling_pause WHERE workspace_id = $1 AND agent_id = $2`,
		workspaceID, *agentID).Scan(&n); err != nil {
		t.Fatalf("count agent pause rows: %v", err)
	}
	return n
}

// isSystemErrorRatePause reads back the agent-level row, if any, and
// reports whether it is exactly the loop's own system source: created_by
// NULL with the fixed reason.
func isSystemErrorRatePause(t *testing.T, pool *pgxpool.Pool, workspaceID, agentID string) bool {
	t.Helper()
	var createdByValid bool
	var reason string
	if err := pool.QueryRow(context.Background(),
		`SELECT created_by IS NOT NULL, reason FROM scheduling_pause WHERE workspace_id = $1 AND agent_id = $2`,
		workspaceID, agentID).Scan(&createdByValid, &reason); err != nil {
		return false // no row in the slot
	}
	return !createdByValid && reason == SchedulingPauseReasonErrorRateSuppression
}

// foreignAgentPause inserts a human agent-level freeze the loop must
// observe but never acquire or lift.
func foreignAgentPause(t *testing.T, pool *pgxpool.Pool, workspaceID, agentID, ownerID, reason string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO scheduling_pause (workspace_id, agent_id, reason, created_by) VALUES ($1, $2, $3, $4)`,
		workspaceID, agentID, reason, ownerID); err != nil {
		t.Fatalf("insert foreign agent pause: %v", err)
	}
}

// countSuppressionEvents / countAuditTransitions are the "owed exactly
// once" counters: one suppression-event row per transition, one audit row
// per transition, each attributable to the system actor.
func countSuppressionEvents(t *testing.T, pool *pgxpool.Pool, agentID, eventType string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_suppression_event WHERE agent_id = $1 AND event_type = $2`,
		agentID, eventType).Scan(&n); err != nil {
		t.Fatalf("count suppression events: %v", err)
	}
	return n
}

func countAuditTransitions(t *testing.T, pool *pgxpool.Pool, workspaceID, agentID, auditEventType string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_event WHERE workspace_id = $1 AND agent_id = $2 AND event_type = $3`,
		workspaceID, agentID, auditEventType).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func assertSystemAuditActor(t *testing.T, pool *pgxpool.Pool, workspaceID, agentID, auditEventType string) {
	t.Helper()
	var actorType string
	var actorIDNull bool
	if err := pool.QueryRow(context.Background(),
		`SELECT actor_type, actor_id IS NULL FROM audit_event WHERE workspace_id = $1 AND agent_id = $2 AND event_type = $3`,
		workspaceID, agentID, auditEventType).Scan(&actorType, &actorIDNull); err != nil {
		t.Fatalf("no audit row to attribute: %v", err)
	}
	if actorType != "system" || !actorIDNull {
		t.Fatalf("automatic transition audit: actor_type=%q actor_id_null=%v, want system/NULL", actorType, actorIDNull)
	}
}

// bpSuppressionHarness owns one bus-wired TaskService plus the broadcast
// capture; a fresh harness over the SAME database models a process
// restart (no in-process timers exist anywhere in the loop).
type bpSuppressionHarness struct {
	svc     *TaskService
	paused  []events.Event
	resumed []events.Event
}

func newBPSuppressionHarness(t *testing.T, pool *pgxpool.Pool) *bpSuppressionHarness {
	t.Helper()
	bus := events.New()
	h := &bpSuppressionHarness{svc: NewTaskService(db.New(pool), pool, nil, bus)}
	bus.Subscribe(protocol.EventAgentSchedulingPaused, func(e events.Event) {
		h.paused = append(h.paused, e)
	})
	bus.Subscribe(protocol.EventAgentSchedulingResumed, func(e events.Event) {
		h.resumed = append(h.resumed, e)
	})
	return h
}

// The full entry -> idempotent-tick -> recovery -> no-op lifecycle of one
// armed agent, and the transitions-only audit/event/broadcast trail.
func TestAgentBackpressureSuppression_EntryThenRecoveryLifecycle(t *testing.T) {
	pool, workspaceID, agentID, ownerID, runtimeID := seedBPFixture(t)
	requireStageBTables(t, pool)
	seedBPPolicy(t, pool, workspaceID, agentID, ownerID)
	// 7 succeeded + 3 rate-limit failed = 10 terminal, 30.0% — exactly at
	// the entry threshold, above the 5 sample floor.
	insertBPTerminalSamples(t, pool, agentID, runtimeID, 7, []string{
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
	})
	h := newBPSuppressionHarness(t, pool)
	ctx := context.Background()

	// First tick: armed entry inserts the system row and owes the full trail.
	entered, cleared, err := h.svc.TickAgentBackpressureSuppression(ctx)
	if err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if entered != 1 || cleared != 0 {
		t.Fatalf("first tick transitions: entered=%d cleared=%d, want 1/0", entered, cleared)
	}
	if !isSystemErrorRatePause(t, pool, workspaceID, agentID) {
		t.Fatalf("agent-level slot: want the loop's system source row (created_by NULL, fixed reason)")
	}
	if got := countPauseRows(t, pool, workspaceID, &agentID); got != 1 {
		t.Fatalf("agent-level pause rows: got %d, want 1", got)
	}
	if got := countAuditTransitions(t, pool, workspaceID, agentID, "agent.scheduling_paused"); got != 1 {
		t.Fatalf("audit paused rows: got %d, want 1", got)
	}
	assertSystemAuditActor(t, pool, workspaceID, agentID, "agent.scheduling_paused")
	if got := countSuppressionEvents(t, pool, agentID, AgentSuppressionEventEntered); got != 1 {
		t.Fatalf("entered suppression events: got %d, want 1", got)
	}
	if len(h.paused) != 1 || len(h.resumed) != 0 {
		t.Fatalf("broadcasts after entry: paused=%d resumed=%d, want 1/0", len(h.paused), len(h.resumed))
	}
	if ev := h.paused[0]; ev.ActorType != "system" {
		t.Fatalf("entry broadcast actor: %q, want system", ev.ActorType)
	}

	// Second tick over the same state: fully idempotent — no row, no audit,
	// no event, no second broadcast.
	entered, cleared, err = h.svc.TickAgentBackpressureSuppression(ctx)
	if err != nil {
		t.Fatalf("idempotent tick: %v", err)
	}
	if entered != 0 || cleared != 0 {
		t.Fatalf("idempotent tick transitions: entered=%d cleared=%d, want 0/0", entered, cleared)
	}
	if got := countAuditTransitions(t, pool, workspaceID, agentID, "agent.scheduling_paused"); got != 1 {
		t.Fatalf("audit paused rows after no-op tick: got %d, want still 1", got)
	}
	if got := countSuppressionEvents(t, pool, agentID, AgentSuppressionEventEntered); got != 1 {
		t.Fatalf("entered events after no-op tick: got %d, want still 1", got)
	}
	if len(h.paused) != 1 {
		t.Fatalf("paused broadcasts after no-op tick: got %d, want still 1", len(h.paused))
	}

	// The failures age out of the trailing windows: the entry judgment
	// disarms and the recovery judgment clears (0 failures, 7 samples >=
	// floor, 0% strictly under the 15% recovery line).
	deleteBPTerminalSamples(t, pool, agentID, string(taskfailure.ReasonAgentProviderCapacityOrRateLimit))
	entered, cleared, err = h.svc.TickAgentBackpressureSuppression(ctx)
	if err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	if entered != 0 || cleared != 1 {
		t.Fatalf("recovery tick transitions: entered=%d cleared=%d, want 0/1", entered, cleared)
	}
	if isSystemErrorRatePause(t, pool, workspaceID, agentID) {
		t.Fatalf("agent-level slot: the system row must be gone after recovery")
	}
	if got := countPauseRows(t, pool, workspaceID, &agentID); got != 0 {
		t.Fatalf("agent-level pause rows after release: got %d, want 0", got)
	}
	if got := countAuditTransitions(t, pool, workspaceID, agentID, "agent.scheduling_resumed"); got != 1 {
		t.Fatalf("audit resumed rows: got %d, want 1", got)
	}
	assertSystemAuditActor(t, pool, workspaceID, agentID, "agent.scheduling_resumed")
	if got := countSuppressionEvents(t, pool, agentID, AgentSuppressionEventCleared); got != 1 {
		t.Fatalf("cleared suppression events: got %d, want 1", got)
	}
	if len(h.resumed) != 1 || len(h.paused) != 1 {
		t.Fatalf("broadcasts after recovery: resumed=%d paused=%d, want 1/1", len(h.resumed), len(h.paused))
	}

	// Third tick with the slot empty and entry disarmed: a quiet no-op.
	entered, cleared, err = h.svc.TickAgentBackpressureSuppression(ctx)
	if err != nil {
		t.Fatalf("post-recovery tick: %v", err)
	}
	if entered != 0 || cleared != 0 {
		t.Fatalf("post-recovery tick transitions: entered=%d cleared=%d, want 0/0", entered, cleared)
	}
	if len(h.paused) != 1 || len(h.resumed) != 1 {
		t.Fatalf("broadcasts after quiet tick: paused=%d resumed=%d, want 1/1", len(h.paused), len(h.resumed))
	}
}

// A human agent-level freeze occupies the slot: the loop observes, takes no
// ownership, writes nothing, and lifts nothing — even when the entry
// judgment is armed.
func TestAgentBackpressureSuppression_HumanAgentPauseObserveOnly(t *testing.T) {
	pool, workspaceID, agentID, ownerID, runtimeID := seedBPFixture(t)
	requireStageBTables(t, pool)
	seedBPPolicy(t, pool, workspaceID, agentID, ownerID)
	insertBPTerminalSamples(t, pool, agentID, runtimeID, 7, []string{
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
	})
	foreignAgentPause(t, pool, workspaceID, agentID, ownerID, "manual_freeze")
	h := newBPSuppressionHarness(t, pool)

	entered, cleared, err := h.svc.TickAgentBackpressureSuppression(context.Background())
	if err != nil {
		t.Fatalf("observe-only tick: %v", err)
	}
	if entered != 0 || cleared != 0 {
		t.Fatalf("observe-only tick transitions: entered=%d cleared=%d, want 0/0", entered, cleared)
	}
	if isSystemErrorRatePause(t, pool, workspaceID, agentID) {
		t.Fatalf("the loop acquired the human's slot: want observe-only")
	}
	var reason string
	if err := pool.QueryRow(context.Background(),
		`SELECT reason FROM scheduling_pause WHERE workspace_id = $1 AND agent_id = $2`,
		workspaceID, agentID).Scan(&reason); err != nil || reason != "manual_freeze" {
		t.Fatalf("foreign row mutated by the loop: reason=%q err=%v, want manual_freeze intact", reason, err)
	}
	if got := countAuditTransitions(t, pool, workspaceID, agentID, "agent.scheduling_paused"); got != 0 {
		t.Fatalf("audit rows owed on observation: got %d, want 0", got)
	}
	if got := countSuppressionEvents(t, pool, agentID, AgentSuppressionEventEntered); got != 0 {
		t.Fatalf("suppression events owed on observation: got %d, want 0", got)
	}
	if len(h.paused) != 0 || len(h.resumed) != 0 {
		t.Fatalf("broadcasts owed on observation: paused=%d resumed=%d, want 0/0", len(h.paused), len(h.resumed))
	}
}

// A workspace-scope human freeze stacks with the agent-level system row:
// the loop creates and releases the agent-level row only, leaving the
// workspace row untouched.
func TestAgentBackpressureSuppression_WorkspaceScopeStacks(t *testing.T) {
	pool, workspaceID, agentID, ownerID, runtimeID := seedBPFixture(t)
	requireStageBTables(t, pool)
	seedBPPolicy(t, pool, workspaceID, agentID, ownerID)
	insertBPTerminalSamples(t, pool, agentID, runtimeID, 7, []string{
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
	})
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO scheduling_pause (workspace_id, reason, created_by) VALUES ($1, $2, $3)`,
		workspaceID, "workspace_freeze", ownerID); err != nil {
		t.Fatalf("insert workspace-level human pause: %v", err)
	}
	h := newBPSuppressionHarness(t, pool)
	ctx := context.Background()

	if entered, _, err := h.svc.TickAgentBackpressureSuppression(ctx); err != nil || entered != 1 {
		t.Fatalf("entry with stacked workspace freeze: entered=%d err=%v, want 1", entered, err)
	}
	if !isSystemErrorRatePause(t, pool, workspaceID, agentID) {
		t.Fatalf("agent-level slot: want the loop's system row alongside the workspace freeze")
	}
	if got := countPauseRows(t, pool, workspaceID, nil); got != 1 {
		t.Fatalf("workspace-level rows: got %d, want 1 (the human freeze)", got)
	}

	// Recovery releases the agent-level system row; the workspace freeze
	// survives — the release is a precise-source delete.
	deleteBPTerminalSamples(t, pool, agentID, string(taskfailure.ReasonAgentProviderCapacityOrRateLimit))
	if _, cleared, err := h.svc.TickAgentBackpressureSuppression(ctx); err != nil || cleared != 1 {
		t.Fatalf("recovery with stacked workspace freeze: cleared=%d err=%v, want 1", cleared, err)
	}
	if isSystemErrorRatePause(t, pool, workspaceID, agentID) {
		t.Fatalf("agent-level slot: system row must be released, workspace row must survive")
	}
	if got := countPauseRows(t, pool, workspaceID, nil); got != 1 {
		t.Fatalf("workspace-level rows after release: got %d, want 1 (untouched)", got)
	}
}

// A foreign agent-level row blocks recovery even when the recovery
// judgment itself clears: only the loop's exact source is releasable.
func TestAgentBackpressureSuppression_ForeignRowBlocksRecovery(t *testing.T) {
	pool, workspaceID, agentID, ownerID, runtimeID := seedBPFixture(t)
	requireStageBTables(t, pool)
	seedBPPolicy(t, pool, workspaceID, agentID, ownerID)
	// 1 failure over 8 terminal = 12.5%: below the 30% entry threshold
	// (disarmed) but also below the 15% recovery line — the recovery
	// judgment clears while a human row holds the slot.
	insertBPTerminalSamples(t, pool, agentID, runtimeID, 7, []string{
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
	})
	foreignAgentPause(t, pool, workspaceID, agentID, ownerID, "manual_freeze")
	h := newBPSuppressionHarness(t, pool)

	entered, cleared, err := h.svc.TickAgentBackpressureSuppression(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if entered != 0 || cleared != 0 {
		t.Fatalf("transitions: entered=%d cleared=%d, want 0/0 (foreign row holds)", entered, cleared)
	}
	var reason string
	if err := pool.QueryRow(context.Background(),
		`SELECT reason FROM scheduling_pause WHERE workspace_id = $1 AND agent_id = $2`,
		workspaceID, agentID).Scan(&reason); err != nil || reason != "manual_freeze" {
		t.Fatalf("human row mutated: reason=%q err=%v", reason, err)
	}
	if got := countAuditTransitions(t, pool, workspaceID, agentID, "agent.scheduling_resumed"); got != 0 {
		t.Fatalf("audit resumed rows owed on a blocked release: got %d, want 0", got)
	}
	if len(h.resumed) != 0 {
		t.Fatalf("resumed broadcasts owed on a blocked release: got %d, want 0", len(h.resumed))
	}
}

// No terminal samples in the recovery window: the pause holds, whatever the
// entry judgment says — no-sample windows never clear.
func TestAgentBackpressureSuppression_NoSamplesNoRecovery(t *testing.T) {
	pool, workspaceID, agentID, ownerID, runtimeID := seedBPFixture(t)
	requireStageBTables(t, pool)
	seedBPPolicy(t, pool, workspaceID, agentID, ownerID)
	insertBPTerminalSamples(t, pool, agentID, runtimeID, 7, []string{
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
	})
	h := newBPSuppressionHarness(t, pool)
	ctx := context.Background()

	if entered, _, err := h.svc.TickAgentBackpressureSuppression(ctx); err != nil || entered != 1 {
		t.Fatalf("entry: entered=%d err=%v, want 1", entered, err)
	}
	// Every terminal sample ages out: the recovery window is empty.
	deleteBPTerminalSamples(t, pool, agentID, string(taskfailure.ReasonAgentProviderCapacityOrRateLimit))
	// ... and the succeeded rows too, so both windows read zero.
	if _, err := pool.Exec(ctx,
		`DELETE FROM agent_task_queue WHERE agent_id = $1 AND status = 'completed'`, agentID); err != nil {
		t.Fatalf("drain succeeded samples: %v", err)
	}

	entered, cleared, err := h.svc.TickAgentBackpressureSuppression(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if entered != 0 || cleared != 0 {
		t.Fatalf("no-sample tick transitions: entered=%d cleared=%d, want 0/0 (no samples, no recovery)", entered, cleared)
	}
	if !isSystemErrorRatePause(t, pool, workspaceID, agentID) {
		t.Fatalf("system pause row vanished on an empty recovery window")
	}
	if len(h.resumed) != 0 {
		t.Fatalf("resumed broadcasts on an empty window: got %d, want 0", len(h.resumed))
	}
}

// Restart recompute: after the entry transition, a brand-new TaskService
// over the SAME database must observe the state purely from rows — no
// paused broadcast owed (nothing new inserted) and the release still owed
// once the windows slide. This is the "process restarted" proof: the loop
// keeps no in-process timers or memory state.
func TestAgentBackpressureSuppression_RestartRecomputesFromDatabase(t *testing.T) {
	pool, workspaceID, agentID, ownerID, runtimeID := seedBPFixture(t)
	requireStageBTables(t, pool)
	seedBPPolicy(t, pool, workspaceID, agentID, ownerID)
	insertBPTerminalSamples(t, pool, agentID, runtimeID, 7, []string{
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
		string(taskfailure.ReasonAgentProviderCapacityOrRateLimit),
	})
	// "Process A" enters the suppression.
	first := newBPSuppressionHarness(t, pool)
	if entered, _, err := first.svc.TickAgentBackpressureSuppression(context.Background()); err != nil || entered != 1 {
		t.Fatalf("first process entry: entered=%d err=%v, want 1", entered, err)
	}
	if len(first.paused) != 1 {
		t.Fatalf("first process paused broadcasts: got %d, want 1", len(first.paused))
	}

	// "Process B": a fresh service and a fresh bus, same database. While
	// the entry judgment is still armed the restart tick keeps the pause
	// and owes nothing.
	second := newBPSuppressionHarness(t, pool)
	entered, cleared, err := second.svc.TickAgentBackpressureSuppression(context.Background())
	if err != nil {
		t.Fatalf("restart tick: %v", err)
	}
	if entered != 0 || cleared != 0 {
		t.Fatalf("restart tick (still armed) transitions: entered=%d cleared=%d, want 0/0", entered, cleared)
	}
	if len(second.paused) != 0 || len(second.resumed) != 0 {
		t.Fatalf("restart tick (still armed) broadcasts: paused=%d resumed=%d, want 0/0", len(second.paused), len(second.resumed))
	}

	// The windows slide: the restart process must recompute both from the
	// database and still owe exactly one release transition.
	deleteBPTerminalSamples(t, pool, agentID, string(taskfailure.ReasonAgentProviderCapacityOrRateLimit))
	entered, cleared, err = second.svc.TickAgentBackpressureSuppression(context.Background())
	if err != nil {
		t.Fatalf("restart recovery tick: %v", err)
	}
	if entered != 0 || cleared != 1 {
		t.Fatalf("restart recovery transitions: entered=%d cleared=%d, want 0/1", entered, cleared)
	}
	if isSystemErrorRatePause(t, pool, workspaceID, agentID) {
		t.Fatalf("restart process: system row must be released")
	}
	if len(second.resumed) != 1 {
		t.Fatalf("restart process resumed broadcasts: got %d, want 1", len(second.resumed))
	}
}
