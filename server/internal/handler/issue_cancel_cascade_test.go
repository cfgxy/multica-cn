package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RUYI-384: cancelling an issue cascades to its runs. Every non-terminal run
// on the issue enters the cancellation flow — queued/deferred rows (no process
// anywhere) flip straight to cancelled, in-flight rows (dispatched/running/
// waiting_local_directory) flip to cancel_requested and are confirmed by the
// daemon's cancel-ack — while terminal rows are never rewritten. The cascade is
// idempotent, refuses new runs on a cancelled issue, and serializes against
// concurrent enqueues through the issue row. These tests replace the MUL-4465
// no-cancel contract (issue_cancel_status_no_cancel_test.go), which RUYI-384
// reverses.

// openRunStatuses are the non-terminal states the issue-cancellation cascade
// sweeps. cancel_requested is covered separately (pre-existing rows are left to
// the daemon's two-phase pipeline).
var openRunStatuses = []string{"queued", "dispatched", "running", "waiting_local_directory", "deferred"}

// insertIssueTaskWithStatus inserts one task for the agent on the issue in the
// given status, populating the per-status columns the schema expects, and
// registers cleanup.
func insertIssueTaskWithStatus(t *testing.T, agentID, issueID, status string) string {
	t.Helper()
	var startedAt, fireAt, waitReason, cancelRequestedAt any // nil => SQL NULL
	switch status {
	case "running", "dispatched":
		startedAt = time.Now()
	case "deferred":
		fireAt = time.Now().Add(time.Hour)
	case "waiting_local_directory":
		waitReason = "waiting for local directory"
	case "cancel_requested":
		cancelRequestedAt = time.Now()
	}
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, started_at, fire_at, wait_reason, cancel_requested_at)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), $2, 0, $3, $4, $5, $6, $7)
		RETURNING id
	`, agentID, status, issueID, startedAt, fireAt, waitReason, cancelRequestedAt).Scan(&taskID); err != nil {
		t.Fatalf("insert %s task: %v", status, err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	return taskID
}

// taskState reads the columns the cascade assertions compare: status,
// completed_at and failure_reason (empty string for SQL NULL).
func taskState(t *testing.T, taskID string) (status string, completedAt *time.Time, failureReason string) {
	t.Helper()
	var completed pgtype.Timestamptz
	var reason pgtype.Text
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, completed_at, failure_reason FROM agent_task_queue WHERE id = $1
	`, taskID).Scan(&status, &completed, &reason); err != nil {
		t.Fatalf("read task %s: %v", taskID, err)
	}
	if completed.Valid {
		at := completed.Time
		completedAt = &at
	}
	return status, completedAt, reason.String
}

// taskTriple reads (status, completed_at, failure_reason) preserving NULL-ness
// so untouched-row comparisons are exact.
func taskTriple(t *testing.T, taskID string) [3]string {
	t.Helper()
	var status string
	var completed pgtype.Timestamptz
	var reason pgtype.Text
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, completed_at, failure_reason FROM agent_task_queue WHERE id = $1
	`, taskID).Scan(&status, &completed, &reason); err != nil {
		t.Fatalf("read task %s: %v", taskID, err)
	}
	completedAt := "<null>"
	if completed.Valid {
		completedAt = completed.Time.UTC().Format(time.RFC3339Nano)
	}
	reasonText := "<null>"
	if reason.Valid {
		reasonText = reason.String
	}
	return [3]string{status, completedAt, reasonText}
}

// cancelIssueViaAPI drives PUT /api/issues/{id} to status=cancelled.
func cancelIssueViaAPI(t *testing.T, issueID string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+issueID, map[string]any{
		"status": "cancelled",
	})
	req = withURLParam(req, "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue cancel: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// settleCancelRequested plays the daemon's cancel-ack: the RUYI-292 phase-2
// convergence (ConvergeCancelRequestedToCancelled) that records the confirmed
// stop once the daemon interrupted the process.
func settleCancelRequested(t *testing.T, taskID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue
		SET status = 'cancelled', completed_at = now()
		WHERE id = $1 AND status = 'cancel_requested'
	`, taskID); err != nil {
		t.Fatalf("settle cancel_requested: %v", err)
	}
}

