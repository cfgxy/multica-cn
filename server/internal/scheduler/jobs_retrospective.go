package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/retrospective"
	"github.com/multica-ai/multica/server/internal/util"
)

// JobNamePromptRetrospective is the daily retrospective's audit name
// (RUYI-305 E3). Stable across releases — do not rename without a migration.
const JobNamePromptRetrospective = "prompt_retrospective"

// RetrospectiveJob returns the JobSpec for the daily retrospective. The
// runner carries the wired TaskEnqueuer (RUYI-552 direction 3): each
// workspace's pass enqueues one round of its configured agent's run on the
// platform task queue — the scheduler never touches runtimes or the LLM.
//
// The cadence is a day because the product's own concept is 每日总复盘 —
// one pass per workspace per day over the completed-issue window. A missed
// day recovers on the next tick (CatchUpLatestOnly): the per-issue watermark
// defines what has been analyzed, not the plan grid, so replaying a missed
// plan would only re-derive the same remaining issues.
//
// RunTimeout stays generous because a tick enqueues up to
// retrospective.IssuesPerRun issues' worth of work plus the stale-run
// reconciliation; the agent runs themselves live on the task queue, outside
// this job's lease.
func RetrospectiveJob(runner *retrospective.Runner) JobSpec {
	return JobSpec{
		Name:              JobNamePromptRetrospective,
		Cadence:           24 * time.Hour,
		ScheduleDelay:     15 * time.Minute,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     7 * 24 * time.Hour,
		RunTimeout:        60 * time.Minute,
		StaleTimeout:      90 * time.Minute,
		HeartbeatInterval: time.Minute,
		AllowStaleReentry: true,
		MaxAttempts:       2,
		RetryBackoff: []time.Duration{
			30 * time.Minute,
		},
		Scopes:  StaticScopes(ScopeGlobal),
		Handler: makeRetrospectiveHandler(runner),
	}
}

// makeRetrospectiveHandler enumerates the enabled per-workspace configs and
// runs one pass per workspace. A workspace without a config (or with the
// feature off) is a silent skip: ErrNoConfig is the disabled state, not a
// failure. ErrNoAgent (a config whose agent vanished, or a pre-migration
// row) has already recorded its failed run on the page — also a silent skip
// here.
func makeRetrospectiveHandler(runner *retrospective.Runner) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		configs, err := runner.Queries.ListEnabledRetrospectiveConfigs(ctx)
		if err != nil {
			return HandlerResult{}, fmt.Errorf("retrospective: list configs: %w", err)
		}

		// Bulk backstop first: runs whose task went terminal without the
		// completion hook (offline sweeps, cancels, crashes) and runs whose
		// task never got enqueued. One UPDATE each against a tiny table.
		if reconciled, err := runner.ReconcileStaleRuns(ctx); err != nil {
			slog.Warn("retrospective: stale-run reconciliation failed", "error", err)
		} else if reconciled > 0 {
			slog.Info("retrospective: reconciled stale runs", "count", reconciled)
		}

		result := map[string]any{}
		var rows int64
		for _, cfg := range configs {
			wsID := util.UUIDToString(cfg.WorkspaceID)
			stats, err := runner.RunWorkspace(ctx, wsID, "schedule")
			if err != nil {
				if errors.Is(err, retrospective.ErrNoConfig) || errors.Is(err, retrospective.ErrNoAgent) {
					continue
				}
				// RunWorkspace records the failure in retrospective_run; the
				// job itself succeeds — a broken workspace must not fail the
				// pass over the others, and the page is the only surface.
				slog.Warn("retrospective workspace pass failed",
					"workspace_id", wsID, "error", err)
				result[wsID] = map[string]any{"status": "failed", "error": err.Error()}
				continue
			}
			rows += int64(stats.IssuesScanned)
			result[wsID] = stats
		}
		return HandlerResult{RowsAffected: rows, Result: result}, nil
	}
}
