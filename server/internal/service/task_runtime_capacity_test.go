package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestClaimTaskRuntimeCapacityIsSharedAndAtomic(t *testing.T) {
	ctx := context.Background()
	pool := newResolveOriginatorPool(t)
	bootstrap := testutil.New(pool, "", "")
	suffix := time.Now().UnixNano()
	userID := bootstrap.User(t, fmt.Sprintf("runtime-cap-user-%d", suffix), fmt.Sprintf("runtime-cap-%d@example.com", suffix))
	workspaceID := bootstrap.Workspace(t, fmt.Sprintf("runtime-cap-%d", suffix), fmt.Sprintf("runtime-cap-%d", suffix))
	fx := testutil.New(pool, workspaceID, userID)
	runtimeID := fx.Runtime(t, "runtime-cap", testutil.Cols{"visibility": "public", "max_concurrent_tasks": 1})
	agentA := fx.Agent(t, "runtime-cap-a", runtimeID, testutil.Cols{"max_concurrent_tasks": 0})
	agentB := fx.Agent(t, "runtime-cap-b", runtimeID, testutil.Cols{"max_concurrent_tasks": 0})
	queued := []string{
		fx.Task(t, agentA, testutil.Cols{"runtime_id": runtimeID, "issue_id": fx.Issue(t, fmt.Sprintf("runtime-cap-a-%d", suffix))}),
		fx.Task(t, agentB, testutil.Cols{"runtime_id": runtimeID, "issue_id": fx.Issue(t, fmt.Sprintf("runtime-cap-b-%d", suffix))}),
	}

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan string, 2)
	errs := make(chan error, 2)
	for _, agentID := range []string{agentA, agentB} {
		wg.Add(1)
		go func(agentID string) {
			defer wg.Done()
			<-start
			task, err := svc.ClaimTask(ctx, util.MustParseUUID(agentID))
			if err != nil {
				errs <- err
				return
			}
			if task != nil {
				results <- util.UUIDToString(task.ID)
			}
		}(agentID)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent claim: %v", err)
	}
	claimed := make(map[string]bool)
	for id := range results {
		claimed[id] = true
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d tasks with runtime limit 1, want exactly 1", len(claimed))
	}
	var dispatched, queuedCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'dispatched'), count(*) FILTER (WHERE status = 'queued') FROM agent_task_queue WHERE id = ANY($1::uuid[])`, queued).Scan(&dispatched, &queuedCount); err != nil {
		t.Fatalf("count claimed tasks: %v", err)
	}
	if dispatched != 1 || queuedCount != 1 {
		t.Fatalf("task states after claims: dispatched=%d queued=%d, want 1 and 1", dispatched, queuedCount)
	}
}

func TestClaimTaskUnlimitedAgentStillUsesRuntimeCapacityAndCountsActiveStates(t *testing.T) {
	ctx := context.Background()
	pool := newResolveOriginatorPool(t)
	bootstrap := testutil.New(pool, "", "")
	suffix := time.Now().UnixNano()
	userID := bootstrap.User(t, fmt.Sprintf("runtime-cap-unlimited-user-%d", suffix), fmt.Sprintf("runtime-cap-unlimited-%d@example.com", suffix))
	workspaceID := bootstrap.Workspace(t, fmt.Sprintf("runtime-cap-unlimited-%d", suffix), fmt.Sprintf("runtime-cap-unlimited-%d", suffix))
	fx := testutil.New(pool, workspaceID, userID)
	runtimeID := fx.Runtime(t, "runtime-cap-unlimited", testutil.Cols{"visibility": "public", "max_concurrent_tasks": 3})
	agentA := fx.Agent(t, "runtime-cap-unlimited-a", runtimeID, testutil.Cols{"max_concurrent_tasks": 0})
	agentB := fx.Agent(t, "runtime-cap-unlimited-b", runtimeID, testutil.Cols{"max_concurrent_tasks": 0})
	for i, status := range []string{"dispatched", "running", "waiting_local_directory"} {
		agentID := agentA
		if i == 1 {
			agentID = agentB
		}
		issueID := fx.Issue(t, fmt.Sprintf("runtime-cap-active-%d-%d", suffix, i))
		fx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "status": status})
	}
	issuePending := fx.Issue(t, fmt.Sprintf("runtime-cap-pending-%d", suffix))
	queuedID := fx.Task(t, agentB, testutil.Cols{"runtime_id": runtimeID, "issue_id": issuePending})

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	claimed, err := svc.ClaimTask(ctx, util.MustParseUUID(agentB))
	if err != nil {
		t.Fatalf("claim with unlimited agent setting: %v", err)
	}
	if claimed != nil {
		t.Fatalf("agent claimed task %s while runtime was already at its limit", util.UUIDToString(claimed.ID))
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, queuedID).Scan(&status); err != nil {
		t.Fatalf("read queued task: %v", err)
	}
	if status != "queued" {
		t.Fatalf("queued task status = %q, want queued", status)
	}
}

func TestClaimTaskZeroRuntimeLimitPreservesUnlimitedBehavior(t *testing.T) {
	ctx := context.Background()
	pool := newResolveOriginatorPool(t)
	bootstrap := testutil.New(pool, "", "")
	suffix := time.Now().UnixNano()
	userID := bootstrap.User(t, fmt.Sprintf("runtime-cap-zero-user-%d", suffix), fmt.Sprintf("runtime-cap-zero-%d@example.com", suffix))
	workspaceID := bootstrap.Workspace(t, fmt.Sprintf("runtime-cap-zero-%d", suffix), fmt.Sprintf("runtime-cap-zero-%d", suffix))
	fx := testutil.New(pool, workspaceID, userID)
	runtimeID := fx.Runtime(t, "runtime-cap-zero", testutil.Cols{"visibility": "public", "max_concurrent_tasks": 0})
	agentA := fx.Agent(t, "runtime-cap-zero-a", runtimeID, testutil.Cols{"max_concurrent_tasks": 0})
	agentB := fx.Agent(t, "runtime-cap-zero-b", runtimeID, testutil.Cols{"max_concurrent_tasks": 0})
	issueA := fx.Issue(t, fmt.Sprintf("runtime-cap-zero-a-%d", suffix))
	issueB := fx.Issue(t, fmt.Sprintf("runtime-cap-zero-b-%d", suffix))
	fx.Task(t, agentA, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueA})
	fx.Task(t, agentB, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueB})

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	for _, agentID := range []string{agentA, agentB} {
		claimed, err := svc.ClaimTask(ctx, util.MustParseUUID(agentID))
		if err != nil {
			t.Fatalf("claim with unlimited runtime: %v", err)
		}
		if claimed == nil {
			t.Fatalf("agent %s did not claim with runtime max_concurrent_tasks=0", agentID)
		}
	}
}
