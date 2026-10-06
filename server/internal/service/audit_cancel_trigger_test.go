package service

// RUYI-355 P2-2 regression: the server-side batch cancel paths must stamp
// every run.cancelled audit event with a dereferenceable trigger source —
// trigger_kind names the causal object type, trigger_ref carries that row's
// id (the MUL-4302 §2 evidence-handle shape audit_event reuses). REST/MCP
// readability rides the existing auditEventDTO pass-through, so the DB row
// is the contract.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func seedQueuedIssueTask(t *testing.T, pool *pgxpool.Pool, agentID, runtimeID, issueID, userID, status string) string {
	t.Helper()
	var taskID string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			originator_user_id, accountable_user_id, originator_source
		)
		VALUES ($1, $2, $3, $4, 0, $5, $5, 'direct_human')
		RETURNING id`, agentID, runtimeID, issueID, status, userID).Scan(&taskID)
	if err != nil {
		t.Fatalf("seed %s task: %v", status, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
	})
	return taskID
}

// auditRunEventTriggers returns event_type → (trigger_kind, trigger_ref) for
// the run-domain audit rows of one task.
func auditRunEventTriggers(t *testing.T, pool *pgxpool.Pool, taskID string) map[string][2]*string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT event_type, trigger_kind, trigger_ref
		FROM audit_event
		WHERE task_id = $1 AND domain = 'run'
		ORDER BY event_type, occurred_at`, taskID)
	if err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	defer rows.Close()
	out := map[string][2]*string{}
	for rows.Next() {
		var eventType string
		var kind, ref *string
		if err := rows.Scan(&eventType, &kind, &ref); err != nil {
			t.Fatalf("scan audit event: %v", err)
		}
		out[eventType] = [2]*string{kind, ref}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit events: %v", err)
	}
	return out
}

func mustTrigger(t *testing.T, events map[string][2]*string, eventType, wantKind, wantRef string) {
	t.Helper()
	got, ok := events[eventType]
	if !ok {
		t.Fatalf("no %s audit event written", eventType)
	}
	kind, ref := got[0], got[1]
	if kind == nil || *kind != wantKind {
		t.Fatalf("%s: trigger_kind = %v, want %q", eventType, kind, wantKind)
	}
	if ref == nil || *ref != wantRef {
		t.Fatalf("%s: trigger_ref = %v, want the dereferenceable %q", eventType, ref, wantRef)
	}
}

// seedExtraAgent clones the fixture agent on the same runtime so a test can
// hold two pending tasks on one issue without tripping the
// one-pending-task-per-issue-agent index.
func seedExtraAgent(t *testing.T, pool *pgxpool.Pool, workspaceID, runtimeID, userID, name string) string {
	t.Helper()
	var agentID string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility,
			max_concurrent_tasks, owner_id, instructions, custom_env, custom_args
		)
		VALUES ($1, $2, 'cloud', '{}'::jsonb, $3, 'workspace', 1, $4, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id`, workspaceID, name, runtimeID, userID).Scan(&agentID)
	if err != nil {
		t.Fatalf("seed extra agent: %v", err)
	}
	return agentID
}

// The issue-cancellation cascade is the QA-observed batch path (P2-2): both
// terminal flips (run.cancelled) and in-flight stop requests
// (run.cancel_requested) name the cancelled issue as their trigger source,
// with a ref that resolves to the issue row.
func TestCancelRunsForCancelledIssueStampsTriggerSource(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, agentID, issueID := seedAttributionFixture(t, pool)
	runtimeID := fixtureRuntimeID(t, pool, agentID)
	cancelledID := seedQueuedIssueTask(t, pool, agentID, runtimeID, issueID, userID, "queued")
	requestedAgentID := seedExtraAgent(t, pool, workspaceID, runtimeID, userID, "audit-trigger-dispatched")
	requestedID := seedQueuedIssueTask(t, pool, requestedAgentID, runtimeID, issueID, userID, "dispatched")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_event WHERE workspace_id = $1`, workspaceID)
	})

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	if err := svc.CancelRunsForCancelledIssue(ctx, mustUUID(t, issueID), mustUUID(t, userID)); err != nil {
		t.Fatalf("cancel runs for cancelled issue: %v", err)
	}

	cancelled := auditRunEventTriggers(t, pool, cancelledID)
	mustTrigger(t, cancelled, AuditRunCancelled, "issue", issueID)
	requested := auditRunEventTriggers(t, pool, requestedID)
	mustTrigger(t, requested, AuditRunCancelRequested, "issue", issueID)
}

// The agent-level "cancel all tasks" sweep is the second server-side batch
// path: the stopped agent is the trigger source of each run.cancelled.
func TestCancelTasksForAgentStampsTriggerSource(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, agentID, issueID := seedAttributionFixture(t, pool)
	runtimeID := fixtureRuntimeID(t, pool, agentID)
	taskID := seedQueuedIssueTask(t, pool, agentID, runtimeID, issueID, userID, "queued")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_event WHERE workspace_id = $1`, workspaceID)
	})

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	if _, err := svc.CancelTasksForAgent(ctx, mustUUID(t, agentID)); err != nil {
		t.Fatalf("cancel tasks for agent: %v", err)
	}

	mustTrigger(t, auditRunEventTriggers(t, pool, taskID), AuditRunCancelled, "agent", agentID)
}

// Single-run cancels have no causal object beyond the run itself: the trigger
// pair must stay NULL there — an empty-string kind/ref would be
// non-dereferenceable noise in the chain.
func TestTaskCancelledEventWithoutTriggerLeavesPairNil(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	_, userID, _, _ := seedAttributionFixture(t, pool)

	ev := TaskCancelledEvent(ctx, db.New(pool), db.AgentTaskQueue{ID: mustUUID(t, userID)},
		AuditReasonUserRequested, AuditActorMember, mustUUID(t, userID), nil, AuditTrigger{})
	if ev.TriggerKind != nil || ev.TriggerRef != nil {
		t.Fatalf("empty AuditTrigger must leave the pair nil, got kind=%v ref=%v", ev.TriggerKind, ev.TriggerRef)
	}

	withTrig := TaskCancelledEvent(ctx, db.New(pool), db.AgentTaskQueue{ID: mustUUID(t, userID)},
		AuditReasonIssueCancelled, AuditActorSystem, pgtype.UUID{}, nil, AuditTrigger{Kind: "issue", Ref: userID})
	if withTrig.TriggerKind == nil || *withTrig.TriggerKind != "issue" {
		t.Fatalf("trigger kind not stamped: %v", withTrig.TriggerKind)
	}
	if withTrig.TriggerRef == nil || *withTrig.TriggerRef != userID {
		t.Fatalf("trigger ref not stamped: %v", withTrig.TriggerRef)
	}
}

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	u, err := util.ParseUUID(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return u
}

func fixtureRuntimeID(t *testing.T, pool *pgxpool.Pool, agentID string) string {
	t.Helper()
	var runtimeID string
	if err := pool.QueryRow(context.Background(),
		`SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("load agent runtime: %v", err)
	}
	return runtimeID
}
