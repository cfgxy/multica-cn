package handler

// RUYI-666: workspace revision (optimistic lock) contract.
//
// The workspace.context instruction surface is written concurrently by Leader
// runs and humans. These tests pin the same contract the issue/comment/
// project/agent revision columns already carry (migrations 351/920/928/944):
// every response carries revision, a matching expected_revision updates and
// increments, a stale one answers 409 revision_conflict without mutating the
// row, and omitting expected_revision keeps the legacy unconditional
// behavior (opt-in, so existing callers regress to zero change).
//
// Conflict bodies must expose revision metadata only — never the losing
// writer's candidate text.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedRevisionWorkspace inserts a dedicated workspace row so revision counts
// never race with other tests mutating the shared fixture workspace.
func seedRevisionWorkspace(t *testing.T, slug string) string {
	t.Helper()
	ctx := context.Background()
	_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)
	wsID := dbfx.Insert(t, "workspace", testutil.Cols{
		"name": "Revision Test Workspace",
		"slug": slug,
	})
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, wsID)
	})
	return wsID
}

func patchWorkspace(t *testing.T, wsID string, body map[string]any) *testutil.Response {
	t.Helper()
	req := newRequest("PATCH", "/api/workspaces/"+wsID, body)
	req = withURLParam(req, "id", wsID)
	return testutil.Call(t, testHandler.UpdateWorkspace, req)
}

// A matching expected_revision updates and increments the revision.
func TestWorkspaceRevisionLifecycle(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler suite fixture unavailable")
	}
	wsID := seedRevisionWorkspace(t, handlerTestSlug("ws-revision-lifecycle"))

	var revision int64
	dbfx.QueryRow(t, `SELECT revision FROM workspace WHERE id = $1`, wsID).Scan(&revision)
	if revision != 1 {
		t.Fatalf("expected fresh workspace revision 1, got %d", revision)
	}

	w := patchWorkspace(t, wsID, map[string]any{
		"context":           "lifecycle context v2",
		"expected_revision": revision,
	}).Want(http.StatusOK)

	var resp WorkspaceResponse
	w.JSON(&resp)
	if resp.Revision != revision+1 {
		t.Errorf("expected revision %d after guarded update, got %d", revision+1, resp.Revision)
	}

	var dbCtx *string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, wsID).Scan(&dbCtx)
	if dbCtx == nil || *dbCtx != "lifecycle context v2" {
		t.Errorf("guarded write did not land: context %v", dbCtx)
	}
}

// A stale expected_revision answers 409 revision_conflict with the expected
// and actual revisions, leaves the row untouched, and never echoes the
// losing writer's candidate text back.
func TestUpdateWorkspaceStaleRevisionConflict(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler suite fixture unavailable")
	}
	wsID := seedRevisionWorkspace(t, handlerTestSlug("ws-revision-conflict"))

	// Advance the real revision so the held value is genuinely stale.
	advanced := patchWorkspace(t, wsID, map[string]any{"context": "conflict baseline"}).
		Want(http.StatusOK)
	var advancedResp WorkspaceResponse
	advanced.JSON(&advancedResp)

	const candidateText = "CONCURRENT-CANDIDATE-TEXT must never be echoed"
	w := patchWorkspace(t, wsID, map[string]any{
		"context":           candidateText,
		"expected_revision": advancedResp.Revision + 5,
	}).Want(http.StatusConflict)

	var conflict struct {
		Error            string `json:"error"`
		Code             string `json:"code"`
		ResourceType     string `json:"resource_type"`
		ResourceID       string `json:"resource_id"`
		ExpectedRevision int64  `json:"expected_revision"`
		ActualRevision   int64  `json:"actual_revision"`
	}
	w.JSON(&conflict)
	if conflict.Code != "revision_conflict" {
		t.Errorf("expected code revision_conflict, got %q", conflict.Code)
	}
	if conflict.ResourceType != "workspace" || conflict.ResourceID != wsID {
		t.Errorf("unexpected resource identity: %s %s", conflict.ResourceType, conflict.ResourceID)
	}
	if conflict.ExpectedRevision != advancedResp.Revision+5 {
		t.Errorf("expected_revision %d, got %d", advancedResp.Revision+5, conflict.ExpectedRevision)
	}
	if conflict.ActualRevision != advancedResp.Revision {
		t.Errorf("actual_revision %d, got %d", advancedResp.Revision, conflict.ActualRevision)
	}
	if strings.Contains(w.Body.String(), candidateText) {
		t.Errorf("conflict body leaked the losing writer's candidate text: %s", w.Body.String())
	}

	var dbCtx *string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, wsID).Scan(&dbCtx)
	if dbCtx == nil || *dbCtx != "conflict baseline" {
		t.Errorf("conflicting write mutated the row: context %v", dbCtx)
	}
}

// The lock must not over-reject: a matching value lands, and a non-positive
// one is a clean 400 like the issue/project contracts.
func TestUpdateWorkspaceExpectedRevisionValidation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler suite fixture unavailable")
	}
	wsID := seedRevisionWorkspace(t, handlerTestSlug("ws-revision-validation"))

	patchWorkspace(t, wsID, map[string]any{
		"context":           "validation v2",
		"expected_revision": 0,
	}).Want(http.StatusBadRequest)

	var resp WorkspaceResponse
	patchWorkspace(t, wsID, map[string]any{
		"context":           "validation v3",
		"expected_revision": 1,
	}).Want(http.StatusOK).JSON(&resp)
	if resp.Revision != 2 {
		t.Errorf("expected revision 2 after guarded update, got %d", resp.Revision)
	}
}

// Opt-in regression: a request without expected_revision keeps the legacy
// unconditional behavior — it lands and bumps the revision, never 409s.
func TestUpdateWorkspaceWithoutExpectedRevisionUnchanged(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler suite fixture unavailable")
	}
	wsID := seedRevisionWorkspace(t, handlerTestSlug("ws-revision-optout"))

	for i := int64(2); i <= 3; i++ {
		var resp WorkspaceResponse
		patchWorkspace(t, wsID, map[string]any{"context": "legacy write"}).
			Want(http.StatusOK).JSON(&resp)
		if resp.Revision != i {
			t.Errorf("legacy write: expected revision %d, got %d", i, resp.Revision)
		}
	}
}

// The prompt-version activation/rollback write is also an instruction write:
// it must bump the revision so a client holding an expected_revision from
// before the rollback loses the race instead of overwriting the restored
// context (same pattern as the project contract from RUYI-354).
func TestPromptVersionContextWriteBumpsRevision(t *testing.T) {
	if testPool == nil {
		t.Skip("handler suite fixture unavailable")
	}
	ctx := context.Background()
	wsID := seedRevisionWorkspace(t, handlerTestSlug("ws-revision-promptversion"))

	wsUUID := parseUUID(wsID)
	before, err := db.New(testPool).GetWorkspace(ctx, wsUUID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	updated, err := db.New(testPool).UpdateWorkspaceContextForPromptVersion(ctx,
		db.UpdateWorkspaceContextForPromptVersionParams{ID: wsUUID, Context: strToText("restored context")})
	if err != nil {
		t.Fatalf("prompt-version context write: %v", err)
	}
	if updated.Revision != before.Revision+1 {
		t.Errorf("expected revision %d after prompt-version write, got %d", before.Revision+1, updated.Revision)
	}
}
