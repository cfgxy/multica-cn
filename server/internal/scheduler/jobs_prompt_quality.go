package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/promptqualityrollup"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptperplexity"
)

// JobNameRollupPromptQuality is the canonical name used in audit rows. Stable
// across releases — do not rename without a migration.
const JobNameRollupPromptQuality = "rollup_prompt_quality"

// PromptQualityJob returns the JobSpec driving the prompt_quality_daily rollup
// (RUYI-184).
//
// The cadence mirrors the task_usage_hourly rollup because the two read the
// same run stream and there is no reason for the quality dashboard to lag the
// usage one. CatchUpLatestOnly is correct for the same reason it is there:
// history is driven by the handler's own watermark, not by replaying missed
// ticks, and each tick is bounded by promptqualityrollup.BucketLimit so a long
// backfill walks forward a few ticks rather than in one run.
func PromptQualityJob(pool *pgxpool.Pool, gen promptperplexity.Generator) JobSpec {
	return JobSpec{
		Name:              JobNameRollupPromptQuality,
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
		Handler: makePromptQualityHandler(pool, gen),
	}
}

func makePromptQualityHandler(pool *pgxpool.Pool, gen promptperplexity.Generator) Handler {
	queries := db.New(pool)
	runner := promptqualityrollup.Runner{Queries: queries}
	perplexity := promptqualityrollup.Perplexity{Queries: queries, Generator: gen}
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		out, err := runner.Run(ctx)
		if err != nil {
			return HandlerResult{}, fmt.Errorf("rollup prompt quality: %w", err)
		}

		if in.Heartbeat != nil {
			_ = in.Heartbeat(ctx)
		}

		// D3 rides the same tick because it is driven by what the rollup just
		// wrote: a version only becomes worth scoring once it has finished
		// runs behind it. Its failure does not fail the tick — the six
		// count-based dimensions are already persisted, and a version that
		// could not be scored stays unscored, which is a state the dashboard
		// renders rather than an error it has to report.
		backlog, scoreErr := perplexity.ScoreBacklog(ctx)

		result := map[string]any{"bucket_limit": promptqualityrollup.BucketLimit}
		result["perplexity_considered"] = backlog.Considered
		result["perplexity_scored"] = backlog.Scored
		result["perplexity_failed"] = backlog.Failed
		if scoreErr != nil {
			result["perplexity_error"] = scoreErr.Error()
		}
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
