// Package retrospective is the daily retrospective (RUYI-305 E3, reworked by
// RUYI-552 direction 3): a base-layer scheduled task that triggers one round
// of the configured agent's run over the issues completed inside the
// configured window. The agent reads those issues with its own tools and
// reports Prompt-improvement drafts as one JSON message; the completion side
// (task.go) distills them into the proposal pool.
//
// Hard boundaries:
//
//   - It never writes to issues — no comments, no reports, no
//     notifications. Every outcome lands in prompt_proposal (drafts) and
//     retrospective_run (records). Failures are visible only on the
//     self-evolution retrospective page.
//   - The trigger walks the platform's own task queue: one enqueued run per
//     pass, originator_source='retrospective', trigger evidence pointing at
//     the run row. It never fabricates a run source, and it creates no issue
//     to anchor on.
//   - Idempotency is per-issue (retrospective_issue_watermark): an issue
//     analyzed by any successful run is never analyzed again, so overlapping
//     windows cannot produce duplicate drafts.
//   - Draft dedup is two-layer (task.go): pool merge against a live
//     same-topic draft (evidence anchors accumulate), and a current-clause
//     pre-check against the workspace carrier's effective content.
package retrospective

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Per-run bounds. IssuesPerRun caps one run's scope (the watermark and the
// next run recover the remainder).
const (
	IssuesPerRun = 20
	// EnqueuePriority sits below everything a human is waiting on — the same
	// courtesy the prompt-quiz run extends: a retrospective landing a cadence
	// later is still valid; a real task waiting behind one is not.
	EnqueuePriority = 0
)

// TaskEnqueuer places the platform task for one retrospective run. The
// implementation (wired once by the API server) inserts the fenced no-issue
// row and wakes runtimes; the func type keeps this package — and its tests —
// free of the task service.
type TaskEnqueuer func(ctx context.Context, params EnqueueParams) (string, error)

// EnqueueParams is everything the fenced insert needs. AgentID/RuntimeID are
// pre-validated by RunWorkspace; OriginatorUserID may be empty for configs
// saved before migration 935 (the audit column stays NULL, nothing is
// invented).
type EnqueueParams struct {
	RunID            string
	WorkspaceID      string
	AgentID          string
	RuntimeID        string
	OriginatorUserID string
	Priority         int32
	Context          []byte
}

// Runner triggers retrospective runs for one workspace at a time. The
// scheduler drives RunWorkspace through a global-scope daily job; the
// handler's manual trigger path calls the same method with trigger=manual.
// The completion side shares this struct via ProcessTaskTerminal.
type Runner struct {
	// DB begins per-draft transactions (watermark + draft insert commit
	// together). Satisfied by *pgxpool.Pool and the handler's txStarter.
	DB interface {
		Begin(ctx context.Context) (pgx.Tx, error)
	}
	Queries *db.Queries
	// Enqueue places the platform task for the run; both trigger entry
	// points share one wired instance.
	Enqueue TaskEnqueuer
	// Now is overridable for tests.
	Now func() time.Time
}

// RunStats is one workspace pass's outcome, feeding the run record, the
// scheduler's HandlerResult and the manual trigger's response. At trigger
// time only the window/scan/task fields are known; the completion side fills
// the analyzed/proposal counts when the agent reports back.
type RunStats struct {
	WorkspaceID       string `json:"workspace_id"`
	RunID             string `json:"run_id"`
	TaskID            string `json:"task_id,omitempty"`
	WindowDays        int32  `json:"window_days"`
	IssuesScanned     int    `json:"issues_scanned"`
	IssuesAnalyzed    int    `json:"issues_analyzed"`
	ProposalsCreated  int    `json:"proposals_created"`
	ProposalsMerged   int    `json:"proposals_merged"`
	DuplicatesSkipped int    `json:"duplicates_skipped"`
}

// ErrNoConfig marks "nothing to do" — no config row, or not enabled. The
// scheduler job treats it as a silent skip; the manual trigger surfaces it
// to the owner as a 400.
var ErrNoConfig = errors.New("retrospective: workspace config missing or disabled")

// ErrNoAgent marks "configured but not runnable": enabled with no agent
// selected (a pre-migration row) or the selected agent missing, archived, or
// runtime-less. The trigger records a failed run naming the reason — the
// page shows it — and the scheduler treats it as a silent skip while the
// manual trigger maps it to a 409.
var ErrNoAgent = errors.New("retrospective: no usable agent configured")

