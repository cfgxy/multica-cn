package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// RUYI-48: while a run is active, a comment mention for the same agent lands
// as a fresh QUEUED task (the (issue, agent) unique index only covers
// queued/dispatched, not running). The running job can read and act on that
// message mid-run, but nothing told the queue — so the queued task stayed
// queued and re-executed the same work after the active run finished.
//
// ConsumeQueuedTasksForIssue (POST /api/issues/{id}/consume-queued-tasks) is
// the formal consumption channel: authenticated by the running task's own
// mat_ task-token identity, it folds every same-head queued task's comments
// into the running task's delivered_comment_ids receipt and cancels the queued
// rows, so completion reconcile never replays them.

// consumeQueuedTaskFixture seeds the "running task + queued message" shape the
// bug lives in and returns (issueID, agentID, runningTaskID, queuedTaskID,
// triggerCommentID, coalescedCommentID).
func consumeQueuedTaskFixture(t *testing.T, number int, runningStatus, runningHead, queuedHead string) (string, string, string, string, string, string) {
	t.Helper()

	var agentID, runtimeID string
	dbfx.QueryRow(t,
		`SELECT id, runtime_id FROM agent WHERE workspace_id = $1 AND runtime_id IS NOT NULL LIMIT 1`,
		testWorkspaceID).Scan(&agentID, &runtimeID)

	issueID := dbfx.Issue(t, "consume-queued fixture", testutil.Cols{
		"status":        "in_progress",
		"number":        number,
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})

	triggerCommentID := dbfx.Comment(t, issueID, "queued follow-up request", testutil.Cols{
		"created_at": testutil.Raw("now() - interval '2 minutes'"),
	})
	coalescedCommentID := dbfx.Comment(t, issueID, "earlier queued request", testutil.Cols{
		"created_at": testutil.Raw("now() - interval '3 minutes'"),
	})

	runningCtx := "{}"
	if runningHead != "" {
		runningCtx = `{"head_sha": "` + runningHead + `"}`
	}
	queuedCtx := "{}"
	if queuedHead != "" {
		queuedCtx = `{"head_sha": "` + queuedHead + `"}`
	}

	var runningTaskID string
	dbfx.QueryRow(t, `
		INSERT INTO agent_task_queue
			(agent_id, runtime_id, issue_id, status, priority, created_at, started_at, context)
		VALUES ($1, $2, $3, $4, 0, now() - interval '10 minutes', now() - interval '5 minutes', $5::jsonb)
		RETURNING id
	`, agentID, runtimeID, issueID, runningStatus, runningCtx).Scan(&runningTaskID)

	var queuedTaskID string
	dbfx.QueryRow(t, `
		INSERT INTO agent_task_queue
			(agent_id, runtime_id, issue_id, status, priority, created_at,
			 trigger_comment_id, coalesced_comment_ids, context)
		VALUES ($1, $2, $3, 'queued', 0, now() - interval '1 minute',
			 $4, ARRAY[$5::uuid], $6::jsonb)
		RETURNING id
	`, agentID, runtimeID, issueID, triggerCommentID, coalescedCommentID, queuedCtx).Scan(&queuedTaskID)

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
	})

	return issueID, agentID, runningTaskID, queuedTaskID, triggerCommentID, coalescedCommentID
}

// consumeQueuedTasksRequest builds an issue-scoped POST whose auth headers
// mirror what the Auth middleware stamps for a mat_ task token (server-set,
// so tests may set them directly): X-Actor-Source + the bound agent/task.
func consumeQueuedTasksRequest(t *testing.T, issueID, actorSource, taskID, agentID string) *http.Request {
	t.Helper()
	req := newRequestAs(testUserID, http.MethodPost, "/api/issues/"+issueID+"/consume-queued-tasks", nil)
	if actorSource != "" {
		req.Header.Set("X-Actor-Source", actorSource)
	}
	if taskID != "" {
		req.Header.Set("X-Task-ID", taskID)
	}
	if agentID != "" {
		req.Header.Set("X-Agent-ID", agentID)
	}
	req = withURLParam(req, "id", issueID)
	return withChatTestWorkspaceCtx(t, req)
}

