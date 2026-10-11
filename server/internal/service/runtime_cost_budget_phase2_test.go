package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

// Phase-2 RUYI-665 acceptance tests, database-backed: the once-per-period CAS
// notice, the warn-only degradation switch, and the deferred-promotion veto.
// They run against DATABASE_URL (an isolated test database — the shared
// `multica` database is refused by the ledger gate) and skip without one.

var budgetPhase2Seq int

// budgetPhase2Fixture is a self-contained fixture: fresh user + workspace +
// member on its own pool, plus helpers to seed budgets, spend, and due
// deferred tasks. Every budget verdict in phase 2 is computed for exactly the
// (runtime, agent-owner) scope this fixture builds.
type budgetPhase2Fixture struct {
	*testutil.Fixture
	pool *pgxpool.Pool
	q    *db.Queries
	svc  *TaskService
}

func newBudgetPhase2Fixture(t *testing.T) *budgetPhase2Fixture {
	t.Helper()
	pool := newResolveOriginatorPool(t)
	q := db.New(pool)

	budgetPhase2Seq++
	fx := testutil.New(pool, "", "")
	userID := fx.User(t, "budget owner", fmt.Sprintf("budget-owner-%d-%d@multica.test", time.Now().UnixNano(), budgetPhase2Seq))
	workspaceID := fx.Workspace(t, "budget ws", fmt.Sprintf("budget-ws-%d-%d", time.Now().UnixNano(), budgetPhase2Seq))
	fx.Member(t, workspaceID, userID, "owner")
	fx.WorkspaceID = workspaceID
	fx.UserID = userID

	return &budgetPhase2Fixture{
		Fixture: fx,
		pool:    pool,
		q:       q,
		svc:     &TaskService{Queries: q, TxStarter: pool, Bus: events.New()},
	}
}

// runtimeAndAgent seeds the runtime + agent pair and returns their ids.
func (f *budgetPhase2Fixture) runtimeAndAgent(t *testing.T, label string) (runtimeID, agentID string) {
	t.Helper()
	runtimeID = f.Runtime(t, label+"-runtime")
	agentID = f.Agent(t, label+"-agent", runtimeID)
	return runtimeID, agentID
}

// setRuntimeBudget inserts a runtime-total budget row with one daily limit.
func (f *budgetPhase2Fixture) setRuntimeBudget(t *testing.T, runtimeID string, dailyTicks int64) {
	t.Helper()
	f.Insert(t, "runtime_cost_budget", testutil.Cols{
		"workspace_id":          f.WorkspaceID,
		"runtime_id":            runtimeID,
		"daily_limit_usd_ticks": dailyTicks,
		"updated_by":            f.UserID,
	})
}

// setRuntimeUserBudget inserts the per-user budget row for one owner on the
// runtime. The runtime-total row keeps a high limit, so a per-user veto cannot
// be confused with a runtime-total one.
func (f *budgetPhase2Fixture) setRuntimeUserBudget(t *testing.T, runtimeID, userID string, dailyTicks int64) {
	t.Helper()
	f.Insert(t, "runtime_cost_budget", testutil.Cols{
		"workspace_id":          f.WorkspaceID,
		"runtime_id":            runtimeID,
		"user_id":               userID,
		"daily_limit_usd_ticks": dailyTicks,
		"updated_by":            f.UserID,
	})
}

// spendRuntime seeds a completed task + priced task_usage on the runtime so
// the enforcement aggregation reads the given spend.
func (f *budgetPhase2Fixture) spendRuntime(t *testing.T, runtimeID string, usdTicks int64) {
	t.Helper()
	spender := f.Agent(t, fmt.Sprintf("spender-%d", time.Now().UnixNano()), runtimeID)
	f.spendOnAgent(t, spender, runtimeID, usdTicks)
}

func (f *budgetPhase2Fixture) spendOnAgent(t *testing.T, agentID, runtimeID string, usdTicks int64) {
	t.Helper()
	taskID := f.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "status": "completed"})
	f.Insert(t, "task_usage", testutil.Cols{
		"task_id":        taskID,
		"provider":       "budget-test",
		"model":          "budget-test-model",
		"input_tokens":   0,
		"output_tokens":  0,
		"cost_usd_ticks": usdTicks,
	})
}

