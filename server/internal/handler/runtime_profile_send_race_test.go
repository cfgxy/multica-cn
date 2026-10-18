package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeleteRuntimeProfileSerializesConcurrentChatSend(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	profileID := insertRuntimeProfileFixture(t, ctx, "Profile Concurrent Send", "codex", "profile-send-codex")
	runtimeID := insertProfileRuntimeFixture(t, ctx, profileID, "Profile Concurrent Send Runtime", "codex")
	agentID := createCascadeFixtureAgent(t, ctx, runtimeID, "Profile Concurrent Send Agent")
	sessionID := createHandlerTestChatSession(t, agentID)

	// Hold the agent row so the send pauses after locking its session. Start
	// profile deletion while that session lock is held, then let the send reach
	// its task insert and runtime foreign-key lock.
	blocker, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin agent lock transaction: %v", err)
	}
	defer blocker.Rollback(context.Background())
	var blockerPID int32
	if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatalf("read blocker backend pid: %v", err)
	}
	var lockedAgentID string
	if err := blocker.QueryRow(ctx, `SELECT id FROM agent WHERE id = $1 FOR UPDATE`, agentID).Scan(&lockedAgentID); err != nil {
		t.Fatalf("lock agent row: %v", err)
	}

	type response struct {
		code int
		body string
	}
	sendResult := make(chan response, 1)
	sendReq := newRequest(http.MethodPost, "/api/chat/sessions/"+sessionID+"/messages", map[string]any{
		"content": "send while deleting profile",
	})
	sendReq = withURLParam(sendReq, "sessionId", sessionID)
	sendReq = withChatTestWorkspaceCtx(t, sendReq)
	sendCtx, sendCancel := context.WithTimeout(sendReq.Context(), 15*time.Second)
	defer sendCancel()
	sendReq = sendReq.WithContext(sendCtx)
	go func() {
		w := httptest.NewRecorder()
		testHandler.SendChatMessage(w, sendReq)
		sendResult <- response{code: w.Code, body: w.Body.String()}
	}()
	select {
	case sent := <-sendResult:
		t.Fatalf("SendChatMessage returned before reaching the controlled row lock: status=%d body=%s", sent.code, sent.body)
	case <-time.After(100 * time.Millisecond):
	}
	waitForBlockedDatabaseQuery(t, ctx, `FROM agent`, blockerPID)

	deleteResult := make(chan response, 1)
	deleteReq := newRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/runtime-profiles/"+profileID, nil)
	deleteReq = withURLParams(deleteReq, "id", testWorkspaceID, "profileId", profileID)
	deleteCtx, deleteCancel := context.WithTimeout(deleteReq.Context(), 15*time.Second)
	defer deleteCancel()
	deleteReq = deleteReq.WithContext(deleteCtx)
	go func() {
		w := httptest.NewRecorder()
		testHandler.DeleteRuntimeProfile(w, deleteReq)
		deleteResult <- response{code: w.Code, body: w.Body.String()}
	}()
	waitForBlockedDatabaseQuery(t, ctx, `FOR UPDATE OF cs`, 0)

	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release agent row lock: %v", err)
	}

	var sent, deleted response
	select {
	case sent = <-sendResult:
	case <-ctx.Done():
		t.Fatalf("SendChatMessage did not finish: %v", ctx.Err())
	}
	select {
	case deleted = <-deleteResult:
	case <-ctx.Done():
		t.Fatalf("DeleteRuntimeProfile did not finish: %v", ctx.Err())
	}
	if sent.code != http.StatusCreated {
		t.Errorf("SendChatMessage status = %d, want 201; body: %s", sent.code, sent.body)
	}
	if deleted.code != http.StatusConflict {
		t.Errorf("DeleteRuntimeProfile status = %d, want 409 for an active agent; body: %s", deleted.code, deleted.body)
	}
}

func waitForBlockedDatabaseQuery(t *testing.T, ctx context.Context, queryFragment string, blockerPID int32) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var found bool
		err := testPool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND wait_event_type = 'Lock'
				  AND query ILIKE '%' || $1 || '%'
				  AND ($2 = 0 OR $2 = ANY(pg_blocking_pids(pid)))
			)`, queryFragment, blockerPID).Scan(&found)
		if err != nil {
			t.Fatalf("inspect blocked query %q: %v", queryFragment, err)
		}
		if found {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for query %q to block: %v", queryFragment, ctx.Err())
		case <-ticker.C:
		}
	}
}
