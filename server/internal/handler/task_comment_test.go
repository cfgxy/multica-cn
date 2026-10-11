package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// seedCommentTask builds the running, issue-backed task the /comment endpoint
// needs — requireDaemonTaskAccess resolves the workspace through the issue.
func seedCommentTask(t *testing.T, label string) string {
	t.Helper()
	agentID := dbfx.Agent(t, label+" agent", handlerTestRuntimeID(t), testutil.Cols{
		"instructions": "",
		"custom_env":   testutil.Raw("'{}'::jsonb"),
		"custom_args":  testutil.Raw("'[]'::jsonb"),
	})
	issueID := dbfx.Issue(t, label+" fixture", testutil.Cols{"status": "in_progress"})
	return dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": handlerTestRuntimeID(t),
		"issue_id":   issueID,
		"status":     "running",
		"started_at": testutil.Raw("now()"),
	})
}

// addTaskCommentRequest builds the daemon-authenticated POST the daemon sends
// when the watchdog ALERT tier trips.
func addTaskCommentRequest(t *testing.T, taskID, content string) *http.Request {
	t.Helper()
	req := testutil.JSONRequest(http.MethodPost,
		"/api/daemon/tasks/"+taskID+"/comment", map[string]any{"content": content})
	req = testutil.WithURLParams(req, "taskId", taskID)
	return req.WithContext(middleware.WithDaemonContext(
		req.Context(), testWorkspaceID, "task-comment-daemon"))
}

// TestAddTaskCommentPersistsSystemComment covers the ALERT leg end to end at
// the handler boundary: a daemon-authenticated POST lands as a system-type
// comment authored by the task's agent, with the task pinned as its source —
// the same shape the strong-stop path's fixed wording has always produced,
// now reachable before the run dies.
func TestAddTaskCommentPersistsSystemComment(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	taskID := seedCommentTask(t, "task-comment")
	content := "⏰ Agent has produced no output for 1h0m0s (alert threshold 1h0m0s). The run is still alive; if this silence is unexpected, check the task transcript or stop the task."

	testutil.Call(t, testHandler.AddTaskComment,
		addTaskCommentRequest(t, taskID, content)).Want(http.StatusOK)

	row := dbfx.QueryRow(t,
		`SELECT c.type, c.content, c.author_type, c.source_task_id
		   FROM comment c
		   JOIN agent_task_queue t ON t.id = $1
		  WHERE c.source_task_id = t.id
		  ORDER BY c.created_at DESC
		  LIMIT 1`, taskID)
	var commentType, stored, authorType string
	var sourceTaskID []byte
	row.Scan(&commentType, &stored, &authorType, &sourceTaskID)
	if commentType != "system" {
		t.Fatalf("comment type = %q, want system", commentType)
	}
	if stored != content {
		t.Fatalf("comment content mismatch:\n got %q\nwant %q", stored, content)
	}
	if authorType != "agent" {
		t.Fatalf("author type = %q, want agent", authorType)
	}
	if len(sourceTaskID) == 0 {
		t.Fatalf("source_task_id must be pinned to the alerting task")
	}
}

// TestAddTaskCommentRejectsEmptyContent keeps an empty payload from minting a
// blank comment row: the service no-ops, and the endpoint still answers 200 —
// the daemon treats comment delivery as best-effort and must not retry on it.
func TestAddTaskCommentRejectsEmptyContent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	taskID := seedCommentTask(t, "task-comment-empty")

	testutil.Call(t, testHandler.AddTaskComment,
		addTaskCommentRequest(t, taskID, "")).Want(http.StatusOK)

	if n := dbfx.Count(t,
		`SELECT COUNT(*) FROM comment WHERE source_task_id = $1`, taskID); n != 0 {
		t.Fatalf("empty content must not create a comment, got %d", n)
	}
}
