package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RUYI-665 phase 2: the auto-retry suppression and the autopilot budget gates.
// The autopilot assertions also lock in the wakeup-gating contract (拒发不废规
// 则、周期重置自然恢复): a budget refusal is a typed SKIP, and a skipped run is
// cron SUCCESS — isAutopilotRunComplete is the exact predicate the scheduler
// consults before advancing the trigger, and SelectAutopilotsExceedingFailure
// Threshold is the exact query the failure monitor consults before auto-paus-
// ing. Both must treat a budget skip as neutral so the schedule stays alive
// and recovers by itself when the period resets.

// autopilotWithTrigger seeds an active autopilot over the fixture's agent with
// one schedule trigger, all owned by the fixture user so admission passes and
// the budget gate is what decides.
func (f *budgetPhase2Fixture) autopilotWithTrigger(t *testing.T, executionMode string) (autopilotID, triggerID string) {
	t.Helper()
	_, agentID := f.runtimeAndAgent(t, fmt.Sprintf("ap-%s", executionMode))
	autopilotID = f.Insert(t, "autopilot", testutil.Cols{
		"workspace_id":    f.WorkspaceID,
		"title":           fmt.Sprintf("budget ap %s %d", executionMode, time.Now().UnixNano()),
		"assignee_type":   "agent",
		"assignee_id":     agentID,
		"status":          "active",
		"execution_mode":  executionMode,
		"created_by_type": "member",
		"created_by_id":   f.UserID,
	})
	triggerID = f.Insert(t, "autopilot_trigger", testutil.Cols{
		"autopilot_id":      autopilotID,
		"kind":              "schedule",
		"enabled":           true,
		"cron_expression":   fmt.Sprintf("%d * * * *", time.Now().UnixNano()%60),
		"published_by_type": "member",
		"published_by_id":   f.UserID,
		"created_by_type":   "member",
		"created_by_id":     f.UserID,
	})
	return autopilotID, triggerID
}

// dispatch drives the full entry point — admission included — so a skip can
// only come from the budget gate, never from admission.
func (f *budgetPhase2Fixture) dispatch(t *testing.T, autopilotID, triggerID string) *db.AutopilotRun {
	t.Helper()
	ap, err := f.q.GetAutopilot(context.Background(), util.MustParseUUID(autopilotID))
	if err != nil {
		t.Fatalf("load autopilot: %v", err)
	}
	svc := &AutopilotService{
		Queries: f.q, TxStarter: f.pool, Bus: f.svc.Bus, TaskSvc: f.svc,
	}
	run, err := svc.DispatchAutopilot(context.Background(), ap, util.MustParseUUID(triggerID), "schedule", nil)
	if err != nil {
		t.Fatalf("DispatchAutopilot: %v", err)
	}
	if run == nil {
		t.Fatal("dispatch returned no run")
	}
	return run
}

// runtimeIDOfAgent reads the runtime an agent is bound to.
func (f *budgetPhase2Fixture) runtimeIDOfAgent(t *testing.T, agentID string) string {
	t.Helper()
	var runtimeID string
	f.QueryRow(t, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID)
	return runtimeID
}