// dueDeferredTask seeds a deferred task whose fire_at has already passed —
// exactly what the promoters pick up on their next tick.
func (f *budgetPhase2Fixture) dueDeferredTask(t *testing.T, agentID, runtimeID, issueID string) string {
	t.Helper()
	return f.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID,
		"issue_id":   issueID,
		"status":     "deferred",
		"fire_at":    testutil.Raw("now() - interval '1 minute'"),
	})
}

func (f *budgetPhase2Fixture) taskStatus(t *testing.T, taskID string) string {
	t.Helper()
	var status string
	f.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status)
	return status
}

func (f *budgetPhase2Fixture) budgetInboxCount(t *testing.T) int {
	t.Helper()
	return f.Count(t, `SELECT count(*) FROM inbox_item WHERE type = 'runtime_budget_exceeded'`)
}

func (f *budgetPhase2Fixture) budgetInboxBodyAndDetails(t *testing.T) (string, string) {
	t.Helper()
	var body, details string
	f.QueryRow(t, `SELECT body, details FROM inbox_item WHERE type = 'runtime_budget_exceeded'`).Scan(&body, &details)
	return body, details
}

func (f *budgetPhase2Fixture) warnOnlyFlags(on bool) {
	provider := featureflag.NewStaticProvider()
	provider.Set(featureflags.RuntimeCostBudgetWarnOnly, featureflag.Rule{Default: on})
	f.svc.FeatureFlags = featureflag.NewService(provider)
}

// TestCheckRuntimeCostBudget_NoBudgetRowsIsUnlimited locks in the phase-1
// acceptance gap: a runtime without budget rows never refuses, no matter how
// much it has spent, and sends no notices.
func TestCheckRuntimeCostBudget_NoBudgetRowsIsUnlimited(t *testing.T) {
	fx := newBudgetPhase2Fixture(t)
	ctx := context.Background()
	runtimeID, agentID := fx.runtimeAndAgent(t, "unlimited")
	fx.spendRuntime(t, runtimeID, 9_999_99)

	agent, err := fx.q.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	if err := fx.svc.checkRuntimeCostBudget(ctx, fx.q, agent, time.Now()); err != nil {
		t.Fatalf("no budget rows must be unlimited, got %v", err)
	}
	if got := fx.budgetInboxCount(t); got != 0 {
		t.Errorf("inbox notices = %d, want 0", got)
	}
}

// TestCheckRuntimeCostBudget_EnforceNotifiesOncePerPeriod is the database-level
// CAS assertion: repeated refusals in one period claim the
// MarkRuntimeCostBudgetNotified marker exactly once, so the inbox receives one
// notice no matter how many further runs are refused.
func TestCheckRuntimeCostBudget_EnforceNotifiesOncePerPeriod(t *testing.T) {
	fx := newBudgetPhase2Fixture(t)
	ctx := context.Background()
	runtimeID, agentID := fx.runtimeAndAgent(t, "cas")
	fx.setRuntimeBudget(t, runtimeID, 100)
	fx.spendRuntime(t, runtimeID, 100)

	agent, err := fx.q.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	for i := 0; i < 3; i++ {
		err := fx.svc.checkRuntimeCostBudget(ctx, fx.q, agent, time.Now())
		var budgetErr *RuntimeBudgetExceededError
		if !errors.As(err, &budgetErr) {
			t.Fatalf("refusal %d: want RuntimeBudgetExceededError, got %v", i, err)
		}
	}
	if got := fx.budgetInboxCount(t); got != 1 {
		t.Fatalf("inbox notices after 3 refusals = %d, want exactly 1 (CAS claim)", got)
	}
	_, details := fx.budgetInboxBodyAndDetails(t)
	if strings.Contains(details, `"warn_only"`) {
		t.Errorf("enforce-mode notice carries a warn_only marker: %s", details)
	}
}

