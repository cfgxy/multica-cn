package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestSchedulingPauseScopeOf pins the scope derivation: the workspace-level
// row (agent_id NULL) reads back as "workspace", the agent-level row as
// "agent", and any future row shape that carries neither stays empty rather
// than lying about a scope.
func TestSchedulingPauseScopeOf(t *testing.T) {
	ws := util.MustParseUUID("11111111-1111-1111-1111-111111111111")
	cases := []struct {
		name string
		row  db.SchedulingPause
		want string
	}{
		{
			// pgtype.UUID zero value carries Valid=false, which the generated
			// code maps to SQL NULL — exactly the workspace-level row shape.
			name: "workspace level row",
			row:  db.SchedulingPause{WorkspaceID: ws, AgentID: pgtype.UUID{}},
			want: SchedulingPauseScopeWorkspace,
		},
		{
			name: "agent level row",
			row:  db.SchedulingPause{WorkspaceID: ws, AgentID: util.MustParseUUID("22222222-2222-2222-2222-222222222222")},
			want: SchedulingPauseScopeAgent,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := schedulingPauseScopeOf(tc.row); got != tc.want {
				t.Fatalf("schedulingPauseScopeOf() = %q; want %q", got, tc.want)
			}
		})
	}
}

// createSchedulingPauseFixture builds a workspace + runtime + agent with ONE
// queued task: the minimum shape a claim-fence test needs. Returns the ids
// the freeze tests act on; rows are removed in reverse dependency order on
// cleanup (no FKs by repo rule, so order is a courtesy for future readers).
func createSchedulingPauseFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (workspaceID, agentID, runtimeID, userID string) {
	t.Helper()

	suffix := time.Now().UnixNano()
	email := fmt.Sprintf("sched-pause-%d@multica.ai", suffix)
	slug := fmt.Sprintf("sched-pause-%d", suffix)

	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Scheduling Pause Test", email).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, 'temporary scheduling pause test workspace', 'SPT')
		RETURNING id
	`, "Scheduling Pause Test", slug).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		t.Fatalf("create member: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider,
			status, device_info, metadata, last_seen_at, visibility, owner_id
		)
		VALUES ($1, NULL, $2, 'cloud', 'scheduling_pause_test', 'online', 'test runtime', '{}'::jsonb, now(), 'private', $3)
		RETURNING id
	`, workspaceID, "Scheduling Pause Runtime", userID).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4)
		RETURNING id
	`, workspaceID, "Scheduling Pause Agent", runtimeID, userID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'scheduling pause issue', 'in_progress', 'none', $2, 'member', $3, 0)
		RETURNING id
	`, workspaceID, userID, 910000+suffix%100000).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, issue_id, status, priority, context, runtime_id)
		VALUES ($1, $2, 'queued', 0, '{}'::jsonb, $3)
	`, agentID, issueID, runtimeID); err != nil {
		t.Fatalf("create queued task: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		pool.Exec(cleanupCtx, `DELETE FROM agent_task_queue WHERE agent_id = $1`, agentID)
		pool.Exec(cleanupCtx, `DELETE FROM audit_event WHERE workspace_id = $1`, workspaceID)
		pool.Exec(cleanupCtx, `DELETE FROM scheduling_pause WHERE workspace_id = $1`, workspaceID)
		pool.Exec(cleanupCtx, `DELETE FROM issue WHERE workspace_id = $1`, workspaceID)
		pool.Exec(cleanupCtx, `DELETE FROM agent WHERE id = $1`, agentID)
		pool.Exec(cleanupCtx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		pool.Exec(cleanupCtx, `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID)
		pool.Exec(cleanupCtx, `DELETE FROM workspace WHERE id = $1`, workspaceID)
		pool.Exec(cleanupCtx, `DELETE FROM "user" WHERE id = $1`, userID)
	})

	return workspaceID, agentID, runtimeID, userID
}

func newSchedulingPauseService(t *testing.T, pool *pgxpool.Pool) *TaskService {
	t.Helper()
	return NewTaskService(db.New(pool), pool, nil, events.New())
}

