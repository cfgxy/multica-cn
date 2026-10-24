package main

// RUYI-859 stage B: the loop entry point (tick + disabled path) over the
// integration fixture. The judgment and transition semantics live in the
// service package's DB-gated lifecycle tests; here we pin that one tick of
// the wired-in tick function performs the real transition, and that a
// non-positive interval disables the loop without touching the database.

import (
	"context"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedSuppressionFixture arms one backpressure policy on the fixture agent
// and plants the terminal samples the entry judgment reads (7 succeeded +
// 3 rate-limit failed = 10, 30.0% — exactly at the 30% threshold with the
// 5-sample floor met). Cleanup is explicit so the shared integration
// workspace stays clean for the other tests in this binary.
func seedSuppressionFixture(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	agentID := pickFixtureAgent(t)

	var runtimeID string
	if err := testPool.QueryRow(ctx,
		`SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("load fixture agent runtime: %v", err)
	}

	queries := db.New(testPool)
	ownerID := parseUUID(testUserID)
	if _, err := queries.UpsertAgentBackpressurePolicy(ctx, db.UpsertAgentBackpressurePolicyParams{
		WorkspaceID:            parseUUID(testWorkspaceID),
		AgentID:                agentID,
		WindowMinutes:          60,
		ErrorRateThresholdPct:  30,
		MinSamples:             5,
		RecoveryWindowMinutes:  30,
		SuppressOnErrorRate:    true,
		CountModelApiFailures:  true,
		CountExecutionFailures: true,
		UpdatedBy:              ownerID,
	}); err != nil {
		t.Fatalf("seed armed policy: %v", err)
	}

	for i := 0; i < 7; i++ {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, completed_at)
			VALUES ($1, $2, 'completed', 0, now())
		`, agentID, runtimeID); err != nil {
			t.Fatalf("seed succeeded sample: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, failure_reason, completed_at)
			VALUES ($1, $2, 'failed', 0, 'agent_error.provider_capacity_or_rate_limit', now())
		`, agentID, runtimeID); err != nil {
			t.Fatalf("seed failed sample: %v", err)
		}
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		testPool.Exec(cleanupCtx, `DELETE FROM agent_task_queue WHERE agent_id = $1`, agentID)
		testPool.Exec(cleanupCtx, `DELETE FROM agent_suppression_event WHERE agent_id = $1`, agentID)
		testPool.Exec(cleanupCtx, `DELETE FROM agent_backpressure_policy WHERE agent_id = $1`, agentID)
		testPool.Exec(cleanupCtx,
			`DELETE FROM scheduling_pause WHERE workspace_id = $1 AND agent_id = $2`, testWorkspaceID, agentID)
		testPool.Exec(cleanupCtx,
			`DELETE FROM audit_event WHERE workspace_id = $1 AND event_type IN ('agent.scheduling_paused','agent.scheduling_resumed')`,
			testWorkspaceID)
	})
}

// One tick of the wired-in entry point performs the full transition: the
// system source pause row, the system-actor audit row and the entered
// suppression-event snapshot, owed exactly once.
func TestAgentErrorRateSuppressor_TickEntersArmedAgent(t *testing.T) {
	seedSuppressionFixture(t)
	ctx := context.Background()
	bus := events.New()
	taskSvc := service.NewTaskService(db.New(testPool), testPool, nil, bus)

	agentID := pickFixtureAgent(t)
	tickAgentErrorRateSuppressor(ctx, taskSvc)

	var pauseReason string
	if err := testPool.QueryRow(ctx,
		`SELECT reason FROM scheduling_pause WHERE workspace_id = $1 AND agent_id = $2`,
		testWorkspaceID, agentID).Scan(&pauseReason); err != nil || pauseReason != "error_rate_suppression" {
		t.Fatalf("after one tick: pause reason=%q err=%v, want the system source row", pauseReason, err)
	}

	var enteredEvents int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_suppression_event WHERE agent_id = $1 AND event_type = 'entered'`,
		agentID).Scan(&enteredEvents); err != nil {
		t.Fatalf("count entered events: %v", err)
	}
	if enteredEvents != 1 {
		t.Fatalf("entered suppression events: got %d, want 1", enteredEvents)
	}

	var pausedAudit int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM audit_event WHERE workspace_id = $1 AND agent_id = $2 AND event_type = 'agent.scheduling_paused' AND actor_type = 'system'`,
		testWorkspaceID, agentID).Scan(&pausedAudit); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if pausedAudit != 1 {
		t.Fatalf("system-actor audit paused rows: got %d, want 1", pausedAudit)
	}

	// The tick entry point is idempotent over the armed state: a second
	// sweep owes nothing further.
	tickAgentErrorRateSuppressor(ctx, taskSvc)
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM audit_event WHERE workspace_id = $1 AND agent_id = $2 AND event_type = 'agent.scheduling_paused'`,
		testWorkspaceID, agentID).Scan(&pausedAudit); err != nil {
		t.Fatalf("recount audit rows: %v", err)
	}
	if pausedAudit != 1 {
		t.Fatalf("audit paused rows after second tick: got %d, want still 1 (no transition)", pausedAudit)
	}
}

// A non-positive interval disables the loop: the runner returns without
// running a tick, and a cancelled context is honored on the startup-delay
// path.
func TestAgentErrorRateSuppressor_DisabledIntervalRunsNoTick(t *testing.T) {
	seedSuppressionFixture(t)
	agentID := pickFixtureAgent(t)
	bus := events.New()
	taskSvc := service.NewTaskService(db.New(testPool), testPool, nil, bus)

	done := make(chan struct{})
	go func() {
		runAgentErrorRateSuppressor(context.Background(), taskSvc, agentSuppressionConfig{Interval: 0})
		close(done)
	}()
	select {
	case <-done:
		// returned immediately: the disabled path owes nothing
	case <-time.After(2 * time.Second):
		t.Fatalf("disabled loop did not return: the interval gate is not honored")
	}

	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM scheduling_pause WHERE workspace_id = $1 AND agent_id = $2`,
		testWorkspaceID, agentID).Scan(&n); err != nil {
		t.Fatalf("count pause rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("disabled loop wrote pause rows: got %d, want 0", n)
	}
}

// The startup delay path honors cancellation before the first tick: a
// cancelled context means no tick ran and no transition is owed.
func TestAgentErrorRateSuppressor_StartupDelayCancellable(t *testing.T) {
	bus := events.New()
	taskSvc := service.NewTaskService(db.New(testPool), testPool, nil, bus)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the runner starts: the delay select must observe it

	done := make(chan struct{})
	go func() {
		runAgentErrorRateSuppressor(ctx, taskSvc, agentSuppressionConfig{Interval: time.Hour, StartupDelay: time.Hour})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("cancelled startup delay did not return")
	}
}