// TestFailTaskAutoRetry_BudgetSuppressedFailsClosed locks in the retry
// suppression: a retryable failure on a spent budget creates NO retry child,
// while the identical failure without budget rows does — proving the harness
// would have retried and the veto is the budget's doing.
func TestFailTaskAutoRetry_BudgetSuppressedFailsClosed(t *testing.T) {
	ctx := context.Background()

	t.Run("spent budget suppresses the retry", func(t *testing.T) {
		fx := newBudgetPhase2Fixture(t)
		_, agentID := fx.runtimeAndAgent(t, "retry-blocked")
		issueID := fx.Issue(t, "retry suppression")
		runtimeID := fx.runtimeIDOfAgent(t, agentID)
		fx.setRuntimeBudget(t, runtimeID, 100)
		fx.spendRuntime(t, runtimeID, 100)
		taskID := fx.Task(t, agentID, testutil.Cols{
			"runtime_id": runtimeID, "issue_id": issueID,
			"status": "running", "attempt": 1, "max_attempts": 2,
		})

		failed, err := fx.svc.FailTask(ctx, util.MustParseUUID(taskID), "boom", "", "", "", "timeout", false, "", "")
		if err != nil {
			t.Fatalf("FailTask: %v", err)
		}
		if failed.Status != "failed" {
			t.Fatalf("parent status = %q, want failed", failed.Status)
		}
		if got := fx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE parent_task_id = $1`, taskID); got != 0 {
			t.Fatalf("retry children = %d, want 0 (budget suppression)", got)
		}
	})

	t.Run("no budget rows retries", func(t *testing.T) {
		fx := newBudgetPhase2Fixture(t)
		_, agentID := fx.runtimeAndAgent(t, "retry-clean")
		issueID := fx.Issue(t, "retry control")
		runtimeID := fx.runtimeIDOfAgent(t, agentID)
		fx.spendRuntime(t, runtimeID, 500)
		taskID := fx.Task(t, agentID, testutil.Cols{
			"runtime_id": runtimeID, "issue_id": issueID,
			"status": "running", "attempt": 1, "max_attempts": 2,
		})

		if _, err := fx.svc.FailTask(ctx, util.MustParseUUID(taskID), "boom", "", "", "", "timeout", false, "", ""); err != nil {
			t.Fatalf("FailTask: %v", err)
		}
		if got := fx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE parent_task_id = $1`, taskID); got != 1 {
			t.Fatalf("retry children = %d, want 1 (the control leg)", got)
		}
	})
}

