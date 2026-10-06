package handler

// RUYI-462: editing or deleting a dispatch comment revokes only runs that
// have not started executing. The RUYI-460 incident: a member edited their
// already-mentioned comment twice while the triggered run was executing and
// both runs silently flipped to cancelled with no user ever asking for a
// stop. These tests drive the real HTTP handlers against the database and
// pin the matrix: queued revokes (and re-triggers) exactly as before,
// running survives body edits, mention removal and comment deletion, and
// completed rows are never rewritten.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func editRunLifecycleMentionContent(t *testing.T, agentID, instruction string) string {
	t.Helper()
	return fmt.Sprintf("[@Lifecycle Agent](mention://agent/%s) %s", agentID, instruction)
}

func postMentionCommentForLifecycleTest(t *testing.T, issueID, agentID, instruction string) string {
	t.Helper()
	return postCommentForTriggerPreviewTest(t, issueID, map[string]any{
		"content": editRunLifecycleMentionContent(t, agentID, instruction),
	})
}

// setLifecycleTaskStatus moves the seeded task into the given execution state
// the way the daemon would: in-flight states get a started_at.
func setLifecycleTaskStatus(t *testing.T, taskID, status string) {
	t.Helper()
	startedAt := "NULL"
	if status == "running" || status == "waiting_local_directory" || status == "completed" {
		startedAt = "now()"
	}
	completedAt := "NULL"
	if status == "completed" {
		completedAt = "now()"
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_task_queue SET status = $2, started_at = `+startedAt+`, completed_at = `+completedAt+` WHERE id = $1`,
		taskID, status); err != nil {
		t.Fatalf("set task %s to %s: %v", taskID, status, err)
	}
}

// fetchQueuedTaskForLifecycleTest returns the task the mention comment really
// enqueued (the mention dispatch itself is covered elsewhere; here it is the
// fixture this test suite manipulates).
func fetchQueuedTaskForLifecycleTest(t *testing.T, issueID string) string {
	t.Helper()
	var taskID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM agent_task_queue WHERE issue_id = $1 AND status = 'queued'`, issueID).Scan(&taskID); err != nil {
		t.Fatalf("fetch queued task for issue: %v", err)
	}
	return taskID
}

func lifecycleTaskStatuses(t *testing.T, issueID string) map[string]int {
	t.Helper()
	rows, err := testPool.Query(context.Background(),
		`SELECT status, count(*) FROM agent_task_queue WHERE issue_id = $1 GROUP BY status`, issueID)
	if err != nil {
		t.Fatalf("list task statuses: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			t.Fatalf("scan task status: %v", err)
		}
		out[status] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate task statuses: %v", err)
	}
	return out
}

func editCommentForLifecycleTest(t *testing.T, commentID, content string) CommentResponse {
	t.Helper()
	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPut, "/api/comments/"+commentID, map[string]any{
		"content": content,
	}), "commentId", commentID)
	testHandler.UpdateComment(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateComment: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp CommentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode updated comment: %v", err)
	}
	return resp
}

