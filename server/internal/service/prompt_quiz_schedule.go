package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// RUYI-941: Quiz scheduling is a separate control plane from ordinary task
// scheduling. The durable override survives restarts; only queued/pre-start
// Quiz rows are cancelled when disabled, while running rows drain and remain
// available to the collector.
const (
	PromptQuizScheduleScopeWorkspace = "workspace"
	PromptQuizScheduleScopeAgent     = "agent"
	PromptQuizScheduleScopeDefault   = "default"
)

type PromptQuizScheduleState struct {
	Enabled      bool
	Scope        string
	HasOverride  bool
	Reason       string
	UpdatedBy    string
	UpdatedAt    string
	QueuedCount  int64
	RunningCount int64
}

// PromptQuizScheduleChange is the result of an auditable control-plane write.
type PromptQuizScheduleChange struct {
	State          PromptQuizScheduleState
	CancelledCount int64
}

// SetPromptQuizSchedule stores an explicit workspace- or agent-level override.
// The config write, queue isolation, and audit event share one transaction.
func (s *TaskService) SetPromptQuizSchedule(ctx context.Context, workspaceID pgtype.UUID, agentID *pgtype.UUID, enabled bool, reason string, actorID pgtype.UUID) (PromptQuizScheduleChange, error) {
	var (
		change PromptQuizScheduleChange
		old    PromptQuizScheduleState
	)
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		// Serialize every Quiz task writer in this workspace with the control
		// change. CreatePromptQuizTask takes the compatible KEY SHARE lock in a
		// separate transaction command before its INSERT, so a writer that was
		// waiting here cannot use a pre-disable snapshot after this commit.
		if _, err := qtx.LockWorkspaceForPromptQuizSchedule(ctx, workspaceID); err != nil {
			return fmt.Errorf("lock workspace Quiz schedule: %w", err)
		}
		if agentID != nil {
			if agent, err := qtx.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: *agentID, WorkspaceID: workspaceID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("agent does not belong to workspace")
				}
				return fmt.Errorf("load agent: %w", err)
			} else if agent.ID != *agentID {
				return fmt.Errorf("agent does not belong to workspace")
			}
		}

		var err error
		old, err = promptQuizScheduleState(ctx, qtx, workspaceID, agentID)
		if err != nil {
			return err
		}
		params := db.UpsertPromptQuizScheduleControlParams{
			WorkspaceID: workspaceID,
			AgentID:     uuidParam(agentID),
			Enabled:     enabled,
			Reason:      reason,
			UpdatedBy:   actorID,
		}
		if agentID == nil {
			if _, err = qtx.UpsertPromptQuizScheduleControl(ctx, params); err != nil {
				return fmt.Errorf("save workspace Quiz schedule: %w", err)
			}
		} else {
			if _, err = qtx.UpsertPromptQuizScheduleControlForAgent(ctx, db.UpsertPromptQuizScheduleControlForAgentParams{
				WorkspaceID: workspaceID,
				AgentID:     *agentID,
				Enabled:     enabled,
				Reason:      reason,
				UpdatedBy:   actorID,
			}); err != nil {
				return fmt.Errorf("save agent Quiz schedule: %w", err)
			}
		}

		if !enabled {
			rows, err := qtx.CancelQueuedPromptQuizTasksForScheduleScope(ctx, db.CancelQueuedPromptQuizTasksForScheduleScopeParams{
				WorkspaceID: workspaceID,
				AgentID:     uuidParam(agentID),
			})
			if err != nil {
				return fmt.Errorf("isolate queued Quiz tasks: %w", err)
			}
			change.CancelledCount = int64(len(rows))
		}

		newState, err := promptQuizScheduleState(ctx, qtx, workspaceID, agentID)
		if err != nil {
			return err
		}
		change.State = newState
		return appendPromptQuizScheduleAudit(ctx, qtx, workspaceID, agentID, actorID, old, newState, change.CancelledCount)
	})
	if err != nil {
		return PromptQuizScheduleChange{}, err
	}
	return change, nil
}

