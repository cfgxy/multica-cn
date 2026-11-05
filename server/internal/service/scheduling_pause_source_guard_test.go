package service

// RUYI-859 stage B review P1-1, DB-gated regressions: the human pause/resume
// paths are source-guarded against the automatic loop's system source row
// (created_by NULL + reason 'error_rate_suppression'). Against such a row
// the human side is READ-ONLY: it neither writes nor lifts the row, and it
// owes no member-paused / member-resumed audit row and no realtime
// transition. The tests seed the system row directly — running the loop's
// own insert/delete would burn the one owed transition the tests must not
// see — and the fixture's reverse-order cleanup removes it.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// assertNoHumanBroadcasts wires a bus-capturing TaskService and asserts
// that a human pause/resume which only OBSERVED the system row owed no
// scheduling broadcasts at all (P1-1: no member audit, no realtime
// transition). It reuses the same capture shape as the stage B harness.
func assertNoHumanBroadcasts(t *testing.T, pool *pgxpool.Pool, workspaceID, agentID, ownerID string) {
	t.Helper()
	ctx := context.Background()
	bus := events.New()
	var paused, resumed int
	bus.Subscribe(protocol.EventAgentSchedulingPaused, func(events.Event) { paused++ })
	bus.Subscribe(protocol.EventAgentSchedulingResumed, func(events.Event) { resumed++ })
	svc := NewTaskService(db.New(pool), pool, nil, bus)

	wsUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)
	if _, _, err := svc.ResumeAgentScheduling(ctx, wsUUID, agentUUID, ownerUUID); err != nil {
		t.Fatalf("observed resume: %v", err)
	}
	if _, _, err := svc.PauseAgentScheduling(ctx, wsUUID, agentUUID, "observed pause", ownerUUID); err != nil {
		t.Fatalf("observed pause: %v", err)
	}
	if paused != 0 || resumed != 0 {
		t.Fatalf("scheduling broadcasts owed on observed human actions: paused=%d resumed=%d, want 0/0", paused, resumed)
	}
}

// insertSystemPause seeds the agent-level slot with the loop's system
// source row — exactly the shape EnsureAgentErrorRateSchedulingPause
// writes: created_by NULL together with the fixed reason.
func insertSystemPause(t *testing.T, pool *pgxpool.Pool, workspaceID, agentID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO scheduling_pause (workspace_id, agent_id, reason) VALUES ($1, $2, $3)`,
		workspaceID, agentID, SchedulingPauseReasonErrorRateSuppression); err != nil {
		t.Fatalf("insert system pause row: %v", err)
	}
}

// countMemberAuditTransitions counts the HUMAN-side scheduling audit rows
// (member actor) for one agent — the P1-1 invariant against a system-held
// slot is zero.
func countMemberAuditTransitions(t *testing.T, pool *pgxpool.Pool, workspaceID, agentID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM audit_event
		WHERE workspace_id = $1 AND agent_id = $2
		  AND actor_type = 'member'
		  AND event_type IN ('agent.scheduling_paused', 'agent.scheduling_resumed')`,
		workspaceID, agentID).Scan(&n); err != nil {
		t.Fatalf("count member audit rows: %v", err)
	}
	return n
}

// TestHumanPauseAgainstSystemRowIsReadOnly pins P1-1 pause side: with the
// loop's system row in the slot, PauseAgentScheduling is a no-op — the row
// is not mutated, not duplicated, and no member-paused audit is owed. The
// returned row is the OBSERVED system row.
func TestHumanPauseAgainstSystemRowIsReadOnly(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, _, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	wsUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)
	insertSystemPause(t, pool, workspaceID, agentID)

	svc := newSchedulingPauseService(t, pool)
	row, _, err := svc.PauseAgentScheduling(ctx, wsUUID, agentUUID, "human drill", ownerUUID)
	if err != nil {
		t.Fatalf("human pause against system row: %v", err)
	}
	// The response must read back the SYSTEM row — the human caller
	// observed a held slot, it did not take it.
	if row.Reason != SchedulingPauseReasonErrorRateSuppression {
		t.Fatalf("pause response reason = %q; want the observed system row's reason", row.Reason)
	}
	if row.CreatedBy.Valid {
		t.Fatal("pause response CreatedBy valid; the observed row is the system row (created_by NULL)")
	}
	if got := countPauseRows(t, pool, workspaceID, &agentID); got != 1 {
		t.Fatalf("agent-level pause rows = %d; want still 1 (a human write must not duplicate the slot)", got)
	}
	if got := countMemberAuditTransitions(t, pool, workspaceID, agentID); got != 0 {
		t.Fatalf("member audit rows owed on an observed slot: got %d, want 0", got)
	}
	if !isSystemErrorRatePause(t, pool, workspaceID, agentID) {
		t.Fatal("system source row mutated by the human pause")
	}
	assertNoHumanBroadcasts(t, pool, workspaceID, agentID, ownerID)
}

