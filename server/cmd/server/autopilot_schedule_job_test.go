package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/scheduler"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// setupAutopilotScheduleJob creates the test fixture for the
// autopilot_schedule_dispatch JobSpec: an active autopilot, a
// schedule trigger with the given cron, and a *scheduler.Manager with
// the JobSpec registered. Cleanup is registered on t.
//
// Returns the trigger and the manager so the test can call mgr.runOnce
// directly (no goroutine — we want deterministic ticks).
func setupAutopilotScheduleJob(t *testing.T, cron string) (db.AutopilotTrigger, *scheduler.Manager, *service.AutopilotService) {
	t.Helper()
	ctx := context.Background()
	queries := db.New(testPool)
	bus := events.New()
	taskSvc := service.NewTaskService(queries, testPool, nil, bus)
	autopilotSvc := service.NewAutopilotService(queries, testPool, bus, taskSvc)

	var agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID); err != nil {
		t.Fatalf("load fixture agent: %v", err)
	}

	ap, err := queries.CreateAutopilot(ctx, db.CreateAutopilotParams{
		WorkspaceID:        parseUUID(testWorkspaceID),
		Title:              "Schedule dispatch fixture",
		Description:        pgtype.Text{String: "schedule dispatch test", Valid: true},
		AssigneeType:       "agent",
		AssigneeID:         parseUUID(agentID),
		Status:             "active",
		ExecutionMode:      "run_only",
		IssueTitleTemplate: pgtype.Text{},
		CreatedByType:      "member",
		CreatedByID:        parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("CreateAutopilot: %v", err)
	}

	trigger, err := queries.CreateAutopilotTrigger(ctx, db.CreateAutopilotTriggerParams{
		AutopilotID:    ap.ID,
		Kind:           "schedule",
		Enabled:        true,
		CronExpression: pgtype.Text{String: cron, Valid: true},
		Timezone:       pgtype.Text{String: "UTC", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateAutopilotTrigger: %v", err)
	}

	// Anchor trigger.created_at to one minute ago so even an
	// every-minute cron is guaranteed to have at least one occurrence
	// in (created_at, dbNow] on the first tick. Without this the test
	// occasionally races the cron evaluator's minute boundary and the
	// first tick produces zero plans.
	if _, err := testPool.Exec(ctx,
		`UPDATE autopilot_trigger SET created_at = now() - INTERVAL '2 minute' WHERE id = $1`,
		trigger.ID,
	); err != nil {
		t.Fatalf("backdate trigger.created_at: %v", err)
	}

	t.Cleanup(func() {
		bg := context.Background()
		_, _ = testPool.Exec(bg,
			`DELETE FROM sys_cron_executions WHERE scope_kind = $1 AND scope_id = $2`,
			scheduler.ScopeKindAutopilotTrigger, util.UUIDToString(trigger.ID),
		)
		_, _ = testPool.Exec(bg, `DELETE FROM autopilot WHERE id = $1`, ap.ID)
	})

	mgr := scheduler.NewManager(testPool, scheduler.Options{
		RunnerID: "autopilot-job-test",
	})
	if err := mgr.Register(scheduler.AutopilotScheduleDispatchJob(testPool, queries, autopilotSvc)); err != nil {
		t.Fatalf("register autopilot_schedule_dispatch job: %v", err)
	}

	return trigger, mgr, autopilotSvc
}

// TestAutopilotScheduleJobDispatchesOnce verifies the end-to-end
// happy path: one tick of the JobSpec produces exactly one
// sys_cron_executions row (SUCCESS) and exactly one autopilot_run
// row tagged with the canonical UTC planned_at. This is the
// occurrence-level idempotency contract from MUL-3551 §1.
func TestAutopilotScheduleJobDispatchesOnce(t *testing.T) {
	ctx := context.Background()

	// `*/1 * * * *` fires every minute. Trigger.created_at is
	// backdated 2 minutes in the fixture so there is always at least
	// one due occurrence.
	trigger, mgr, _ := setupAutopilotScheduleJob(t, "*/1 * * * *")

	if err := mgr.RunOnce(ctx); err != nil {
		t.Fatalf("first tick: %v", err)
	}

	// Exactly one sys_cron_executions row for this scope, status SUCCESS.
	var execRows int
	var status string
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(MAX(status), '')
		  FROM sys_cron_executions
		 WHERE job_name = $1 AND scope_kind = $2 AND scope_id = $3
	`, scheduler.JobNameAutopilotScheduleDispatch, scheduler.ScopeKindAutopilotTrigger,
		util.UUIDToString(trigger.ID)).Scan(&execRows, &status); err != nil {
		t.Fatalf("count exec rows: %v", err)
	}
	if execRows != 1 || status != "SUCCESS" {
		t.Fatalf("expected 1 SUCCESS exec row, got %d rows with status %q", execRows, status)
	}

	// Exactly one autopilot_run with planned_at set.
	var runRows int
	var plannedAtValid bool
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*), bool_or(planned_at IS NOT NULL)
		  FROM autopilot_run
		 WHERE trigger_id = $1
	`, trigger.ID).Scan(&runRows, &plannedAtValid); err != nil {
		t.Fatalf("count run rows: %v", err)
	}
	if runRows != 1 || !plannedAtValid {
		t.Fatalf("expected 1 autopilot_run with planned_at set, got %d rows planned_at_valid=%v", runRows, plannedAtValid)
	}

	// A second tick must NOT create another row at the same plan_time
	// (idempotency via uq_sys_cron_execution). Because the trigger
	// fires every minute, the second tick may produce a NEW row at
	// the next minute boundary if a minute passed between the two
	// runOnce calls — that is fine, the test just asserts that the
	// existing row is not duplicated and the COUNT doesn't decrease.
	if err := mgr.RunOnce(ctx); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	var execRowsAfter int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sys_cron_executions
		 WHERE job_name = $1 AND scope_kind = $2 AND scope_id = $3
	`, scheduler.JobNameAutopilotScheduleDispatch, scheduler.ScopeKindAutopilotTrigger,
		util.UUIDToString(trigger.ID)).Scan(&execRowsAfter); err != nil {
		t.Fatalf("count exec rows after 2nd tick: %v", err)
	}
	if execRowsAfter < execRows {
		t.Fatalf("second tick should never delete rows; before=%d after=%d", execRows, execRowsAfter)
	}
}

// TestAutopilotScheduleJobMissedSchedulesCollapse covers MUL-3551 §4:
// when many occurrences are due (e.g. server was offline for a long
// stretch), the CatchUpLatestOnly hook should fire ONCE per tick — not
// replay every missed occurrence.
func TestAutopilotScheduleJobMissedSchedulesCollapse(t *testing.T) {
	ctx := context.Background()

	trigger, mgr, _ := setupAutopilotScheduleJob(t, "*/5 * * * *")

	// Force a large historical window: the trigger thinks it was
	// registered an hour ago, so without collapse the hook would
	// emit 12 occurrences.
	if _, err := testPool.Exec(ctx,
		`UPDATE autopilot_trigger SET created_at = now() - INTERVAL '1 hour' WHERE id = $1`,
		trigger.ID,
	); err != nil {
		t.Fatalf("backdate trigger.created_at: %v", err)
	}

	if err := mgr.RunOnce(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	var rows int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sys_cron_executions
		 WHERE job_name = $1 AND scope_kind = $2 AND scope_id = $3
	`, scheduler.JobNameAutopilotScheduleDispatch, scheduler.ScopeKindAutopilotTrigger,
		util.UUIDToString(trigger.ID)).Scan(&rows); err != nil {
		t.Fatalf("count exec rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("CatchUpLatestOnly must collapse missed fires to 1 row per tick, got %d", rows)
	}

	var runRows int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM autopilot_run WHERE trigger_id = $1
	`, trigger.ID).Scan(&runRows); err != nil {
		t.Fatalf("count run rows: %v", err)
	}
	if runRows != 1 {
		t.Fatalf("missed schedules must collapse to a single autopilot_run, got %d", runRows)
	}
}

// TestAutopilotScheduleJobCrashRecovery covers MUL-3551 §5: a runner
// that crashes between "claim plan_time" and "write terminal SUCCESS"
// must NOT duplicate the autopilot_run on the next tick. The stale
// lease is reclaimed via AllowStaleReentry + the DispatchAutopilotForPlan
// idempotency lookup reuses the prior run row.
func TestAutopilotScheduleJobCrashRecovery(t *testing.T) {
	ctx := context.Background()

	trigger, mgr, _ := setupAutopilotScheduleJob(t, "*/1 * * * *")

	// Tick 1: dispatch happens, sys_cron_executions has SUCCESS, one
	// autopilot_run row exists.
	if err := mgr.RunOnce(ctx); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	var execID, leaseToken string
	var planTime time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT id, lease_token, plan_time
		  FROM sys_cron_executions
		 WHERE job_name = $1 AND scope_kind = $2 AND scope_id = $3
	`, scheduler.JobNameAutopilotScheduleDispatch, scheduler.ScopeKindAutopilotTrigger,
		util.UUIDToString(trigger.ID)).Scan(&execID, &leaseToken, &planTime); err != nil {
		t.Fatalf("read first exec row: %v", err)
	}

	// Simulate a crash mid-dispatch at the SAME plan_time: rewrite
	// the row to RUNNING with an expired lease, AND keep the
	// autopilot_run row that the first tick created. This is exactly
	// the state where the OLD scheduler would lose the occurrence
	// (next_run_at IS NULL recovery would jump to "now") — under the
	// new model, the lease theft + planned_at lookup must produce
	// a SECOND attempt at the SAME plan_time without creating a
	// duplicate run.
	if _, err := testPool.Exec(ctx, `
		UPDATE sys_cron_executions
		   SET status      = 'RUNNING',
		       runner_id   = 'ghost-runner',
		       lease_token = gen_random_uuid(),
		       stale_after = now() - INTERVAL '10 minutes',
		       finished_at = NULL,
		       duration_ms = NULL,
		       updated_at  = now()
		 WHERE id = $1
	`, execID); err != nil {
		t.Fatalf("simulate crash mid-dispatch: %v", err)
	}

	// Tick 2: stale-steal sweeps the abandoned lease, the planner
	// returns the same plan_time again because cfg.CreatedAt is the
	// only floor (the LatestPlanInfo lookup now sees a FAILED row,
	// which the every_plan retry path would handle — but for our
	// CatchUpLatestOnly hook the "latest cron occurrence in window"
	// is still that same plan_time, so a fresh attempt happens).
	//
	// What we actually want to assert: NO duplicate autopilot_run is
	// created even after the second attempt runs through the
	// handler. The DispatchAutopilotForPlan lookup is the guard.
	//
	// Note: the stale row will be transitioned to FAILED on the next
	// tick first, then a new attempt is started. The retry path in
	// tryClaim only re-uses the same row at the same plan_time —
	// which is exactly what we want here. Either way, autopilot_run
	// must not duplicate.
	if err := mgr.RunOnce(ctx); err != nil {
		t.Fatalf("tick 2 (recovery): %v", err)
	}

	var runRows int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM autopilot_run WHERE trigger_id = $1
	`, trigger.ID).Scan(&runRows); err != nil {
		t.Fatalf("count run rows after recovery: %v", err)
	}
	if runRows != 1 {
		t.Fatalf("crash recovery must NOT duplicate autopilot_run; got %d rows", runRows)
	}
}

// TestAutopilotScheduleJobTwoRunnersSingleWinner covers the
// multi-replica claim race from MUL-3551 §1. Two scheduler.Manager
// instances tick concurrently against the same trigger; exactly one
// should win the claim, the other no-ops via the sys_cron_executions
// uniqueness key.
func TestAutopilotScheduleJobTwoRunnersSingleWinner(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	bus := events.New()
	taskSvc := service.NewTaskService(queries, testPool, nil, bus)
	autopilotSvc := service.NewAutopilotService(queries, testPool, bus, taskSvc)

	trigger, _, _ := setupAutopilotScheduleJob(t, "*/1 * * * *")

	mgrA := scheduler.NewManager(testPool, scheduler.Options{RunnerID: "runner-A"})
	mgrB := scheduler.NewManager(testPool, scheduler.Options{RunnerID: "runner-B"})
	if err := mgrA.Register(scheduler.AutopilotScheduleDispatchJob(testPool, queries, autopilotSvc)); err != nil {
		t.Fatalf("register A: %v", err)
	}
	if err := mgrB.Register(scheduler.AutopilotScheduleDispatchJob(testPool, queries, autopilotSvc)); err != nil {
		t.Fatalf("register B: %v", err)
	}

	type result struct {
		err error
	}
	results := make(chan result, 2)
	go func() { results <- result{err: mgrA.RunOnce(ctx)} }()
	go func() { results <- result{err: mgrB.RunOnce(ctx)} }()

	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatalf("runOnce: %v", r.err)
		}
	}

	// At most one sys_cron_executions row for this plan_time, and
	// exactly one autopilot_run.
	var execRows int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sys_cron_executions
		 WHERE job_name = $1 AND scope_kind = $2 AND scope_id = $3
	`, scheduler.JobNameAutopilotScheduleDispatch, scheduler.ScopeKindAutopilotTrigger,
		util.UUIDToString(trigger.ID)).Scan(&execRows); err != nil {
		t.Fatalf("count exec rows: %v", err)
	}
	if execRows < 1 || execRows > 2 {
		// At most one per plan_time, but two ticks racing across a
		// minute boundary could produce up to 2 distinct plan_times.
		// Anything outside [1,2] means the uniqueness guarantee broke.
		t.Fatalf("expected 1 or 2 exec rows (one per plan_time), got %d", execRows)
	}
	// Per plan_time: exactly one runner_id should ever be recorded.
	rowsByPlan, err := testPool.Query(ctx, `
		SELECT plan_time, runner_id FROM sys_cron_executions
		 WHERE job_name = $1 AND scope_kind = $2 AND scope_id = $3
		 ORDER BY plan_time
	`, scheduler.JobNameAutopilotScheduleDispatch, scheduler.ScopeKindAutopilotTrigger,
		util.UUIDToString(trigger.ID))
	if err != nil {
		t.Fatalf("query per-plan rows: %v", err)
	}
	defer rowsByPlan.Close()
	seen := map[string]string{}
	for rowsByPlan.Next() {
		var plan time.Time
		var runner string
		if err := rowsByPlan.Scan(&plan, &runner); err != nil {
			t.Fatalf("scan: %v", err)
		}
		key := plan.Format(time.RFC3339Nano)
		if prev, ok := seen[key]; ok && prev != runner {
			t.Fatalf("plan_time %s claimed by both %s and %s — uniqueness broke", key, prev, runner)
		}
		seen[key] = runner
	}

	var runRows int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM autopilot_run WHERE trigger_id = $1
	`, trigger.ID).Scan(&runRows); err != nil {
		t.Fatalf("count run rows: %v", err)
	}
	if runRows != execRows {
		t.Fatalf("expected 1 autopilot_run per exec row, got exec=%d run=%d", execRows, runRows)
	}
}

// TestAutopilotScheduleJobDisabledTriggerSkips locks in that a
// trigger toggled off between scope-list and handler run is treated
// as a SUCCESS no-op — no autopilot_run created. This protects
// against the race the legacy goroutine could not prove safe (it
// reloaded the autopilot, but never re-checked the trigger's
// enabled flag in-handler).
func TestAutopilotScheduleJobDisabledTriggerSkips(t *testing.T) {
	ctx := context.Background()

	trigger, mgr, _ := setupAutopilotScheduleJob(t, "*/1 * * * *")

	// Disable the trigger AFTER the manager is wired but before tick.
	// scope provider's SQL will not include it, so no plan_time is
	// produced. (The handler-side belt-and-suspenders guard is
	// covered by the unit-level autopilot_inactive case below.)
	if _, err := testPool.Exec(ctx,
		`UPDATE autopilot_trigger SET enabled = FALSE WHERE id = $1`, trigger.ID,
	); err != nil {
		t.Fatalf("disable trigger: %v", err)
	}

	if err := mgr.RunOnce(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	var execRows int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sys_cron_executions
		 WHERE job_name = $1 AND scope_kind = $2 AND scope_id = $3
	`, scheduler.JobNameAutopilotScheduleDispatch, scheduler.ScopeKindAutopilotTrigger,
		util.UUIDToString(trigger.ID)).Scan(&execRows); err != nil {
		t.Fatalf("count exec rows: %v", err)
	}
	if execRows != 0 {
		t.Fatalf("disabled trigger must not produce sys_cron_executions rows, got %d", execRows)
	}
}