// RestorePromptQuizSchedule removes one explicit override. Missing rows are an
// idempotent restore to the default; a narrower override can still remain
// effective when a workspace override is removed. If the resulting effective
// state is disabled, queued Quiz work is isolated before the transaction ends.
func (s *TaskService) RestorePromptQuizSchedule(ctx context.Context, workspaceID pgtype.UUID, agentID *pgtype.UUID, actorID pgtype.UUID) (PromptQuizScheduleChange, error) {
	var change PromptQuizScheduleChange
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		if _, err := qtx.LockWorkspaceForPromptQuizSchedule(ctx, workspaceID); err != nil {
			return fmt.Errorf("lock workspace Quiz schedule restore: %w", err)
		}
		old, err := promptQuizScheduleState(ctx, qtx, workspaceID, agentID)
		if err != nil {
			return err
		}
		if _, err := qtx.DeletePromptQuizScheduleControl(ctx, db.DeletePromptQuizScheduleControlParams{
			WorkspaceID: workspaceID,
			AgentID:     uuidParam(agentID),
		}); err != nil {
			return fmt.Errorf("restore Quiz schedule default: %w", err)
		}
		effective, err := promptQuizScheduleState(ctx, qtx, workspaceID, agentID)
		if err != nil {
			return err
		}
		if !effective.Enabled {
			rows, err := qtx.CancelQueuedPromptQuizTasksForScheduleScope(ctx, db.CancelQueuedPromptQuizTasksForScheduleScopeParams{
				WorkspaceID: workspaceID,
				AgentID:     uuidParam(agentID),
			})
			if err != nil {
				return fmt.Errorf("isolate queued Quiz tasks after restore: %w", err)
			}
			change.CancelledCount = int64(len(rows))
		}
		change.State, err = promptQuizScheduleState(ctx, qtx, workspaceID, agentID)
		if err != nil {
			return err
		}
		if old.Enabled != change.State.Enabled || old.HasOverride {
			if err := appendPromptQuizScheduleAudit(ctx, qtx, workspaceID, agentID, actorID, old, change.State, change.CancelledCount); err != nil {
				return err
			}
		}
		return nil
	})
	return change, err
}

func (s *TaskService) PromptQuizScheduleState(ctx context.Context, workspaceID pgtype.UUID, agentID *pgtype.UUID) (PromptQuizScheduleState, error) {
	return promptQuizScheduleState(ctx, s.Queries, workspaceID, agentID)
}

func promptQuizScheduleState(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, agentID *pgtype.UUID) (PromptQuizScheduleState, error) {
	state := PromptQuizScheduleState{Enabled: true, Scope: PromptQuizScheduleScopeDefault}
	var row db.PromptQuizScheduleControl
	var err error
	if agentID == nil {
		row, err = q.GetPromptQuizScheduleControlForWorkspace(ctx, workspaceID)
	} else {
		row, err = q.GetPromptQuizScheduleControlForAgent(ctx, db.GetPromptQuizScheduleControlForAgentParams{WorkspaceID: workspaceID, AgentID: *agentID})
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return state, fmt.Errorf("read Quiz schedule: %w", err)
	}
	if err == nil {
		state.Enabled = row.Enabled
		state.HasOverride = true
		state.Scope = PromptQuizScheduleScopeWorkspace
		if row.AgentID.Valid {
			state.Scope = PromptQuizScheduleScopeAgent
		}
		state.Reason = row.Reason
		state.UpdatedBy = util.UUIDToString(row.UpdatedBy)
		if row.UpdatedAt.Valid {
			state.UpdatedAt = row.UpdatedAt.Time.UTC().Format(time.RFC3339)
		}
	}
	counts, err := q.CountPromptQuizTasksForScheduleScope(ctx, db.CountPromptQuizTasksForScheduleScopeParams{
		WorkspaceID: workspaceID,
		AgentID:     uuidParam(agentID),
	})
	if err != nil {
		return state, fmt.Errorf("count Quiz tasks: %w", err)
	}
	state.QueuedCount = counts.QueuedCount
	state.RunningCount = counts.RunningCount
	return state, nil
}

func appendPromptQuizScheduleAudit(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, agentID *pgtype.UUID, actorID pgtype.UUID, old, next PromptQuizScheduleState, cancelled int64) error {
	details, _ := json.Marshal(map[string]any{
		"scope":           next.Scope,
		"agent_id":        promptQuizUUIDString(agentID),
		"old_enabled":     old.Enabled,
		"new_enabled":     next.Enabled,
		"reason":          next.Reason,
		"cancelled_count": cancelled,
		"queued_count":    next.QueuedCount,
		"running_count":   next.RunningCount,
	})
	if agentID != nil {
		return AppendAuditEvents(ctx, q, AgentEvent(AuditAgentPromptQuizScheduleUpdated, AuditActorMember, actorID, workspaceID, *agentID, details))
	}
	return AppendAuditEvents(ctx, q, Event{
		ID:          dbid.NewV7(),
		WorkspaceID: workspaceID,
		Domain:      AuditDomainOps,
		EventType:   AuditOpsPromptQuizScheduleUpdated,
		OccurredAt:  time.Now().UTC(),
		ActorType:   AuditActorMember,
		ActorID:     actorID,
		Details:     details,
	})
}

func uuidParam(id *pgtype.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return *id
}

func promptQuizUUIDString(id *pgtype.UUID) string {
	if id == nil {
		return ""
	}
	return util.UUIDToString(*id)
}