// RunWorkspace scans one workspace's completed-issue window and enqueues the
// agent's run over it. It returns ErrNoConfig when the workspace has no
// enabled config row and ErrNoAgent (after recording the failed run) when no
// usable agent is configured.
func (r *Runner) RunWorkspace(ctx context.Context, workspaceID string, trigger string) (*RunStats, error) {
	now := r.now()
	cfg, err := r.Queries.GetRetrospectiveConfig(ctx, util.MustParseUUID(workspaceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoConfig
		}
		return nil, fmt.Errorf("retrospective config: %w", err)
	}
	if !cfg.Enabled {
		return nil, ErrNoConfig
	}

	windowEnd := now
	windowStart := now.AddDate(0, 0, -int(cfg.WindowDays))
	run, err := r.Queries.InsertRetrospectiveRun(ctx, db.InsertRetrospectiveRunParams{
		WorkspaceID: util.MustParseUUID(workspaceID),
		Trigger:     trigger,
		WindowStart: pgtype.Timestamptz{Time: windowStart, Valid: true},
		WindowEnd:   pgtype.Timestamptz{Time: windowEnd, Valid: true},
		Detail:      []byte("{}"),
	})
	if err != nil {
		return nil, fmt.Errorf("insert retrospective run: %w", err)
	}
	runID := util.UUIDToString(run.ID)
	stats := &RunStats{WorkspaceID: workspaceID, RunID: runID, WindowDays: cfg.WindowDays}

	// Agent gate: the run is one round of the configured agent, so a config
	// without a usable agent records a failed run naming the reason instead
	// of enqueueing anything. PUT enforces enabled→agent_id going forward;
	// these paths catch rows saved before migration 935 and agents that
	// became unusable after saving.
	agentReason := ""
	switch {
	case !cfg.AgentID.Valid:
		agentReason = "每日总复盘未配置执行智能体：请在自进化 → 每日总复盘配置中选择一个智能体后重试"
	default:
		agent, agentErr := r.Queries.GetAgent(ctx, cfg.AgentID)
		switch {
		case agentErr != nil:
			agentReason = "配置的执行智能体不存在：请重新选择后保存"
		case agent.ArchivedAt.Valid:
			agentReason = "配置的执行智能体已归档：请重新选择后保存"
		case !agent.RuntimeID.Valid:
			agentReason = "配置的执行智能体没有可用运行时：请为该智能体配置运行时后重试"
		default:
			stats.IssuesScanned, agentReason = r.enqueueRun(ctx, cfg, agent, run, windowStart, windowEnd, stats)
			if agentReason == "" && stats.IssuesScanned == 0 {
				// Empty window: nothing to analyze, no agent spend — close
				// the run as a clean success right here.
				if err := r.finishRun(ctx, run, "succeeded", "", runDetail{}, stats); err != nil {
					return stats, err
				}
				return stats, nil
			}
		}
	}
	if agentReason != "" {
		if err := r.finishRun(ctx, run, "failed", agentReason, runDetail{}, stats); err != nil {
			return stats, err
		}
		return stats, ErrNoAgent
	}
	return stats, nil
}

// enqueueRun scans the window (watermark-filtered, capped), enqueues the
// agent's run over the scanned issues and links the task onto the run row.
// It returns the scanned count; a non-empty reason string means the enqueue
// itself failed and the run must be finished with it.
func (r *Runner) enqueueRun(ctx context.Context, cfg db.RetrospectiveConfig, agent db.Agent, run db.RetrospectiveRun, windowStart, windowEnd time.Time, stats *RunStats) (int, string) {
	var scanned []TaskContextIssue
	statuses := []string{"done"}
	if cfg.IncludeInReview {
		statuses = append(statuses, "in_review")
	}
	for _, status := range statuses {
		issues, err := r.Queries.ListIssuesCompletedInWindow(ctx, db.ListIssuesCompletedInWindowParams{
			WorkspaceID: run.WorkspaceID,
			Status:      status,
			UpdatedAt:   pgtype.Timestamptz{Time: windowStart, Valid: true},
			UpdatedAt_2: pgtype.Timestamptz{Time: windowEnd, Valid: true},
		})
		if err != nil {
			return len(scanned), fmt.Sprintf("扫描窗口失败: %v", err)
		}
		for _, issue := range issues {
			if len(scanned) >= IssuesPerRun {
				break
			}
			issueID := util.UUIDToString(issue.ID)
			// Idempotency watermark first: already-analyzed issues are
			// invisible to this run, whatever put them in the window.
			watermarked, err := r.Queries.HasRetrospectiveWatermark(ctx, db.HasRetrospectiveWatermarkParams{
				WorkspaceID: run.WorkspaceID,
				IssueID:     issue.ID,
			})
			if err != nil {
				return len(scanned), fmt.Sprintf("水位查询失败: %v", err)
			}
			if watermarked == 1 {
				continue
			}
			scanned = append(scanned, TaskContextIssue{ID: issueID, Title: truncateRunes(issue.Title, maxTitleRunes)})
		}
		if len(scanned) >= IssuesPerRun {
			break
		}
	}
	stats.IssuesScanned = len(scanned)

	// Nothing pending: finish immediately — no task, no agent spend. The
	// watermark defines what has been analyzed, so an empty scan is a clean
	// success, not a failure.
	if len(scanned) == 0 {
		return 0, ""
	}

	// The scanned set rides on the run row from the start: the completion
	// processor validates the agent's reported ids against it.
	detail, _ := json.Marshal(runDetail{IssueIDs: taskContextIssueIDs(scanned)})
	if err := r.Queries.UpdateRetrospectiveRunDetail(ctx, db.UpdateRetrospectiveRunDetailParams{
		ID:     run.ID,
		Detail: detail,
	}); err != nil {
		return len(scanned), fmt.Sprintf("记录扫描范围失败: %v", err)
	}

	taskCtx, _ := json.Marshal(TaskContext{
		Kind:        TaskKind,
		RunID:       stats.RunID,
		WorkspaceID: stats.WorkspaceID,
		WindowStart: windowStart.UTC().Format(time.RFC3339),
		WindowEnd:   windowEnd.UTC().Format(time.RFC3339),
		Issues:      scanned,
	})
	taskID, err := r.Enqueue(ctx, EnqueueParams{
		RunID:            stats.RunID,
		WorkspaceID:      stats.WorkspaceID,
		AgentID:          util.UUIDToString(agent.ID),
		RuntimeID:        util.UUIDToString(agent.RuntimeID),
		OriginatorUserID: util.UUIDToString(cfg.UpdatedBy),
		Priority:         EnqueuePriority,
		Context:          taskCtx,
	})
	if err != nil {
		return len(scanned), fmt.Sprintf("复盘任务入列失败: %v", err)
	}
	stats.TaskID = taskID
	if err := r.Queries.SetRetrospectiveRunTaskID(ctx, db.SetRetrospectiveRunTaskIDParams{
		ID:     run.ID,
		TaskID: util.MustParseUUID(taskID),
	}); err != nil {
		// The task is live; the run record merely lost its back-pointer and
		// the age-out reconciler will fail the row if the task dies unseen.
		slog.Warn("retrospective: run row missing task back-pointer",
			"run_id", stats.RunID, "task_id", taskID, "error", err)
	}
	return len(scanned), ""
}

