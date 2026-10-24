package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
)

type runningOrdinaryTaskFixture struct {
	IssueID, AgentID, RuntimeID, TaskID, TriggerID string
}

func newRunningOrdinaryTaskFixture(t *testing.T) runningOrdinaryTaskFixture {
	t.Helper()
	runtimeID := dbfx.Runtime(t, "ordinary-no-action-runtime")
	agentID := dbfx.Agent(t, "ordinary-no-action-agent", runtimeID)
	issueID := dbfx.Issue(t, "ordinary no action", testutil.Cols{"assignee_type": "agent", "assignee_id": agentID})
	triggerID := dbfx.Comment(t, issueID, "please check whether anything changed")
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "trigger_comment_id": triggerID,
		"status": "running", "started_at": testutil.Raw("now()"),
	})
	dbfx.Exec(t, `UPDATE agent_task_queue SET delivered_comment_ids = ARRAY[$2::uuid] WHERE id = $1`, taskID, triggerID)
	return runningOrdinaryTaskFixture{IssueID: issueID, AgentID: agentID, RuntimeID: runtimeID, TaskID: taskID, TriggerID: triggerID}
}

func noActionRequest(fx runningOrdinaryTaskFixture, reason string) *http.Request {
	req := newRequest(http.MethodPost, "/api/issues/"+fx.IssueID+"/no-action", map[string]any{"reason": reason})
	req = withURLParam(req, "id", fx.IssueID)
	req.Header.Set("X-Agent-ID", fx.AgentID)
	req.Header.Set("X-Task-ID", fx.TaskID)
	req.Header.Set("X-Actor-Source", "task_token")
	return req
}