// TestSchedulingPauseBlocksClaim is the core fence contract: a frozen agent
// claims nothing, the queued row stays put, and resume lets the very next
// claim take it in created_at order.
func TestSchedulingPauseBlocksClaim(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, _, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	wsUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)

	svc := newSchedulingPauseService(t, pool)

	// Baseline: unfrozen, the claim succeeds.
	task, err := svc.ClaimTask(ctx, agentUUID)
	if err != nil || task == nil {
		t.Fatalf("baseline claim: task=%v err=%v", task, err)
	}
	// Give it back: re-queue so the frozen half of the test has work.
	if _, err := pool.Exec(ctx, `
		UPDATE agent_task_queue SET status='queued', dispatched_at=NULL, started_at=NULL
		WHERE id = $1
	`, util.UUIDToString(task.ID)); err != nil {
		t.Fatalf("requeue: %v", err)
	}

	// Freeze.
	if _, _, err := svc.PauseAgentScheduling(ctx, wsUUID, agentUUID, "incident drill", ownerUUID); err != nil {
		t.Fatalf("pause: %v", err)
	}

	frozen, err := svc.ClaimTask(ctx, agentUUID)
	if err != nil {
		t.Fatalf("claim while frozen returned error (fence must answer with nil, not err): %v", err)
	}
	if frozen != nil {
		t.Fatalf("frozen agent claimed task %s — fence failed", util.UUIDToString(frozen.ID))
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM agent_task_queue WHERE id = $1`, util.UUIDToString(task.ID)).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "queued" {
		t.Fatalf("frozen task status = %q; want queued (pause must never touch task rows)", status)
	}

	// Resume; the next claim takes the task again.
	if _, err := svc.ResumeAgentScheduling(ctx, wsUUID, agentUUID, ownerUUID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	resumed, err := svc.ClaimTask(ctx, agentUUID)
	if err != nil {
		t.Fatalf("claim after resume: %v", err)
	}
	if resumed == nil {
		t.Fatal("claim after resume returned nil — queue did not flow")
	}
}

// TestSchedulingPauseWorkspaceScopeAndResumeSemantics covers the stacked
// freeze: workspace-level freeze blocks the agent even without an
// agent-level row, an AGENT-level resume cannot lift it, and only the
// workspace resume lets the queue flow again.
func TestSchedulingPauseWorkspaceScopeAndResumeSemantics(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, _, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	wsUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)

	svc := newSchedulingPauseService(t, pool)

	if _, _, err := svc.PauseWorkspaceScheduling(ctx, wsUUID, "org-wide drain", ownerUUID); err != nil {
		t.Fatalf("workspace pause: %v", err)
	}

	blocked, err := svc.ClaimTask(ctx, agentUUID)
	if err != nil || blocked != nil {
		t.Fatalf("workspace-frozen claim: task=%v err=%v; want nil/nil", blocked, err)
	}

	// The scope read must say which freeze is in effect.
	state, err := svc.AgentSchedulingState(ctx, wsUUID, agentUUID)
	if err != nil {
		t.Fatalf("agent state: %v", err)
	}
	if !state.Paused || state.Scope != SchedulingPauseScopeWorkspace {
		t.Fatalf("agent state = %+v; want paused with workspace scope", state)
	}

	// Agent-level resume is a no-op against a workspace freeze...
	if _, err := svc.ResumeAgentScheduling(ctx, wsUUID, agentUUID, ownerUUID); err != nil {
		t.Fatalf("agent resume under workspace freeze: %v", err)
	}
	stillBlocked, err := svc.ClaimTask(ctx, agentUUID)
	if err != nil || stillBlocked != nil {
		t.Fatalf("claim after agent-only resume: task=%v err=%v; want nil/nil", stillBlocked, err)
	}

	// ...and only the workspace resume lifts it.
	if _, err := svc.ResumeWorkspaceScheduling(ctx, wsUUID, ownerUUID); err != nil {
		t.Fatalf("workspace resume: %v", err)
	}
	flowed, err := svc.ClaimTask(ctx, agentUUID)
	if err != nil {
		t.Fatalf("claim after workspace resume: %v", err)
	}
	if flowed == nil {
		t.Fatal("claim after workspace resume returned nil — queue did not flow")
	}
}

// TestSchedulingPauseIdempotentKeepsOriginalOperator pins the upsert
// semantics: a second freeze does not create a second row and does not
// steal the first operator's attribution.
func TestSchedulingPauseIdempotentKeepsOriginalOperator(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, _, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	wsUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)

	svc := newSchedulingPauseService(t, pool)

	first, _, err := svc.PauseAgentScheduling(ctx, wsUUID, agentUUID, "first reason", ownerUUID)
	if err != nil {
		t.Fatalf("first pause: %v", err)
	}
	second, _, err := svc.PauseAgentScheduling(ctx, wsUUID, agentUUID, "second reason", ownerUUID)
	if err != nil {
		t.Fatalf("second pause: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("second pause created a new row %s (first %s) — upsert must be idempotent", second.ID, first.ID)
	}
	if second.Reason != "first reason" {
		t.Fatalf("second pause overwrote reason to %q — original operator attribution must be preserved", second.Reason)
	}

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM scheduling_pause WHERE workspace_id=$1 AND agent_id=$2`,
		workspaceID, agentID).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("scheduling_pause rows = %d; want 1", rows)
	}
}

