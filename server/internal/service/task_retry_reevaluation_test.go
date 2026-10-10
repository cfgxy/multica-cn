package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestFailedTaskRetryReevaluationRebuildsAfterTheSuccessorEnds locks in the
// RUYI-579 W3 contract end to end: a retryable failure whose pending slot is
// held by a successor must not be dropped silently — the decision arms a
// future re-evaluation on the failed parent, the sweeper rebuilds the retry
// once the marker is due and the successor is gone, a future re-arm survives
// the expiry-guarded clear, and an exhausted budget retires the marker
// instead of retrying forever.
func TestFailedTaskRetryReevaluationRebuildsAfterTheSuccessorEnds(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	_, _, agentID, issueID := seedAttributionFixture(t, pool)
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}

	var runtimeID string
	if err := pool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("read agent runtime: %v", err)
	}

	// The parent: a delivery-guard refusal on its first attempt, budget left.
	var parentID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, attempt, max_attempts, failure_reason, session_id, work_dir, channel_context_revision)
		VALUES ($1, $2, $3, 'failed', 0, 1, 2, 'delivery_guard', 'src-session', '/tmp/src-workdir', 7)
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&parentID); err != nil {
		t.Fatalf("insert parent task: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE parent_task_id = $1 OR id = $1`, parentID)
	})

	// A successor holds the pending slot: the immediate retry is impossible.
	var successorID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, attempt, max_attempts, session_id, work_dir, channel_context_revision)
		VALUES ($1, $2, $3, 'queued', 0, 1, 2, 'src-session', '/tmp/src-workdir', 7)
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&successorID); err != nil {
		t.Fatalf("insert successor task: %v", err)
	}

	parent, err := q.GetAgentTask(ctx, parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := svc.MaybeRetryFailedTask(ctx, parent)
	if err != nil {
		t.Fatalf("MaybeRetryFailedTask: %v", err)
	}
	if child != nil {
		t.Fatal("a retry was created while a successor held the pending slot")
	}
	var armedAt pgtype.Timestamptz
	if err := pool.QueryRow(ctx, `SELECT fire_at FROM agent_task_queue WHERE id = $1`, parentID).Scan(&armedAt); err != nil {
		t.Fatalf("read armed marker: %v", err)
	}
	if !armedAt.Valid || !armedAt.Time.After(time.Now()) {
		t.Fatalf("re-evaluation marker = %v, want a future fire_at", armedAt)
	}

	// The successor reaches a terminal state; time passes; the marker comes
	// due. The sweep must rebuild the retry and retire the marker in one go.
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status = 'completed' WHERE id = $1`, successorID); err != nil {
		t.Fatalf("complete successor: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET fire_at = now() - interval '1 second' WHERE id = $1`, parentID); err != nil {
		t.Fatalf("make marker due: %v", err)
	}
	_, created := svc.ReevaluateFailedTaskRetries(ctx)
	if created < 1 {
		t.Fatal("the sweep created no retry after the successor ended")
	}
	var pendingChildren int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_queue
		WHERE parent_task_id = $1 AND status IN ('queued', 'deferred')
	`, parentID).Scan(&pendingChildren); err != nil {
		t.Fatalf("count retry children: %v", err)
	}
	if pendingChildren != 1 {
		t.Fatalf("retry children = %d, want exactly the rebuilt one", pendingChildren)
	}
	if err := pool.QueryRow(ctx, `SELECT fire_at FROM agent_task_queue WHERE id = $1`, parentID).Scan(&armedAt); err != nil {
		t.Fatalf("read marker after rebuild: %v", err)
	}
	if armedAt.Valid {
		t.Errorf("marker survived the rebuild, want it retired with the retry: %v", armedAt)
	}

	// The concurrency guard: a FUTURE marker — a re-arm by a concurrent
	// pass — must survive the expiry-guarded clear, or a re-armed row would
	// lose its re-evaluation.
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status = 'failed', fire_at = now() + interval '10 minutes' WHERE id = $1`, parentID); err != nil {
		t.Fatalf("re-arm marker: %v", err)
	}
	if err := q.ClearFailedTaskRetryReevaluation(ctx, parentID); err != nil {
		t.Fatalf("ClearFailedTaskRetryReevaluation: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT fire_at FROM agent_task_queue WHERE id = $1`, parentID).Scan(&armedAt); err != nil {
		t.Fatalf("read marker after guarded clear: %v", err)
	}
	if !armedAt.Valid {
		t.Fatal("the expiry-guarded clear erased a future re-arm")
	}

	// Budget exhausted: the sweep stops re-arming — the marker is retired and
	// the row leaves the sweep for good, with no retry created.
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET attempt = 9, fire_at = now() - interval '1 second' WHERE id = $1`, parentID); err != nil {
		t.Fatalf("exhaust budget: %v", err)
	}
	svc.ReevaluateFailedTaskRetries(ctx)
	if err := pool.QueryRow(ctx, `SELECT fire_at FROM agent_task_queue WHERE id = $1`, parentID).Scan(&armedAt); err != nil {
		t.Fatalf("read marker after exhausted sweep: %v", err)
	}
	if armedAt.Valid {
		t.Errorf("marker survived an exhausted budget, want it retired: %v", armedAt)
	}
	var retryCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_queue WHERE parent_task_id = $1 AND status IN ('queued', 'deferred')
	`, parentID).Scan(&retryCount); err != nil {
		t.Fatalf("count retries after exhausted sweep: %v", err)
	}
	if retryCount != 1 {
		t.Errorf("retry count = %d, want still just the one rebuilt earlier", retryCount)
	}
}
