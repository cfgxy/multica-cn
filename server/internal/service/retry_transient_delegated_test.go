package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

// RUYI-601: transient agent-side failures — a crashed agent subprocess
// (process_failure) or a provider 429/5xx (capacity_or_rate_limit,
// server_error) — must continue on the ORIGINAL agent through the existing
// auto-retry path before delegated-failure recovery hands coordination back
// to the delegating (Leader) task. The retry budget stays the task's default
// (first run + one retry, no bespoke backoff); once exhausted, the recovery
// path behaves exactly as before. All three reasons are resume-safe, so the
// retry child inherits the parent's session and work_dir.
var transientRetryReasons = []string{
	"agent_error.process_failure",
	"agent_error.provider_capacity_or_rate_limit",
	"agent_error.provider_server_error",
}

// Deterministic agent-side and platform-prep failures must never buy an
// automatic retry: they reproduce identically on the next attempt, and the
// delegated-failure recovery (coordinator handback) is the correct response.
var nonRetryableReasons = []string{
	"local_directory_error",
	"agent_error.agent_timeout",
	"agent_error.provider_quota_limit",
}

// TestTransientDelegatedFailureRetriesOnOriginalAgent walks the primary
// FailTask path: an attempt=1 delegated task failing with a transient reason
// spawns a same-agent retry child that inherits the delegation lineage
// (delegated_from_task_id) and resume context, and does NOT wake the
// delegating coordinator.
func TestTransientDelegatedFailureRetriesOnOriginalAgent(t *testing.T) {
	for _, reason := range transientRetryReasons {
		t.Run(reason, func(t *testing.T) {
			f, svc := seedDelegatedFailureFixture(t)
			ctx := context.Background()
			failedID := f.insertWorkerTask(t, "running", "comment", 1, 2)
			if _, err := f.pool.Exec(ctx, `
				UPDATE agent_task_queue SET session_id = 'sess-ruyi601', work_dir = '/tmp/ruyi601-wd'
				WHERE id = $1`, failedID); err != nil {
				t.Fatalf("stamp resume context: %v", err)
			}

			if _, err := svc.FailTask(ctx, failedID, "zcode-acp process exited", "", "", "", reason, false, "", ""); err != nil {
				t.Fatalf("FailTask: %v", err)
			}

			assertSingleRetryChild(t, f, failedID, reason)
			assertCoordinatorNotWoken(t, f, failedID)
		})
	}
}

// TestMaybeRetryFailedTaskTransientReasons walks the orphan-sweeper path for
// the same three transient reasons: the freshly-failed row gets a same-agent
// queued child carrying the delegation lineage.
func TestMaybeRetryFailedTaskTransientReasons(t *testing.T) {
	for _, reason := range transientRetryReasons {
		t.Run(reason, func(t *testing.T) {
			f, svc := seedDelegatedFailureFixture(t)
			ctx := context.Background()
			failedID := f.insertWorkerTask(t, "failed", "comment", 1, 2)
			if _, err := f.pool.Exec(ctx, `
				UPDATE agent_task_queue SET session_id = 'sess-ruyi601', work_dir = '/tmp/ruyi601-wd',
				       failure_reason = $2, error = 'worker process exited', completed_at = now()
				WHERE id = $1`, failedID, reason); err != nil {
				t.Fatalf("stamp failure: %v", err)
			}
			failed, err := svc.Queries.GetAgentTask(ctx, failedID)
			if err != nil {
				t.Fatalf("load failed task: %v", err)
			}

			child, err := svc.MaybeRetryFailedTask(ctx, failed)
			if err != nil {
				t.Fatalf("MaybeRetryFailedTask: %v", err)
			}
			if child == nil {
				t.Fatalf("MaybeRetryFailedTask(%s) = nil, want retry child", reason)
			}
			assertSingleRetryChild(t, f, failedID, reason)
		})
	}
}

