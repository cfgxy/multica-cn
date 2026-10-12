package service

// RUYI-648 regression: an issue's priority is snapshotted onto its queue rows
// at enqueue time, so editing the issue afterwards left every still-queued row
// on the stale value — the agent's claim ordering (priority DESC, created_at
// ASC) kept serving the old urgency and a "just made urgent" issue waited
// behind a FIFO of pri=0 rows. The replay updates every queued row of the
// issue; rows a daemon already claimed (dispatched/running) and terminal rows
// keep the snapshot they were enqueued with — their ordering decision has
// been made.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func queuedTaskPriority(t *testing.T, pool *pgxpool.Pool, taskID string) int32 {
	t.Helper()
	var priority int32
	if err := pool.QueryRow(context.Background(),
		`SELECT priority FROM agent_task_queue WHERE id = $1`, taskID).Scan(&priority); err != nil {
		t.Fatalf("read task priority: %v", err)
	}
	return priority
}

// seedIssueWithPriority adds one issue to an existing fixture workspace so a
// subtest can exercise its own (issue, priority) pair without paying for a
// fresh workspace every time. Numbers are minted per call: uq_issue_workspace_number
// has no sequence default, so a second issue in the workspace needs an explicit one.
var replayIssueNumber atomic.Int64

func seedIssueWithPriority(t *testing.T, pool *pgxpool.Pool, workspaceID, userID, agentID, priority string) string {
	t.Helper()
	number := time.Now().UnixNano()%1_000_000_000 + replayIssueNumber.Add(1)
	var issueID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, creator_type, creator_id, assignee_type, assignee_id, priority, number, position)
		VALUES ($1, 'priority replay issue', 'member', $2, 'agent', $3, $4, $5, 0)
		RETURNING id`, workspaceID, userID, agentID, priority, number).Scan(&issueID); err != nil {
		t.Fatalf("seed issue (priority %s): %v", priority, err)
	}
	return issueID
}

// One issue, four rows across two agents: only the queued rows follow the
// issue's new priority. The (issue_id, agent_id) partial unique index on
// queued/dispatched rows is respected by putting the claimed-but-unterminal
// statuses next to a queued row of a different agent.
func TestUpdateQueuedTaskPrioritiesForIssue_ReplaysQueuedRowsOnly(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, agentID, issueID := seedAttributionFixture(t, pool)
	runtimeID := fixtureRuntimeID(t, pool, agentID)
	otherAgentID := seedExtraAgent(t, pool, workspaceID, runtimeID, userID, "priority-replay-other")

	queuedA := seedQueuedIssueTask(t, pool, agentID, runtimeID, issueID, userID, "queued")
	runningA := seedQueuedIssueTask(t, pool, agentID, runtimeID, issueID, userID, "running")
	queuedB := seedQueuedIssueTask(t, pool, otherAgentID, runtimeID, issueID, userID, "queued")
	completedB := seedQueuedIssueTask(t, pool, otherAgentID, runtimeID, issueID, userID, "completed")

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	rows, err := svc.UpdateQueuedTaskPrioritiesForIssue(ctx, mustUUID(t, issueID), "urgent")
	if err != nil {
		t.Fatalf("UpdateQueuedTaskPrioritiesForIssue: %v", err)
	}
	if rows != 2 {
		t.Fatalf("updated rows = %d, want 2 (the two queued rows)", rows)
	}
	if got := queuedTaskPriority(t, pool, queuedA); got != 4 {
		t.Errorf("queued task priority = %d, want 4 (urgent)", got)
	}
	if got := queuedTaskPriority(t, pool, queuedB); got != 4 {
		t.Errorf("cross-agent queued task priority = %d, want 4 (urgent)", got)
	}
	if got := queuedTaskPriority(t, pool, runningA); got != 0 {
		t.Errorf("running task priority = %d, want 0 (claimed rows keep their snapshot)", got)
	}
	if got := queuedTaskPriority(t, pool, completedB); got != 0 {
		t.Errorf("completed task priority = %d, want 0 (terminal rows are history)", got)
	}
}

// The replayed value is the same priorityToInt mapping the enqueue path uses,
// so a replayed row and a freshly enqueued row sort identically.
func TestUpdateQueuedTaskPrioritiesForIssue_MapsIssuePriorityToInt(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, agentID, _ := seedAttributionFixture(t, pool)
	runtimeID := fixtureRuntimeID(t, pool, agentID)

	cases := []struct {
		priority string
		want     int32
	}{{"urgent", 4}, {"high", 3}, {"medium", 2}, {"low", 1}, {"none", 0}}

	for _, tc := range cases {
		t.Run(tc.priority, func(t *testing.T) {
			issueID := seedIssueWithPriority(t, pool, workspaceID, userID, agentID, tc.priority)
			taskID := seedQueuedIssueTask(t, pool, agentID, runtimeID, issueID, userID, "queued")

			svc := NewTaskService(db.New(pool), pool, nil, events.New())
			rows, err := svc.UpdateQueuedTaskPrioritiesForIssue(ctx, mustUUID(t, issueID), tc.priority)
			if err != nil {
				t.Fatalf("UpdateQueuedTaskPrioritiesForIssue(%s): %v", tc.priority, err)
			}
			if rows != 1 {
				t.Fatalf("updated rows = %d, want 1", rows)
			}
			if got := queuedTaskPriority(t, pool, taskID); got != tc.want {
				t.Errorf("priority %q replayed as %d, want %d", tc.priority, got, tc.want)
			}
		})
	}
}