// TestCancelIssueCancelsRunningRun — regression 1: a running run stops when the
// issue is cancelled. The server-side flip lands as cancel_requested (stop
// accepted, awaiting the daemon interrupt); the daemon's ack then converges the
// row to cancelled.
func TestCancelIssueCancelsRunningRun(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ownerAgent := createHandlerTestAgent(t, "CancelCascadeRunning", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, ownerAgent, 92201, "cancel-cascade-running")
	taskID := insertIssueTaskWithStatus(t, ownerAgent, issueID, "running")

	cancelIssueViaAPI(t, issueID)

	status, _, failureReason := taskState(t, taskID)
	if status != "cancel_requested" {
		t.Fatalf("running task must enter cancel_requested on issue cancel, got %q", status)
	}
	if failureReason != "" {
		t.Fatalf("in-flight row keeps no failure stamp, got %q", failureReason)
	}

	settleCancelRequested(t, taskID)
	status, completedAt, _ := taskState(t, taskID)
	if status != "cancelled" || completedAt == nil {
		t.Fatalf("daemon ack must converge to cancelled with completed_at, got %q completed=%v", status, completedAt)
	}
}

// TestCancelIssuePreventsQueuedRunFromStarting — regression 2: a queued run
// never starts after the issue is cancelled. The row flips straight to
// cancelled (no process anywhere), so the daemon claim has nothing to pick up.
func TestCancelIssuePreventsQueuedRunFromStarting(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ownerAgent := createHandlerTestAgent(t, "CancelCascadeQueued", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, ownerAgent, 92202, "cancel-cascade-queued")
	taskID := insertIssueTaskWithStatus(t, ownerAgent, issueID, "queued")

	cancelIssueViaAPI(t, issueID)

	status, completedAt, failureReason := taskState(t, taskID)
	if status != "cancelled" || completedAt == nil {
		t.Fatalf("queued task must flip straight to cancelled, got %q completed=%v", status, completedAt)
	}
	if failureReason != "issue_cancelled" {
		t.Fatalf("directly cancelled row carries failure_reason='issue_cancelled' for traceability, got %q", failureReason)
	}

	// The claim guard refuses a queued row whose issue is cancelled — the
	// belt-and-suspenders for rows that slip past the enqueue fence. Seed one
	// the way a torn race would leave it and watch the claim walk away.
	var agentUUID, runtimeUUID pgtype.UUID
	if err := testPool.QueryRow(context.Background(),
		`SELECT id, runtime_id FROM agent WHERE id = $1`, ownerAgent).Scan(&agentUUID, &runtimeUUID); err != nil {
		t.Fatalf("load agent: %v", err)
	}
	orphanID := ""
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id)
		VALUES ($1, $2, 'queued', 0, $3)
		RETURNING id
	`, ownerAgent, runtimeUUID, issueID).Scan(&orphanID); err != nil {
		t.Fatalf("seed orphan queued row: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, orphanID) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, claimErr := db.New(testPool).ClaimAgentTask(ctx, db.ClaimAgentTaskParams{
		AgentID:          agentUUID,
		RuntimeID:        runtimeUUID,
		PrepareLeaseSecs: 30,
		RuntimeStaleSecs: service.RuntimeClaimFreshnessSeconds,
	}); claimErr == nil {
		t.Fatal("claim must refuse a queued run on a cancelled issue")
	}
}

// TestCancelIssueCancelsEveryOpenRun — regression 3: one issue, many open runs
// (each on its own agent: the (issue, agent) pending unique index forbids one
// agent holding both the queued and the dispatched slot) — every one is
// addressed. Rows already in cancel_requested stay in the daemon's hands
// untouched.
func TestCancelIssueCancelsEveryOpenRun(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	issueID := insertAgentAssignedIssue(t, createHandlerTestAgent(t, "CancelCascadeMultiOwner", []byte("[]")), 92203, "cancel-cascade-multi")

	tasks := make(map[string]string, len(openRunStatuses)+2)
	for _, status := range openRunStatuses {
		agent := createHandlerTestAgent(t, "CancelCascadeMulti"+status, []byte("[]"))
		tasks[status] = insertIssueTaskWithStatus(t, agent, issueID, status)
	}
	tasks["mention_running"] = insertIssueTaskWithStatus(t,
		createHandlerTestAgent(t, "CancelCascadeMultiMention", []byte("[]")), issueID, "running")

	// A pre-existing cancel_requested row is already inside the two-phase
	// pipeline; the cascade must neither rewrite it nor lose it.
	preRequested := insertIssueTaskWithStatus(t,
		createHandlerTestAgent(t, "CancelCascadeMultiPreReq", []byte("[]")), issueID, "cancel_requested")
	before := taskTriple(t, preRequested)

	cancelIssueViaAPI(t, issueID)

	for _, status := range []string{"queued", "deferred"} {
		got, _, _ := taskState(t, tasks[status])
		if got != "cancelled" {
			t.Fatalf("%s run must flip straight to cancelled, got %q", status, got)
		}
	}
	for _, label := range []string{"dispatched", "running", "waiting_local_directory", "mention_running"} {
		got, _, _ := taskState(t, tasks[label])
		if got != "cancel_requested" {
			t.Fatalf("%s run must enter cancel_requested, got %q", label, got)
		}
	}

	if after := taskTriple(t, preRequested); after != before {
		t.Fatalf("cascade must not rewrite an existing cancel_requested row: before=%v after=%v", before, after)
	}
}

// TestCancelIssueWithoutActiveRunsSucceeds — regression 4: cancelling an issue
// with no open runs is a plain no-op, not an error.
func TestCancelIssueWithoutActiveRunsSucceeds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ownerAgent := createHandlerTestAgent(t, "CancelCascadeIdle", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, ownerAgent, 92204, "cancel-cascade-idle")

	cancelIssueViaAPI(t, issueID)
}

// TestCancelIssueLeavesTerminalRunsUntouched — regression 5: completed, failed
// and already-cancelled runs keep their exact rows.
func TestCancelIssueLeavesTerminalRunsUntouched(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ownerAgent := createHandlerTestAgent(t, "CancelCascadeTerminal", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, ownerAgent, 92205, "cancel-cascade-terminal")

	terminalTasks := make(map[string]string, 3)
	for _, status := range []string{"completed", "failed", "cancelled"} {
		var completedAt any // completed/failed carry one; a cancelled row does not
		if status != "cancelled" {
			completedAt = time.Now()
		}
		var taskID string
		if err := testPool.QueryRow(context.Background(), `
			INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, completed_at)
			VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), $2, 0, $3, $4)
			RETURNING id
		`, ownerAgent, status, issueID, completedAt).Scan(&taskID); err != nil {
			t.Fatalf("insert %s task: %v", status, err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
		terminalTasks[status] = taskID
	}

	before := make(map[string][3]string, len(terminalTasks))
	for status, id := range terminalTasks {
		before[status] = taskTriple(t, id)
	}

	cancelIssueViaAPI(t, issueID)

	for status, id := range terminalTasks {
		if after := taskTriple(t, id); after != before[status] {
			t.Fatalf("%s run must stay untouched: before=%v after=%v", status, before[status], after)
		}
	}
}

// TestCancelIssueRepeatedIsIdempotent — regression 6: cancelling twice changes
// nothing the second time and never errors.
func TestCancelIssueRepeatedIsIdempotent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ownerAgent := createHandlerTestAgent(t, "CancelCascadeRepeat", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, ownerAgent, 92206, "cancel-cascade-repeat")
	taskID := insertIssueTaskWithStatus(t, ownerAgent, issueID, "running")

	cancelIssueViaAPI(t, issueID)
	first := taskTriple(t, taskID)

	cancelIssueViaAPI(t, issueID) // repeat must not error nor rewrite

	if after := taskTriple(t, taskID); after != first {
		t.Fatalf("repeat cancel must not move the row: first=%v second=%v", first, after)
	}
}

// TestCancelledIssueRefusesAssignRun — regression 7a: one request that both
// (re)assigns the issue and cancels it must not enqueue a run for the cancelled
// issue.
func TestCancelledIssueRefusesAssignRun(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ownerAgent := createHandlerTestAgent(t, "CancelCascadeAssignA", []byte("[]"))
	otherAgent := createHandlerTestAgent(t, "CancelCascadeAssignB", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, ownerAgent, 92207, "cancel-cascade-assign")

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+issueID, map[string]any{
		"assignee_id": otherAgent,
		"status":      "cancelled",
	})
	req = withURLParam(req, "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("assign+cancel update: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var runs int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`, issueID).Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != 0 {
		t.Fatalf("cancelled issue must not gain a run from the same request, found %d", runs)
	}
}

