package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/promptquizsweep"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// JobNamePromptQuizSweep is the canonical name used in audit rows. Stable
// across releases — do not rename without a migration.
const JobNamePromptQuizSweep = "prompt_quiz_sweep"

// PromptQuizJob returns the JobSpec driving the periodic quiz (RUYI-185).
//
// The cadence is an hour rather than the quality rollup's five minutes: a tick
// orders agent runs, and those take minutes each. Ticking faster would only
// stack measurements behind the ones still executing, which the sweep's own
// in-flight ceiling would then refuse — paying for the wake-up to do nothing.
//
// CatchUpLatestOnly for the same reason as the rollup: the work is defined by
// how far each version's stored sample is from N, not by which ticks were
// missed, so replaying a missed tick would re-derive the identical remainder.
// A deployment that was down overnight resumes with one tick, not twelve.
func PromptQuizJob(pool *pgxpool.Pool) JobSpec {
	return JobSpec{
		Name:              JobNamePromptQuizSweep,
		Cadence:           1 * time.Hour,
		ScheduleDelay:     10 * time.Minute,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     24 * time.Hour,
		RunTimeout:        10 * time.Minute,
		StaleTimeout:      20 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff: []time.Duration{
			2 * time.Minute,
			10 * time.Minute,
			30 * time.Minute,
		},
		Scopes:  StaticScopes(ScopeGlobal),
		Handler: makePromptQuizHandler(pool),
	}
}

func makePromptQuizHandler(pool *pgxpool.Pool) Handler {
	runner := promptquizsweep.Runner{Queries: db.New(pool)}
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		out, err := runner.Run(ctx)
		if err != nil {
			return HandlerResult{}, fmt.Errorf("prompt quiz sweep: %w", err)
		}
		if in.Heartbeat != nil {
			_ = in.Heartbeat(ctx)
		}
		return HandlerResult{
			RowsAffected: int64(out.Collected + out.Enqueued),
			Result: map[string]any{
				"collected":      out.Collected,
				"enqueued":       out.Enqueued,
				"enqueue_limit":  promptquizsweep.EnqueueLimit,
				"inflight_limit": promptquizsweep.InFlightLimit,
			},
		}, nil
	}
}
