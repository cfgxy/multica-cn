package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// batchClaimFixture provisions two runtimes on one machine, each with its own
// agent (max_concurrent_tasks=5): agent1 (on rt1) has two queued tasks on two
// different issues; agent2 (on rt2) has one queued task. Returns the two
// runtime IDs.
func batchClaimFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (rt1, rt2 string) {
	t.Helper()
	suffix := time.Now().UnixNano()

	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1,$2) RETURNING id`,
		"Batch Claim Test", fmt.Sprintf("batch-claim-%d@multica.ai", suffix)).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	var workspaceID string
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ($1,$2,$3,$4) RETURNING id`,
		"Batch Claim Test", fmt.Sprintf("batch-claim-%d", suffix), "temp batch claim test workspace", "BCR").Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, workspaceID, userID); err != nil {
		t.Fatalf("create member: %v", err)
	}

	mkRuntime := func(name, provider string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, visibility, owner_id)
			VALUES ($1, 'daemon-batch', $2, 'cloud', $3, 'online', 'test runtime', '{}'::jsonb, now(), 'private', $4)
			RETURNING id`, workspaceID, name, provider, userID).Scan(&id); err != nil {
			t.Fatalf("create runtime %s: %v", name, err)
		}
		return id
	}
	mkAgent := func(name, runtimeID string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id)
			VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 5, $4)
			RETURNING id`, workspaceID, name, runtimeID, userID).Scan(&id); err != nil {
			t.Fatalf("create agent %s: %v", name, err)
		}
		return id
	}

	rt1 = mkRuntime("Batch RT1", "batch_provider_1")
	rt2 = mkRuntime("Batch RT2", "batch_provider_2")
	agent1 := mkAgent("Batch Agent1", rt1)
	agent2 := mkAgent("Batch Agent2", rt2)

	mkQueuedTask := func(agentID, runtimeID string, n int) {
		var issueID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
			VALUES ($1, $2, 'in_progress', 'none', $3, 'member', $4, $5)
			RETURNING id`, workspaceID, fmt.Sprintf("batch issue %d", n), userID, 800000+n, n).Scan(&issueID); err != nil {
			t.Fatalf("create issue %d: %v", n, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO agent_task_queue (agent_id, issue_id, status, priority, context, runtime_id)
			VALUES ($1, $2, 'queued', 0, '{}'::jsonb, $3)`, agentID, issueID, runtimeID); err != nil {
			t.Fatalf("create task %d: %v", n, err)
		}
		// Space out created_at so ordering is deterministic.
		time.Sleep(2 * time.Millisecond)
	}
	mkQueuedTask(agent1, rt1, 1)
	mkQueuedTask(agent1, rt1, 2)
	mkQueuedTask(agent2, rt2, 3)

	t.Cleanup(func() {
		c := context.Background()
		pool.Exec(c, `DELETE FROM agent_task_queue WHERE agent_id IN ($1,$2)`, agent1, agent2)
		pool.Exec(c, `DELETE FROM issue WHERE workspace_id = $1`, workspaceID)
		pool.Exec(c, `DELETE FROM agent WHERE id IN ($1,$2)`, agent1, agent2)
		pool.Exec(c, `DELETE FROM agent_runtime WHERE id IN ($1,$2)`, rt1, rt2)
		pool.Exec(c, `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID)
		pool.Exec(c, `DELETE FROM workspace WHERE id = $1`, workspaceID)
		pool.Exec(c, `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return rt1, rt2
}

// TestClaimTasksForRuntimes_MultiRuntimeDrain verifies the machine-level batch
// claim (MUL-4257): a single call claims across all runtimes, one task per
// agent per call (matching the singular path's dedup), routes each task to its
// runtime, respects a subsequent drain, and reports empty once nothing is
// queued.
func TestClaimTasksForRuntimes_MultiRuntimeDrain(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())

	rt1, rt2 := batchClaimFixture(t, ctx, pool)
	ids := []pgtype.UUID{util.MustParseUUID(rt1), util.MustParseUUID(rt2)}

	// Call 1: one task per agent (agent1→rt1, agent2→rt2) => 2 tasks, one per runtime.
	got1, err := svc.ClaimTasksForRuntimes(ctx, ids, 5)
	if err != nil {
		t.Fatalf("call1: %v", err)
	}
	if len(got1) != 2 {
		t.Fatalf("call1 claimed %d tasks, want 2", len(got1))
	}
	seen := map[string]int{}
	for _, task := range got1 {
		seen[util.UUIDToString(task.RuntimeID)]++
	}
	if seen[rt1] != 1 || seen[rt2] != 1 {
		t.Fatalf("call1 runtime distribution = %v, want one task each for rt1/rt2", seen)
	}

	// Call 2: agent1 still has a second queued task (different issue, capacity 5);
	// agent2 is drained => exactly 1 task, on rt1.
	got2, err := svc.ClaimTasksForRuntimes(ctx, ids, 5)
	if err != nil {
		t.Fatalf("call2: %v", err)
	}
	if len(got2) != 1 {
		t.Fatalf("call2 claimed %d tasks, want 1", len(got2))
	}
	if util.UUIDToString(got2[0].RuntimeID) != rt1 {
		t.Fatalf("call2 claimed runtime = %s, want rt1", util.UUIDToString(got2[0].RuntimeID))
	}

	// Call 3: everything dispatched => empty.
	got3, err := svc.ClaimTasksForRuntimes(ctx, ids, 5)
	if err != nil {
		t.Fatalf("call3: %v", err)
	}
	if len(got3) != 0 {
		t.Fatalf("call3 claimed %d tasks, want 0", len(got3))
	}
}

// TestClaimTasksForRuntimes_MaxTasksCap verifies max_tasks bounds the number of
// tasks returned in a single batch call.
func TestClaimTasksForRuntimes_MaxTasksCap(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())

	rt1, rt2 := batchClaimFixture(t, ctx, pool)
	ids := []pgtype.UUID{util.MustParseUUID(rt1), util.MustParseUUID(rt2)}

	got, err := svc.ClaimTasksForRuntimes(ctx, ids, 1)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("claimed %d tasks with max_tasks=1, want 1", len(got))
	}
}

