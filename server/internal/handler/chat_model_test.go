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
