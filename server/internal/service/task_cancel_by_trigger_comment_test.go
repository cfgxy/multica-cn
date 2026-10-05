package service

// RUYI-462: editing or deleting a dispatch comment revokes only runs that
// have NOT started executing. A run that already entered the execution path
// (running / waiting_local_directory) must survive the source comment's
// edit or delete — the old blanket cancel turned "edit text" into "interrupt
// execution" with no user asking for a stop. Terminal rows are untouched.
// Whatever this path DOES cancel stays auditable: cancel_reason /
// cancel_actor_type on the row plus a run.cancelled audit event naming the
// comment as the trigger source.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func seedTriggerCommentRow(t *testing.T, pool *pgxpool.Pool, workspaceID, issueID, userID string) string {
	t.Helper()
	var commentID string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content)
		VALUES ($1, $2, 'member', $3, 'RUYI-462 trigger comment')
		RETURNING id`, workspaceID, issueID, userID).Scan(&commentID)
	if err != nil {
		t.Fatalf("seed trigger comment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM comment WHERE id = $1`, commentID)
	})
	return commentID
}

// seedTriggerCommentTask inserts one task in the given status pointing at the
// trigger comment (directly or via the coalesced arm). inFlight marks states
// the daemon has begun handling so the row gets a started_at like a real run.
func seedTriggerCommentTask(t *testing.T, pool *pgxpool.Pool, agentID, runtimeID, issueID, userID, status, triggerCommentID string, coalesced []pgtype.UUID) string {
	t.Helper()
	ctx := context.Background()
	inFlight := status == "running" || status == "waiting_local_directory"
	startedAt := any(nil)
	if inFlight || status == "completed" {
		startedAt = time.Now()
	}
	completedAt := any(nil)
	if status == "completed" {
		completedAt = time.Now()
	}
	var triggerCommentArg any
	if triggerCommentID != "" {
		triggerCommentArg = triggerCommentID
	}
	var taskID string
	err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			originator_user_id, accountable_user_id, originator_source,
			trigger_comment_id, coalesced_comment_ids, started_at, completed_at
		)
		VALUES ($1, $2, $3, $4, 0, $5, $5, 'direct_human',
			$6, COALESCE($7::uuid[], '{}'), $8, $9)
		RETURNING id`,
		agentID, runtimeID, issueID, status, userID,
		triggerCommentArg, coalesced, startedAt, completedAt).Scan(&taskID)
	if err != nil {
		t.Fatalf("seed %s task: %v", status, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
	})
	return taskID
}

func taskStatusAndCancelAttribution(t *testing.T, pool *pgxpool.Pool, taskID string) (status string, cancelReason any, cancelActorType any) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT status, cancel_reason, cancel_actor_type FROM agent_task_queue WHERE id = $1`, taskID).
		Scan(&status, &cancelReason, &cancelActorType)
	if err != nil {
		t.Fatalf("reload task %s: %v", taskID, err)
	}
	return status, cancelReason, cancelActorType
}

// TestCancelTasksByTriggerCommentSparesInFlightRuns pins the status matrix for
// comment edit/delete revocation: queued / dispatched / deferred are revoked,
// running / waiting_local_directory survive untouched, terminal rows are never
// rewritten, and every revoked row carries the system attribution.
func TestCancelTasksByTriggerCommentSparesInFlightRuns(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, agentID, issueID := seedAttributionFixture(t, pool)
	runtimeID := fixtureRuntimeID(t, pool, agentID)
	commentID := seedTriggerCommentRow(t, pool, workspaceID, issueID, userID)

	cases := []struct {
		name          string
		status        string
		wantCancelled bool
	}{
		{"queued revoked", "queued", true},
		{"dispatched revoked", "dispatched", true},
		{"deferred revoked", "deferred", true},
		{"running spared", "running", false},
		{"waiting for local directory spared", "waiting_local_directory", false},
		{"completed untouched", "completed", false},
	}
	taskIDs := make(map[string]string, len(cases))
	for i, tc := range cases {
		// One agent per status keeps the pending unique index
		// (queued/dispatched per issue+agent) out of the way.
		statusAgent := agentID
		if i > 0 {
			statusAgent = seedExtraAgent(t, pool, workspaceID, runtimeID, userID, fmt.Sprintf("ruyi462-%s", tc.status))
		}
		taskIDs[tc.status] = seedTriggerCommentTask(t, pool, statusAgent, runtimeID, issueID, userID, tc.status, commentID, nil)
	}

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	cancelled, err := svc.CancelTasksByTriggerComment(ctx, mustUUID(t, commentID))
	if err != nil {
		t.Fatalf("cancel tasks by trigger comment: %v", err)
	}
	// UPDATE ... RETURNING reports the post-cancel row, so membership is
	// judged by task id, not by the (already rewritten) status column.
	cancelledByID := make(map[string]bool, len(cancelled))
	for _, row := range cancelled {
		cancelledByID[row.ID.String()] = true
	}

	for _, tc := range cases {
		status, cancelReason, cancelActorType := taskStatusAndCancelAttribution(t, pool, taskIDs[tc.status])
		if tc.wantCancelled {
			if !cancelledByID[taskIDs[tc.status]] {
				t.Fatalf("%s: task not in returned cancelled set", tc.name)
			}
			if status != "cancelled" {
				t.Fatalf("%s: row status = %q, want cancelled", tc.name, status)
			}
			if cancelReason == nil || cancelReason.(string) != AuditReasonTriggerCommentDeleted {
				t.Fatalf("%s: cancel_reason = %v, want %q", tc.name, cancelReason, AuditReasonTriggerCommentDeleted)
			}
			if cancelActorType == nil || cancelActorType.(string) != AuditActorSystem {
				t.Fatalf("%s: cancel_actor_type = %v, want %q", tc.name, cancelActorType, AuditActorSystem)
			}
			continue
		}
		if gotCancelled := cancelledByID[taskIDs[tc.status]]; gotCancelled {
			t.Fatalf("%s: unexpectedly reported as cancelled", tc.name)
		}
		if status != tc.status {
			t.Fatalf("%s: row status = %q, want preserved %q", tc.name, status, tc.status)
		}
	}
}