// TestClaimTasksForRuntimes_AnyDueRuntimeChecksTheCompleteSet pins MUL-6486's
// collection-level reclaim gate. Per-runtime backstops can begin staggered, but
// one due member must run the single UPDATE over every runtime on that daemon;
// filtering the SQL array to only the due member both misses recoverable work
// and lets fixed query overhead drift back toward one call per poll.
func TestClaimTasksForRuntimes_AnyDueRuntimeChecksTheCompleteSet(t *testing.T) {
	ctx := context.Background()
	rdb := newRedisTestClient(t)
	pool := newTaskClaimRacePool(t)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	svc.ReclaimCheck = NewReclaimCheckCache(rdb)

	rt1, rt2 := batchClaimFixture(t, ctx, pool)
	ids := []pgtype.UUID{util.MustParseUUID(rt1), util.MustParseUUID(rt2)}

	var staleTaskID string
	if err := pool.QueryRow(ctx, `
		SELECT id FROM agent_task_queue
		WHERE runtime_id = $1
		ORDER BY created_at
		LIMIT 1
	`, rt2).Scan(&staleTaskID); err != nil {
		t.Fatalf("load rt2 task: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'dispatched', dispatched_at = now() - interval '10 minutes',
		    started_at = NULL, prepare_lease_expires_at = NULL
		WHERE id = $1
	`, staleTaskID); err != nil {
		t.Fatalf("make rt2 task stale: %v", err)
	}

	now := time.Now()
	svc.ReclaimCheck.MarkChecked(
		ctx,
		[]string{rt1},
		now.Add(-ReclaimCheckBackstopInterval-time.Second),
		now.Add(ReclaimCheckRetryInterval),
	)
	svc.ReclaimCheck.MarkChecked(ctx, []string{rt2}, now, now.Add(ReclaimCheckRetryInterval))
	due := svc.ReclaimCheck.DueRuntimeIDs(ctx, []string{rt1, rt2}, now)
	if len(due) != 1 || due[0] != rt1 {
		t.Fatalf("precondition due runtimes = %v, want [%s]", due, rt1)
	}

	claimed, err := svc.ClaimTasksForRuntimes(ctx, ids, 5)
	if err != nil {
		t.Fatalf("batch claim: %v", err)
	}
	for _, task := range claimed {
		if util.UUIDToString(task.ID) == staleTaskID {
			return
		}
	}
	t.Fatalf("batch claim omitted stale task %s on non-due runtime %s: got %v", staleTaskID, rt2, claimed)
}

// TestClaimTasksForRuntimes_EmptyReclaimDefersHintForRetry covers the recovery
// hole where the task hint was previously made inert after one empty UPDATE.
// A temporarily stale runtime heartbeat is intentionally correlated with a
// lost claim response: the task remains dispatched, and its hint must become
// due again after the short retry rather than waiting for the 90-second
// backstop.
func TestClaimTasksForRuntimes_EmptyReclaimDefersHintForRetry(t *testing.T) {
	ctx := context.Background()
	rdb := newRedisTestClient(t)
	pool := newTaskClaimRacePool(t)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	svc.ReclaimCheck = NewReclaimCheckCache(rdb)

	rt1, _ := batchClaimFixture(t, ctx, pool)
	runtimeID := util.MustParseUUID(rt1)
	var taskID string
	if err := pool.QueryRow(ctx, `
		SELECT id FROM agent_task_queue
		WHERE runtime_id = $1
		ORDER BY created_at
		LIMIT 1
	`, rt1).Scan(&taskID); err != nil {
		t.Fatalf("load task: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'dispatched', dispatched_at = now() - interval '10 minutes',
		    started_at = NULL, prepare_lease_expires_at = NULL
		WHERE id = $1
	`, taskID); err != nil {
		t.Fatalf("make task stale: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE agent_runtime
		SET status = 'online', last_seen_at = now() - interval '10 minutes'
		WHERE id = $1
	`, rt1); err != nil {
		t.Fatalf("make runtime temporarily ineligible: %v", err)
	}

	now := time.Now()
	svc.ReclaimCheck.MarkChecked(ctx, []string{rt1}, now, now.Add(ReclaimCheckRetryInterval))
	svc.ReclaimCheck.Track(ctx, rt1, taskID, now.Add(-time.Second))
	if due := svc.ReclaimCheck.DueRuntimeIDs(ctx, []string{rt1}, now); len(due) != 1 {
		t.Fatalf("precondition due runtimes = %v, want [%s]", due, rt1)
	}

	if claimed, err := svc.ClaimTasksForRuntimes(ctx, []pgtype.UUID{runtimeID}, 5); err != nil {
		t.Fatalf("batch claim: %v", err)
	} else if len(claimed) != 0 {
		t.Fatalf("claimed tasks with stale runtime heartbeat: %v", claimed)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read task after empty reclaim: %v", err)
	}
	if status != "dispatched" {
		t.Fatalf("task status after empty reclaim = %q, want dispatched", status)
	}

	score, err := rdb.ZScore(ctx, reclaimCheckScheduleKey(rt1), taskID).Result()
	if err != nil {
		t.Fatalf("read deferred hint: %v", err)
	}
	retryAt := time.UnixMilli(int64(score))
	if retryAt.Before(now.Add(ReclaimCheckRetryInterval)) || retryAt.After(time.Now().Add(ReclaimCheckRetryInterval+time.Second)) {
		t.Fatalf("retry hint = %v, want approximately one retry interval after the check", retryAt)
	}
	if due := svc.ReclaimCheck.DueRuntimeIDs(ctx, []string{rt1}, retryAt.Add(-time.Millisecond)); len(due) != 0 {
		t.Fatalf("hint retriggered before retry deadline: %v", due)
	}
	if due := svc.ReclaimCheck.DueRuntimeIDs(ctx, []string{rt1}, retryAt.Add(time.Millisecond)); len(due) != 1 {
		t.Fatalf("hint did not retrigger after empty reclaim: %v", due)
	}
}

// TestClaimTasksForRuntimes_EmptyInputs guards the trivial short-circuits.
func TestClaimTasksForRuntimes_EmptyInputs(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())

	if got, err := svc.ClaimTasksForRuntimes(ctx, nil, 5); err != nil || got != nil {
		t.Fatalf("nil runtimes: got=%v err=%v, want nil,nil", got, err)
	}
	rt1, _ := batchClaimFixture(t, ctx, pool)
	if got, err := svc.ClaimTasksForRuntimes(ctx, []pgtype.UUID{util.MustParseUUID(rt1)}, 0); err != nil || got != nil {
		t.Fatalf("maxTasks=0: got=%v err=%v, want nil,nil", got, err)
	}
}

func TestClaimTasksForRuntimes_ModelCallBudgetLeavesResidue(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	fixture := newRuntimeModelCallBudgetClaimFixture(t, pool, 1)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())

	claimed, err := svc.ClaimTasksForRuntimes(ctx, []pgtype.UUID{util.MustParseUUID(fixture.runtimeID)}, 2)
	if err != nil {
		t.Fatalf("batch claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("batch claimed %d tasks with one remaining model-call slot, want 1", len(claimed))
	}
	var dispatched, queued int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'dispatched'), count(*) FILTER (WHERE status = 'queued')
		FROM agent_task_queue
		WHERE id = ANY($1::uuid[])
	`, []string{fixture.taskA, fixture.taskB}).Scan(&dispatched, &queued); err != nil {
		t.Fatalf("count task states: %v", err)
	}
	if dispatched != 1 || queued != 1 {
		t.Fatalf("task states after batch claim: dispatched=%d queued=%d, want 1 and 1", dispatched, queued)
	}
}

func TestClaimTaskAndBatchShareRuntimeModelCallBudget(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	fixture := newRuntimeModelCallBudgetClaimFixture(t, pool, 1)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	for _, agentID := range []string{fixture.agentA, fixture.agentB} {
		triggerName := fmt.Sprintf("claim_budget_batch_sleep_%d", time.Now().UnixNano())
		createSleepTrigger(t, ctx, pool, triggerName, triggerName+"_fn", agentID)
	}

	start := make(chan struct{})
	singleResult := make(chan *db.AgentTaskQueue, 1)
	batchResult := make(chan []db.AgentTaskQueue, 1)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		task, err := svc.ClaimTask(ctx, util.MustParseUUID(fixture.agentA))
		if err != nil {
			errs <- err
			return
		}
		singleResult <- task
	}()
	go func() {
		defer wg.Done()
		<-start
		tasks, err := svc.ClaimTasksForRuntimes(ctx, []pgtype.UUID{util.MustParseUUID(fixture.runtimeID)}, 2)
		if err != nil {
			errs <- err
			return
		}
		batchResult <- tasks
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent claim: %v", err)
	}
	single := <-singleResult
	batch := <-batchResult
	claimed := len(batch)
	if single != nil {
		claimed++
	}
	if claimed != 1 {
		t.Fatalf("single and batch claimed %d tasks with one remaining model-call slot, want 1", claimed)
	}
	var dispatched, queued int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'dispatched'), count(*) FILTER (WHERE status = 'queued')
		FROM agent_task_queue
		WHERE id = ANY($1::uuid[])
	`, []string{fixture.taskA, fixture.taskB}).Scan(&dispatched, &queued); err != nil {
		t.Fatalf("count task states: %v", err)
	}
	if dispatched != 1 || queued != 1 {
		t.Fatalf("task states after concurrent single and batch claims: dispatched=%d queued=%d, want 1 and 1", dispatched, queued)
	}
}

func TestClaimTasksForRuntimes_ModelCallBudgetCountsStaleReclaimReservation(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	fixture := newRuntimeModelCallBudgetClaimFixture(t, pool, 1)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())

	if _, err := pool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'dispatched',
		    dispatched_at = now() - interval '10 minutes',
		    started_at = NULL,
		    prepare_lease_expires_at = NULL
		WHERE id = $1
	`, fixture.taskA); err != nil {
		t.Fatalf("make task stale-dispatched: %v", err)
	}

	claimed, err := svc.ClaimTasksForRuntimes(ctx, []pgtype.UUID{util.MustParseUUID(fixture.runtimeID)}, 2)
	if err != nil {
		t.Fatalf("batch claim: %v", err)
	}
	if len(claimed) != 1 || util.UUIDToString(claimed[0].ID) != fixture.taskA {
		t.Fatalf("reclaimed tasks = %v, want only stale task %s", claimed, fixture.taskA)
	}
	var dispatched, queued int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'dispatched'), count(*) FILTER (WHERE status = 'queued')
		FROM agent_task_queue
		WHERE id = ANY($1::uuid[])
	`, []string{fixture.taskA, fixture.taskB}).Scan(&dispatched, &queued); err != nil {
		t.Fatalf("count task states: %v", err)
	}
	if dispatched != 1 || queued != 1 {
		t.Fatalf("task states after stale reclaim: dispatched=%d queued=%d, want 1 and 1", dispatched, queued)
	}
}

func TestClaimTasksForRuntimes_ModelCallBudgetRejectsExpiredStaleReservation(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	fixture := newRuntimeModelCallBudgetClaimFixture(t, pool, 1)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())

	if _, err := pool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'dispatched',
		    dispatched_at = now() - interval '2 hours',
		    started_at = NULL,
		    prepare_lease_expires_at = NULL
		WHERE id = $1
	`, fixture.taskA); err != nil {
		t.Fatalf("make task stale-dispatched outside the budget window: %v", err)
	}
	fixture.fx.Insert(t, "task_usage", testutil.Cols{
		"task_id":          fixture.taskB,
		"provider":         "budget-test",
		"model":            "budget-exhausted",
		"model_call_count": int64(1),
	})
	fixture.fx.InsertNoID(t, "task_usage_model_call_evidence", testutil.Cols{
		"task_id":     fixture.taskB,
		"provider":    "budget-test",
		"model":       "budget-exhausted",
		"evidence_id": "budget-exhausted-1",
	}, "task_id = $1 AND provider = $2 AND model = $3 AND evidence_id = $4", fixture.taskB, "budget-test", "budget-exhausted", "budget-exhausted-1")

	claimed, err := svc.ClaimTasksForRuntimes(ctx, []pgtype.UUID{util.MustParseUUID(fixture.runtimeID)}, 2)
	if err != nil {
		t.Fatalf("batch claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %v with an exhausted model-call budget, want none", claimed)
	}
	var taskAStatus, taskBStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, fixture.taskA).Scan(&taskAStatus); err != nil {
		t.Fatalf("read stale task status: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, fixture.taskB).Scan(&taskBStatus); err != nil {
		t.Fatalf("read queued task status: %v", err)
	}
	if taskAStatus != "dispatched" || taskBStatus != "queued" {
		t.Fatalf("task states after exhausted stale reclaim: taskA=%s taskB=%s, want dispatched and queued", taskAStatus, taskBStatus)
	}
}

