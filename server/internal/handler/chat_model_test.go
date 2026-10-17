package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func setChatModel(t *testing.T, sessionID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := withURLParam(newRequest(http.MethodPatch, "/api/chat-sessions/"+sessionID+"/model", map[string]any{
		"model_override": body,
	}), "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.SetChatSessionModel(w, req)
	return w
}

func putTestModelCatalog(t *testing.T, runtimeID, modelID string) {
	t.Helper()
	if err := testHandler.ModelCatalogCache.Put(context.Background(), runtimeID, []ModelEntry{{ID: modelID}}, nil, true); err != nil {
		t.Fatalf("cache test model catalog: %v", err)
	}
}

func waitForWaiterCountBlockedBy(t *testing.T, holderPID, want int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var waiting int
		if err := testPool.QueryRow(context.Background(), `
			WITH RECURSIVE blocked(pid) AS (
				SELECT pid FROM pg_stat_activity
				WHERE datname = current_database()
				  AND state = 'active'
				  AND wait_event_type = 'Lock'
				  AND wait_event IN ('transactionid', 'tuple')
				  AND $1::int = ANY(pg_blocking_pids(pid))
				UNION
				SELECT activity.pid FROM pg_stat_activity activity
				JOIN blocked parent ON parent.pid = ANY(pg_blocking_pids(activity.pid))
				WHERE activity.datname = current_database()
				  AND activity.state = 'active'
				  AND activity.wait_event_type = 'Lock'
				  AND activity.wait_event IN ('transactionid', 'tuple')
			)
			SELECT count(*) FROM blocked
		`, holderPID).Scan(&waiting); err != nil {
			t.Fatalf("probe pg_stat_activity for waiters blocked by pid %d: %v", holderPID, err)
		}
		if waiting >= want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSetChatSessionModelWaitsForRuntimeRebind(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	oldRuntimeID := handlerTestRuntimeID(t)
	newRuntimeID := newTestRuntime(t, "Chat Model Rebind Target", "online")
	agentID := createHandlerTestAgent(t, "ChatModelRebindRace", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)
	putTestModelCatalog(t, oldRuntimeID, "old-runtime-model")
	putTestModelCatalog(t, newRuntimeID, "new-runtime-model")

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rebind transaction: %v", err)
	}
	defer tx.Rollback(context.Background())
	holderPID := holderBackendPID(t, ctx, tx)
	sessionUUID := parseUUID(sessionID)
	qtx := testHandler.Queries.WithTx(tx)
	if _, err := qtx.LockChatSessionForRuntimeBind(ctx, sessionUUID); err != nil {
		t.Fatalf("lock chat session: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agent SET runtime_id = $1, model = NULL WHERE id = $2`, newRuntimeID, agentID); err != nil {
		t.Fatalf("rebind agent runtime: %v", err)
	}

	type result struct {
		code int
		body string
	}
	results := make(chan result, 1)
	go func() {
		w := setChatModel(t, sessionID, "old-runtime-model")
		results <- result{code: w.Code, body: w.Body.String()}
	}()
	if !waitForWaiterBlockedBy(t, holderPID, 10*time.Second) {
		select {
		case got := <-results:
			t.Fatalf("model update passed an uncommitted rebind lock: %d %s", got.code, got.body)
		default:
			t.Fatal("model update never blocked on the chat session lock")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit rebind: %v", err)
	}
	select {
	case got := <-results:
		if got.code != http.StatusConflict {
			t.Fatalf("model update status = %d, want 409: %s", got.code, got.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("model update did not finish after rebind committed")
	}
	var override pgtype.Text
	if err := testPool.QueryRow(ctx, `SELECT model_override FROM chat_session WHERE id = $1`, sessionID).Scan(&override); err != nil {
		t.Fatalf("read model override: %v", err)
	}
	if override.Valid {
		t.Fatalf("stale runtime model was persisted after rebind: %q", override.String)
	}
}

func TestOrdinaryRuntimeRebindSerializesChatModelUpdate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, oldRuntimeID, newRuntimeID := rebindAgentFixture(t, "chat-model-ordinary-rebind")
	sessionID := createHandlerTestChatSession(t, agentID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM task_message WHERE task_id IN (SELECT id FROM agent_task_queue WHERE chat_session_id = $1)`, sessionID)
		_, _ = testPool.Exec(ctx, `DELETE FROM chat_message WHERE chat_session_id = $1`, sessionID)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE chat_session_id = $1`, sessionID)
	})
	putTestModelCatalog(t, oldRuntimeID, "old-runtime-only-model")
	putTestModelCatalog(t, newRuntimeID, "new-runtime-only-model")

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin session lock transaction: %v", err)
	}
	defer tx.Rollback(context.Background())
	holderPID := holderBackendPID(t, ctx, tx)
	if _, err := testHandler.Queries.WithTx(tx).LockChatSessionForRuntimeBind(ctx, parseUUID(sessionID)); err != nil {
		t.Fatalf("lock chat session: %v", err)
	}

	type result struct {
		code int
		body string
	}
	rebindResults := make(chan result, 1)
	go func() {
		w := httptest.NewRecorder()
		req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
			"runtime_id": newRuntimeID,
		}), "id", agentID)
		testHandler.UpdateAgent(w, req)
		rebindResults <- result{code: w.Code, body: w.Body.String()}
	}()
	if !waitForWaiterBlockedBy(t, holderPID, 10*time.Second) {
		t.Fatalf("ordinary UpdateAgent runtime rebind did not wait on the chat session lock")
	}

	modelResults := make(chan result, 1)
	go func() {
		w := setChatModel(t, sessionID, "old-runtime-only-model")
		modelResults <- result{code: w.Code, body: w.Body.String()}
	}()
	if !waitForWaiterCountBlockedBy(t, holderPID, 2, 10*time.Second) {
		t.Fatalf("expected UpdateAgent and chat model update to wait on the chat session lock")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit session lock transaction: %v", err)
	}

	select {
	case got := <-rebindResults:
		if got.code != http.StatusOK {
			t.Fatalf("UpdateAgent runtime rebind: got %d: %s", got.code, got.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ordinary runtime rebind did not finish after session lock release")
	}
	select {
	case got := <-modelResults:
		if got.code != http.StatusConflict {
			t.Fatalf("old-runtime model save after rebind: got %d, want 409: %s", got.code, got.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("chat model update did not finish after session lock release")
	}

	session, err := testHandler.Queries.GetChatSession(ctx, parseUUID(sessionID))
	if err != nil {
		t.Fatalf("reload chat session: %v", err)
	}
	if session.ModelOverride.Valid {
		t.Fatalf("old-runtime model override persisted after rebind: %q", session.ModelOverride.String)
	}
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("reload rebound agent: %v", err)
	}
	sent, err := testHandler.TaskService.SendDirectChatMessage(ctx, session, agent, parseUUID(testUserID), "send after ordinary runtime rebind", nil, "member", parseUUID(testUserID))
	if err != nil {
		t.Fatalf("send chat after ordinary runtime rebind: %v", err)
	}
	if !sent.Task.RuntimeID.Valid || uuidToString(sent.Task.RuntimeID) != newRuntimeID {
		t.Fatalf("sent task runtime = %+v, want %s", sent.Task.RuntimeID, newRuntimeID)
	}
	if !sent.Task.ChatModelSnapshot.Valid || sent.Task.ChatModelSnapshot.String == "old-runtime-only-model" {
		t.Fatalf("sent task retained old-runtime model snapshot: %+v", sent.Task.ChatModelSnapshot)
	}
}

func TestSetChatSessionModelCommitsBeforeEnqueueSnapshot(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	agentID := createHandlerTestAgent(t, "ChatModelEnqueueOrder", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM task_message WHERE task_id IN (SELECT id FROM agent_task_queue WHERE chat_session_id = $1)`, sessionID)
		_, _ = testPool.Exec(ctx, `DELETE FROM chat_message WHERE chat_session_id = $1`, sessionID)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE chat_session_id = $1`, sessionID)
	})
	putTestModelCatalog(t, runtimeID, "confirmed-model")

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock transaction: %v", err)
	}
	defer tx.Rollback(context.Background())
	holderPID := holderBackendPID(t, ctx, tx)
	if _, err := testHandler.Queries.WithTx(tx).LockChatSessionForRuntimeBind(ctx, parseUUID(sessionID)); err != nil {
		t.Fatalf("lock chat session: %v", err)
	}
	results := make(chan *httptest.ResponseRecorder, 1)
	go func() { results <- setChatModel(t, sessionID, "confirmed-model") }()
	if !waitForWaiterBlockedBy(t, holderPID, 10*time.Second) {
		t.Fatal("model update never blocked on the chat session lock")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("release session lock: %v", err)
	}
	select {
	case w := <-results:
		if w.Code != http.StatusOK {
			t.Fatalf("SetChatSessionModel: expected 200, got %d: %s", w.Code, w.Body.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("model update did not finish after lock release")
	}

	session, err := testHandler.Queries.GetChatSession(ctx, parseUUID(sessionID))
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("reload agent: %v", err)
	}
	sent, err := testHandler.TaskService.SendDirectChatMessage(ctx, session, agent, parseUUID(testUserID), "snapshot the confirmed model", nil, "member", parseUUID(testUserID))
	if err != nil {
		t.Fatalf("enqueue chat task: %v", err)
	}
	if !sent.Task.ChatModelSnapshot.Valid || sent.Task.ChatModelSnapshot.String != "confirmed-model" {
		t.Fatalf("task model snapshot = %+v, want confirmed-model", sent.Task.ChatModelSnapshot)
	}
}

