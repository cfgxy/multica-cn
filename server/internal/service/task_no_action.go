package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrTaskNoActionNotActive      = errors.New("task is not active")
	ErrTaskNoActionLeader         = errors.New("squad leader tasks must record no_action through squad activity")
	ErrTaskNoActionHasOutput      = errors.New("task already produced a comment output")
	ErrTaskNoActionRecorded       = errors.New("task already recorded no_action; comments are not allowed")
	ErrTaskNoActionReasonRequired = errors.New("no_action reason is required")
)

func activeTaskStatus(status string) bool {
	switch status {
	case "dispatched", "running", "waiting_local_directory":
		return true
	default:
		return false
	}
}

// RecordTaskNoAction marks an ordinary issue task as intentionally silent.
// The task row lock serializes this decision against task-token comment writes:
// whichever commits first makes the other path fail closed.
func (s *TaskService) RecordTaskNoAction(ctx context.Context, taskID pgtype.UUID, reason string) (db.AgentTaskQueue, error) {
	if s.TxStarter == nil {
		return db.AgentTaskQueue{}, errors.New("task no_action requires a transaction starter")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return db.AgentTaskQueue{}, ErrTaskNoActionReasonRequired
	}

	var task db.AgentTaskQueue
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		locked, err := qtx.GetAgentTaskForUpdate(ctx, taskID)
		if err != nil {
			return fmt.Errorf("load task for no_action: %w", err)
		}
		if locked.NoActionAt.Valid {
			task = locked
			return nil
		}
		if !locked.IssueID.Valid || !activeTaskStatus(locked.Status) {
			return ErrTaskNoActionNotActive
		}
		if locked.IsLeaderTask && locked.SquadID.Valid {
			squad, squadErr := qtx.GetSquad(ctx, locked.SquadID)
			switch {
			case squadErr == nil && squad.LeaderID == locked.AgentID:
				return ErrTaskNoActionLeader
			case squadErr != nil && !errors.Is(squadErr, pgx.ErrNoRows):
				return fmt.Errorf("load task squad for no_action: %w", squadErr)
			}
		}
		if locked.CommentOutputAt.Valid {
			return ErrTaskNoActionHasOutput
		}
		hasComment, err := qtx.HasCommentForSourceTask(ctx, locked.ID)
		if err != nil {
			return fmt.Errorf("check task comment output: %w", err)
		}
		if hasComment {
			return ErrTaskNoActionHasOutput
		}
		task, err = qtx.SetAgentTaskNoAction(ctx, db.SetAgentTaskNoActionParams{
			TaskID: locked.ID,
			Reason: reason,
		})
		if err != nil {
			return fmt.Errorf("record task no_action: %w", err)
		}
		return nil
	})
	return task, err
}
