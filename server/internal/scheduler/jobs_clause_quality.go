package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/clausequalityrollup"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// JobNameRollupClauseQuality is the canonical name used in audit rows. Stable
// across releases — do not rename without a migration.
const JobNameRollupClauseQuality = "rollup_clause_quality"

// ClauseQualityJob returns the JobSpec driving the clause_quality_daily
// rollup (RUYI-751, ADR 007 §7).
//
// It mirrors the prompt quality job's shape on purpose: the two read the same
// run stream (clause buckets are a join of clause_injection onto it), a 5
// minute cadence keeps the governance view honest about injection counts, and
// CatchUpLatestOnly lets a long backfill walk forward a few ticks under
// clausequalityrollup.BucketLimit rather than in one run.
//
// The rollback checker rides the same tick (the D3 precedent): it only sees
// clauses whose buckets just moved, and the per-version comparison is
// latched, so an idle clause costs one latch read per activity burst, not
// per tick.
func ClauseQualityJob(pool *pgxpool.Pool) JobSpec {
	return JobSpec{
		Name:              JobNameRollupClauseQuality,
		Cadence:           5 * time.Minute,
		ScheduleDelay:     5 * time.Minute,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     24 * time.Hour,
		RunTimeout:        25 * time.Minute,
		StaleTimeout:      30 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff: []time.Duration{
			1 * time.Minute,
			5 * time.Minute,
			15 * time.Minute,
		},
		Scopes:  StaticScopes(ScopeGlobal),
		Handler: makeClauseQualityHandler(pool),
	}
}

func makeClauseQualityHandler(pool *pgxpool.Pool) Handler {
	queries := db.New(pool)
	runner := clausequalityrollup.Runner{Queries: queries}
	checker := clausequalityrollup.RollbackChecker{Queries: queries}
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		out, err := runner.Run(ctx)
		if err != nil {
			return HandlerResult{}, fmt.Errorf("rollup clause quality: %w", err)
		}

		if in.Heartbeat != nil {
			_ = in.Heartbeat(ctx)
		}

		// A failed rollback evaluation never fails the tick — the day
		// buckets are already persisted — but it is reported, not swallowed.
		rollback := checker.EvaluateClauses(ctx, out.ClauseIDs)

		result := map[string]any{"bucket_limit": clausequalityrollup.BucketLimit}
		result["rollback_evaluated"] = rollback.Evaluated
		result["rollback_suggested"] = rollback.Suggested
		result["rollback_failures"] = rollback.Failures
		if !out.WatermarkBefore.IsZero() {
			result["watermark_before"] = out.WatermarkBefore.UTC().Format(time.RFC3339)
		}
		if !out.WatermarkAfter.IsZero() {
			result["watermark_after"] = out.WatermarkAfter.UTC().Format(time.RFC3339)
		}
		return HandlerResult{
			RowsAffected: int64(out.Buckets),
			Result:       result,
		}, nil
	}
}
