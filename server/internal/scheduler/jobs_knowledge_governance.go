package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/knowledgegovernance"
)

// JobNameKnowledgeGovernance is the durable scheduler audit name for periodic
// evidence-led reviews. Keep it stable once deployed.
const JobNameKnowledgeGovernance = "knowledge_governance"

// KnowledgeGovernanceJob reviews each live mirrored knowledge directory daily.
// It reuses the platform scheduler's durable leases and retry records; the
// agent work itself remains a normal asynchronous task owned by its run row.
func KnowledgeGovernanceJob(runner *knowledgegovernance.Runner) JobSpec {
	return JobSpec{
		Name:              JobNameKnowledgeGovernance,
		Cadence:           24 * time.Hour,
		ScheduleDelay:     30 * time.Minute,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     7 * 24 * time.Hour,
		RunTimeout:        30 * time.Minute,
		StaleTimeout:      45 * time.Minute,
		HeartbeatInterval: time.Minute,
		AllowStaleReentry: true,
		MaxAttempts:       2,
		RetryBackoff: []time.Duration{
			15 * time.Minute,
		},
		Scopes:  StaticScopes(ScopeGlobal),
		Handler: makeKnowledgeGovernanceHandler(runner),
	}
}

func makeKnowledgeGovernanceHandler(runner *knowledgegovernance.Runner) Handler {
	return func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
		if reconciled, err := runner.ReconcileStaleRuns(ctx); err != nil {
			slog.Warn("knowledge governance: stale-run reconciliation failed", "error", err)
		} else if reconciled > 0 {
			slog.Info("knowledge governance: reconciled stale runs", "count", reconciled)
		}

		targets, err := runner.ListScheduleTargets(ctx)
		if err != nil {
			return HandlerResult{}, fmt.Errorf("knowledge governance: list schedule targets: %w", err)
		}

		result := make(map[string]any, len(targets))
		var rows int64
		for _, target := range targets {
			stats, err := runner.RunWorkspace(ctx, target.WorkspaceID, target.DirID, target.ActorID, "schedule")
			switch {
			case errors.Is(err, knowledgegovernance.ErrNoExecutor):
				result[target.DirID] = map[string]any{"status": "skipped", "reason": "agent_not_configured"}
			case errors.Is(err, knowledgegovernance.ErrScopeRunning):
				result[target.DirID] = map[string]any{"status": "skipped", "reason": "scope_running"}
			case err != nil:
				slog.Warn("knowledge governance workspace pass failed",
					"workspace_id", target.WorkspaceID, "directory_id", target.DirID, "error", err)
				result[target.DirID] = map[string]any{"status": "failed", "error": err.Error()}
			default:
				rows++
				result[target.DirID] = stats
			}
		}
		return HandlerResult{RowsAffected: rows, Result: result}, nil
	}
}