// TestHumanResumeAgainstSystemRowIsReadOnly pins P1-1 resume side: the
// human resume delete is source-guarded (created_by NOT NULL), so against a
// system-held slot it lifts nothing and owes no member-resumed audit. The
// slot keeps the system row; the post-action state reports it still paused.
func TestHumanResumeAgainstSystemRowIsReadOnly(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, _, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	wsUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)
	insertSystemPause(t, pool, workspaceID, agentID)

	svc := newSchedulingPauseService(t, pool)
	pause, _, err := svc.ResumeAgentScheduling(ctx, wsUUID, agentUUID, ownerUUID)
	if err != nil {
		t.Fatalf("human resume against system row: %v", err)
	}
	// The returned slot state is still the system row: the agent stays
	// paused and the human action faked nothing.
	if !pause.AgentID.Valid || pause.Reason != SchedulingPauseReasonErrorRateSuppression {
		t.Fatalf("resume post-state = %+v; want the still-held system row", pause)
	}
	if !isSystemErrorRatePause(t, pool, workspaceID, agentID) {
		t.Fatal("system source row lifted by a human resume: it must survive (the loop is its only releaser)")
	}
	if got := countMemberAuditTransitions(t, pool, workspaceID, agentID); got != 0 {
		t.Fatalf("member audit rows owed on an unlifted slot: got %d, want 0", got)
	}
	assertNoHumanBroadcasts(t, pool, workspaceID, agentID, ownerID)
}

// TestHumanResumeStillLiftsHumanRows pins the preserved human-source
// semantics: a HUMAN row (created_by set) is still lifted by the human
// resume with exactly one member-resumed audit owed — the guard must not
// have broken the original path.
func TestHumanResumeStillLiftsHumanRows(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, _, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	wsUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)

	svc := newSchedulingPauseService(t, pool)
	if _, _, err := svc.PauseAgentScheduling(ctx, wsUUID, agentUUID, "human drill", ownerUUID); err != nil {
		t.Fatalf("human pause: %v", err)
	}
	pause, _, err := svc.ResumeAgentScheduling(ctx, wsUUID, agentUUID, ownerUUID)
	if err != nil {
		t.Fatalf("human resume: %v", err)
	}
	if pause.AgentID.Valid {
		t.Fatalf("resume post-state = %+v; want an empty slot", pause)
	}
	if got := countPauseRows(t, pool, workspaceID, &agentID); got != 0 {
		t.Fatalf("agent-level pause rows after human resume: got %d, want 0", got)
	}
	var paused, resumed int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event_type='agent.scheduling_paused'),
		       count(*) FILTER (WHERE event_type='agent.scheduling_resumed')
		FROM audit_event
		WHERE workspace_id = $1 AND agent_id = $2 AND actor_type = 'member'`,
		workspaceID, agentID).Scan(&paused, &resumed); err != nil {
		t.Fatalf("read member audit rows: %v", err)
	}
	if paused != 1 || resumed != 1 {
		t.Fatalf("member audit rows paused=%d resumed=%d; want 1/1 (human path semantics preserved)", paused, resumed)
	}
}

// TestHumanPauseAfterSystemRowIsHeld pins the mixed-ownership read-only
// shape: a human resume first (lifts nothing, the system row holds), then
// a human pause (still writes nothing, no second row, no member audit).
func TestHumanPauseAfterSystemRowIsHeld(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, _, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	wsUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)
	insertSystemPause(t, pool, workspaceID, agentID)

	svc := newSchedulingPauseService(t, pool)
	if _, _, err := svc.ResumeAgentScheduling(ctx, wsUUID, agentUUID, ownerUUID); err != nil {
		t.Fatalf("resume against system row: %v", err)
	}
	row, _, err := svc.PauseAgentScheduling(ctx, wsUUID, agentUUID, "late human", ownerUUID)
	if err != nil {
		t.Fatalf("pause against system row: %v", err)
	}
	if row.Reason != SchedulingPauseReasonErrorRateSuppression {
		t.Fatalf("pause response reason = %q; want the observed system row", row.Reason)
	}
	if got := countPauseRows(t, pool, workspaceID, &agentID); got != 1 {
		t.Fatalf("pause rows = %d; want still 1 (no duplication)", got)
	}
	if got := countMemberAuditTransitions(t, pool, workspaceID, agentID); got != 0 {
		t.Fatalf("member audit rows after an observed resume + pause: got %d, want 0", got)
	}
}
