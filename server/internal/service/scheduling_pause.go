package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// RUYI-608: the scheduling freeze switch — service half.
//
// Pause/resume write the scheduling_pause rows (migration 936); the actual
// claim gate is the NOT EXISTS fence inside ClaimAgentTask, so a freeze
// holds against daemon restarts, server restarts and claim races by
// construction. This file owns the management semantics around that gate:
// idempotent freeze rows, the audit trail, the realtime broadcast, and the
// resume-time EmptyClaim invalidation (bump + wakeup) that makes a resumed
// agent's queue flow on the next poll instead of after EmptyClaimCacheTTL.
//
// What pause deliberately does NOT do: it never touches task rows. Queued
// tasks keep queueing and coalescing while frozen; dispatched/running work
// drains normally because complete/fail never consult this table. Cancel
// remains a separate capability (CancelTask) — freeze-then-triage needs the
// queue intact.
//
// RUYI-618 evolution path: programmatic callers (backpressure/suppression)
// reuse PauseAgentScheduling / PauseWorkspaceScheduling to write the same
// rows — no second mechanism.

// Scheduling pause scopes, mirrored into audit details and API payloads.
const (
	SchedulingPauseScopeAgent     = "agent"
	SchedulingPauseScopeWorkspace = "workspace"
)

// SchedulingState is the user-facing freeze status of an agent or a
// workspace: whether a freeze is active, at which scope, who froze it and
// how much queued work is currently held back.
type SchedulingState struct {
	Paused    bool   `json:"paused"`
	Scope     string `json:"scope,omitempty"`
	Reason    string `json:"reason,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	// QueuedCount is the frozen queue depth: queued tasks that the fence is
	// holding back. Reported regardless of pause state (an unpaused agent's
	// count is just its live queue length); the UI renders it as frozen
	// only when Paused is true.
	QueuedCount int64 `json:"queued_count"`
}

// ErrSchedulingPauseNotFound is returned by the Get* state readers when the
// addressed agent does not exist in the workspace. Resume treats a missing
// freeze row as already-resumed (idempotent), not as an error.
var ErrSchedulingPauseNotFound = errors.New("scheduling pause: agent not found in workspace")

// PauseAgentScheduling freezes one agent's task consumption. Idempotent: a
// second freeze returns the ORIGINAL row unchanged (first operator's
// attribution preserved). The audit write shares the freeze's transaction —
// an administrative control-plane action without its trail is treated as
// not having happened. Returns the pause row and the frozen queue depth.
func (s *TaskService) PauseAgentScheduling(ctx context.Context, workspaceID, agentID pgtype.UUID, reason string, actorMemberID pgtype.UUID) (db.SchedulingPause, int64, error) {
	var (
		pause  db.SchedulingPause
		queued int64
	)
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		agent, err := qtx.GetAgent(ctx, agentID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrSchedulingPauseNotFound, util.UUIDToString(agentID))
			}
			return fmt.Errorf("load agent: %w", err)
		}
		if agent.WorkspaceID != workspaceID {
			return fmt.Errorf("%w: %s", ErrSchedulingPauseNotFound, util.UUIDToString(agentID))
		}
		p, err := qtx.UpsertAgentSchedulingPause(ctx, db.UpsertAgentSchedulingPauseParams{
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			Reason:      reason,
			CreatedBy:   actorMemberID,
		})
		if err != nil {
			return fmt.Errorf("freeze agent scheduling: %w", err)
		}
		pause = p

		q, err := qtx.CountQueuedTasksForAgent(ctx, agentID)
		if err != nil {
			return fmt.Errorf("count frozen queued tasks: %w", err)
		}
		queued = q

		details, _ := json.Marshal(map[string]any{
			"scope":        SchedulingPauseScopeAgent,
			"reason":       reason,
			"queued_count": queued,
		})
		return AppendAuditEvents(ctx, qtx, AgentEvent(AuditAgentSchedulingPaused, AuditActorMember,
			actorMemberID, workspaceID, agentID, details))
	})
	if err != nil {
		return db.SchedulingPause{}, 0, err
	}

	s.broadcastSchedulingChange(ctx, workspaceID, &agentID, SchedulingPauseScopeAgent, true, reason, queued)
	return pause, queued, nil
}

// ResumeAgentScheduling lifts an agent-level freeze and reports how many
// queued tasks were being held back. Idempotent: no freeze row is a no-op
// returning 0. On success it bumps the EmptyClaim verdict for the agent's
// runtime (and wakes the daemon) so the freshly-unfrozen queue is consumed
// on the next poll instead of after EmptyClaimCacheTTL.
func (s *TaskService) ResumeAgentScheduling(ctx context.Context, workspaceID, agentID pgtype.UUID, actorMemberID pgtype.UUID) (int64, error) {
	var queued int64
	var runtimeID pgtype.UUID
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		agent, err := qtx.GetAgent(ctx, agentID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrSchedulingPauseNotFound, util.UUIDToString(agentID))
			}
			return fmt.Errorf("load agent: %w", err)
		}
		if agent.WorkspaceID != workspaceID {
			return fmt.Errorf("%w: %s", ErrSchedulingPauseNotFound, util.UUIDToString(agentID))
		}
		runtimeID = agent.RuntimeID

		// Count BEFORE deleting so a concurrent claim between the two
		// statements cannot inflate the reported number: worst case the
		// count is read a moment before a claim lands and over-reports by
		// the tasks claimed in that window; it never under-reports held
		// work that is about to flow.
		q, err := qtx.CountQueuedTasksForAgent(ctx, agentID)
		if err != nil {
			return fmt.Errorf("count frozen queued tasks: %w", err)
		}
		queued = q

		if err := qtx.DeleteAgentSchedulingPause(ctx, db.DeleteAgentSchedulingPauseParams{
			WorkspaceID: workspaceID,
			AgentID:     agentID,
		}); err != nil {
			return fmt.Errorf("resume agent scheduling: %w", err)
		}

		details, _ := json.Marshal(map[string]any{
			"scope":        SchedulingPauseScopeAgent,
			"queued_count": queued,
		})
		return AppendAuditEvents(ctx, qtx, AgentEvent(AuditAgentSchedulingResumed, AuditActorMember,
			actorMemberID, workspaceID, agentID, details))
	})
	if err != nil {
		return 0, err
	}

	// Bump even when no freeze row existed: the daemon may still hold a
	// cached empty verdict from before an out-of-band freeze removal, and a
	// redundant bump costs one extra DB poll at worst.
	if runtimeID.Valid {
		s.notifyRuntimeMayHaveWork(runtimeID, "")
	}
	s.broadcastSchedulingChange(ctx, workspaceID, &agentID, SchedulingPauseScopeAgent, false, "", queued)
	return queued, nil
}

// PauseWorkspaceScheduling freezes every agent in the workspace with one
// workspace-level row (agent_id NULL). Same idempotency and audit semantics
// as PauseAgentScheduling. Agent-level rows are neither created nor removed:
// the two scopes stack, and any active freeze keeps the fence closed.
func (s *TaskService) PauseWorkspaceScheduling(ctx context.Context, workspaceID pgtype.UUID, reason string, actorMemberID pgtype.UUID) (db.SchedulingPause, int64, error) {
	var (
		pause  db.SchedulingPause
		queued int64
	)
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		p, err := qtx.UpsertWorkspaceSchedulingPause(ctx, db.UpsertWorkspaceSchedulingPauseParams{
			WorkspaceID: workspaceID,
			Reason:      reason,
			CreatedBy:   actorMemberID,
		})
		if err != nil {
			return fmt.Errorf("freeze workspace scheduling: %w", err)
		}
		pause = p

		q, err := qtx.CountQueuedTasksByWorkspace(ctx, workspaceID)
		if err != nil {
			return fmt.Errorf("count frozen queued tasks: %w", err)
		}
		queued = q

		details, _ := json.Marshal(map[string]any{
			"scope":        SchedulingPauseScopeWorkspace,
			"reason":       reason,
			"queued_count": queued,
		})
		return AppendAuditEvents(ctx, qtx, schedulingOpsEvent(AuditOpsSchedulingPaused, actorMemberID, workspaceID, details))
	})
	if err != nil {
		return db.SchedulingPause{}, 0, err
	}

	s.broadcastSchedulingChange(ctx, workspaceID, nil, SchedulingPauseScopeWorkspace, true, reason, queued)
	return pause, queued, nil
}

// ResumeWorkspaceScheduling lifts the workspace-level freeze. Agent-level
// freezes (if any) intentionally survive: the scopes stack, and "resume the
// workspace" must not silently re-enable an agent someone froze
// individually. Bumps every runtime bound to a workspace agent so no
// daemon keeps a stale empty verdict.
func (s *TaskService) ResumeWorkspaceScheduling(ctx context.Context, workspaceID pgtype.UUID, actorMemberID pgtype.UUID) (int64, error) {
	var (
		queued    int64
		runtimes  []pgtype.UUID
		hadFrozen bool
	)
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		// Read the freeze row BEFORE deleting it: existence decides whether
		// this resume is a real state change (audit row + runtime bumps) or
		// an idempotent no-op.
		_, frozenErr := qtx.GetWorkspaceSchedulingPause(ctx, workspaceID)
		hadFrozen = frozenErr == nil
		if frozenErr != nil && !errors.Is(frozenErr, pgx.ErrNoRows) {
			return fmt.Errorf("read scheduling pause: %w", frozenErr)
		}

		q, err := qtx.CountQueuedTasksByWorkspace(ctx, workspaceID)
		if err != nil {
			return fmt.Errorf("count frozen queued tasks: %w", err)
		}
		queued = q

		if err := qtx.DeleteWorkspaceSchedulingPause(ctx, workspaceID); err != nil {
			return fmt.Errorf("resume workspace scheduling: %w", err)
		}

		rts, err := qtx.ListAgentRuntimesByWorkspace(ctx, workspaceID)
		if err != nil {
			return fmt.Errorf("list workspace runtimes: %w", err)
		}
		runtimes = rts

		// A no-op resume (nothing frozen) carries no state change worth an
		// audit row; the caller still gets the queued count for its UX.
		// The deleted freeze row is the audit anchor for the paused period.
		if !hadFrozen {
			return nil
		}
		details, _ := json.Marshal(map[string]any{
			"scope":        SchedulingPauseScopeWorkspace,
			"queued_count": queued,
		})
		return AppendAuditEvents(ctx, qtx, schedulingOpsEvent(AuditOpsSchedulingResumed, actorMemberID, workspaceID, details))
	})
	if err != nil {
		return 0, err
	}

	if hadFrozen {
		for _, rt := range runtimes {
			s.notifyRuntimeMayHaveWork(rt, "")
		}
	}
	s.broadcastSchedulingChange(ctx, workspaceID, nil, SchedulingPauseScopeWorkspace, false, "", queued)
	return queued, nil
}

// AgentSchedulingState reports an agent's freeze status and queued depth.
// Workspace-level freezes count as paused for the agent (the fence treats
// any matching row as a block), with the workspace scope surfaced so the
// UI can explain WHO froze it.
func (s *TaskService) AgentSchedulingState(ctx context.Context, workspaceID, agentID pgtype.UUID) (SchedulingState, error) {
	state := SchedulingState{}
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		if _, err := qtx.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          agentID,
			WorkspaceID: workspaceID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrSchedulingPauseNotFound, util.UUIDToString(agentID))
			}
			return fmt.Errorf("load agent: %w", err)
		}
		return readSchedulingState(ctx, qtx, workspaceID, &agentID, &state)
	})
	return state, err
}

// WorkspaceSchedulingState reports the workspace-level freeze status and
// the workspace-wide queued depth.
func (s *TaskService) WorkspaceSchedulingState(ctx context.Context, workspaceID pgtype.UUID) (SchedulingState, error) {
	state := SchedulingState{}
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		if _, err := qtx.GetWorkspace(ctx, workspaceID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: workspace %s", ErrSchedulingPauseNotFound, util.UUIDToString(workspaceID))
			}
			return fmt.Errorf("load workspace: %w", err)
		}
		return readSchedulingState(ctx, qtx, workspaceID, nil, &state)
	})
	return state, err
}

// readSchedulingState assembles the state struct. agentID nil means the
// workspace-level view. Two reads (pause row + queued count) — cheap, and
// the queued count doubles as the freeze-preview number.
func readSchedulingState(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, agentID *pgtype.UUID, state *SchedulingState) error {
	if agentID != nil {
		if row, err := qtx.GetSchedulingPauseForAgent(ctx, db.GetSchedulingPauseForAgentParams{
			WorkspaceID: workspaceID,
			AgentID:     *agentID,
		}); err == nil {
			fillStateFromRow(state, row)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read scheduling pause: %w", err)
		}
		q, err := qtx.CountQueuedTasksForAgent(ctx, *agentID)
		if err != nil {
			return fmt.Errorf("count queued tasks: %w", err)
		}
		state.QueuedCount = q
		return nil
	}

	if row, err := qtx.GetWorkspaceSchedulingPause(ctx, workspaceID); err == nil {
		fillStateFromRow(state, row)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read scheduling pause: %w", err)
	}
	q, err := qtx.CountQueuedTasksByWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("count queued tasks: %w", err)
	}
	state.QueuedCount = q
	return nil
}

func fillStateFromRow(state *SchedulingState, row db.SchedulingPause) {
	state.Paused = true
	if row.AgentID.Valid {
		state.Scope = SchedulingPauseScopeAgent
	} else {
		state.Scope = SchedulingPauseScopeWorkspace
	}
	state.Reason = row.Reason
	state.CreatedBy = util.UUIDToString(row.CreatedBy)
	if row.CreatedAt.Valid {
		state.CreatedAt = row.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
}

// schedulingPauseScopeOf names the freeze scope for claim-path logs.
func schedulingPauseScopeOf(row db.SchedulingPause) string {
	if row.AgentID.Valid {
		return SchedulingPauseScopeAgent
	}
	return SchedulingPauseScopeWorkspace
}

// schedulingOpsEvent builds an ops-domain audit event for workspace-level
// freeze lifecycle writes (no single agent dimension to anchor).
func schedulingOpsEvent(eventType string, actorMemberID, workspaceID pgtype.UUID, details []byte) Event {
	return Event{
		ID:          dbid.NewV7(),
		WorkspaceID: workspaceID,
		Domain:      AuditDomainOps,
		EventType:   eventType,
		OccurredAt:  time.Now().UTC(),
		ActorType:   AuditActorMember,
		ActorID:     actorMemberID,
		Details:     details,
	}
}

// broadcastSchedulingChange publishes the freeze lifecycle to workspace
// realtime subscribers. agentID nil = workspace-level change; the payload's
// agent_id field is then absent so clients invalidate the workspace surface,
// not just one agent card.
func (s *TaskService) broadcastSchedulingChange(ctx context.Context, workspaceID pgtype.UUID, agentID *pgtype.UUID, scope string, paused bool, reason string, queued int64) {
	if s.Bus == nil {
		return
	}
	payload := map[string]any{
		"workspace_id": util.UUIDToString(workspaceID),
		"scope":        scope,
		"paused":       paused,
		"queued_count": queued,
	}
	if reason != "" {
		payload["reason"] = reason
	}
	if agentID != nil {
		payload["agent_id"] = util.UUIDToString(*agentID)
	}
	eventType := protocol.EventAgentSchedulingResumed
	if paused {
		eventType = protocol.EventAgentSchedulingPaused
	}
	actorType := "system"
	s.Bus.Publish(events.Event{
		Type:        eventType,
		WorkspaceID: util.UUIDToString(workspaceID),
		ActorType:   actorType,
		Payload:     payload,
	})
}