// TestTransientRetryBudgetExhaustedFallsThroughToRecovery pins the ceiling:
// at attempt == retryAttemptCeiling the sweeper creates no child, and the
// delegated-failure recovery still routes coordination back to the source
// (Leader) task — the pre-RUYI-601 behavior for budget-exhausted chains.
func TestTransientRetryBudgetExhaustedFallsThroughToRecovery(t *testing.T) {
	f, svc := seedDelegatedFailureFixture(t)
	ctx := context.Background()
	failedID := f.insertWorkerTask(t, "failed", "comment", 2, 2)
	if _, err := f.pool.Exec(ctx, `
		UPDATE agent_task_queue SET failure_reason = 'agent_error.process_failure', error = 'worker process exited', completed_at = now()
		WHERE id = $1`, failedID); err != nil {
		t.Fatalf("stamp failure: %v", err)
	}
	failed, err := svc.Queries.GetAgentTask(ctx, failedID)
	if err != nil {
		t.Fatalf("load failed task: %v", err)
	}

	if child, err := svc.MaybeRetryFailedTask(ctx, failed); err != nil || child != nil {
		t.Fatalf("MaybeRetryFailedTask at ceiling = child %v err %v, want nil/nil", child, err)
	}
	handled, err := svc.recoverDelegatedTaskFailure(ctx, failed)
	if err != nil || !handled {
		t.Fatalf("recoverDelegatedTaskFailure = handled %v err %v, want true/nil", handled, err)
	}
	var recoveryCount int
	var recoveryAgent string
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(max(agent_id::text), '') FROM agent_task_queue
		WHERE trigger_evidence_kind = 'delegated_failure' AND trigger_evidence_ref_id = $1`, failedID).
		Scan(&recoveryCount, &recoveryAgent); err != nil {
		t.Fatalf("read recovery task: %v", err)
	}
	if recoveryCount != 1 || recoveryAgent != f.coordinator {
		t.Fatalf("recovery = count %d agent %s, want 1/%s", recoveryCount, recoveryAgent, f.coordinator)
	}
}

// TestDeterministicFailuresNotAutoRetried locks the negative boundary: the
// listed reasons produce no automatic retry on either the FailTask path or
// the sweeper path, so only the delegated-failure recovery answers them.
func TestDeterministicFailuresNotAutoRetried(t *testing.T) {
	for _, reason := range nonRetryableReasons {
		t.Run(reason, func(t *testing.T) {
			f, svc := seedDelegatedFailureFixture(t)
			ctx := context.Background()
			failedID := f.insertWorkerTask(t, "running", "comment", 1, 2)

			if _, err := svc.FailTask(ctx, failedID, "deterministic failure", "", "", "", reason, false, "", ""); err != nil {
				t.Fatalf("FailTask: %v", err)
			}
			assertNoRetryChild(t, f, failedID)

			// The sweeper must independently refuse the same reason.
			sweeperID := f.insertWorkerTask(t, "failed", "comment", 1, 2)
			if _, err := f.pool.Exec(ctx, `
				UPDATE agent_task_queue SET failure_reason = $2, error = 'deterministic failure', completed_at = now()
				WHERE id = $1`, sweeperID, reason); err != nil {
				t.Fatalf("stamp failure: %v", err)
			}
			swept, err := svc.Queries.GetAgentTask(ctx, sweeperID)
			if err != nil {
				t.Fatalf("load failed task: %v", err)
			}
			if child, err := svc.MaybeRetryFailedTask(ctx, swept); err != nil || child != nil {
				t.Fatalf("MaybeRetryFailedTask(%s) = child %v err %v, want nil/nil", reason, child, err)
			}
			assertNoRetryChild(t, f, sweeperID)
		})
	}
}

// assertSingleRetryChild verifies the retry child the transient-retry policy
// must produce: exactly one, same agent and issue, immediately claimable,
// budget self-consistent, delegation lineage and resume context inherited.
func assertSingleRetryChild(t *testing.T, f *delegatedFailureFixture, parentID pgtype.UUID, reason string) {
	t.Helper()
	ctx := context.Background()
	var (
		count                int
		agentID, issueID     string
		status               string
		attempt, maxAttempts int32
		delegatedFrom        string
		sessionID, workDir   string
		retryOf              string
	)
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(max(agent_id::text), ''), COALESCE(max(issue_id::text), ''),
		       max(status), max(attempt), max(max_attempts),
		       COALESCE(max(delegated_from_task_id::text), ''), COALESCE(max(session_id::text), ''),
		       COALESCE(max(work_dir::text), ''), COALESCE(max(retry_of_task_id::text), '')
		FROM agent_task_queue WHERE parent_task_id = $1`, parentID).
		Scan(&count, &agentID, &issueID, &status, &attempt, &maxAttempts,
			&delegatedFrom, &sessionID, &workDir, &retryOf); err != nil {
		t.Fatalf("read retry child (%s): %v", reason, err)
	}
	if count != 1 {
		t.Fatalf("retry child count = %d, want 1 (%s)", count, reason)
	}
	if agentID != f.worker {
		t.Fatalf("retry child agent = %s, want original worker %s (%s)", agentID, f.worker, reason)
	}
	if issueID != f.workerIssue {
		t.Fatalf("retry child issue = %s, want %s (%s)", issueID, f.workerIssue, reason)
	}
	if status != "queued" {
		t.Fatalf("retry child status = %q, want queued (immediate retry) (%s)", status, reason)
	}
	if attempt != 2 || maxAttempts != 2 {
		t.Fatalf("retry child attempt/max = %d/%d, want 2/2 (%s)", attempt, maxAttempts, reason)
	}
	if delegatedFrom != f.sourceTask {
		t.Fatalf("retry child delegated_from = %q, want source task %s (%s)", delegatedFrom, f.sourceTask, reason)
	}
	if sessionID != "sess-ruyi601" || workDir != "/tmp/ruyi601-wd" {
		t.Fatalf("retry child resume context = session %q workdir %q, want inherited (%s)", sessionID, workDir, reason)
	}
	if retryOf != util.UUIDToString(parentID) {
		t.Fatalf("retry child retry_of = %q, want parent %s (%s)", retryOf, util.UUIDToString(parentID), reason)
	}
}

func assertCoordinatorNotWoken(t *testing.T, f *delegatedFailureFixture, failedID pgtype.UUID) {
	t.Helper()
	var recoveries, comments int
	if err := f.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_task_queue
		WHERE trigger_evidence_kind = 'delegated_failure' AND trigger_evidence_ref_id = $1`, failedID).
		Scan(&recoveries); err != nil {
		t.Fatalf("count recovery tasks: %v", err)
	}
	if err := f.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM comment
		WHERE issue_id = $1 AND type = 'progress_update' AND source_task_id = $2`,
		f.issueID, failedID).Scan(&comments); err != nil {
		t.Fatalf("count recovery comments: %v", err)
	}
	if recoveries != 0 || comments != 0 {
		t.Fatalf("coordinator woken: recovery tasks/comments = %d/%d, want 0/0", recoveries, comments)
	}
}

func assertNoRetryChild(t *testing.T, f *delegatedFailureFixture, parentID pgtype.UUID) {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE parent_task_id = $1`, parentID).Scan(&count); err != nil {
		t.Fatalf("count retry children: %v", err)
	}
	if count != 0 {
		t.Fatalf("retry child count = %d, want 0", count)
	}
}