func TestRecordTaskNoActionCompletesWithoutComment(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fx := newRunningOrdinaryTaskFixture(t)
	testutil.Call(t, testHandler.RecordTaskNoAction, noActionRequest(fx, "the trigger contains no new material work")).Want(http.StatusOK)

	var marked bool
	dbfx.QueryRow(t, `SELECT no_action_at IS NOT NULL AND no_action_reason <> '' FROM agent_task_queue WHERE id = $1`, fx.TaskID).Scan(&marked)
	if !marked {
		t.Fatal("task no_action marker was not persisted")
	}

	commentReq := commentRequest(http.MethodPost, fx.IssueID, "", map[string]any{
		"content": "late comment after no_action", "parent_id": fx.TriggerID,
	})
	commentReq.Header.Set("X-Agent-ID", fx.AgentID)
	commentReq.Header.Set("X-Task-ID", fx.TaskID)
	commentReq.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.CreateComment, commentReq).Want(http.StatusConflict)

	completeRunningTask(t, runningSquadLeaderTaskFixture{IssueID: fx.IssueID, LeaderID: fx.AgentID, TaskID: fx.TaskID}, "No action needed.")
	if got := countAgentCommentsForIssue(t, fx.IssueID, fx.AgentID); got != 0 {
		t.Fatalf("no_action completion created %d agent comments, want 0", got)
	}
	var status string
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id = $1`, fx.TaskID).Scan(&status)
	if status != "completed" {
		t.Fatalf("task status = %q, want completed", status)
	}
	if got := pendingTaskCountForAgentIssue(t, fx.IssueID, fx.AgentID); got != 0 {
		t.Fatalf("no_action completion queued %d follow-up task(s), want 0", got)
	}
}

func TestRecordTaskNoActionRejectsAfterCommentAndForLeader(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fx := newRunningOrdinaryTaskFixture(t)
	commentReq := commentRequest(http.MethodPost, fx.IssueID, "", map[string]any{
		"content": "substantive result", "parent_id": fx.TriggerID,
	})
	commentReq.Header.Set("X-Agent-ID", fx.AgentID)
	commentReq.Header.Set("X-Task-ID", fx.TaskID)
	commentReq.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.CreateComment, commentReq).Want(http.StatusCreated)
	testutil.Call(t, testHandler.RecordTaskNoAction, noActionRequest(fx, "nothing else to add")).Want(http.StatusConflict)

	leader := newRunningSquadLeaderTaskFixture(t)
	leaderReq := newRequest(http.MethodPost, "/api/issues/"+leader.IssueID+"/no-action", map[string]any{"reason": "leader no action"})
	leaderReq = withURLParam(leaderReq, "id", leader.IssueID)
	leaderReq.Header.Set("X-Agent-ID", leader.LeaderID)
	leaderReq.Header.Set("X-Task-ID", leader.TaskID)
	leaderReq.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.RecordTaskNoAction, leaderReq).Want(http.StatusConflict)
}

func TestRecordTaskNoActionAllowsClaimDowngradedLeaderAndTerminalRetry(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	t.Run("claim downgraded leader", func(t *testing.T) {
		leader := newRunningSquadLeaderTaskFixture(t)
		newLeaderID := dbfx.Agent(t, "replacement squad leader", dbfx.Runtime(t, "replacement leader runtime"))
		dbfx.Exec(t, `UPDATE squad SET leader_id = $2 WHERE id = $1`, leader.SquadID, newLeaderID)
		if _, err := testHandler.TaskService.RecordTaskNoAction(ctx, parseUUID(leader.TaskID), "claim delivered without a leader briefing"); err != nil {
			t.Fatalf("record downgraded leader no_action: %v", err)
		}
	})

	t.Run("retry after completion", func(t *testing.T) {
		fx := newRunningOrdinaryTaskFixture(t)
		first, err := testHandler.TaskService.RecordTaskNoAction(ctx, parseUUID(fx.TaskID), "nothing material changed")
		if err != nil {
			t.Fatalf("record first no_action: %v", err)
		}
		dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1`, fx.TaskID)
		retry, err := testHandler.TaskService.RecordTaskNoAction(ctx, parseUUID(fx.TaskID), "response retry")
		if err != nil {
			t.Fatalf("retry recorded no_action: %v", err)
		}
		if !retry.NoActionAt.Valid || retry.NoActionAt.Time != first.NoActionAt.Time {
			t.Fatalf("retry changed no_action marker: first=%v retry=%v", first.NoActionAt, retry.NoActionAt)
		}
	})
}

func TestCommentEditAndNoActionAreMutuallyExclusive(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	t.Run("edit first blocks no_action", func(t *testing.T) {
		fx := newRunningOrdinaryTaskFixture(t)
		commentID := dbfx.Comment(t, fx.IssueID, "initial result", testutil.Cols{
			"author_type": "agent", "author_id": fx.AgentID,
			"parent_id": fx.TriggerID, "source_task_id": fx.TaskID,
		})
		edit := commentRequest(http.MethodPut, fx.IssueID, commentID, map[string]any{"content": "verified result"})
		edit.Header.Set("X-Agent-ID", fx.AgentID)
		edit.Header.Set("X-Task-ID", fx.TaskID)
		edit.Header.Set("X-Actor-Source", "task_token")
		testutil.Call(t, testHandler.UpdateComment, edit).Want(http.StatusOK)
		testutil.Call(t, testHandler.RecordTaskNoAction, noActionRequest(fx, "nothing else changed")).Want(http.StatusConflict)
	})

	t.Run("no_action first blocks edit", func(t *testing.T) {
		fx := newRunningOrdinaryTaskFixture(t)
		commentID := dbfx.Comment(t, fx.IssueID, "older result", testutil.Cols{
			"author_type": "agent", "author_id": fx.AgentID,
			"parent_id": fx.TriggerID,
		})
		testutil.Call(t, testHandler.RecordTaskNoAction, noActionRequest(fx, "the trigger is only an acknowledgement")).Want(http.StatusOK)
		edit := commentRequest(http.MethodPut, fx.IssueID, commentID, map[string]any{"content": "late edit"})
		edit.Header.Set("X-Agent-ID", fx.AgentID)
		edit.Header.Set("X-Task-ID", fx.TaskID)
		edit.Header.Set("X-Actor-Source", "task_token")
		testutil.Call(t, testHandler.UpdateComment, edit).Want(http.StatusConflict)
	})
}

