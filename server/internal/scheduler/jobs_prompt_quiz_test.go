package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPromptQuizSweepIsSerialisedAcrossReplicas is the concurrency-safety half
// of RUYI-185 acceptance 7.
//
// The sweep orders agent runs, so two replicas ticking the same plan_time would
// order the same remainder twice and push the sample past N. Serialisation is
// not the handler's job: it comes from the scheduler's uniqueness key on
// (job_name, scope_kind, scope_id, plan_time) — the same single-winner property
// concurrent_claim_test.go establishes for the usage rollup. This test pins that
// the quiz job's own spec participates in it, by racing replicas at one plan
// time and asserting the handler ran exactly once.
func TestPromptQuizSweepIsSerialisedAcrossReplicas(t *testing.T) {
	pool := integrationPool(t)

	// The real spec, with a per-test name so a CI run does not collide with the
	// deployment's own audit rows, and a counting handler in place of the sweep
	// (what is under test here is the lease, not what the sweep writes —
	// promptquizsweep's own suite covers that).
	job := PromptQuizJob(pool)
	job.Name = uniqueJobName(t, "prompt_quiz_sweep")
	var runs atomic.Int32
	job.Handler = func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		runs.Add(1)
		return HandlerResult{}, nil
	}
	t.Cleanup(func() { cleanupExecutions(t, pool, job.Name) })

	ctx := context.Background()
	now, err := dbNow(ctx, pool)
	if err != nil {
		t.Fatalf("dbNow: %v", err)
	}
	planTime := FloorPlan(now.Add(-job.ScheduleDelay), job.Cadence)

	const replicas = 6
	claims := make([]claim, replicas)
	errs := make([]error, replicas)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			c, err := tryClaim(ctx, pool, &job, ScopeGlobal, planTime, now, "replica-"+string(rune('A'+i)))
			claims[i], errs[i] = c, err
			if err == nil && (c.Won || c.Stole) {
				_, _ = job.Handler(ctx, HandlerInput{Job: &job, Scope: ScopeGlobal, PlanTime: planTime})
			}
		}()
	}
	close(start)
	wg.Wait()

	won, stole, conflicted := 0, 0, 0
	for i, c := range claims {
		if errs[i] != nil {
			t.Fatalf("replica %d claim: %v", i, errs[i])
		}
		switch {
		case c.Won:
			won++
		case c.Stole:
			stole++
		case c.Conflicted:
			conflicted++
		}
	}
	if won != 1 || stole != 0 || conflicted != replicas-1 {
		t.Fatalf("won=%d stole=%d conflicted=%d, want exactly one winner and %d conflicts",
			won, stole, conflicted, replicas-1)
	}
	if got := runs.Load(); got != 1 {
		t.Errorf("sweep handler ran %d times for one plan, want 1", got)
	}

	rows := 0
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sys_cron_executions WHERE job_name = $1 AND plan_time = $2`,
		job.Name, planTime).Scan(&rows); err != nil {
		t.Fatalf("count executions: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d execution rows for one plan, want 1", rows)
	}
}

// TestPromptQuizSweepDoesNotReplayMissedTicks pins CatchUpLatestOnly on the
// quiz job. The sweep's work is defined by how far each version's sample is
// from N, not by which ticks were missed, so a deployment that was down
// overnight must resume with one tick — replaying twelve would order the same
// remainder twelve times, each tick counting only what the previous one had not
// yet finished.
func TestPromptQuizSweepDoesNotReplayMissedTicks(t *testing.T) {
	job := PromptQuizJob(nil)
	if job.CatchUpMode != CatchUpLatestOnly {
		t.Errorf("catch-up mode is %v, want latest_only", job.CatchUpMode)
	}
	if job.Cadence != time.Hour {
		t.Errorf("cadence is %v, want 1h", job.Cadence)
	}
	// A stale lease must be re-enterable: the sweep is idempotent, so a tick
	// whose process died must not block the next one for a whole cadence.
	if !job.AllowStaleReentry {
		t.Error("stale re-entry is disabled; an abandoned tick would hold the plan")
	}
	if job.StaleTimeout <= job.RunTimeout {
		t.Errorf("stale timeout %v must exceed run timeout %v, or a healthy tick gets stolen",
			job.StaleTimeout, job.RunTimeout)
	}
}