// TestSchedulingPauseAuditTrail checks the fail-closed audit pair: pause and
// resume each leave exactly one event in the agent domain naming the acting
// member, and both events share the pause's transaction semantics.
func TestSchedulingPauseAuditTrail(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, _, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	wsUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)

	svc := newSchedulingPauseService(t, pool)

	if _, _, err := svc.PauseAgentScheduling(ctx, wsUUID, agentUUID, "audit drill", ownerUUID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := svc.ResumeAgentScheduling(ctx, wsUUID, agentUUID, ownerUUID); err != nil {
		t.Fatalf("resume: %v", err)
	}

	rows := struct{ paused, resumed int }{}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event_type='agent.scheduling_paused'),
		       count(*) FILTER (WHERE event_type='agent.scheduling_resumed')
		FROM audit_event WHERE workspace_id = $1
	`, workspaceID).Scan(&rows.paused, &rows.resumed); err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	if rows.paused != 1 || rows.resumed != 1 {
		t.Fatalf("audit rows paused=%d resumed=%d; want 1/1", rows.paused, rows.resumed)
	}
}

// TestResumeAgentSchedulingBumpsEmptyClaimCache is the resume-consumes-on-
// first-poll guarantee: a runtime whose empty-claim verdict is cached must
// see its version bumped by resume, so the cached verdict no longer matches
// and the next poll goes to Postgres instead of waiting out the TTL.
func TestResumeAgentSchedulingBumpsEmptyClaimCache(t *testing.T) {
	rdb := newRedisTestClient(t)
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, runtimeID, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	wsUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)

	cache := NewEmptyClaimCache(rdb)
	if cache == nil {
		t.Fatal("NewEmptyClaimCache returned nil for a live client")
	}
	svc := newSchedulingPauseService(t, pool)
	svc.EmptyClaim = cache

	if _, _, err := svc.PauseAgentScheduling(ctx, wsUUID, agentUUID, "cache drill", ownerUUID); err != nil {
		t.Fatalf("pause: %v", err)
	}

	// Simulate the daemon having cached an "empty" verdict before the freeze.
	if err := rdb.Set(ctx, "mul:claim:runtime:version:"+runtimeID, 7, time.Hour).Err(); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	cache.MarkEmpty(ctx, runtimeID, 7)
	before := cache.CurrentVersion(ctx, runtimeID)

	if _, err := svc.ResumeAgentScheduling(ctx, wsUUID, agentUUID, ownerUUID); err != nil {
		t.Fatalf("resume: %v", err)
	}

	after := cache.CurrentVersion(ctx, runtimeID)
	if after == before {
		t.Fatalf("empty-claim version unchanged after resume (%d) — cached verdict would stall the resumed queue for %s", before, EmptyClaimCacheTTL)
	}
	if cache.IsEmpty(ctx, runtimeID) {
		t.Fatal("empty verdict still cached after resume — next poll would skip Postgres")
	}
}

// TestDeleteWorkspaceSweepsSchedulingPause pins the application-layer sweep:
// workspace deletion removes its scheduling_pause rows (repo rule: no FK
// cascade, cleanup is explicit in the DeleteWorkspace CTE).
func TestDeleteWorkspaceSweepsSchedulingPause(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, _, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	wsUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)

	svc := newSchedulingPauseService(t, pool)
	if _, _, err := svc.PauseAgentScheduling(ctx, wsUUID, agentUUID, "sweep drill", ownerUUID); err != nil {
		t.Fatalf("pause: %v", err)
	}

	// Drive the exact query DeleteWorkspace executes (its CTE chain carries
	// the scheduling_pause sweep), not a hand-written DELETE.
	queries := db.New(pool)
	if err := queries.DeleteWorkspace(ctx, wsUUID); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM scheduling_pause WHERE workspace_id = $1`, workspaceID).Scan(&rows); err != nil {
		t.Fatalf("count after sweep: %v", err)
	}
	if rows != 0 {
		t.Fatalf("scheduling_pause rows = %d after workspace deletion; want 0 (orphaned freeze would outlive its workspace)", rows)
	}
}
