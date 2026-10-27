package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTaskStatusAllowsComment(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{status: "dispatched", want: true},
		{status: "running", want: true},
		{status: "waiting_local_directory", want: true},
		{status: "cancel_requested", want: false},
		{status: "cancelled", want: false},
		{status: "completed", want: false},
		{status: "failed", want: false},
		{status: "deferred", want: false},
	} {
		if got := taskStatusAllowsComment(tc.status); got != tc.want {
			t.Errorf("taskStatusAllowsComment(%q) = %t, want %t", tc.status, got, tc.want)
		}
	}
}

func TestCreateComment_TaskTokenRequiresActiveRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	t.Run("running task can reply", func(t *testing.T) {
		fx := newRunningSquadLeaderTaskFixture(t)
		w := httptest.NewRecorder()
		r := newRequest("POST", "/api/issues/"+fx.IssueID+"/comments", map[string]any{
			"content":   "reply from active task token [@leader](mention://agent/" + fx.LeaderID + ")",
			"parent_id": fx.TriggerCommentID,
		})
		r = withURLParam(r, "id", fx.IssueID)
		r.Header.Set("X-Agent-ID", fx.LeaderID)
		r.Header.Set("X-Task-ID", fx.TaskID)
		r.Header.Set("X-Actor-Source", "task_token")

		testHandler.CreateComment(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("active task comment: expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	for _, status := range []string{"cancel_requested", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			fx := newRunningSquadLeaderTaskFixture(t)
			if _, err := testPool.Exec(context.Background(), `
				UPDATE agent_task_queue SET status = $2 WHERE id = $1
			`, fx.TaskID, status); err != nil {
				t.Fatalf("set task status to %s: %v", status, err)
			}

			w := httptest.NewRecorder()
			r := newRequest("POST", "/api/issues/"+fx.IssueID+"/comments", map[string]any{
				"content":   "late reply from cancelled task token",
				"parent_id": fx.TriggerCommentID,
			})
			r = withURLParam(r, "id", fx.IssueID)
			r.Header.Set("X-Agent-ID", fx.LeaderID)
			r.Header.Set("X-Task-ID", fx.TaskID)
			r.Header.Set("X-Actor-Source", "task_token")

			testHandler.CreateComment(w, r)
			if w.Code != http.StatusConflict {
				t.Fatalf("task status %s: expected 409, got %d: %s", status, w.Code, w.Body.String())
			}
			if got := countAgentCommentsForIssue(t, fx.IssueID, fx.LeaderID); got != 0 {
				t.Fatalf("task status %s: expected no agent comment, got %d", status, got)
			}
		})
	}
}

func TestCreateComment_TaskTokenCancelRaceRejectsWrite(t *testing.T) {
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
	req := newRequest("POST", "/api/issues/"+fx.IssueID+"/comments", map[string]any{
		"content":   "late reply from cancelled task token [@leader](mention://agent/" + fx.LeaderID + ")",
		"parent_id": fx.TriggerCommentID,
	})
	req = req.WithContext(ctx)
	req = withURLParam(req, "id", fx.IssueID)
	req.Header.Set("X-Agent-ID", fx.LeaderID)
	req.Header.Set("X-Task-ID", fx.TaskID)
	req.Header.Set("X-Actor-Source", "task_token")
	go func() {
		w := httptest.NewRecorder()
		testHandler.CreateComment(w, req)
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
		t.Fatalf("comment request did not finish: %v", ctx.Err())
	}
	if got.code != http.StatusConflict {
		t.Fatalf("task-token comment after cancellation: expected 409, got %d: %s", got.code, got.body)
	}
	if count := countAgentCommentsForIssue(t, fx.IssueID, fx.LeaderID); count != 0 {
		t.Fatalf("cancelled task comment must not persist, found %d", count)
	}
}
