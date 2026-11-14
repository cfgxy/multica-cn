package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

type runtimeModelCallBudgetClaimFixture struct {
	fx        *testutil.Fixture
	runtimeID string
	agentA    string
	agentB    string
	taskA     string
	taskB     string
}

func newRuntimeModelCallBudgetClaimFixture(t *testing.T, pool *pgxpool.Pool, maxCalls int64) runtimeModelCallBudgetClaimFixture {
	t.Helper()

	suffix := time.Now().UnixNano()
	bootstrap := testutil.New(pool, "", "")
	userID := bootstrap.User(t, fmt.Sprintf("runtime-budget-user-%d", suffix), fmt.Sprintf("runtime-budget-%d@example.com", suffix))
	workspaceID := bootstrap.Workspace(t, fmt.Sprintf("runtime-budget-%d", suffix), fmt.Sprintf("runtime-budget-%d", suffix))
	fx := testutil.New(pool, workspaceID, userID)
	runtimeID := fx.Runtime(t, fmt.Sprintf("runtime-budget-%d", suffix), testutil.Cols{
		"visibility":                       "public",
		"max_concurrent_tasks":             0,
		"model_call_budget_enabled":        true,
		"model_call_budget_max_calls":      maxCalls,
		"model_call_budget_window_seconds": 3600,
	})
	agentA := fx.Agent(t, fmt.Sprintf("runtime-budget-a-%d", suffix), runtimeID, testutil.Cols{"max_concurrent_tasks": -1})
	agentB := fx.Agent(t, fmt.Sprintf("runtime-budget-b-%d", suffix), runtimeID, testutil.Cols{"max_concurrent_tasks": -1})
	taskA := fx.Task(t, agentA, testutil.Cols{
		"runtime_id": runtimeID,
		"issue_id":   fx.Issue(t, fmt.Sprintf("runtime-budget-a-issue-%d", suffix)),
	})
	taskB := fx.Task(t, agentB, testutil.Cols{
		"runtime_id": runtimeID,
		"issue_id":   fx.Issue(t, fmt.Sprintf("runtime-budget-b-issue-%d", suffix)),
	})
	return runtimeModelCallBudgetClaimFixture{
		fx:        fx,
		runtimeID: runtimeID,
		agentA:    agentA,
		agentB:    agentB,
		taskA:     taskA,
		taskB:     taskB,
	}
}

func TestClaimTaskRuntimeModelCallBudgetIsSharedAndAtomic(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	fixture := newRuntimeModelCallBudgetClaimFixture(t, pool, 1)
	svc := NewTaskService(db.New(pool), pool, nil, events.New())

	for _, agentID := range []string{fixture.agentA, fixture.agentB} {
		triggerName := fmt.Sprintf("claim_budget_sleep_%d", time.Now().UnixNano())
		createSleepTrigger(t, ctx, pool, triggerName, triggerName+"_fn", agentID)
	}

	start := make(chan struct{})
	claimed := make(chan string, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, agentID := range []string{fixture.agentA, fixture.agentB} {
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
				claimed <- util.UUIDToString(task.ID)
			}
		}(agentID)
	}
	close(start)
	wg.Wait()
	close(claimed)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent claim: %v", err)
	}
	var claimedIDs []string
	for taskID := range claimed {
		claimedIDs = append(claimedIDs, taskID)
	}
	if len(claimedIDs) != 1 {
		t.Fatalf("claimed %d tasks with one remaining model-call slot, want 1", len(claimedIDs))
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
		t.Fatalf("task states after concurrent claims: dispatched=%d queued=%d, want 1 and 1", dispatched, queued)
	}
}

func TestClaimTaskRuntimeModelCallBudgetEvidenceReplacesReservation(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	fixture := newRuntimeModelCallBudgetClaimFixture(t, pool, 2)
	queries := db.New(pool)
	svc := NewTaskService(queries, pool, nil, events.New())

	claimedA, err := svc.ClaimTask(ctx, util.MustParseUUID(fixture.agentA))
	if err != nil {
		t.Fatalf("claim first task: %v", err)
	}
	if claimedA == nil {
		t.Fatal("first task was not claimed")
	}
	fixture.fx.Insert(t, "task_usage", testutil.Cols{
		"task_id":          fixture.taskA,
		"provider":         "budget-test",
		"model":            "reservation-replacement",
		"model_call_count": int64(1),
	})
	fixture.fx.InsertNoID(t, "task_usage_model_call_evidence", testutil.Cols{
		"task_id":     fixture.taskA,
		"provider":    "budget-test",
		"model":       "reservation-replacement",
		"evidence_id": "reservation-replacement-1",
	}, "task_id = $1 AND provider = $2 AND model = $3 AND evidence_id = $4", fixture.taskA, "budget-test", "reservation-replacement", "reservation-replacement-1")

	admission, err := queries.GetRuntimeModelCallBudgetAdmission(ctx, util.MustParseUUID(fixture.runtimeID))
	if err != nil {
		t.Fatalf("read budget admission: %v", err)
	}
	if admission.KnownCalls != 1 || admission.ReservedCalls != 0 || admission.UnknownUsageRows != 0 {
		t.Fatalf("admission after evidence = known %d reserved %d unknown %d, want 1, 0, 0", admission.KnownCalls, admission.ReservedCalls, admission.UnknownUsageRows)
	}
	claimedB, err := svc.ClaimTask(ctx, util.MustParseUUID(fixture.agentB))
	if err != nil {
		t.Fatalf("claim second task: %v", err)
	}
	if claimedB == nil {
		t.Fatal("evidence replaced a reservation but the second task was not claimed")
	}
}