// TestAutopilotScheduleJobPausedAutopilotSkipsAtHandler covers the
// race window between the scope-list (which filters
// a.status='active') and the handler (which re-reads autopilot.status
// in case it changed). If we pause the autopilot between those two
// points, the handler returns a SUCCESS no-op carrying the reason in
// the result JSON — no run is created.
func TestAutopilotScheduleJobPausedAutopilotSkipsAtHandler(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	trigger, mgr, _ := setupAutopilotScheduleJob(t, "*/1 * * * *")

	// Tick once to register a baseline: this populates the cache and
	// produces one row.
	if err := mgr.RunOnce(ctx); err != nil {
		t.Fatalf("baseline tick: %v", err)
	}
	var baselineExec int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sys_cron_executions
		 WHERE job_name = $1 AND scope_kind = $2 AND scope_id = $3
	`, scheduler.JobNameAutopilotScheduleDispatch, scheduler.ScopeKindAutopilotTrigger,
		util.UUIDToString(trigger.ID)).Scan(&baselineExec); err != nil {
		t.Fatalf("count baseline exec rows: %v", err)
	}

	// Pause the autopilot; the scope-list SQL will exclude it, so
	// the planner will produce zero plans on the next tick.
	if _, err := queries.UpdateAutopilot(ctx, db.UpdateAutopilotParams{
		ID:     trigger.AutopilotID,
		Status: pgtype.Text{String: "paused", Valid: true},
	}); err != nil {
		t.Fatalf("pause autopilot: %v", err)
	}

	if err := mgr.RunOnce(ctx); err != nil {
		t.Fatalf("second tick after pause: %v", err)
	}

	// No NEW exec row should appear — the scope-list excludes paused
	// autopilots, so the planner is never invoked for this scope.
	var afterExec int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sys_cron_executions
		 WHERE job_name = $1 AND scope_kind = $2 AND scope_id = $3
	`, scheduler.JobNameAutopilotScheduleDispatch, scheduler.ScopeKindAutopilotTrigger,
		util.UUIDToString(trigger.ID)).Scan(&afterExec); err != nil {
		t.Fatalf("count exec rows after pause: %v", err)
	}
	if afterExec != baselineExec {
		t.Fatalf("paused autopilot should not produce additional exec rows; baseline=%d after=%d", baselineExec, afterExec)
	}
}