// TestCheckRuntimeCostBudget_WarnOnlyAllowsAndStillNotices verifies the
// degradation switch end to end: a spent budget in warn-only mode returns nil
// (the run proceeds), still fires the once-per-period notice, and the notice
// says runs are allowed — the enforce copy would be false information.
func TestCheckRuntimeCostBudget_WarnOnlyAllowsAndStillNotices(t *testing.T) {
	fx := newBudgetPhase2Fixture(t)
	ctx := context.Background()
	runtimeID, agentID := fx.runtimeAndAgent(t, "warn")
	fx.setRuntimeBudget(t, runtimeID, 100)
	fx.spendRuntime(t, runtimeID, 100)
	fx.warnOnlyFlags(true)

	agent, err := fx.q.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := fx.svc.checkRuntimeCostBudget(ctx, fx.q, agent, time.Now()); err != nil {
			t.Fatalf("warn-only run %d must be allowed, got %v", i, err)
		}
	}
	if got := fx.budgetInboxCount(t); got != 1 {
		t.Fatalf("warn-only inbox notices = %d, want exactly 1 (the CAS claim survives the mode)", got)
	}
	body, details := fx.budgetInboxBodyAndDetails(t)
	if !strings.Contains(body, "warn-only") || !strings.Contains(body, "still allowed") {
		t.Errorf("warn-only body must say runs are allowed, got %q", body)
	}
	if strings.Contains(body, "refused") {
		t.Errorf("warn-only body must not claim runs are refused: %q", body)
	}
	normalized := strings.ReplaceAll(details, " ", "")
	if !strings.Contains(normalized, `"warn_only":"true"`) {
		t.Errorf("warn-only details must carry the marker, got %s", details)
	}
}