// TestEnqueueTaskForIssueRefusedOnCancelledIssue — regression 7b: the enqueue
// path itself refuses a cancelled issue with ErrIssueCancelled.
func TestEnqueueTaskForIssueRefusedOnCancelledIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ownerAgent := createHandlerTestAgent(t, "CancelCascadeEnqueue", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, ownerAgent, 92208, "cancel-cascade-enqueue")
	cancelIssueViaAPI(t, issueID)

	issue, err := testHandler.Queries.GetIssue(context.Background(), util.MustParseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	if _, enqueueErr := testHandler.TaskService.EnqueueTaskForIssue(context.Background(), issue); !errors.Is(enqueueErr, service.ErrIssueCancelled) {
		t.Fatalf("enqueue on cancelled issue must fail with ErrIssueCancelled, got %v", enqueueErr)
	}
}

// TestIssueCancellationSerializesWithEnqueue — regression 7c: the enqueue fence
// and the cancellation mutually exclude on the issue row, in both directions.
//
// Direction A: while an uncommitted cancellation holds the issue row, the
// guarded enqueue blocks — and after the cancellation commits, the enqueue is
// refused. Direction B: while an enqueue's issue fence lock (FOR SHARE) is
// held, the cancellation's UPDATE blocks. pg_blocking_pids attributes each
// waiter to the holder's backend, so the assertions are timing-free.
func TestIssueCancellationSerializesWithEnqueue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ownerAgent := createHandlerTestAgent(t, "CancelCascadeFence", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, ownerAgent, 92209, "cancel-cascade-fence")
	ctx := context.Background()

	// Direction A.
	txCancel, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin cancel tx: %v", err)
	}
	cancelPID := holderBackendPID(t, ctx, txCancel)
	if _, err := txCancel.Exec(ctx,
		`UPDATE issue SET status = 'cancelled' WHERE id = $1`, issueID); err != nil {
		t.Fatalf("hold uncommitted cancel: %v", err)
	}

	issue, err := testHandler.Queries.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	enqueued := make(chan error, 1)
	go func() {
		_, enqueueErr := testHandler.TaskService.EnqueueTaskForIssue(ctx, issue)
		enqueued <- enqueueErr
	}()
	if !waitForWaiterBlockedBy(t, cancelPID, 10*time.Second) {
		t.Fatal("enqueue must block on the issue row while a cancellation is uncommitted")
	}
	if err := txCancel.Commit(ctx); err != nil {
		t.Fatalf("commit cancel: %v", err)
	}
	if enqueueErr := <-enqueued; !errors.Is(enqueueErr, service.ErrIssueCancelled) {
		t.Fatalf("enqueue after committed cancel must fail with ErrIssueCancelled, got %v", enqueueErr)
	}

	// Direction B.
	txFence, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin fence tx: %v", err)
	}
	defer txFence.Rollback(ctx)
	fencePID := holderBackendPID(t, ctx, txFence)
	if _, err := txFence.Exec(ctx,
		`SELECT * FROM issue WHERE id = $1 FOR SHARE`, issueID); err != nil {
		t.Fatalf("hold issue fence: %v", err)
	}
	cancelled := make(chan error, 1)
	go func() {
		_, cancelErr := testPool.Exec(ctx,
			`UPDATE issue SET status = 'cancelled' WHERE id = $1`, issueID)
		cancelled <- cancelErr
	}()
	if !waitForWaiterBlockedBy(t, fencePID, 10*time.Second) {
		t.Fatal("cancellation must block on the issue row while an enqueue fence is held")
	}
	if err := txFence.Rollback(ctx); err != nil {
		t.Fatalf("release fence: %v", err)
	}
	if cancelErr := <-cancelled; cancelErr != nil {
		t.Fatalf("cancellation proceeds once the fence releases: %v", cancelErr)
	}
}
