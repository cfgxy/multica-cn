package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// suppressRunRequest builds the write under test: promote issueID out of
// backlog while asking the server to suppress the run. asAgent stamps the
// server-set task-token actor markers the auth middleware writes for a run's
// own CLI call, so resolveActor yields the trusted "agent" actor — the only
// input that may decide the agent branch (a client-declared field must not).
func suppressRunRequest(t *testing.T, issueID, status string, asAgent string) *http.Request {
	t.Helper()
	req := testutil.WithURLParams(
		testutil.JSONRequest("PUT", "/api/issues/"+issueID, map[string]any{
			"status": status, "suppress_run": true,
		}),
		"id", issueID)
	testutil.WithHeaders(req, "X-User-ID", testUserID, "X-Workspace-ID", testWorkspaceID)
	if asAgent != "" {
		stampSuppressTaskActor(t, req, issueID, asAgent)
	}
	return req
}

// backlogIssueFor parks a fresh issue in backlog already owned by agentID, the
// shape RUYI-248 stranded: assignee set, no run, waiting on a promotion.
func backlogIssueFor(t *testing.T, agentID string) string {
	t.Helper()
	return dbfx.Issue(t, "suppress anchor", testutil.Cols{
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
}

// runtimeIDForAgent reads the runtime an agent is bound to. A task in an
// active status carries it too (agent_task_queue_active_requires_runtime).
func runtimeIDForAgent(t *testing.T, agentID string) string {
	t.Helper()
	var runtimeID string
	dbfx.QueryRow(t, `SELECT runtime_id FROM agent WHERE id = $1`, agentID).Scan(&runtimeID)
	return runtimeID
}

// TestSuppressRunFromAgentNeedsAnchorRun covers the RUYI-248 regression: a
// task-scoped agent actor asking to suppress the run on an issue that holds no
// run at all is asking to discard the work, not to defer it ("--no-start" is
// documented as "without starting ANOTHER run"). The suppression is therefore
// honored only when an active run exists to anchor on.
func TestSuppressRunFromAgentNeedsAnchorRun(t *testing.T) {
	agentID := seededReadyAgentID(t)

	// 1. agent actor + suppress + backlog→active + no task → enqueues anyway.
	t.Run("agent without active run enqueues", func(t *testing.T) {
		issueID := backlogIssueFor(t, agentID)
		testutil.Call(t, testHandler.UpdateIssue, suppressRunRequest(t, issueID, "in_progress", agentID)).Want(http.StatusOK)
		if got := taskCountFor(t, issueID, agentID); got != 1 {
			t.Fatalf("agent suppress on a run-less issue must still enqueue: want 1 task, got %d", got)
		}
	})

	// 2. member actor + suppress + backlog→active → the human "暂时不启动"
	//    affordance is untouched.
	t.Run("member suppress still honored", func(t *testing.T) {
		issueID := backlogIssueFor(t, agentID)
		testutil.Call(t, testHandler.UpdateIssue, suppressRunRequest(t, issueID, "in_progress", "")).Want(http.StatusOK)
		if got := taskCountFor(t, issueID, agentID); got != 0 {
			t.Fatalf("member suppress_run must enqueue nothing, got %d tasks", got)
		}
	})

	// 3. agent actor + suppress + an active (running) task on the pair → the
	//    suppression has a run to anchor on and is honored, so no second run.
	//    A running task is deliberately not a *pending* one: pending is already
	//    coalesced upstream by WillEnqueueRun, which would make this case pass
	//    even without the active-task condition.
	t.Run("agent with active run honors suppress", func(t *testing.T) {
		issueID := backlogIssueFor(t, agentID)
		dbfx.Task(t, agentID, testutil.Cols{
			"issue_id":   issueID,
			"status":     "running",
			"runtime_id": runtimeIDForAgent(t, agentID),
		})
		testutil.Call(t, testHandler.UpdateIssue, suppressRunRequest(t, issueID, "in_progress", agentID)).Want(http.StatusOK)
		if got := taskCountFor(t, issueID, agentID); got != 1 {
			t.Fatalf("suppress with an active run must not add a second run: want the 1 pre-existing task, got %d", got)
		}
	})

	// 4. agent actor + suppress + todo→in_progress: not a backlog promotion, so
	//    it never triggered a run and still does not.
	t.Run("agent non-backlog transition unchanged", func(t *testing.T) {
		issueID := dbfx.Issue(t, "suppress non-backlog", testutil.Cols{
			"status": "todo", "assignee_type": "agent", "assignee_id": agentID,
		})
		testutil.Call(t, testHandler.UpdateIssue, suppressRunRequest(t, issueID, "in_progress", agentID)).Want(http.StatusOK)
		if got := taskCountFor(t, issueID, agentID); got != 0 {
			t.Fatalf("todo→in_progress is no trigger source, got %d tasks", got)
		}
	})
}

// TestBatchSuppressRunFromAgentNeedsAnchorRun mirrors cases 1 and 2 on the
// batch write path, which carries its own copy of the suppress check.
func TestBatchSuppressRunFromAgentNeedsAnchorRun(t *testing.T) {
	agentID := seededReadyAgentID(t)

	batchRequest := func(issueID, asAgent string) *http.Request {
		req := testutil.JSONRequest("PUT", "/api/issues/batch?workspace_id="+testWorkspaceID, map[string]any{
			"issue_ids": []string{issueID},
			"updates":   map[string]any{"status": "in_progress", "suppress_run": true},
		})
		testutil.WithHeaders(req, "X-User-ID", testUserID, "X-Workspace-ID", testWorkspaceID)
		if asAgent != "" {
			stampSuppressTaskActor(t, req, issueID, asAgent)
		}
		return req
	}

	t.Run("agent without active run enqueues", func(t *testing.T) {
		issueID := backlogIssueFor(t, agentID)
		testutil.Call(t, testHandler.BatchUpdateIssues, batchRequest(issueID, agentID)).Want(http.StatusOK)
		if got := taskCountFor(t, issueID, agentID); got != 1 {
			t.Fatalf("batch agent suppress on a run-less issue must still enqueue: want 1 task, got %d", got)
		}
	})

	t.Run("member suppress still honored", func(t *testing.T) {
		issueID := backlogIssueFor(t, agentID)
		testutil.Call(t, testHandler.BatchUpdateIssues, batchRequest(issueID, "")).Want(http.StatusOK)
		if got := taskCountFor(t, issueID, agentID); got != 0 {
			t.Fatalf("batch member suppress_run must enqueue nothing, got %d tasks", got)
		}
	})
}

// An agent write must carry a real active task. Without a target-issue anchor,
// the caller runs on another issue so suppression still tests the run-less pair.
func stampSuppressTaskActor(t *testing.T, req *http.Request, issueID, agentID string) {
	t.Helper()
	var taskID string
	if err := testPool.QueryRow(t.Context(), `SELECT id FROM agent_task_queue
		WHERE issue_id=$1 AND agent_id=$2 AND status IN ('running','dispatched','waiting_local_directory')
		ORDER BY created_at DESC LIMIT 1`, issueID, agentID).Scan(&taskID); errors.Is(err, pgx.ErrNoRows) {
		callerIssue := dbfx.Issue(t, "suppression caller")
		taskID = dbfx.Task(t, agentID, testutil.Cols{"issue_id": callerIssue, "status": "running", "runtime_id": runtimeIDForAgent(t, agentID)})
	} else if err != nil {
		t.Fatal(err)
	}
	testutil.WithHeaders(req, "X-Actor-Source", "task_token", "X-Agent-ID", agentID, "X-Task-ID", taskID)
}
