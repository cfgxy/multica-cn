package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func taskTokenIssueStatusRequest(ctx context.Context, fx runningSquadLeaderTaskFixture) *http.Request {
	req := newRequest(http.MethodPut, "/api/issues/"+fx.IssueID, map[string]any{
		"status": "in_progress",
	})
	req = req.WithContext(ctx)
	req = withURLParam(req, "id", fx.IssueID)
	req.Header.Set("X-Agent-ID", fx.LeaderID)
	req.Header.Set("X-Task-ID", fx.TaskID)
	req.Header.Set("X-Actor-Source", "task_token")
	return req
}

func TestUpdateIssue_TaskTokenRequiresActiveRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	t.Run("running task can update status", func(t *testing.T) {
		fx := newRunningSquadLeaderTaskFixture(t)
		w := httptest.NewRecorder()
		testHandler.UpdateIssue(w, taskTokenIssueStatusRequest(context.Background(), fx))
		if w.Code != http.StatusOK {
			t.Fatalf("active task issue update: expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	for _, status := range []string{"cancel_requested", "cancelled", "completed", "failed", "deferred"} {
		t.Run(status, func(t *testing.T) {
			fx := newRunningSquadLeaderTaskFixture(t)
			if _, err := testPool.Exec(context.Background(), `
				UPDATE agent_task_queue SET status = $2 WHERE id = $1
			`, fx.TaskID, status); err != nil {
				t.Fatalf("set task status to %s: %v", status, err)
			}

			w := httptest.NewRecorder()
			testHandler.UpdateIssue(w, taskTokenIssueStatusRequest(context.Background(), fx))
			if w.Code != http.StatusConflict {
				t.Fatalf("task status %s: expected 409, got %d: %s", status, w.Code, w.Body.String())
			}

			var issueStatus string
			if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, fx.IssueID).Scan(&issueStatus); err != nil {
				t.Fatalf("read issue status: %v", err)
			}
			if issueStatus != "todo" {
				t.Fatalf("task status %s: issue status = %q, want todo", status, issueStatus)
			}
		})
	}
}

func TestBatchUpdateIssues_TaskTokenRequiresActiveRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fx := newRunningSquadLeaderTaskFixture(t)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue SET status = 'cancelled' WHERE id = $1
	`, fx.TaskID); err != nil {
		t.Fatalf("cancel task: %v", err)
	}

	req := newRequest(http.MethodPatch, "/api/issues/batch?workspace_id="+testWorkspaceID, map[string]any{
		"issue_ids": []string{fx.IssueID},
		"updates": map[string]any{
			"status": "in_progress",
		},
	})
	req.Header.Set("X-Agent-ID", fx.LeaderID)
	req.Header.Set("X-Task-ID", fx.TaskID)
	req.Header.Set("X-Actor-Source", "task_token")

	w := httptest.NewRecorder()
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("cancelled task batch issue update: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var issueStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, fx.IssueID).Scan(&issueStatus); err != nil {
		t.Fatalf("read issue status: %v", err)
	}
	if issueStatus != "todo" {
		t.Fatalf("cancelled task batch update must not persist, status = %q", issueStatus)
	}
}

func TestUpdateIssue_TaskTokenCancelRaceRejectsWrite(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	fx := newRunningSquadLeaderTaskFixture(t)
	cancelTx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin cancel transaction: %v", err)
	}
	defer cancelTx.Rollback(context.Background())
	var cancelPID int32
	if err := cancelTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&cancelPID); err != nil {
		t.Fatalf("read cancel backend pid: %v", err)
	}
	if _, err := cancelTx.Exec(ctx, `UPDATE agent_task_queue SET status = 'cancel_requested' WHERE id = $1`, fx.TaskID); err != nil {
		t.Fatalf("stage task cancellation: %v", err)
	}

	type response struct {
		code int
		body string
	}
	result := make(chan response, 1)
	go func() {
		w := httptest.NewRecorder()
		testHandler.UpdateIssue(w, taskTokenIssueStatusRequest(ctx, fx))
		result <- response{code: w.Code, body: w.Body.String()}
	}()

	waitForBlockedDatabaseQuery(t, ctx, "active_task", cancelPID)
	if err := cancelTx.Commit(ctx); err != nil {
		t.Fatalf("commit task cancellation: %v", err)
	}

	var got response
	select {
	case got = <-result:
	case <-ctx.Done():
		t.Fatalf("issue update request did not finish: %v", ctx.Err())
	}
	if got.code != http.StatusConflict {
		t.Fatalf("task-token issue update after cancellation: expected 409, got %d: %s", got.code, got.body)
	}

	var issueStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, fx.IssueID).Scan(&issueStatus); err != nil {
		t.Fatalf("read issue status: %v", err)
	}
	if issueStatus != "todo" {
		t.Fatalf("cancelled task issue update must not persist, status = %q", issueStatus)
	}
}
