package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// RUYI-350: the MCP update_issue tool rides the ordinary PUT /api/issues/:id
// channel, so the guarantees its description promises — in-place rewrite with
// identity preserved, untouched PATCH fields intact, comments and history
// kept, new values readable back immediately, and no agent run enqueued for a
// pure metadata edit — are pinned here against the full handler + database
// stack, exactly the path an MCP caller exercises.

func updateIssueViaHandler(t *testing.T, issueID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, body), "id", issueID)
	testHandler.UpdateIssue(w, req)
	return w
}

func getIssueViaHandler(t *testing.T, issueID string) IssueResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issueID, nil), "id", issueID)
	testHandler.GetIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssue %s = %d: %s", issueID, w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	return issue
}

func countIssueTasks(t *testing.T, issueID string) int {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`, issueID).Scan(&count); err != nil {
		t.Fatalf("count agent_task_queue for issue: %v", err)
	}
	return count
}

// TestIssueUpdateE2E_InPlaceRewritePreservesIdentityAndHistory walks the
// RUYI-326 scenario that motivated update_issue: an issue needs its title and
// Markdown description rewritten in place. It must keep id, identifier,
// number, project and parent, keep its comment thread, expose the new values
// to an immediate read-back, and advance revision on every real change.
func TestIssueUpdateE2E_InPlaceRewritePreservesIdentityAndHistory(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	w := httptest.NewRecorder()
	testHandler.CreateIssue(w, newRequest("POST", "/api/issues", map[string]any{
		"title":       "RUYI-350 e2e original",
		"description": "original body",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue = %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	// A parent relation plus a comment: the relations and history that an
	// in-place rewrite must preserve.
	var parentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, creator_type, creator_id, number)
		VALUES ($1, 'RUYI-350 e2e parent', 'member', $2,
		        (SELECT coalesce(max(number), 0) + 1000 FROM issue WHERE workspace_id = $1))
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&parentID); err != nil {
		t.Fatalf("insert parent issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, parentID) })
	if w := updateIssueViaHandler(t, created.ID, map[string]any{
		"parent_issue_id": parentID,
	}); w.Code != http.StatusOK {
		t.Fatalf("attach parent = %d: %s", w.Code, w.Body.String())
	}
	var commentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content)
		VALUES ($1, $2, 'member', $3, 'history must survive the rewrite')
		RETURNING id
	`, created.ID, testWorkspaceID, testUserID).Scan(&commentID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM comment WHERE id = $1`, commentID) })

	// Title-only rewrite.
	if w := updateIssueViaHandler(t, created.ID, map[string]any{
		"title": "RUYI-350 e2e rewritten title",
	}); w.Code != http.StatusOK {
		t.Fatalf("title-only update = %d: %s", w.Code, w.Body.String())
	}
	// Markdown description rewrite (the RUYI-326 blocker).
	markdown := "# Rewritten plan\n\n- step **one**\n- step two\n\n```sql\nSELECT 1;\n```"
	if w := updateIssueViaHandler(t, created.ID, map[string]any{
		"description": markdown,
	}); w.Code != http.StatusOK {
		t.Fatalf("description update = %d: %s", w.Code, w.Body.String())
	}

	// Immediate read-back: new values visible, identity and untouched fields
	// (including description from the earlier write) intact.
	after := getIssueViaHandler(t, created.ID)
	if after.ID != created.ID || after.Identifier != created.Identifier || after.Number != created.Number {
		t.Fatalf("identity drifted: id=%s identifier=%s number=%d, want %s/%s/%d",
			after.ID, after.Identifier, after.Number, created.ID, created.Identifier, created.Number)
	}
	if after.Title != "RUYI-350 e2e rewritten title" {
		t.Fatalf("title = %q, want rewritten title", after.Title)
	}
	if after.Description == nil || *after.Description != markdown {
		t.Fatalf("description read-back mismatch:\n got %v\nwant %q", after.Description, markdown)
	}
	if after.ParentIssueID == nil || *after.ParentIssueID != parentID {
		t.Fatalf("parent_issue_id = %v, want %q (untouched by title/description writes)", after.ParentIssueID, parentID)
	}
	if after.Revision <= created.Revision {
		t.Fatalf("revision = %d, want > %d after two real writes", after.Revision, created.Revision)
	}

	// The comment thread survives the rewrite, unchanged.
	var commentContent string
	if err := testPool.QueryRow(ctx,
		`SELECT content FROM comment WHERE id = $1`, commentID).Scan(&commentContent); err != nil {
		t.Fatalf("reload comment after rewrite: %v", err)
	}
	if commentContent != "history must survive the rewrite" {
		t.Fatalf("comment mutated by rewrite: %q", commentContent)
	}
}

// TestIssueUpdateE2E_MetadataWriteStartsNoRun pins the no-side-effect half of
// the update_issue contract (acceptance 7): a pure metadata edit — title,
// description, priority, dates — must enqueue no agent task on any code path.
func TestIssueUpdateE2E_MetadataWriteStartsNoRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	w := httptest.NewRecorder()
	testHandler.CreateIssue(w, newRequest("POST", "/api/issues", map[string]any{
		"title": "RUYI-350 e2e no-run metadata edit",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue = %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	if w := updateIssueViaHandler(t, created.ID, map[string]any{
		"title":       "metadata rewrite",
		"description": "pure metadata body",
		"priority":    "high",
		"start_date":  "2026-10-03",
		"due_date":    "2026-10-09",
	}); w.Code != http.StatusOK {
		t.Fatalf("metadata update = %d: %s", w.Code, w.Body.String())
	}

	if count := countIssueTasks(t, created.ID); count != 0 {
		t.Fatalf("agent_task_queue count = %d after a pure metadata write, want 0", count)
	}
}

// TestIssueUpdateE2E_ExpectedRevisionConflictWritesNothing pins the optimistic
// lock half of the update_issue contract (acceptance 6): a write carrying a
// stale expected_revision answers 409 revision_conflict and the concurrent
// winner's value survives untouched.
func TestIssueUpdateE2E_ExpectedRevisionConflictWritesNothing(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	w := httptest.NewRecorder()
	testHandler.CreateIssue(w, newRequest("POST", "/api/issues", map[string]any{
		"title": "RUYI-350 e2e optimistic lock",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue = %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	// Writer A wins at the current revision.
	if w := updateIssueViaHandler(t, created.ID, map[string]any{
		"title": "concurrent winner",
	}); w.Code != http.StatusOK {
		t.Fatalf("winner write = %d: %s", w.Code, w.Body.String())
	}

	// Writer B replays its stale read: the write must be refused with the
	// structured conflict body, leaving A's value in place.
	stale := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+created.ID, map[string]any{
		"title":             "stale overwrite",
		"expected_revision": created.Revision,
	}), "id", created.ID)
	testHandler.UpdateIssue(stale, req)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale write = %d, want 409: %s", stale.Code, stale.Body.String())
	}
	var conflict struct {
		Code            string `json:"code"`
		ExpectedRevision int64  `json:"expected_revision"`
		ActualRevision   int64  `json:"actual_revision"`
	}
	if err := json.NewDecoder(stale.Body).Decode(&conflict); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if conflict.Code != "revision_conflict" || conflict.ActualRevision <= conflict.ExpectedRevision {
		t.Fatalf("conflict body = %+v, want code revision_conflict with actual > expected", conflict)
	}
	if after := getIssueViaHandler(t, created.ID); after.Title != "concurrent winner" {
		t.Fatalf("title = %q after refused stale write, want the concurrent winner preserved", after.Title)
	}
}

// TestIssueUpdateE2E_DoneIssueStaysEditable settles acceptance 3's open
// question: done/cancelled issues MAY be edited through this channel, same as
// the web app — the write succeeds instead of failing silently.
func TestIssueUpdateE2E_DoneIssueStaysEditable(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	w := httptest.NewRecorder()
	testHandler.CreateIssue(w, newRequest("POST", "/api/issues", map[string]any{
		"title": "RUYI-350 e2e done-issue edit",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue = %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})

	if w := updateIssueViaHandler(t, created.ID, map[string]any{"status": "done"}); w.Code != http.StatusOK {
		t.Fatalf("move to done = %d: %s", w.Code, w.Body.String())
	}
	if w := updateIssueViaHandler(t, created.ID, map[string]any{
		"title":       "edited after done",
		"description": "done issues stay editable",
	}); w.Code != http.StatusOK {
		t.Fatalf("edit done issue = %d: %s", w.Code, w.Body.String())
	}
	after := getIssueViaHandler(t, created.ID)
	if after.Status != "done" {
		t.Fatalf("status = %q, want done preserved", after.Status)
	}
	if after.Title != "edited after done" {
		t.Fatalf("title = %q, want the done-issue edit to apply", after.Title)
	}
}
