package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/retrospective"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// JobNamePromptRetrospective is the daily retrospective's audit name
// (RUYI-305 E3). Stable across releases — do not rename without a migration.
const JobNamePromptRetrospective = "prompt_retrospective"

// RetrospectiveJob returns the JobSpec for the daily retrospective. resolve
// is the per-workspace LLM resolver (RUYI-552): the UI-saved workspace
// config first, the deployment env defaults as fallback — built by the API
// server, so the scheduler never imports pkg/llm.
//
// The cadence is a day because the product's own concept is 每日总复盘 —
// one pass per workspace per day over the completed-issue window. A missed
// day recovers on the next tick (CatchUpLatestOnly): the per-issue watermark
// defines what has been analyzed, not the plan grid, so replaying a missed
// plan would only re-derive the same remaining issues.
//
// RunTimeout is generous because one tick can analyze up to
// retrospective.IssuesPerRun issues, each bounded by a per-issue LLM call.
func RetrospectiveJob(pool *pgxpool.Pool, resolve retrospective.LLMResolver) JobSpec {
	runner := &retrospective.Runner{
		DB:      pool,
		Queries: db.New(pool),
	}
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
		Handler: makeRetrospectiveHandler(runner, resolve),
	}
}

// makeRetrospectiveHandler enumerates the enabled per-workspace configs and
// runs one pass per workspace. A workspace without a config (or with the
// feature off) is a silent skip: ErrNoConfig is the disabled state, not a
// failure.
func makeRetrospectiveHandler(runner *retrospective.Runner, resolve retrospective.LLMResolver) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		configs, err := runner.Queries.ListEnabledRetrospectiveConfigs(ctx)
		if err != nil {
			return HandlerResult{}, fmt.Errorf("retrospective: list configs: %w", err)
		}

		result := map[string]any{}
		var rows int64
		for _, cfg := range configs {
			wsID := util.UUIDToString(cfg.WorkspaceID)
			// Per-workspace resolution (RUYI-552): the workspace's UI-saved
			// LLM config or the deployment fallback; nil client = the run
			// records the disabled state. The loop is sequential, so reusing
			// the one runner is safe.
			client, redact := resolve(ctx, wsID)
			runner.LLM = client
			runner.Redact = redact
			stats, err := runner.RunWorkspace(ctx, wsID, "schedule")
			if err != nil {
				if errors.Is(err, retrospective.ErrNoConfig) {
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
			rows += int64(stats.IssuesAnalyzed)
			result[wsID] = stats
		}
		return HandlerResult{RowsAffected: rows, Result: result}, nil
	}
}