// TestAutopilotRunOnly_BudgetSkipKeepsScheduleAlive: the gate records a typed
// skipped run (not a failure), the reason is amount-free, no task is enqueued,
// and both wakeup consumers treat the skip as neutral.
func TestAutopilotRunOnly_BudgetSkipKeepsScheduleAlive(t *testing.T) {
	fx := newBudgetPhase2Fixture(t)
	autopilotID, triggerID := fx.autopilotWithTrigger(t, "run_only")
	var agentID string
	fx.QueryRow(t, `SELECT assignee_id::text FROM autopilot WHERE id = $1`, autopilotID).Scan(&agentID)
	runtimeID := fx.runtimeIDOfAgent(t, agentID)
	fx.setRuntimeBudget(t, runtimeID, 100)
	fx.spendRuntime(t, runtimeID, 100)

	run := fx.dispatch(t, autopilotID, triggerID)
	if run.Status != "skipped" {
		t.Fatalf("run status = %q, want skipped", run.Status)
	}
	if run.ReasonCode.String != "budget_exceeded" {
		t.Errorf("reason_code = %q, want budget_exceeded", run.ReasonCode.String)
	}
	if strings.ContainsAny(run.FailureReason.String, "0123456789") {
		t.Errorf("failure_reason %q carries digits; the persisted reason must be amount-free", run.FailureReason.String)
	}
	if got := fx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE autopilot_run_id = $1`, util.UUIDToString(run.ID)); got != 0 {
		t.Fatalf("refused run_only still enqueued %d tasks", got)
	}

	// Wakeup leg 1 — the scheduler: a skipped run is complete, so the cron
	// records SUCCESS and the trigger's next fire advances.
	if !isAutopilotRunComplete(*run) {
		t.Error("isAutopilotRunComplete(skipped) = false; the trigger would stall instead of advancing")
	}
	// Wakeup leg 2 — the failure monitor: skipped is invisible to the ratio,
	// so the autopilot is not auto-paused.
	exceeding, err := fx.q.SelectAutopilotsExceedingFailureThreshold(context.Background(),
		db.SelectAutopilotsExceedingFailureThresholdParams{
			Since:              pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
			MinRuns:            1,
			FailRatioThreshold: 0.5,
		})
	if err != nil {
		t.Fatalf("failure threshold query: %v", err)
	}
	for _, row := range exceeding {
		if util.UUIDToString(row.ID) == autopilotID {
			t.Errorf("budget-skipped autopilot selected for auto-pause (total=%d failed=%d)", row.TotalRuns, row.FailedRuns)
		}
	}
}

// TestAutopilotRunOnly_NoBudgetDispatches is the control leg: the same shape
// without budget rows runs, so the skip above is the budget's doing.
func TestAutopilotRunOnly_NoBudgetDispatches(t *testing.T) {
	fx := newBudgetPhase2Fixture(t)
	autopilotID, triggerID := fx.autopilotWithTrigger(t, "run_only")
	var agentID string
	fx.QueryRow(t, `SELECT assignee_id::text FROM autopilot WHERE id = $1`, autopilotID).Scan(&agentID)
	fx.spendRuntime(t, fx.runtimeIDOfAgent(t, agentID), 500)

	run := fx.dispatch(t, autopilotID, triggerID)
	if run.Status == "skipped" {
		t.Fatalf("control dispatch was skipped (%s), want a real dispatch", run.FailureReason.String)
	}
	if !run.TaskID.Valid {
		t.Fatalf("run status = %q without a task id", run.Status)
	}
}

// TestAutopilotCreateIssue_BudgetPrecheckBeforeIssueMint: the transaction-top
// pre-check refuses before the duplicate guard or an issue number is burned —
// a spent budget must not mint an orphaned autopilot issue on every fire.
func TestAutopilotCreateIssue_BudgetPrecheckBeforeIssueMint(t *testing.T) {
	fx := newBudgetPhase2Fixture(t)
	autopilotID, triggerID := fx.autopilotWithTrigger(t, "create_issue")
	var agentID string
	fx.QueryRow(t, `SELECT assignee_id::text FROM autopilot WHERE id = $1`, autopilotID).Scan(&agentID)
	runtimeID := fx.runtimeIDOfAgent(t, agentID)
	fx.setRuntimeBudget(t, runtimeID, 100)
	fx.spendRuntime(t, runtimeID, 100)

	run := fx.dispatch(t, autopilotID, triggerID)
	if run.Status != "skipped" {
		t.Fatalf("run status = %q, want skipped", run.Status)
	}
	if run.ReasonCode.String != "budget_exceeded" {
		t.Errorf("reason_code = %q, want budget_exceeded", run.ReasonCode.String)
	}
	if run.IssueID.Valid {
		t.Errorf("skipped run links issue %s; the pre-check must refuse before any issue exists", util.UUIDToString(run.IssueID))
	}
	if got := fx.Count(t, `SELECT count(*) FROM issue WHERE origin_type = 'autopilot' AND origin_id = $1`, autopilotID); got != 0 {
		t.Fatalf("refused create_issue still minted %d issues", got)
	}
	if got := fx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id IS NOT NULL AND autopilot_run_id = $1`, util.UUIDToString(run.ID)); got != 0 {
		t.Errorf("refused create_issue enqueued %d tasks", got)
	}
}

// TestAutopilotCreateIssue_NoBudgetCreatesIssue is the control leg.
func TestAutopilotCreateIssue_NoBudgetCreatesIssue(t *testing.T) {
	fx := newBudgetPhase2Fixture(t)
	autopilotID, triggerID := fx.autopilotWithTrigger(t, "create_issue")
	var agentID string
	fx.QueryRow(t, `SELECT assignee_id::text FROM autopilot WHERE id = $1`, autopilotID).Scan(&agentID)
	fx.spendRuntime(t, fx.runtimeIDOfAgent(t, agentID), 500)

	run := fx.dispatch(t, autopilotID, triggerID)
	if run.Status == "skipped" {
		t.Fatalf("control dispatch was skipped (%s), want issue_created", run.FailureReason.String)
	}
	if !run.IssueID.Valid {
		t.Fatalf("run status = %q without an issue id", run.Status)
	}
}
