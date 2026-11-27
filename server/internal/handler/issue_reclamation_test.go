package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/middleware"
)

func TestIssueReclamationFence(t *testing.T) {
	ctx := t.Context()
	runtime := createClaimReclaimRuntime(t, ctx, "reclamation runtime")
	agent, issue := createClaimReclaimAgentAndIssue(t, ctx, runtime, "reclamation agent")
	first, second := strings.Repeat("a", 64), strings.Repeat("b", 64)
	call := func(ctx context.Context, finish bool, token, scope string) *httptest.ResponseRecorder {
		body := map[string]string{"token": token}
		r := newRequest("POST", "/", body).WithContext(ctx)
		r = withURLParams(r, "workspaceId", testWorkspaceID, "issueId", issue)
		if scope != "" {
			r = r.WithContext(middleware.WithDaemonTokenOwnerID(middleware.WithDaemonContext(r.Context(), scope, "reclaim-test-daemon"), testUserID))
		}
		w := httptest.NewRecorder()
		if finish {
			testHandler.FinishIssueReclamation(w, r)
		} else {
			testHandler.BeginIssueReclamation(w, r)
		}
		return w
	}
	request := func(finish bool, token, scope string, want int) {
		t.Helper()
		w := call(ctx, finish, token, scope)
		if w.Code != want {
			t.Fatalf("finish=%v: got %d, want %d: %s", finish, w.Code, want, w.Body.String())
		}
	}
	exec := func(code, query string, args ...any) {
		t.Helper()
		_, err := testPool.Exec(ctx, query, args...)
		if code == "" && err == nil {
			return
		}
		var pgErr *pgconn.PgError
		if code != "" && errors.As(err, &pgErr) && pgErr.Code == code {
			return
		}
		t.Fatalf("%s: expected SQLSTATE %q, got %v", query, code, err)
	}
	ok, conflict := http.StatusNoContent, http.StatusConflict
	request(false, first, "", http.StatusUnauthorized)
	request(false, first, "other-workspace", http.StatusNotFound)
	request(false, "malformed", testWorkspaceID, http.StatusBadRequest)
	request(false, first, testWorkspaceID, conflict)
	request(true, first, testWorkspaceID, ok)
	exec("", "UPDATE issue SET status='done' WHERE id=$1", issue)
	request(false, first, testWorkspaceID, conflict)
	run := createHandlerTestTaskForAgentOnIssue(t, agent, issue)
	request(false, second, testWorkspaceID, conflict)
	exec("", "UPDATE agent_task_queue SET status='completed' WHERE id=$1", run)
	request(false, second, testWorkspaceID, ok)
	defer func() { _ = call(ctx, true, second, testWorkspaceID) }()
	request(false, second, testWorkspaceID, ok)
	exec("MC001", "UPDATE issue SET status='in_progress' WHERE id=$1", issue)
	exec("MC001", "DELETE FROM issue WHERE id=$1", issue)
	exec("MC001", "UPDATE agent_task_queue SET status='queued' WHERE id=$1", run)
	exec("MC001", "INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status) VALUES ($1, $2, $3, 'queued')", agent, runtime, issue)
	request(true, second, testWorkspaceID, ok)
	request(false, second, testWorkspaceID, conflict)
	// A concurrent reopen takes the Issue row before the reclamation request.
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "UPDATE issue SET status='in_progress' WHERE id=$1", issue); err != nil {
		t.Fatal(err)
	}
	third := strings.Repeat("c", 64)
	raceCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- call(raceCtx, false, third, testWorkspaceID) }()
	select {
	case w := <-result:
		t.Fatalf("begin returned before reopen committed: %d %s", w.Code, w.Body.String())
	case <-time.After(75 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-result:
		if w.Code != conflict {
			t.Fatalf("begin after concurrent reopen: %d %s", w.Code, w.Body.String())
		}
	case <-raceCtx.Done():
		t.Fatal(raceCtx.Err())
	}
	request(true, second, testWorkspaceID, ok)
	exec("", "DELETE FROM issue WHERE id=$1", issue)
	var reservations int
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM issue_reclamation WHERE issue_id=$1", issue).Scan(&reservations); err != nil || reservations != 0 {
		t.Fatalf("Issue deletion left %d reservations: %v", reservations, err)
	}
}