func TestClaimTasksForRuntimes_ModelCallBudgetSkipsRejectedStaleCandidates(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	fixture := newRuntimeModelCallBudgetClaimFixture(t, pool, 1)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())

	if _, err := pool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'dispatched',
		    priority = CASE WHEN id = $1 THEN 10 ELSE 0 END,
		    dispatched_at = CASE
		        WHEN id = $1 THEN now() - interval '2 hours'
		        ELSE now() - interval '10 minutes'
		    END,
		    started_at = NULL,
		    prepare_lease_expires_at = NULL
		WHERE id = ANY($2::uuid[])
	`, fixture.taskA, []string{fixture.taskA, fixture.taskB}); err != nil {
		t.Fatalf("create rejected and retained stale candidates: %v", err)
	}
	fixture.fx.Insert(t, "task_usage", testutil.Cols{
		"task_id":          fixture.taskB,
		"provider":         "budget-test",
		"model":            "retained-stale-candidate",
		"model_call_count": int64(1),
	})
	fixture.fx.InsertNoID(t, "task_usage_model_call_evidence", testutil.Cols{
		"task_id":     fixture.taskB,
		"provider":    "budget-test",
		"model":       "retained-stale-candidate",
		"evidence_id": "retained-stale-candidate-1",
	}, "task_id = $1 AND provider = $2 AND model = $3 AND evidence_id = $4", fixture.taskB, "budget-test", "retained-stale-candidate", "retained-stale-candidate-1")

	claimed, err := svc.ClaimTasksForRuntimes(ctx, []pgtype.UUID{util.MustParseUUID(fixture.runtimeID)}, 1)
	if err != nil {
		t.Fatalf("batch claim: %v", err)
	}
	if len(claimed) != 1 || util.UUIDToString(claimed[0].ID) != fixture.taskB {
		t.Fatalf("reclaimed tasks = %v, want retained candidate %s", claimed, fixture.taskB)
	}
}

func TestClaimTaskAndStaleReclaimShareRuntimeModelCallBudget(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	fixture := newRuntimeModelCallBudgetClaimFixture(t, pool, 1)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())

	if _, err := pool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'dispatched',
		    dispatched_at = now() - interval '2 hours',
		    started_at = NULL,
		    prepare_lease_expires_at = NULL
		WHERE id = $1
	`, fixture.taskA); err != nil {
		t.Fatalf("make task stale-dispatched outside the budget window: %v", err)
	}
	triggerName := fmt.Sprintf("claim_budget_stale_reclaim_sleep_%d", time.Now().UnixNano())
	createSleepTrigger(t, ctx, pool, triggerName, triggerName+"_fn", fixture.agentB)

	start := make(chan struct{})
	freshResult := make(chan *db.AgentTaskQueue, 1)
	reclaimResult := make(chan []db.AgentTaskQueue, 1)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		task, err := svc.ClaimTask(ctx, util.MustParseUUID(fixture.agentB))
		if err != nil {
			errs <- err
			return
		}
		freshResult <- task
	}()
	go func() {
		defer wg.Done()
		<-start
		tasks, err := svc.ClaimTasksForRuntimes(ctx, []pgtype.UUID{util.MustParseUUID(fixture.runtimeID)}, 1)
		if err != nil {
			errs <- err
			return
		}
		reclaimResult <- tasks
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent fresh claim and stale reclaim: %v", err)
	}

	claimed := len(<-reclaimResult)
	if <-freshResult != nil {
		claimed++
	}
	if claimed != 1 {
		t.Fatalf("fresh claim and expired stale reclaim claimed %d tasks with one remaining model-call slot, want 1", claimed)
	}
	var dispatched, queued int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'dispatched'), count(*) FILTER (WHERE status = 'queued')
		FROM agent_task_queue
		WHERE id = ANY($1::uuid[])
	`, []string{fixture.taskA, fixture.taskB}).Scan(&dispatched, &queued); err != nil {
		t.Fatalf("count task states: %v", err)
	}
	if dispatched != 1 || queued != 1 {
		t.Fatalf("task states after concurrent fresh claim and stale reclaim: dispatched=%d queued=%d, want 1 and 1", dispatched, queued)
	}
}
