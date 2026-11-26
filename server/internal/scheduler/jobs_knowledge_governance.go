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

// KnowledgeGovernanceJob evaluates due workspace-local schedule windows every
// hour. Runner.ListScheduleTargets enforces each persisted daily/weekly/monthly
// bucket, keeping the scheduler lease durable without coupling to retrospective.
func KnowledgeGovernanceJob(runner *knowledgegovernance.Runner) JobSpec {
	return JobSpec{
		Name:              JobNameKnowledgeGovernance,
		Cadence:           time.Hour,
		ScheduleDelay:     5 * time.Minute,
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
		completedWorkspaces := make(map[string]knowledgegovernance.ScheduleTarget)
		blockedWorkspaces := make(map[string]bool)
		var rows int64
		for _, target := range targets {
			stats, err := runner.RunWorkspace(ctx, target.WorkspaceID, target.DirID, target.ActorID, "schedule")
			switch {
			case errors.Is(err, knowledgegovernance.ErrNoExecutor):
				blockedWorkspaces[target.WorkspaceID] = true
				result[target.DirID] = map[string]any{"status": "skipped", "reason": "agent_not_configured"}
			case errors.Is(err, knowledgegovernance.ErrScopeRunning):
				blockedWorkspaces[target.WorkspaceID] = true
				result[target.DirID] = map[string]any{"status": "skipped", "reason": "scope_running"}
			case err != nil:
				blockedWorkspaces[target.WorkspaceID] = true
				slog.Warn("knowledge governance workspace pass failed",
					"workspace_id", target.WorkspaceID, "directory_id", target.DirID, "error", err)
				result[target.DirID] = map[string]any{"status": "failed", "error": err.Error()}
			default:
				rows++
				completedWorkspaces[target.WorkspaceID] = target
				result[target.DirID] = stats
			}
		}
		for workspaceID, target := range completedWorkspaces {
			if blockedWorkspaces[workspaceID] {
				continue
			}
			if err := runner.MarkScheduleBucket(ctx, workspaceID, target.Cadence, target.Timezone); err != nil {
				slog.Warn("knowledge governance schedule bucket update failed", "workspace_id", workspaceID, "error", err)
			}
		}
		return HandlerResult{RowsAffected: rows, Result: result}, nil
	}
}
