package handler

import (
	"net/http"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestPromptQuizAgentScopePersistsExplicitEmptyAndAudit(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	agentID := retroAgent(t, wsID, "scope executor")
	itemID := dbfx.Insert(t, "prompt_quiz_item", map[string]any{
		"workspace_id": wsID, "slug": "scope-item-" + agentID[:8], "title": "scope item", "body": "scope body", "runtime_profile": "member",
	})
	defer testPool.Exec(t.Context(), `DELETE FROM prompt_quiz_agent_scope_item WHERE agent_id = $1`, agentID)
	defer testPool.Exec(t.Context(), `DELETE FROM prompt_quiz_agent_scope_audit WHERE agent_id = $1`, agentID)
	defer testPool.Exec(t.Context(), `DELETE FROM prompt_quiz_agent_scope WHERE agent_id = $1`, agentID)
	defer testPool.Exec(t.Context(), `DELETE FROM prompt_quiz_item WHERE id = $1`, itemID)

	put := func(ids []string) (int, string) {
		req := withURLParam(legislationReq(ownerID, wsID, http.MethodPut, "/api/prompt-quiz/agents/"+agentID+"/scope", map[string]any{"item_ids": ids}), "agentId", agentID)
		return legislationCall(t, testHandler.PutPromptQuizAgentScope, req)
	}
	if code, body := put([]string{itemID}); code != http.StatusOK {
		t.Fatalf("set scope = %d: %s", code, body)
	}
	if code, body := put([]string{}); code != http.StatusOK {
		t.Fatalf("set empty scope = %d: %s", code, body)
	}
	var explicit bool
	var children int
	var audits int
	dbfx.QueryRow(t, `SELECT EXISTS(SELECT 1 FROM prompt_quiz_agent_scope WHERE agent_id = $1)`, agentID).Scan(&explicit)
	dbfx.QueryRow(t, `SELECT count(*) FROM prompt_quiz_agent_scope_item WHERE agent_id = $1`, agentID).Scan(&children)
	dbfx.QueryRow(t, `SELECT count(*) FROM prompt_quiz_agent_scope_audit WHERE agent_id = $1`, agentID).Scan(&audits)
	if !explicit || children != 0 || audits != 2 {
		t.Fatalf("explicit empty/audit mismatch: explicit=%t children=%d audits=%d", explicit, children, audits)
	}

	// The item delete must retain the explicit marker while removing the child.
	if _, err := testHandler.Queries.DeletePromptQuizItem(t.Context(), db.DeletePromptQuizItemParams{ID: parseUUID(itemID), WorkspaceID: parseUUID(wsID)}); err != nil {
		t.Fatal(err)
	}
	dbfx.QueryRow(t, `SELECT EXISTS(SELECT 1 FROM prompt_quiz_agent_scope WHERE agent_id = $1)`, agentID).Scan(&explicit)
	dbfx.QueryRow(t, `SELECT count(*) FROM prompt_quiz_agent_scope_item WHERE agent_id = $1`, agentID).Scan(&children)
	if !explicit || children != 0 {
		t.Fatalf("hard-delete must retain explicit empty marker: explicit=%t children=%d", explicit, children)
	}
}
