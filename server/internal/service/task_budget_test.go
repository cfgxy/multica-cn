package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/taskbudget"
	"strings"
)

func TestBudgetRuntimeConfigSnapshot(t *testing.T) {
	task := db.AgentTaskQueue{Context: []byte(`{"head_sha":"abc","budget_scope":{"scope_id":"s","profile":{"max_runs":2,"per_run_timeout_seconds":8}}}`)}
	got, err := TaskBudgetRuntimeConfig(task, json.RawMessage(`{"max_turns":5,"budget_profile":{"max_runs":100}}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := taskbudget.ParseRuntimeConfig(got)
	if err != nil || p.MaxRuns != 2 || p.PerRunTimeoutSeconds != 8 {
		t.Fatalf("frozen profile lost: %s %v", got, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got, &fields); err != nil || string(fields["max_turns"]) != "5" {
		t.Fatal("unrelated runtime config lost")
	}
	got, err = TaskBudgetRuntimeConfig(db.AgentTaskQueue{}, got)
	p, parseErr := taskbudget.ParseRuntimeConfig(got)
	if err != nil || parseErr != nil || p != nil {
		t.Fatal("legacy lineage silently adopted profile")
	}
}

func TestBudgetContinuationBounded(t *testing.T) {
	for _, reason := range []string{"max_turns", "context_budget"} {
		parent := db.AgentTaskQueue{Attempt: 1, MaxAttempts: 2, IssueID: dbid.NewV7()}
		if !retryEligible(reason, parent) {
			t.Fatalf("%s lost continuation", reason)
		}
		parent.Attempt = 2
		if retryEligible(reason, parent) {
			t.Fatalf("%s exceeded attempts", reason)
		}
	}
	for _, reason := range []string{"budget_exhausted", "budget_invalid"} {
		if retryEligible(reason, db.AgentTaskQueue{Attempt: 1, MaxAttempts: 10, IssueID: dbid.NewV7()}) {
			t.Fatalf("terminal %s retried", reason)
		}
		target, err := loadDelegatedFailureRecoveryTarget(context.Background(), nil, db.AgentTaskQueue{Status: "failed", DelegatedFromTaskID: dbid.NewV7(), FailureReason: pgtype.Text{String: reason, Valid: true}})
		if err != nil || target != nil {
			t.Fatal("terminal budget failure started delegated recovery")
		}
	}
}

func TestBudgetConcurrentAdmissionAndFrozenLineage(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	_, _, agentID, issueID := seedAttributionFixture(t, pool)
	agentUUID := util.MustParseUUID(agentID)
	if _, err := pool.Exec(ctx, `UPDATE agent SET runtime_config='{"budget_profile":{"max_runs":2,"per_run_timeout_seconds":9}}' WHERE id=$1`, agentID); err != nil {
		t.Fatal(err)
	}
	svc := NewTaskService(q, pool, nil, events.New())
	var runtimeID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id=$1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	create := func(q *db.Queries) (db.AgentTaskQueue, error) {
		return q.CreateAgentTask(ctx, db.CreateAgentTaskParams{ID: dbid.NewV7(), AgentID: agentUUID, RuntimeID: runtimeID, HeadSha: pgtype.Text{String: "review", Valid: true}})
	}
	root, err := svc.budgetTaskCreate(ctx, nil, agentUUID, nil, create)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := taskbudget.FromContext(root.Context)
	if err != nil || snapshot == nil {
		t.Fatalf("missing root snapshot: %s %v", root.Context, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent SET runtime_config='{"budget_profile":{"max_runs":100}}' WHERE id=$1`, agentID); err != nil {
		t.Fatal(err)
	}
	var accepted, denied atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			child, err := svc.budgetTaskCreate(ctx, nil, agentUUID, []pgtype.UUID{root.ID}, create)
			if err == nil {
				accepted.Add(1)
				childScope, e := taskbudget.FromContext(child.Context)
				if e != nil || childScope.ScopeID != snapshot.ScopeID || childScope.Profile.MaxRuns != 2 {
					errs <- errors.New("child reset frozen scope")
				}
			} else if errors.Is(err, ErrTaskBudgetExhausted) {
				denied.Add(1)
			} else {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if accepted.Load() != 1 || denied.Load() != 11 {
		t.Fatalf("accepted=%d denied=%d", accepted.Load(), denied.Load())
	}
	var head string
	if err := pool.QueryRow(ctx, `SELECT context->>'head_sha' FROM agent_task_queue WHERE id=$1`, root.ID).Scan(&head); err != nil || head != "review" {
		t.Fatal("snapshot overwrote review context")
	}
	// The same admission helper is used for manual rerun and delegation.
	if _, err := svc.budgetTaskCreate(ctx, nil, agentUUID, []pgtype.UUID{root.ID}, create); !errors.Is(err, ErrTaskBudgetExhausted) {
		t.Fatalf("rerun bypassed limit: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='failed',failure_reason='max_turns',issue_id=$2 WHERE id=$1`, root.ID, issueID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = svc.createBudgetRetry(ctx, q.WithTx(tx), db.CreateRetryTaskParams{ID: root.ID, NewTaskID: dbid.NewV7()})
	if !errors.Is(err, ErrTaskBudgetExhausted) {
		t.Fatalf("retry bypassed scope: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := q.GetAgentTask(ctx, root.ID)
	if err != nil || stored.FailureReason.String != "budget_exhausted" {
		t.Fatalf("no dedicated terminal reason: %s %v", stored.FailureReason.String, err)
	}
}

func TestBudgetUsageSoftGateAndUnknownCost(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	_, _, agentID, _ := seedAttributionFixture(t, pool)
	var runtimeID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id=$1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	agentUUID := util.MustParseUUID(agentID)
	for _, tc := range []struct {
		name, profile              string
		input, output, read, write int64
		cost                       pgtype.Int8
		deny                       bool
	}{
		{"all token categories", `{"max_total_tokens":100}`, 10, 20, 30, 40, pgtype.Int8{}, true},
		{"known cost", `{"max_cost_usd_ticks":50}`, 0, 0, 0, 0, pgtype.Int8{Int64: 50, Valid: true}, true},
		{"unknown cost", `{"max_cost_usd_ticks":50}`, 1, 0, 0, 0, pgtype.Int8{}, true},
		{"below limit", `{"max_total_tokens":101}`, 10, 20, 30, 40, pgtype.Int8{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `UPDATE agent SET runtime_config=jsonb_build_object('budget_profile',$2::jsonb) WHERE id=$1`, agentID, tc.profile); err != nil {
				t.Fatal(err)
			}
			svc := NewTaskService(q, pool, nil, events.New())
			create := func(q *db.Queries) (db.AgentTaskQueue, error) {
				return q.CreateAgentTask(ctx, db.CreateAgentTaskParams{ID: dbid.NewV7(), AgentID: agentUUID, RuntimeID: runtimeID})
			}
			root, err := svc.budgetTaskCreate(ctx, nil, agentUUID, nil, create)
			if err != nil {
				t.Fatal(err)
			}
			if err := q.UpsertTaskUsage(ctx, db.UpsertTaskUsageParams{TaskID: root.ID, Provider: "fake", Model: "m", InputTokens: tc.input, OutputTokens: tc.output, CacheReadTokens: tc.read, CacheWriteTokens: tc.write, CostUsdTicks: tc.cost}); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='completed' WHERE id=$1`, root.ID); err != nil {
				t.Fatal(err)
			}
			_, err = svc.budgetTaskCreate(ctx, nil, agentUUID, []pgtype.UUID{root.ID}, create)
			denied := errors.Is(err, ErrTaskBudgetExhausted) || errors.Is(err, ErrTaskBudgetUsageUnknown)
			if denied != tc.deny || (err != nil && !denied) {
				t.Fatalf("denied=%v err=%v", tc.deny, err)
			}
		})
	}
}

func TestBudgetFailTaskAndManualRerunCannotRevive(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	_, userID, agentID, issueID := seedAttributionFixture(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE agent SET runtime_config='{"budget_profile":{"max_runs":1}}' WHERE id=$1`, agentID); err != nil {
		t.Fatal(err)
	}
	svc := NewTaskService(q, pool, nil, events.New())
	issue, err := q.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	root, err := svc.EnqueueTaskForIssue(ctx, issue)
	if err != nil {
		t.Fatal(err)
	}
	// Admission must refuse before the rerun cancels the existing queue head.
	_, err = svc.RerunIssue(ctx, issue.ID, pgtype.UUID{}, pgtype.UUID{}, util.MustParseUUID(userID), nil)
	if !errors.Is(err, ErrTaskBudgetExhausted) {
		t.Fatalf("source-less rerun bypassed scope: %v", err)
	}
	stillQueued, err := q.GetAgentTask(ctx, root.ID)
	if err != nil || stillQueued.Status != "queued" {
		t.Fatal("inadmissible rerun cancelled pending work")
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='running', started_at=now() WHERE id=$1`, root.ID); err != nil {
		t.Fatal(err)
	}
	failed, err := svc.FailTask(ctx, root.ID, "execution turn cap", "partial-session", "/tmp/recoverable-work", "", "max_turns", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if failed.FailureReason.String != "budget_exhausted" || failed.WorkDir.String != "/tmp/recoverable-work" || failed.SessionID.String != "partial-session" {
		t.Fatalf("terminal scope or recovery info lost: %+v", failed)
	}
	if retried, err := svc.MaybeRetryFailedTask(ctx, *failed); err != nil || retried != nil {
		t.Fatal("scope revived by sweeper")
	}
	if _, err := svc.RerunIssue(ctx, issue.ID, root.ID, pgtype.UUID{}, util.MustParseUUID(userID), nil); !errors.Is(err, ErrTaskBudgetExhausted) {
		t.Fatalf("explicit rerun revived scope: %v", err)
	}
}

func TestBudgetRetryInheritsAndLateUsageClosesNextAdmission(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	_, _, agentID, issueID := seedAttributionFixture(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE agent SET runtime_config='{"budget_profile":{"max_runs":3,"max_total_tokens":100}}' WHERE id=$1`, agentID); err != nil {
		t.Fatal(err)
	}
	svc := NewTaskService(q, pool, nil, events.New())
	issue, err := q.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	root, err := svc.EnqueueTaskForIssue(ctx, issue)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertTaskUsage(ctx, db.UpsertTaskUsageParams{TaskID: root.ID, Provider: "fake", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='failed', failure_reason='max_turns', max_attempts=3 WHERE id=$1`, root.ID); err != nil {
		t.Fatal(err)
	}
	parent, err := q.GetAgentTask(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := svc.MaybeRetryFailedTask(ctx, parent)
	if err != nil || child == nil {
		t.Fatalf("retry not admitted: %v", err)
	}
	rootScope, _ := taskbudget.FromContext(root.Context)
	childScope, _ := taskbudget.FromContext(child.Context)
	if childScope.ScopeID != rootScope.ScopeID {
		t.Fatal("automatic retry reset scope")
	}
	// The late report cannot stop already admitted work, but closes the next gate.
	if err := q.UpsertTaskUsage(ctx, db.UpsertTaskUsageParams{TaskID: root.ID, Provider: "fake", Model: "m", InputTokens: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='failed', failure_reason='max_turns' WHERE id=$1`, child.ID); err != nil {
		t.Fatal(err)
	}
	parent, err = q.GetAgentTask(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next, err := svc.MaybeRetryFailedTask(ctx, parent); err != nil || next != nil {
		t.Fatalf("late usage bypassed: %+v %v", next, err)
	}
	stored, err := q.GetAgentTask(ctx, child.ID)
	if err != nil || stored.FailureReason.String != "budget_exhausted" {
		t.Fatal("sweeper failed to commit scope stop")
	}
}

func TestBudgetUnknownUsageAllowsExplicitRetryAfterReport(t *testing.T) {
	for _, field := range []string{"max_total_tokens", "max_cost_usd_ticks"} {
		t.Run(field, func(t *testing.T) {
			pool := newResolveOriginatorPool(t)
			ctx := context.Background()
			q := db.New(pool)
			_, userID, agentID, issueID := seedAttributionFixture(t, pool)
			if _, err := pool.Exec(ctx, `UPDATE agent SET runtime_config=jsonb_build_object('budget_profile', jsonb_build_object($2::text, 100)) WHERE id=$1`, agentID, field); err != nil {
				t.Fatal(err)
			}
			svc := NewTaskService(q, pool, nil, events.New())
			issue, err := q.GetIssue(ctx, util.MustParseUUID(issueID))
			if err != nil {
				t.Fatal(err)
			}
			root, err := svc.EnqueueTaskForIssue(ctx, issue)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='running',started_at=now() WHERE id=$1`, root.ID); err != nil {
				t.Fatal(err)
			}
			failed, err := svc.FailTask(ctx, root.ID, "execution stop", "session", "/tmp/work", "", "max_turns", false, "", "")
			if err != nil || failed.FailureReason.String != "max_turns" {
				t.Fatalf("unknown usage permanently closed scope: %+v %v", failed, err)
			}
			if _, _, err := svc.RetryRun(ctx, issue.ID, root.ID, util.MustParseUUID(userID), nil); !errors.Is(err, ErrTaskBudgetUsageUnknown) {
				t.Fatalf("missing usage allowed: %v", err)
			}
			if field == "max_cost_usd_ticks" {
				if err := q.UpsertTaskUsage(ctx, db.UpsertTaskUsageParams{TaskID: root.ID, Provider: "fake", Model: "m", InputTokens: 1}); err != nil {
					t.Fatal(err)
				}
				if retried, err := svc.MaybeRetryFailedTask(ctx, *failed); err != nil || retried != nil {
					t.Fatalf("unknown-cost retry escaped gate: %+v %v", retried, err)
				}
				stored, err := q.GetAgentTask(ctx, root.ID)
				if err != nil || stored.FailureReason.String != "max_turns" {
					t.Fatal("NULL cost permanently exhausted scope")
				}
				if _, _, err := svc.RetryRun(ctx, issue.ID, root.ID, util.MustParseUUID(userID), nil); !errors.Is(err, ErrTaskBudgetUsageUnknown) {
					t.Fatalf("NULL cost allowed: %v", err)
				}
			}
			if err := q.UpsertTaskUsage(ctx, db.UpsertTaskUsageParams{TaskID: root.ID, Provider: "fake", Model: "m", InputTokens: 1, CostUsdTicks: pgtype.Int8{Int64: 1, Valid: true}}); err != nil {
				t.Fatal(err)
			}
			child, created, err := svc.RetryRun(ctx, issue.ID, root.ID, util.MustParseUUID(userID), nil)
			if err != nil || !created || child == nil {
				t.Fatalf("late report did not release explicit retry: %+v %v", child, err)
			}
			a, _ := taskbudget.FromContext(root.Context)
			b, _ := taskbudget.FromContext(child.Context)
			if a.ScopeID != b.ScopeID {
				t.Fatal("retry reset scope")
			}
		})
	}
}

func TestBudgetConcurrentRerunLastReservationKeepsWinner(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	_, userID, agentID, issueID := seedAttributionFixture(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE agent SET runtime_config='{"budget_profile":{"max_runs":2}}' WHERE id=$1`, agentID); err != nil {
		t.Fatal(err)
	}
	svc := NewTaskService(q, pool, nil, events.New())
	issue, err := q.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	root, err := svc.EnqueueTaskForIssue(ctx, issue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status='failed',failure_reason='max_turns' WHERE id=$1`, root.ID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.RerunIssue(ctx, issue.ID, root.ID, pgtype.UUID{}, util.MustParseUUID(userID), nil)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, denied := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrTaskBudgetExhausted) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || denied != 1 {
		t.Fatalf("accepted=%d denied=%d", accepted, denied)
	}
	var queued, cancelled int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='queued'), count(*) FILTER (WHERE status='cancelled') FROM agent_task_queue WHERE issue_id=$1`, issue.ID).Scan(&queued, &cancelled); err != nil {
		t.Fatal(err)
	}
	if queued != 1 || cancelled != 0 {
		t.Fatalf("losing rerun cancelled winner: queued=%d cancelled=%d", queued, cancelled)
	}
}

func TestBudgetAutopilotRootEntryFreezesProfile(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, userID, agentID, _ := seedAttributionFixture(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE agent SET runtime_config='{"budget_profile":{"max_runs":2,"per_run_timeout_seconds":7}}' WHERE id=$1`, agentID); err != nil {
		t.Fatal(err)
	}
	autopilotID, runID := seedRunOnlyAutopilot(t, pool, workspaceID, agentID, userID)
	ap, err := q.GetAutopilot(ctx, util.MustParseUUID(autopilotID))
	if err != nil {
		t.Fatal(err)
	}
	run, err := q.GetAutopilotRun(ctx, util.MustParseUUID(runID))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewAutopilotService(q, pool, events.New(), NewTaskService(q, pool, nil, events.New()))
	if err := svc.dispatchRunOnly(ctx, ap, &run, util.MustParseUUID(userID)); err != nil {
		t.Fatal(err)
	}
	task, err := q.GetAgentTask(ctx, run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := taskbudget.FromContext(task.Context)
	if err != nil || snapshot == nil || snapshot.SchemaVersion != 1 || snapshot.Profile.PerRunTimeoutSeconds != 7 || task.IssueID.Valid {
		t.Fatalf("missing autopilot budget: %s %v", task.Context, err)
	}
}

type budgetFailCreateStarter struct{ TxStarter }

func (s budgetFailCreateStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return budgetFailCreateTx{Tx: tx}, nil
}

type budgetFailCreateTx struct{ pgx.Tx }
type budgetFailedRow struct{}

func (budgetFailedRow) Scan(...any) error {
	return errors.New("injected budget rerun creation failure")
}
func (tx budgetFailCreateTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "-- name: CreateAgentTask :one") {
		return budgetFailedRow{}
	}
	return tx.Tx.QueryRow(ctx, sql, args...)
}

func TestBudgetRerunCreateFailureRollsBackCancellation(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	_, userID, agentID, issueID := seedAttributionFixture(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE agent SET runtime_config='{"budget_profile":{"max_runs":2}}' WHERE id=$1`, agentID); err != nil {
		t.Fatal(err)
	}
	svc := NewTaskService(q, pool, nil, events.New())
	issue, err := q.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	root, err := svc.EnqueueTaskForIssue(ctx, issue)
	if err != nil {
		t.Fatal(err)
	}
	svc.TxStarter = budgetFailCreateStarter{TxStarter: pool}
	if _, err := svc.RerunIssue(ctx, issue.ID, root.ID, pgtype.UUID{}, util.MustParseUUID(userID), nil); err == nil {
		t.Fatal("expected create failure")
	}
	stored, err := q.GetAgentTask(ctx, root.ID)
	if err != nil || stored.Status != "queued" {
		t.Fatalf("failed replacement committed cancellation: %+v %v", stored, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, issue.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed rerun left replacement row")
	}
}