// TestPromoteDueDeferredTasks_BudgetGate covers the deferred-fallback
// promotion veto (RUYI-665 phase 2): a spent budget holds the due task
// deferred, a reset promotes it on a later tick, warn-only promotes while
// still noticing, and the batch promoter vetoes only the blocked agent.
func TestPromoteDueDeferredTasks_BudgetGate(t *testing.T) {
	ctx := context.Background()

	t.Run("spent budget holds the due task deferred, reset recovers", func(t *testing.T) {
		fx := newBudgetPhase2Fixture(t)
		runtimeID, agentID := fx.runtimeAndAgent(t, "deferred-blocked")
		issueID := fx.Issue(t, "deferred budget gate")
		fx.setRuntimeBudget(t, runtimeID, 100)
		fx.spendRuntime(t, runtimeID, 100)
		taskID := fx.dueDeferredTask(t, agentID, runtimeID, issueID)

		if err := fx.svc.PromoteDueDeferredTasksForRuntime(ctx, util.MustParseUUID(runtimeID)); err != nil {
			t.Fatalf("promote: %v", err)
		}
		if status := fx.taskStatus(t, taskID); status != "deferred" {
			t.Fatalf("task status = %q, want deferred (budget veto)", status)
		}
		if got := fx.budgetInboxCount(t); got != 1 {
			t.Errorf("inbox notices = %d, want 1 (the veto tells the owner why)", got)
		}

		// Natural recovery: the period resets (the budget is removed here),
		// and the still-due task is promoted on its own on the next tick.
		fx.Exec(t, `DELETE FROM runtime_cost_budget WHERE runtime_id = $1`, util.MustParseUUID(runtimeID))
		if err := fx.svc.PromoteDueDeferredTasksForRuntime(ctx, util.MustParseUUID(runtimeID)); err != nil {
			t.Fatalf("promote after reset: %v", err)
		}
		if status := fx.taskStatus(t, taskID); status != "queued" {
			t.Fatalf("after reset task status = %q, want queued (natural recovery)", status)
		}
	})

	t.Run("no budget rows promotes", func(t *testing.T) {
		fx := newBudgetPhase2Fixture(t)
		runtimeID, agentID := fx.runtimeAndAgent(t, "deferred-clean")
		issueID := fx.Issue(t, "deferred no budget")
		fx.spendRuntime(t, runtimeID, 500)
		taskID := fx.dueDeferredTask(t, agentID, runtimeID, issueID)

		if err := fx.svc.PromoteDueDeferredTasksForRuntime(ctx, util.MustParseUUID(runtimeID)); err != nil {
			t.Fatalf("promote: %v", err)
		}
		if status := fx.taskStatus(t, taskID); status != "queued" {
			t.Fatalf("task status = %q, want queued (no budget rows = no gate)", status)
		}
	})

	t.Run("warn-only promotes and notices", func(t *testing.T) {
		fx := newBudgetPhase2Fixture(t)
		runtimeID, agentID := fx.runtimeAndAgent(t, "deferred-warn")
		issueID := fx.Issue(t, "deferred warn only")
		fx.setRuntimeBudget(t, runtimeID, 100)
		fx.spendRuntime(t, runtimeID, 100)
		fx.warnOnlyFlags(true)
		taskID := fx.dueDeferredTask(t, agentID, runtimeID, issueID)

		if err := fx.svc.PromoteDueDeferredTasksForRuntime(ctx, util.MustParseUUID(runtimeID)); err != nil {
			t.Fatalf("promote: %v", err)
		}
		if status := fx.taskStatus(t, taskID); status != "queued" {
			t.Fatalf("warn-only task status = %q, want queued (degraded to notice-only)", status)
		}
		if got := fx.budgetInboxCount(t); got != 1 {
			t.Errorf("warn-only inbox notices = %d, want 1", got)
		}
	})

	t.Run("batch promoter vetoes only the blocked agent", func(t *testing.T) {
		fx := newBudgetPhase2Fixture(t)
		runtimeID, agentA := fx.runtimeAndAgent(t, "batch-blocked")
		otherOwner := fx.User(t, "batch clean owner", fmt.Sprintf("batch-clean-%d@multica.test", time.Now().UnixNano()))
		fx.Member(t, fx.WorkspaceID, otherOwner, "member")
		agentB := fx.Agent(t, "batch-clean-agent", runtimeID, testutil.Cols{"owner_id": otherOwner})
		issueID := fx.Issue(t, "batch veto")
		// Runtime total stays far under its cap; only the per-user row for the
		// fixture owner is spent, so the veto must come from the user scope.
		fx.setRuntimeBudget(t, runtimeID, 1_000_000)
		fx.setRuntimeUserBudget(t, runtimeID, fx.UserID, 100)
		fx.spendOnAgent(t, agentA, runtimeID, 100)
		taskA := fx.dueDeferredTask(t, agentA, runtimeID, issueID)
		taskB := fx.dueDeferredTask(t, agentB, runtimeID, issueID)

		// The exact sequence ClaimTasksForRuntimes runs (the batch promoter is
		// not a service method of its own): gate read → veto → promote minus
		// the vetoed rows.
		due, err := fx.q.ListDueDeferredTasksForBudgetGate(ctx, db.ListDueDeferredTasksForBudgetGateParams{
			RuntimeIds:       []pgtype.UUID{util.MustParseUUID(runtimeID)},
			RuntimeStaleSecs: RuntimeClaimFreshnessSeconds,
		})
		if err != nil {
			t.Fatalf("gate read: %v", err)
		}
		if len(due) != 2 {
			t.Fatalf("due rows = %d, want both deferred tasks", len(due))
		}
		promoted, err := fx.q.PromoteDueDeferredTasksForRuntimes(ctx, db.PromoteDueDeferredTasksForRuntimesParams{
			RuntimeIds:       []pgtype.UUID{util.MustParseUUID(runtimeID)},
			RuntimeStaleSecs: RuntimeClaimFreshnessSeconds,
			ExcludedIds:      fx.svc.budgetBlockedDeferredTaskIDs(ctx, due),
		})
		if err != nil {
			t.Fatalf("batch promote: %v", err)
		}
		if len(promoted) != 1 || util.UUIDToString(promoted[0].ID) != taskB {
			t.Fatalf("promoted = %v, want only the clean agent's task %s", promoted, taskB)
		}
		if status := fx.taskStatus(t, taskA); status != "deferred" {
			t.Errorf("blocked agent task = %q, want deferred", status)
		}
		if status := fx.taskStatus(t, taskB); status != "queued" {
			t.Errorf("clean agent task = %q, want queued", status)
		}
	})
}