func deleteCommentForLifecycleTest(t *testing.T, commentID string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodDelete, "/api/comments/"+commentID, nil), "commentId", commentID)
	testHandler.DeleteComment(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteComment: expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

// The RUYI-460 incident, as a regression: a running run survives its trigger
// comment being edited, and the re-computed trigger enqueues the new body as
// a fresh queued run instead of killing the in-flight one.
func TestEditCommentKeepingMentionSparesRunningRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Lifecycle Keep Mention", nil)
	issueID := createCommentTriggerPreviewIssue(t, "lifecycle keep mention", "", "")
	commentID := postMentionCommentForLifecycleTest(t, issueID, agentID, "first instruction")
	taskID := fetchQueuedTaskForLifecycleTest(t, issueID)
	setLifecycleTaskStatus(t, taskID, "running")

	resp := editCommentForLifecycleTest(t, commentID, editRunLifecycleMentionContent(t, agentID, "second instruction after the run already started"))
	outcome := findCommentOutcome(t, resp.TriggerOutcomes, agentID)
	if outcome.Status != DispatchQueued {
		t.Fatalf("edit keeping mention outcome = %+v, want queued", outcome)
	}

	statuses := lifecycleTaskStatuses(t, issueID)
	if statuses["running"] != 1 {
		t.Fatalf("running runs after edit = %d, want 1 (the in-flight run must survive)", statuses["running"])
	}
	if statuses["queued"] != 1 {
		t.Fatalf("queued runs after edit = %d, want 1 (recomputed trigger for the new body)", statuses["queued"])
	}

	var cancelled int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE id = $1 AND status = 'cancelled'`, taskID).Scan(&cancelled); err != nil {
		t.Fatalf("reload seeded task: %v", err)
	}
	if cancelled != 0 {
		t.Fatalf("the seeded running task was flipped to cancelled by the edit")
	}
}

// Removing the mention retracts the request for work that has not started
// (queued revokes, nothing re-triggers), but a run that already began keeps
// executing — deleting the @agent from the text is not a stop command.
func TestEditCommentRemovingMentionSparesRunningRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Lifecycle Remove Mention", nil)
	issueID := createCommentTriggerPreviewIssue(t, "lifecycle remove mention", "", "")
	commentID := postMentionCommentForLifecycleTest(t, issueID, agentID, "start something")
	taskID := fetchQueuedTaskForLifecycleTest(t, issueID)
	setLifecycleTaskStatus(t, taskID, "running")

	editCommentForLifecycleTest(t, commentID, "mention removed, plain discussion text now")

	statuses := lifecycleTaskStatuses(t, issueID)
	if statuses["running"] != 1 {
		t.Fatalf("running runs after mention removal = %d, want 1", statuses["running"])
	}
	if statuses["queued"] != 0 {
		t.Fatalf("queued runs after mention removal = %d, want 0", statuses["queued"])
	}
}

// The queued revocation semantics are preserved: editing a comment whose run
// has not started still revokes the queued dispatch and re-triggers with the
// new body (same behavior the coalesced-batch delivery tests already pin).
func TestEditCommentStillRevokesQueuedAndRequeues(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Lifecycle Queued Keep", nil)
	issueID := createCommentTriggerPreviewIssue(t, "lifecycle queued keep", "", "")
	commentID := postMentionCommentForLifecycleTest(t, issueID, agentID, "queued instruction")
	taskID := fetchQueuedTaskForLifecycleTest(t, issueID)

	resp := editCommentForLifecycleTest(t, commentID, editRunLifecycleMentionContent(t, agentID, "revised queued instruction"))
	outcome := findCommentOutcome(t, resp.TriggerOutcomes, agentID)
	if outcome.Status != DispatchQueued {
		t.Fatalf("queued edit keeping mention outcome = %+v, want queued", outcome)
	}

	statuses := lifecycleTaskStatuses(t, issueID)
	if statuses["queued"] != 1 {
		t.Fatalf("queued runs after queued-task edit = %d, want 1", statuses["queued"])
	}
	var oldCancelled int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE id = $1 AND status = 'cancelled'
		 AND cancel_reason = 'trigger_comment_deleted' AND cancel_actor_type = 'system'`, taskID).Scan(&oldCancelled); err != nil {
		t.Fatalf("reload old queued task: %v", err)
	}
	if oldCancelled != 1 {
		t.Fatalf("old queued task not revoked with system attribution")
	}
}

// Terminal rows are never rewritten by an edit.
func TestEditCommentLeavesCompletedTaskAlone(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Lifecycle Completed", nil)
	issueID := createCommentTriggerPreviewIssue(t, "lifecycle completed", "", "")
	commentID := postMentionCommentForLifecycleTest(t, issueID, agentID, "already finished work")
	taskID := fetchQueuedTaskForLifecycleTest(t, issueID)
	setLifecycleTaskStatus(t, taskID, "completed")

	editCommentForLifecycleTest(t, commentID, editRunLifecycleMentionContent(t, agentID, "edit after completion"))

	statuses := lifecycleTaskStatuses(t, issueID)
	if statuses["completed"] != 1 {
		t.Fatalf("completed runs after edit = %d, want 1 (terminal rows untouched)", statuses["completed"])
	}
	if statuses["queued"] != 1 {
		t.Fatalf("queued runs after edit = %d, want 1 (recomputed trigger)", statuses["queued"])
	}
}

// Deleting the source comment follows the same rule: queued revokes, running
// survives (its trigger pointer is nulled by the FK, the run itself goes on).
func TestDeleteCommentSparesRunningRunButRevokesQueued(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runningAgent := createHandlerTestAgent(t, "Lifecycle Delete Running", nil)
	runningIssue := createCommentTriggerPreviewIssue(t, "lifecycle delete running", "", "")
	runningComment := postMentionCommentForLifecycleTest(t, runningIssue, runningAgent, "delete me while running")
	runningTask := fetchQueuedTaskForLifecycleTest(t, runningIssue)
	setLifecycleTaskStatus(t, runningTask, "running")
	deleteCommentForLifecycleTest(t, runningComment)

	if statuses := lifecycleTaskStatuses(t, runningIssue); statuses["running"] != 1 {
		t.Fatalf("running runs after comment delete = %d, want 1", statuses["running"])
	}

	queuedAgent := createHandlerTestAgent(t, "Lifecycle Delete Queued", nil)
	queuedIssue := createCommentTriggerPreviewIssue(t, "lifecycle delete queued", "", "")
	queuedComment := postMentionCommentForLifecycleTest(t, queuedIssue, queuedAgent, "delete me while queued")
	fetchQueuedTaskForLifecycleTest(t, queuedIssue)
	deleteCommentForLifecycleTest(t, queuedComment)

	if statuses := lifecycleTaskStatuses(t, queuedIssue); statuses["cancelled"] != 1 {
		t.Fatalf("cancelled runs after queued comment delete = %d, want 1", statuses["cancelled"])
	}
}