// The coalesced arm ($1 = ANY(coalesced_comment_ids)) follows the same rule as
// the direct-trigger arm: revocable while queued, spared once executing.
func TestCancelTasksByTriggerCommentCoalescedArmFollowsSameRule(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, agentID, issueID := seedAttributionFixture(t, pool)
	runtimeID := fixtureRuntimeID(t, pool, agentID)
	commentID := seedTriggerCommentRow(t, pool, workspaceID, issueID, userID)
	commentUUID := mustUUID(t, commentID)

	queuedAgent := seedExtraAgent(t, pool, workspaceID, runtimeID, userID, "ruyi462-coalesced-queued")
	runningAgent := seedExtraAgent(t, pool, workspaceID, runtimeID, userID, "ruyi462-coalesced-running")
	queuedID := seedTriggerCommentTask(t, pool, queuedAgent, runtimeID, issueID, userID, "queued", "", []pgtype.UUID{commentUUID})
	runningID := seedTriggerCommentTask(t, pool, runningAgent, runtimeID, issueID, userID, "running", "", []pgtype.UUID{commentUUID})

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	cancelled, err := svc.CancelTasksByTriggerComment(ctx, commentUUID)
	if err != nil {
		t.Fatalf("cancel tasks by trigger comment: %v", err)
	}
	if len(cancelled) != 1 || cancelled[0].ID.String() != queuedID {
		var got []string
		for _, row := range cancelled {
			got = append(got, row.ID.String())
		}
		t.Fatalf("cancelled = %v, want exactly the queued coalesced row %s", got, queuedID)
	}
	if status, _, _ := taskStatusAndCancelAttribution(t, pool, runningID); status != "running" {
		t.Fatalf("coalesced running task status = %q, want preserved running", status)
	}
}

// Whatever this path stops must stay auditable: one run.cancelled audit event
// per revoked row naming the comment as trigger source, and none for the
// surviving running run.
func TestCancelTasksByTriggerCommentStampsAuditAttribution(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, agentID, issueID := seedAttributionFixture(t, pool)
	runtimeID := fixtureRuntimeID(t, pool, agentID)
	commentID := seedTriggerCommentRow(t, pool, workspaceID, issueID, userID)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_event WHERE workspace_id = $1`, workspaceID)
	})

	queuedID := seedTriggerCommentTask(t, pool, agentID, runtimeID, issueID, userID, "queued", commentID, nil)
	sparedAgent := seedExtraAgent(t, pool, workspaceID, runtimeID, userID, "ruyi462-audit-running")
	sparedID := seedTriggerCommentTask(t, pool, sparedAgent, runtimeID, issueID, userID, "running", commentID, nil)

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	if _, err := svc.CancelTasksByTriggerComment(ctx, mustUUID(t, commentID)); err != nil {
		t.Fatalf("cancel tasks by trigger comment: %v", err)
	}

	revoked := auditRunEventTriggers(t, pool, queuedID)
	mustTrigger(t, revoked, AuditRunCancelled, "comment", commentID)
	spared := auditRunEventTriggers(t, pool, sparedID)
	if _, ok := spared[AuditRunCancelled]; ok {
		t.Fatalf("surviving running task must not gain a run.cancelled audit event")
	}
}