func TestTaskTokenCommentLocksIssueBeforeTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fx := newRunningOrdinaryTaskFixture(t)

	issueTx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin issue transaction: %v", err)
	}
	defer issueTx.Rollback(context.Background())
	var blockerPID int32
	if err := issueTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatalf("read blocker pid: %v", err)
	}
	if _, err := issueTx.Exec(ctx, `SELECT id FROM issue WHERE id = $1 FOR UPDATE`, fx.IssueID); err != nil {
		t.Fatalf("lock issue: %v", err)
	}

	type response struct {
		code int
		body string
	}
	result := make(chan response, 1)
	commentReq := commentRequest(http.MethodPost, fx.IssueID, "", map[string]any{
		"content": "task output", "parent_id": fx.TriggerID,
	}).WithContext(ctx)
	commentReq = withURLParam(commentReq, "id", fx.IssueID)
	commentReq.Header.Set("X-Agent-ID", fx.AgentID)
	commentReq.Header.Set("X-Task-ID", fx.TaskID)
	commentReq.Header.Set("X-Actor-Source", "task_token")
	go func() {
		w := httptest.NewRecorder()
		testHandler.CreateComment(w, commentReq)
		result <- response{code: w.Code, body: w.Body.String()}
	}()

	waitForBlockedDatabaseQuery(t, ctx, "FOR UPDATE", blockerPID)
	if _, err := issueTx.Exec(ctx, `SELECT id FROM agent_task_queue WHERE id = $1 FOR SHARE NOWAIT`, fx.TaskID); err != nil {
		t.Fatalf("comment locked task before issue: %v", err)
	}
	if err := issueTx.Commit(ctx); err != nil {
		t.Fatalf("commit issue transaction: %v", err)
	}
	select {
	case got := <-result:
		if got.code != http.StatusCreated {
			t.Fatalf("comment response = %d: %s", got.code, got.body)
		}
	case <-ctx.Done():
		t.Fatalf("comment request did not finish: %v", ctx.Err())
	}
}

func TestConsumeQueuedTasksDoesNotCrossExecutionRoles(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fx := newRunningSquadLeaderTaskFixture(t)
	dbfx.Exec(t, `UPDATE agent_task_queue SET is_leader_task = FALSE, squad_id = $2 WHERE id = $1`, fx.TaskID, fx.SquadID)
	queuedLeaderID := dbfx.Task(t, fx.LeaderID, testutil.Cols{
		"runtime_id": testRuntimeID, "issue_id": fx.IssueID, "status": "queued",
		"is_leader_task": true, "squad_id": fx.SquadID,
	})

	result, err := testHandler.TaskService.ConsumeQueuedTasksForRunningTask(ctx, parseUUID(fx.TaskID))
	if err != nil {
		t.Fatalf("consume queued: %v", err)
	}
	if len(result.Tasks) != 0 {
		t.Fatalf("worker task consumed %d leader task(s)", len(result.Tasks))
	}
	var status string
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id = $1`, queuedLeaderID).Scan(&status)
	if status != "queued" {
		t.Fatalf("leader task status = %q, want queued", status)
	}

	if _, err := testHandler.TaskService.RecordTaskNoAction(ctx, parseUUID(fx.TaskID), "no material work"); err != nil {
		t.Fatalf("record worker no_action: %v", err)
	}
	if _, err := testHandler.TaskService.ConsumeQueuedTasksForRunningTask(ctx, parseUUID(fx.TaskID)); !errors.Is(err, service.ErrTaskNotConsumable) {
		t.Fatalf("consume after no_action = %v, want ErrTaskNotConsumable", err)
	}
}
