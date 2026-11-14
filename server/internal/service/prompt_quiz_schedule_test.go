package service

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestPromptQuizScheduleDisableIsolatesQuizTasks covers the default-enabled
// state, agent-specific disable, queue cancellation, ordinary-task isolation,
// and restoring the missing-row default.
func TestPromptQuizScheduleDisableIsolatesQuizTasks(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, runtimeID, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	workspaceUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	state, err := svc.PromptQuizScheduleState(ctx, workspaceUUID, &agentUUID)
	if err != nil {
		t.Fatalf("read default Quiz schedule: %v", err)
	}
	if !state.Enabled || state.Scope != PromptQuizScheduleScopeDefault || state.HasOverride {
		t.Fatalf("default Quiz schedule = %+v; want enabled default", state)
	}

	var quizID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, status, priority, context, runtime_id,
			originator_source, trigger_evidence_kind
		) VALUES ($1, 'queued', 0, '{}'::jsonb, $2, 'quiz', 'quiz_item')
		RETURNING id
	`, agentID, runtimeID).Scan(&quizID); err != nil {
		t.Fatalf("create Quiz task: %v", err)
	}
	var runningQuizID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, status, priority, context, runtime_id, started_at,
			originator_source, trigger_evidence_kind
		) VALUES ($1, 'running', 0, '{}'::jsonb, $2, now(), 'quiz', 'quiz_item')
		RETURNING id
	`, agentID, runtimeID).Scan(&runningQuizID); err != nil {
		t.Fatalf("create running Quiz task: %v", err)
	}

	change, err := svc.SetPromptQuizSchedule(ctx, workspaceUUID, &agentUUID, false, "Mika Quiz incident", ownerUUID)
	if err != nil {
		t.Fatalf("disable agent Quiz schedule: %v", err)
	}
	if change.State.Enabled || change.State.Scope != PromptQuizScheduleScopeAgent || change.CancelledCount != 1 || change.State.RunningCount != 1 {
		t.Fatalf("disable change = %+v; want disabled agent scope, one cancellation, and one running task", change)
	}

	var runningStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, runningQuizID).Scan(&runningStatus); err != nil {
		t.Fatalf("read running Quiz task: %v", err)
	}
	if runningStatus != "running" {
		t.Fatalf("running Quiz task status = %q; want running", runningStatus)
	}

	var status, reason string
	if err := pool.QueryRow(ctx, `
		SELECT status, cancel_reason FROM agent_task_queue WHERE id = $1
	`, quizID).Scan(&status, &reason); err != nil {
		t.Fatalf("read cancelled Quiz task: %v", err)
	}
	if status != "cancelled" || reason != "prompt_quiz_schedule_disabled" {
		t.Fatalf("Quiz task = status %q reason %q; want cancelled control-plane result", status, reason)
	}

	ordinary, err := svc.ClaimTask(ctx, agentUUID)
	if err != nil {
		t.Fatalf("claim ordinary task while Quiz disabled: %v", err)
	}
	if ordinary == nil {
		t.Fatal("ordinary Issue task was blocked by Quiz-only disable")
	}
	if ordinary.ID == util.MustParseUUID(quizID) {
		t.Fatal("cancelled Quiz task was claimed")
	}

	if _, err := svc.RestorePromptQuizSchedule(ctx, workspaceUUID, &agentUUID, ownerUUID); err != nil {
		t.Fatalf("restore agent Quiz schedule: %v", err)
	}
	state, err = svc.PromptQuizScheduleState(ctx, workspaceUUID, &agentUUID)
	if err != nil {
		t.Fatalf("read restored Quiz schedule: %v", err)
	}
	if !state.Enabled || state.Scope != PromptQuizScheduleScopeDefault || state.HasOverride {
		t.Fatalf("restored Quiz schedule = %+v; want enabled default", state)
	}

}

// TestPromptQuizScheduleAgentOverrideSurvivesWorkspaceDisable verifies that a
// narrower enabled override is not cancelled by a later workspace disable.
func TestPromptQuizScheduleAgentOverrideSurvivesWorkspaceDisable(t *testing.T) {
	ctx := context.Background()
	pool := newTaskClaimRacePool(t)
	workspaceID, agentID, runtimeID, ownerID := createSchedulingPauseFixture(t, ctx, pool)
	workspaceUUID := util.MustParseUUID(workspaceID)
	agentUUID := util.MustParseUUID(agentID)
	ownerUUID := util.MustParseUUID(ownerID)

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	if _, err := svc.SetPromptQuizSchedule(ctx, workspaceUUID, nil, false, "workspace hold", ownerUUID); err != nil {
		t.Fatalf("disable workspace Quiz schedule: %v", err)
	}
	if _, err := svc.SetPromptQuizSchedule(ctx, workspaceUUID, &agentUUID, true, "Mika exception", ownerUUID); err != nil {
		t.Fatalf("enable agent Quiz override: %v", err)
	}

	var quizID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, status, priority, context, runtime_id,
			originator_source, trigger_evidence_kind
		) VALUES ($1, 'queued', 0, '{}'::jsonb, $2, 'quiz', 'quiz_item')
		RETURNING id
	`, agentID, runtimeID).Scan(&quizID); err != nil {
		t.Fatalf("create exception Quiz task: %v", err)
	}

	change, err := svc.SetPromptQuizSchedule(ctx, workspaceUUID, nil, false, "workspace hold refreshed", ownerUUID)
	if err != nil {
		t.Fatalf("refresh workspace Quiz disable: %v", err)
	}
	if change.CancelledCount != 0 {
		t.Fatalf("workspace refresh cancelled %d agent-exempt Quiz tasks; want 0", change.CancelledCount)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, quizID).Scan(&status); err != nil {
		t.Fatalf("read agent-exempt Quiz task: %v", err)
	}
	if status != "queued" {
		t.Fatalf("agent-exempt Quiz task status = %q; want queued", status)
	}
	state, err := svc.PromptQuizScheduleState(ctx, workspaceUUID, &agentUUID)
	if err != nil {
		t.Fatalf("read agent override: %v", err)
	}
	if !state.Enabled || state.Scope != PromptQuizScheduleScopeAgent {
		t.Fatalf("agent override state = %+v; want enabled agent override", state)
	}

	change, err = svc.RestorePromptQuizSchedule(ctx, workspaceUUID, &agentUUID, ownerUUID)
	if err != nil {
		t.Fatalf("restore agent exception: %v", err)
	}
	if change.State.Enabled || change.CancelledCount != 1 {
		t.Fatalf("restore agent exception = %+v; want workspace-disabled state and one cancellation", change)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, quizID).Scan(&status); err != nil {
		t.Fatalf("read isolated Quiz task after restore: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("Quiz task after restoring disabled workspace = %q; want cancelled", status)
	}
}