// TestConsumeQueuedTasks_ReleasesQueuedTaskAndRecordsReceipt is the core
// fix: consuming folds the queued task's comments into the running task's
// delivered receipt and cancels the queued row, so the queue list clears and
// the message never runs twice.
func TestConsumeQueuedTasks_ReleasesQueuedTaskAndRecordsReceipt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	issueID, agentID, runningTaskID, queuedTaskID, triggerCommentID, coalescedCommentID :=
		consumeQueuedTaskFixture(t, 999310, "running", "", "")

	w := httptest.NewRecorder()
	testHandler.ConsumeQueuedTasksForIssue(w, consumeQueuedTasksRequest(t, issueID, "task_token", runningTaskID, agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("consume: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ConsumedTasks []struct {
			ID string `json:"id"`
		} `json:"consumed_tasks"`
		ConsumedCommentIDs []string `json:"consumed_comment_ids"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.ConsumedTasks) != 1 || resp.ConsumedTasks[0].ID != queuedTaskID {
		t.Fatalf("expected queued task %s consumed, got %+v", queuedTaskID, resp.ConsumedTasks)
	}
	if len(resp.ConsumedCommentIDs) != 2 {
		t.Fatalf("expected 2 consumed comment ids, got %v", resp.ConsumedCommentIDs)
	}

	var status, parentTaskID, failureReason string
	var completed bool
	dbfx.QueryRow(t, `
		SELECT status, COALESCE(parent_task_id::text, ''), COALESCE(failure_reason, ''),
		       completed_at IS NOT NULL
		FROM agent_task_queue WHERE id = $1
	`, queuedTaskID).Scan(&status, &parentTaskID, &failureReason, &completed)
	if status != "cancelled" || !completed {
		t.Fatalf("queued task not released: status=%q completed=%v", status, completed)
	}
	if parentTaskID != runningTaskID {
		t.Fatalf("expected consumed task to point at running task %s, got %q", runningTaskID, parentTaskID)
	}
	if failureReason != "consumed_by_running_task" {
		t.Fatalf("expected consumed_by_running_task failure reason, got %q", failureReason)
	}

	var delivered bool
	dbfx.QueryRow(t, `
		SELECT $1::uuid = ANY(delivered_comment_ids) AND $2::uuid = ANY(delivered_comment_ids)
		FROM agent_task_queue WHERE id = $3
	`, triggerCommentID, coalescedCommentID, runningTaskID).Scan(&delivered)
	if !delivered {
		t.Fatalf("running task receipt missing consumed comments")
	}
}

// TestConsumeQueuedTasks_ConsumedMessagesNotReplayedOnCompletion pins the
// other half of the fix: after consumption, completion reconcile must NOT
// re-enqueue the consumed comments as a follow-up run (the duplicate
// execution the Owner observed).
func TestConsumeQueuedTasks_ConsumedMessagesNotReplayedOnCompletion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	issueID, agentID, runningTaskID, _, _, _ :=
		consumeQueuedTaskFixture(t, 999310, "running", "", "")

	w := httptest.NewRecorder()
	testHandler.ConsumeQueuedTasksForIssue(w, consumeQueuedTasksRequest(t, issueID, "task_token", runningTaskID, agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("consume: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Completing the run drives reconcileCommentsOnCompletion; the consumed
	// comments are in the delivered receipt, so no follow-up may appear.
	if cw := completeTaskViaHandler(t, runningTaskID, "done"); cw.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", cw.Code, cw.Body.String())
	}

	if n := queuedTaskCountForAgentIssue(t, issueID, agentID); n != 0 {
		t.Fatalf("expected no queued follow-up after completion, got %d", n)
	}
}

// TestConsumeQueuedTasks_IsHeadScoped (TEN-356): a queued task stamped for a
// DIFFERENT head must never be folded into an old-head running task — an
// old-head run would swallow a new-head review request.
func TestConsumeQueuedTasks_IsHeadScoped(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	issueID, agentID, runningTaskID, queuedTaskID, _, _ :=
		consumeQueuedTaskFixture(t, 999312, "running", "head-old", "head-new")

	w := httptest.NewRecorder()
	testHandler.ConsumeQueuedTasksForIssue(w, consumeQueuedTasksRequest(t, issueID, "task_token", runningTaskID, agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("consume: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if got := taskStatus(t, queuedTaskID); got != "queued" {
		t.Fatalf("different-head queued task must stay queued, got %q", got)
	}
}

// TestConsumeQueuedTasks_RejectsNonTaskTokenCredential: consumption is the
// running job's own act, so a human JWT / mul_ PAT credential must not be
// able to release queue entries.
func TestConsumeQueuedTasks_RejectsNonTaskTokenCredential(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	issueID, agentID, runningTaskID, queuedTaskID, _, _ :=
		consumeQueuedTaskFixture(t, 999310, "running", "", "")

	w := httptest.NewRecorder()
	testHandler.ConsumeQueuedTasksForIssue(w, consumeQueuedTasksRequest(t, issueID, "", runningTaskID, agentID))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-task-token credential, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, queuedTaskID); got != "queued" {
		t.Fatalf("rejected consume must not touch the queue, got %q", got)
	}
}

// TestConsumeQueuedTasks_RejectsTaskNotActive: once the run is over, its
// credential can no longer consume — completion reconcile owns the messages.
func TestConsumeQueuedTasks_RejectsTaskNotActive(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	issueID, agentID, runningTaskID, queuedTaskID, _, _ :=
		consumeQueuedTaskFixture(t, 999313, "completed", "", "")

	w := httptest.NewRecorder()
	testHandler.ConsumeQueuedTasksForIssue(w, consumeQueuedTasksRequest(t, issueID, "task_token", runningTaskID, agentID))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a non-active consuming task, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, queuedTaskID); got != "queued" {
		t.Fatalf("rejected consume must not touch the queue, got %q", got)
	}
}

// TestConsumeQueuedTasks_NoQueueIsNoop keeps the call retry-safe: with no
// TestConsumeQueuedTasks_ReconsumeIsNoop keeps the call retry-safe: once the
// queued task is released, a repeat consumption succeeds with an empty result
// instead of erroring or double-cancelling.
func TestConsumeQueuedTasks_ReconsumeIsNoop(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	issueID, agentID, runningTaskID, _, _, _ :=
		consumeQueuedTaskFixture(t, 999310, "running", "", "")

	first := httptest.NewRecorder()
	testHandler.ConsumeQueuedTasksForIssue(first, consumeQueuedTasksRequest(t, issueID, "task_token", runningTaskID, agentID))
	if first.Code != http.StatusOK {
		t.Fatalf("first consume: expected 200, got %d: %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	testHandler.ConsumeQueuedTasksForIssue(second, consumeQueuedTasksRequest(t, issueID, "task_token", runningTaskID, agentID))
	if second.Code != http.StatusOK {
		t.Fatalf("repeat consume: expected 200, got %d: %s", second.Code, second.Body.String())
	}

	var resp struct {
		ConsumedTasks []struct {
			ID string `json:"id"`
		} `json:"consumed_tasks"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.ConsumedTasks) != 0 {
		t.Fatalf("expected empty repeat consumption, got %+v", resp.ConsumedTasks)
	}
}

// TestConsumeQueuedTasks_RejectsForeignTask: the X-Task-ID binding must match
// both the calling agent and the URL issue — a task token from another
// issue/agent cannot release this issue's queue.
func TestConsumeQueuedTasks_RejectsForeignTask(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	otherIssueID, _, otherTaskID, _, _, _ :=
		consumeQueuedTaskFixture(t, 999314, "running", "", "")
	issueID, agentID, _, queuedTaskID, _, _ :=
		consumeQueuedTaskFixture(t, 999315, "running", "", "")

	w := httptest.NewRecorder()
	testHandler.ConsumeQueuedTasksForIssue(w, consumeQueuedTasksRequest(t, issueID, "task_token", otherTaskID, agentID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for foreign consuming task, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, queuedTaskID); got != "queued" {
		t.Fatalf("rejected consume must not touch the queue, got %q", got)
	}
	_ = otherIssueID
}
