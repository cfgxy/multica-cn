package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ErrTaskNotConsumable: the consuming task is not an active claim-receipt task
// (dispatched/running/waiting_local_directory) or carries no issue, so it may
// not absorb queued messages. Completion reconcile owns the messages then.
var ErrTaskNotConsumable = errors.New("task is not active; queued messages can only be consumed by a running task")

// ConsumeQueuedTasksResult reports what a consumption absorbed.
type ConsumeQueuedTasksResult struct {
	// Tasks are the cancelled queued rows, in consumption order.
	Tasks []db.AgentTaskQueue
	// CommentIDs are every trigger and coalesced comment the queued rows
	// carried — the ids appended to the running task's delivered receipt.
	CommentIDs []pgtype.UUID
}

// ConsumeQueuedTasksForRunningTask releases the (issue, agent) pair's queued
// tasks into an ACTIVE task of the same pair (RUYI-48).
//
// Background: the (issue, agent) unique index only covers queued/dispatched, so
// while a task is RUNNING a new comment mention enqueues a fresh queued task.
// The running job can read and act on that message mid-run, but the queue is
// not told — the queued task stays queued and re-executes the same work after
// the active run completes. This is the formal consumption channel: the running
// job declares "I have read and am handling the queued messages", and the queue
// releases them instead of double-executing.
//
// One transaction: row-lock the consuming task, verify it is active, cancel all
// same-head queued rows (parent_task_id + failure_reason keep the audit trail),
// and fold their comments into the consuming task's delivered_comment_ids so
// reconcileCommentsOnCompletion never replays them. Head-scoping matches
// MergeCommentIntoPendingTask (TEN-356): a non-empty head only consumes
// same-head rows; an empty head (no linked PR) consumes regardless.
//
// Messages that are NOT consumed keep the existing at-least-once coverage:
// completion reconcile still replays them as a single bounded follow-up.
func (s *TaskService) ConsumeQueuedTasksForRunningTask(ctx context.Context, consumingTaskID pgtype.UUID) (*ConsumeQueuedTasksResult, error) {
	var (
		consumed  []db.AgentTaskQueue
		commentID []pgtype.UUID
	)
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		// Row lock serializes against CompleteTask: either consumption lands
		// first (reconcile then sees the receipt) or completion lands first
		// (the guard below refuses the late consumption).
		consuming, err := qtx.GetAgentTaskForUpdate(ctx, consumingTaskID)
		if err != nil {
			return fmt.Errorf("load consuming task: %w", err)
		}
		if !consuming.IssueID.Valid || (consuming.Status != "dispatched" && consuming.Status != "running" && consuming.Status != "waiting_local_directory") {
			return ErrTaskNotConsumable
		}

		// Mirror the enqueue merge's head key (TEN-356): context->>'head_sha'.
		var headSha *string
		if len(consuming.Context) > 0 {
			var ctxMap map[string]any
			if err := json.Unmarshal(consuming.Context, &ctxMap); err == nil {
				if raw, ok := ctxMap["head_sha"].(string); ok && raw != "" {
					headSha = &raw
				}
			}
		}

		cancelled, err := qtx.ConsumeQueuedTasksForRunningTask(ctx, db.ConsumeQueuedTasksForRunningTaskParams{
			Error:         pgtype.Text{String: "consumed by running task " + util.UUIDToString(consuming.ID), Valid: true},
			FailureReason: pgtype.Text{String: "consumed_by_running_task", Valid: true},
			ParentTaskID:  consuming.ID,
			IssueID:       consuming.IssueID,
			AgentID:       consuming.AgentID,
			HeadSha:       pgtype.Text{String: derefText(headSha), Valid: headSha != nil},
		})
		if err != nil {
			return fmt.Errorf("consume queued tasks: %w", err)
		}
		if len(cancelled) == 0 {
			return nil
		}

		seen := make(map[string]struct{})
		ids := make([]pgtype.UUID, 0, 2*len(cancelled))
		for _, task := range cancelled {
			for _, id := range append([]pgtype.UUID{task.TriggerCommentID}, task.CoalescedCommentIds...) {
				if !id.Valid {
					continue
				}
				key := util.UUIDToString(id)
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				ids = append(ids, id)
			}
		}

		// The statement re-checks activeness, so a terminal flip under the row
		// lock (impossible today — GetAgentTaskForUpdate holds the lock) still
		// fails closed instead of stamping receipts onto a finished run.
		if _, err := qtx.AppendDeliveredCommentIds(ctx, db.AppendDeliveredCommentIdsParams{
			CommentIds: ids,
			TaskID:     consuming.ID,
		}); err != nil {
			return fmt.Errorf("record consumed comments on running task: %w", err)
		}

		consumed = cancelled
		commentID = ids
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, task := range consumed {
		consumedCount := len(task.CoalescedCommentIds)
		if task.TriggerCommentID.Valid {
			consumedCount++
		}
		slog.Info("queued task consumed by running task",
			"consumed_task_id", util.UUIDToString(task.ID),
			"consuming_task_id", util.UUIDToString(consumingTaskID),
			"issue_id", util.UUIDToString(task.IssueID),
			"agent_id", util.UUIDToString(task.AgentID),
			"consumed_comments", consumedCount)
		// Mirror the queued-cancel path's fan-out so the UI queue list and any
		// waiters observe the release immediately.
		s.ReconcileAgentStatus(ctx, task.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, task)
		s.NotifyTaskFinished(task)
	}

	return &ConsumeQueuedTasksResult{Tasks: consumed, CommentIDs: commentID}, nil
}

func derefText(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
