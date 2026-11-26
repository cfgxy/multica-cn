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
	var beforeExplicit, afterExplicit bool
	var beforeItems, afterItems []byte
	dbfx.QueryRow(t, `SELECT before_explicit, after_explicit, before_item_ids, after_item_ids FROM prompt_quiz_agent_scope_audit WHERE agent_id=$1 ORDER BY created_at ASC LIMIT 1`, agentID).Scan(&beforeExplicit, &afterExplicit, &beforeItems, &afterItems)
	if beforeExplicit || !afterExplicit || string(beforeItems) != "[]" || string(afterItems) != "[\""+itemID+"\"]" {
		t.Fatalf("legacy->selected audit mismatch: %t %t %s %s", beforeExplicit, afterExplicit, beforeItems, afterItems)
	}
	if code, body := put([]string{}); code != http.StatusOK {
		t.Fatalf("set empty scope = %d: %s", code, body)
	}
	dbfx.QueryRow(t, `SELECT before_explicit, after_explicit, before_item_ids, after_item_ids FROM prompt_quiz_agent_scope_audit WHERE agent_id=$1 ORDER BY created_at DESC LIMIT 1`, agentID).Scan(&beforeExplicit, &afterExplicit, &beforeItems, &afterItems)
	if !beforeExplicit || !afterExplicit || string(beforeItems) != "[\""+itemID+"\"]" || string(afterItems) != "[]" {
		t.Fatalf("selected->empty audit mismatch: %t %t %s %s", beforeExplicit, afterExplicit, beforeItems, afterItems)
	}
	if code, body := put([]string{itemID}); code != http.StatusOK {
		t.Fatalf("reselect scope = %d: %s", code, body)
	}
	// Delete while the relation exists: this fails if DeletePromptQuizItem does not remove scope children.
	if _, err := testHandler.Queries.DeletePromptQuizItem(t.Context(), db.DeletePromptQuizItemParams{ID: parseUUID(itemID), WorkspaceID: parseUUID(wsID)}); err != nil {
		t.Fatal(err)
	}
	var explicit bool
	var children int
	dbfx.QueryRow(t, `SELECT EXISTS(SELECT 1 FROM prompt_quiz_agent_scope WHERE agent_id = $1)`, agentID).Scan(&explicit)
	dbfx.QueryRow(t, `SELECT count(*) FROM prompt_quiz_agent_scope_item WHERE agent_id = $1`, agentID).Scan(&children)
	if !explicit || children != 0 {
		t.Fatalf("hard-delete must retain explicit empty marker: explicit=%t children=%d", explicit, children)
	}
}