// ReconcileStaleRuns is the bulk backstop the scheduler runs on every tick:
// runs whose task went terminal without the completion hook seeing it
// (offline-runtime sweeps, cancel paths, daemon crashes) and runs whose task
// never got enqueued. First terminal verdict wins — the status guards skip
// runs the completion processor already finished.
func (r *Runner) ReconcileStaleRuns(ctx context.Context) (int64, error) {
	n1, err := r.Queries.ReconcileTerminalRetrospectiveRuns(ctx)
	if err != nil {
		return 0, fmt.Errorf("retrospective: reconcile terminal: %w", err)
	}
	n2, err := r.Queries.ReconcileUnenqueuedRetrospectiveRuns(ctx)
	if err != nil {
		return n1, fmt.Errorf("retrospective: reconcile unenqueued: %w", err)
	}
	return n1 + n2, nil
}

// finishRun merges extra detail into the run row's existing detail and
// writes the terminal state. Every terminal path funnels through here, so
// the scanned set recorded at trigger survives every finish.
func (r *Runner) finishRun(ctx context.Context, run db.RetrospectiveRun, status, errMsg string, extra runDetail, stats *RunStats) error {
	detail := parseRunDetail(run.Detail)
	if extra.IssueIDs != nil {
		detail.IssueIDs = extra.IssueIDs
	}
	detail.AnalyzedIssueIDs = extra.AnalyzedIssueIDs
	out, err := json.Marshal(detail)
	if err != nil {
		out = []byte("{}")
	}
	if _, ferr := r.Queries.FinishRetrospectiveRun(ctx, db.FinishRetrospectiveRunParams{
		ID:                run.ID,
		WorkspaceID:       run.WorkspaceID,
		Status:            status,
		IssuesScanned:     int32(stats.IssuesScanned),
		IssuesAnalyzed:    int32(stats.IssuesAnalyzed),
		ProposalsCreated:  int32(stats.ProposalsCreated),
		ProposalsMerged:   int32(stats.ProposalsMerged),
		DuplicatesSkipped: int32(stats.DuplicatesSkipped),
		Error:             errMsg,
		Detail:            out,
	}); ferr != nil {
		return fmt.Errorf("finish retrospective run: %w", ferr)
	}
	return nil
}

func (r *Runner) watermark(ctx context.Context, workspaceID, issueID, runID string) error {
	return r.Queries.InsertRetrospectiveWatermark(ctx, db.InsertRetrospectiveWatermarkParams{
		WorkspaceID: util.MustParseUUID(workspaceID), IssueID: util.MustParseUUID(issueID), LastRunID: util.MustParseUUID(runID),
	})
}

func (r *Runner) withTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(r.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func taskContextIssueIDs(in []TaskContextIssue) []string {
	out := make([]string, 0, len(in))
	for _, i := range in {
		out = append(out, i.ID)
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