func TestSetChatSessionModelClearAndFailuresPreservePreviousValue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	agentID := createHandlerTestAgent(t, "ChatModelValidation", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)
	putTestModelCatalog(t, runtimeID, "supported-model")
	if _, err := testPool.Exec(ctx, `UPDATE chat_session SET model_override = 'previous-model' WHERE id = $1`, sessionID); err != nil {
		t.Fatalf("seed prior model override: %v", err)
	}
	assertOverride := func(want string, wantValid bool) {
		t.Helper()
		var got pgtype.Text
		if err := testPool.QueryRow(ctx, `SELECT model_override FROM chat_session WHERE id = $1`, sessionID).Scan(&got); err != nil {
			t.Fatalf("read model override: %v", err)
		}
		if got.Valid != wantValid || (wantValid && got.String != want) {
			t.Fatalf("model override = %+v, want valid=%v value=%q", got, wantValid, want)
		}
	}

	if w := setChatModel(t, sessionID, "unsupported-model"); w.Code != http.StatusConflict {
		t.Fatalf("unsupported model status = %d, want 409: %s", w.Code, w.Body.String())
	}
	assertOverride("previous-model", true)

	if w := setChatModel(t, sessionID, nil); w.Code != http.StatusOK {
		t.Fatalf("clear model override: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertOverride("", false)

	if _, err := testPool.Exec(ctx, `UPDATE chat_session SET model_override = 'previous-model' WHERE id = $1`, sessionID); err != nil {
		t.Fatalf("restore prior model override: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = NULL WHERE id = $1`, agentID); err != nil {
		t.Fatalf("remove agent runtime: %v", err)
	}
	if w := setChatModel(t, sessionID, "supported-model"); w.Code != http.StatusConflict {
		t.Fatalf("missing runtime status = %d, want 409: %s", w.Code, w.Body.String())
	}
	assertOverride("previous-model", true)
	if w := setChatModel(t, sessionID, nil); w.Code != http.StatusOK {
		t.Fatalf("clear with missing runtime: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertOverride("", false)
}

func TestSetChatSessionModelUnavailableCatalogPreservesPreviousValue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := newTestRuntime(t, "Chat Model Catalog Missing", "online")
	agentID := createHandlerTestAgent(t, "ChatModelCatalogUnavailable", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = $1 WHERE id = $2`, runtimeID, agentID); err != nil {
		t.Fatalf("set agent runtime: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE chat_session SET model_override = 'previous-model' WHERE id = $1`, sessionID); err != nil {
		t.Fatalf("seed prior model override: %v", err)
	}
	w := setChatModel(t, sessionID, "unverified-model")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable catalog status = %d, want 503: %s", w.Code, w.Body.String())
	}
	var override pgtype.Text
	if err := testPool.QueryRow(ctx, `SELECT model_override FROM chat_session WHERE id = $1`, sessionID).Scan(&override); err != nil {
		t.Fatalf("read model override: %v", err)
	}
	if !override.Valid || override.String != "previous-model" {
		t.Fatalf("catalog failure changed existing override: %+v", override)
	}
}