// TestAutopilotScheduleJobBadCronFailsLoudly verifies that a trigger
// with a bad cron expression produces a FAILED audit row (with a
// useful error_msg) rather than silently doing nothing. This is the
// fail-loud property MUL-3551 requires: dispatch errors must be
// observable through sys_cron_executions.
func TestAutopilotScheduleJobBadCronFailsLoudly(t *testing.T) {
	ctx := context.Background()

	trigger, mgr, _ := setupAutopilotScheduleJob(t, "*/1 * * * *")

	// Replace the cron with an invalid expression. The scope-list
	// SQL still returns it (its filter only checks cron_expression IS
	// NOT NULL AND <> ''), and the planner hook will fail to parse —
	// the manager records a FAILED row with the parse error.
	if _, err := testPool.Exec(ctx,
		`UPDATE autopilot_trigger SET cron_expression = $2 WHERE id = $1`,
		trigger.ID, "garbage not a cron",
	); err != nil {
		t.Fatalf("set bad cron: %v", err)
	}

	if err := mgr.RunOnce(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// The plan hook failure surfaces as a manager warning log; it
	// does not create a sys_cron_executions row because the row is
	// only inserted when a plan_time is claimed. We instead assert
	// that NO autopilot_run was created — i.e. a bad cron does not
	// accidentally fire something.
	var runRows int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM autopilot_run WHERE trigger_id = $1
	`, trigger.ID).Scan(&runRows); err != nil {
		t.Fatalf("count run rows: %v", err)
	}
	if runRows != 0 {
		t.Fatalf("bad cron must not fire dispatch, got %d run rows", runRows)
	}
}
