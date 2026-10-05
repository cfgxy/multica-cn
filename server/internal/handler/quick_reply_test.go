package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/quickreply"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Workspace quick-reply catalog tests (RUYI-435).
//
// The shared test workspace is created with raw SQL (no catalog rows), which
// is exactly the unseeded case the list endpoint self-heals.

// quickReplyFixturePrefix namespaces every row these tests create so cleanup
// can sweep without touching rows another test process owns.
const quickReplyFixturePrefix = "qr-test-"

// seedTestQuickReplies makes the shared test workspace's catalog present.
func seedTestQuickReplies(t *testing.T) {
	t.Helper()
	if err := quickreply.Ensure(context.Background(), testHandler.Queries, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("seed quick replies: %v", err)
	}
}

// cleanupQuickReply removes one test row when its test ends.
func cleanupQuickReply(t *testing.T, id string) {
	t.Helper()
	dbfx.Cleanup(t, `DELETE FROM quick_reply WHERE id = $1`, parseUUID(id))
}

// createTestQuickReply inserts a template directly through the API as the
// workspace owner and registers cleanup. Returns the parsed response.
func createTestQuickReply(t *testing.T, name, content string) QuickReplyResponse {
	t.Helper()
	seedTestQuickReplies(t)
	var created QuickReplyResponse
	testutil.Call(t, testHandler.CreateQuickReply,
		newRequest(http.MethodPost, "/api/quick-replies", map[string]any{
			"name": name, "content": content,
		})).Want(http.StatusCreated).JSON(&created)
	cleanupQuickReply(t, created.ID)
	return created
}

// TestQuickReplySeedMatchesOwnerSpec pins the 5 default templates to the
// RUYI-435 owner spec verbatim — name AND body. The spec's texts are the
// product contract: a paraphrased seed would ship different words than every
// document and screenshot describing the feature.
func TestQuickReplySeedMatchesOwnerSpec(t *testing.T) {
	seedTestQuickReplies(t)

	replies, err := testHandler.Queries.ListQuickReplies(context.Background(), parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("list quick replies: %v", err)
	}

	type expected struct {
		name    string
		content string
	}
	want := []expected{
		{"解决 PR 冲突", "请基于最新 main 处理当前 PR 冲突，保留有效改动；处理完成后重新执行必要验证并更新 PR。"},
		{"继续未完成工作", "请基于当前已有成果继续推进，仅完成尚未完成的部分，不要重复已经完成的工作；完成后给出结果和验证证据。"},
		{"补测试 / QA", "请补齐当前 Issue 所需的实际测试 / QA 验证，并附上可核验的测试结果或证据；确认通过后再进入结单。"},
		{"确认并结单", "请核对当前 Issue 的全部要求是否已经完成且无遗漏；确认满足验收要求后完成结单。"},
		{"检查遗留事项", "请检查当前 Issue 是否还有未完成事项、待我决策事项，以及未提交或未合入的代码；如有请逐项列出，如无请明确确认。"},
	}

	byName := map[string]QuickReplyResponse{}
	for _, r := range replies {
		if len(r.Name) >= len(quickReplyFixturePrefix) && r.Name[:len(quickReplyFixturePrefix)] == quickReplyFixturePrefix {
			continue // another test's fixture row, swept by its own cleanup
		}
		byName[r.Name] = quickReplyToResponse(r)
	}
	for _, w := range want {
		got, ok := byName[w.name]
		if !ok {
			t.Errorf("seeded catalog missing default %q", w.name)
			continue
		}
		if got.Content != w.content {
			t.Errorf("seeded %q content drifted from the owner spec:\n  got:  %q\n  want: %q", w.name, got.Content, w.content)
		}
	}
}

// TestQuickReplyEnsureIsIdempotent covers the rolling-deploy case: two pods
// can seed the same workspace concurrently and the second must be a no-op.
func TestQuickReplyEnsureIsIdempotent(t *testing.T) {
	seedTestQuickReplies(t)
	if err := quickreply.Ensure(context.Background(), testHandler.Queries, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("second Ensure should be a no-op, got: %v", err)
	}

	replies, err := testHandler.Queries.ListQuickReplies(context.Background(), parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("list quick replies: %v", err)
	}
	byName := map[string]struct{}{}
	for _, r := range replies {
		if r.Name == "解决 PR 冲突" || r.Name == "继续未完成工作" || r.Name == "补测试 / QA" ||
			r.Name == "确认并结单" || r.Name == "检查遗留事项" {
			byName[r.Name] = struct{}{}
		}
	}
	if len(byName) != 5 {
		t.Fatalf("expected exactly 5 seeded names after double seeding, got %d", len(byName))
	}
}

// TestQuickReplyListSelfHeals pins the read-path seeding for workspaces that
// predate the feature: wiping the catalog must restore exactly the defaults
// on the next list read, without any backfill migration.
func TestQuickReplyListSelfHeals(t *testing.T) {
	ctx := context.Background()
	seedTestQuickReplies(t)
	if _, err := testPool.Exec(ctx, `DELETE FROM quick_reply WHERE workspace_id = $1`, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("wipe catalog: %v", err)
	}

	var resp struct {
		QuickReplies []QuickReplyResponse `json:"quick_replies"`
		Total        int                  `json:"total"`
	}
	testutil.Call(t, testHandler.ListQuickReplies,
		newRequest(http.MethodGet, "/api/quick-replies", nil)).Want(http.StatusOK).JSON(&resp)
	if resp.Total != 5 {
		t.Fatalf("self-heal after wipe should restore 5 defaults, got %d", resp.Total)
	}
}

// TestQuickReplyWriteRequiresAdmin is the permission negative: a plain member
// gets 403 on every mutating endpoint while the workspace owner succeeds.
// Removing the requireWorkspaceRole gate makes this fail (the member's
// request would succeed), which is the assertion-validity criterion the
// dispatch card demands.
func TestQuickReplyWriteRequiresAdmin(t *testing.T) {
	seedTestQuickReplies(t)

	// A second user who is a plain member of the shared workspace.
	ctx := context.Background()
	memberEmail := fmt.Sprintf("qr-member-%s@multica.ai", handlerFixtureSuffix)
	var memberUserID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (email, name)
		VALUES ($1, 'QR Member Test')
		RETURNING id
	`, memberEmail).Scan(&memberUserID); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	dbfx.Cleanup(t, `DELETE FROM "user" WHERE id = $1`, parseUUID(memberUserID))
	dbfx.Cleanup(t, `DELETE FROM member WHERE user_id = $1`, parseUUID(memberUserID))
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, memberUserID); err != nil {
		t.Fatalf("add member row: %v", err)
	}

	reqAsMember := func(method, path string, body any) *http.Request {
		req := newRequest(method, path, body)
		req.Header.Set("X-User-ID", memberUserID)
		return req
	}

	testutil.Call(t, testHandler.CreateQuickReply,
		reqAsMember(http.MethodPost, "/api/quick-replies", map[string]any{
			"name": quickReplyFixturePrefix + "member-cannot-create", "content": "nope",
		})).Want(http.StatusForbidden)

	// The rejected create must not have written a row.
	var leaked int
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM quick_reply WHERE workspace_id = $1 AND name = $2`,
		parseUUID(testWorkspaceID), quickReplyFixturePrefix+"member-cannot-create").Scan(&leaked); err != nil {
		t.Fatalf("count rejected create: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("rejected member create leaked %d row(s)", leaked)
	}

	existing := createTestQuickReply(t, quickReplyFixturePrefix+"admin-only-target", "before")
	testutil.Call(t, testHandler.UpdateQuickReply,
		reqAsMember(http.MethodPatch, "/api/quick-replies/"+existing.ID, map[string]any{
			"content": "member edit",
		})).Want(http.StatusForbidden)
	testutil.Call(t, testHandler.DeleteQuickReply,
		reqAsMember(http.MethodDelete, "/api/quick-replies/"+existing.ID, nil)).Want(http.StatusForbidden)

	// The owner's own writes still work on the same row.
	var updated QuickReplyResponse
	testutil.Call(t, testHandler.UpdateQuickReply,
		withURLParam(newRequest(http.MethodPatch, "/api/quick-replies/"+existing.ID, map[string]any{
			"content": "owner edit",
		}), "id", existing.ID)).Want(http.StatusOK).JSON(&updated)
	if updated.Content != "owner edit" {
		t.Fatalf("owner update lost: %q", updated.Content)
	}

	// Reorder rejects a member too.
	testutil.Call(t, testHandler.ReorderQuickReplies,
		reqAsMember(http.MethodPatch, "/api/quick-replies/reorder", map[string]any{
			"ids": []string{existing.ID},
		})).Want(http.StatusForbidden)
}

// TestQuickReplyWorkspaceIsolation pins the per-workspace boundary: another
// workspace's rows are invisible in the list and unreachable by id, and a
// reorder payload carrying a foreign id is rejected without moving the
// foreign row.
func TestQuickReplyWorkspaceIsolation(t *testing.T) {
	ctx := context.Background()
	seedTestQuickReplies(t)

	// Second workspace owned by the same fixture user.
	foreignSlug := "qr-isolation-" + handlerFixtureSuffix
	var foreignWsID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug)
		VALUES ('QR Isolation WS', $1)
		RETURNING id
	`, foreignSlug).Scan(&foreignWsID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	dbfx.Cleanup(t, `DELETE FROM quick_reply WHERE workspace_id = $1`, parseUUID(foreignWsID))
	dbfx.Cleanup(t, `DELETE FROM workspace WHERE id = $1`, parseUUID(foreignWsID))
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, foreignWsID, testUserID); err != nil {
		t.Fatalf("add foreign owner: %v", err)
	}

	// A row in the foreign workspace.
	var foreignReplyID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO quick_reply (workspace_id, name, content, position)
		VALUES ($1, $2, 'foreign template', 0)
		RETURNING id
	`, foreignWsID, quickReplyFixturePrefix+"foreign-row").Scan(&foreignReplyID); err != nil {
		t.Fatalf("create foreign row: %v", err)
	}

	// The shared workspace's list never shows it.
	var resp struct {
		QuickReplies []QuickReplyResponse `json:"quick_replies"`
		Total        int                  `json:"total"`
	}
	testutil.Call(t, testHandler.ListQuickReplies,
		newRequest(http.MethodGet, "/api/quick-replies", nil)).Want(http.StatusOK).JSON(&resp)
	for _, r := range resp.QuickReplies {
		if r.ID == foreignReplyID {
			t.Fatalf("foreign workspace row leaked into this workspace's list")
		}
	}

	// …and is unreachable by id from this workspace.
	req := withURLParam(newRequest(http.MethodPatch, "/api/quick-replies/"+foreignReplyID, map[string]any{
		"content": "cross-tenant edit",
	}), "id", foreignReplyID)
	testutil.Call(t, testHandler.UpdateQuickReply, req).Want(http.StatusNotFound)

	// A reorder payload with the foreign id is a 400 and moves nothing.
	foreignBefore, err := testHandler.Queries.GetQuickReply(ctx, db.GetQuickReplyParams{
		ID: parseUUID(foreignReplyID), WorkspaceID: parseUUID(foreignWsID),
	})
	if err != nil {
		t.Fatalf("read foreign row: %v", err)
	}
	reorderReq := newRequest(http.MethodPatch, "/api/quick-replies/reorder", map[string]any{
		"ids": []string{foreignReplyID},
	})
	testutil.Call(t, testHandler.ReorderQuickReplies, reorderReq).Want(http.StatusBadRequest)
	foreignAfter, err := testHandler.Queries.GetQuickReply(ctx, db.GetQuickReplyParams{
		ID: parseUUID(foreignReplyID), WorkspaceID: parseUUID(foreignWsID),
	})
	if err != nil {
		t.Fatalf("re-read foreign row: %v", err)
	}
	if foreignAfter.Position != foreignBefore.Position {
		t.Fatalf("rejected reorder moved a foreign row: %v -> %v", foreignBefore.Position, foreignAfter.Position)
	}
}

// TestQuickReplyCRUDAndReorder walks one template through create (append +
// explicit position), PATCH update, reorder and delete.
func TestQuickReplyCRUDAndReorder(t *testing.T) {
	seedTestQuickReplies(t)

	// Create appends after everything currently in the catalog.
	var first QuickReplyResponse
	testutil.Call(t, testHandler.CreateQuickReply,
		newRequest(http.MethodPost, "/api/quick-replies", map[string]any{
			"name": quickReplyFixturePrefix + "crud-a", "content": "template A",
		})).Want(http.StatusCreated).JSON(&first)
	cleanupQuickReply(t, first.ID)
	var second QuickReplyResponse
	testutil.Call(t, testHandler.CreateQuickReply,
		newRequest(http.MethodPost, "/api/quick-replies", map[string]any{
			"name": quickReplyFixturePrefix + "crud-b", "content": "template B",
		})).Want(http.StatusCreated).JSON(&second)
	cleanupQuickReply(t, second.ID)
	if !(second.Position > first.Position) {
		t.Fatalf("append must place the new row after the previous one: %v then %v", first.Position, second.Position)
	}

	// Explicit position slots between neighbours.
	var slotted QuickReplyResponse
	testutil.Call(t, testHandler.CreateQuickReply,
		newRequest(http.MethodPost, "/api/quick-replies", map[string]any{
			"name": quickReplyFixturePrefix + "crud-mid", "content": "template M", "position": first.Position + 0.5,
		})).Want(http.StatusCreated).JSON(&slotted)
	cleanupQuickReply(t, slotted.ID)

	var resp struct {
		QuickReplies []QuickReplyResponse `json:"quick_replies"`
	}
	testutil.Call(t, testHandler.ListQuickReplies,
		newRequest(http.MethodGet, "/api/quick-replies", nil)).Want(http.StatusOK).JSON(&resp)
	if len(resp.QuickReplies) < 3 {
		t.Fatalf("expected at least 3 rows after creates, got %d", len(resp.QuickReplies))
	}
	// Ordered read: A < M < B by position.
	positions := map[string]float64{}
	for _, r := range resp.QuickReplies {
		positions[r.ID] = r.Position
	}
	if !(positions[first.ID] < positions[slotted.ID] && positions[slotted.ID] < positions[second.ID]) {
		t.Fatalf("explicit position did not slot between: A=%v M=%v B=%v", positions[first.ID], positions[slotted.ID], positions[second.ID])
	}

	// Reorder swaps A and B to the front in a chosen order.
	testutil.Call(t, testHandler.ReorderQuickReplies,
		newRequest(http.MethodPatch, "/api/quick-replies/reorder", map[string]any{
			"ids": allQuickReplyIDsInListOrder(resp.QuickReplies, []string{second.ID, first.ID, slotted.ID}),
		})).Want(http.StatusOK)

	testutil.Call(t, testHandler.ListQuickReplies,
		newRequest(http.MethodGet, "/api/quick-replies", nil)).Want(http.StatusOK).JSON(&resp)
	var gotOrder []string
	for _, r := range resp.QuickReplies {
		switch r.ID {
		case second.ID, first.ID, slotted.ID:
			gotOrder = append(gotOrder, r.ID)
		}
	}
	if len(gotOrder) != 3 || gotOrder[0] != second.ID || gotOrder[1] != first.ID || gotOrder[2] != slotted.ID {
		t.Fatalf("reorder not applied: %v", gotOrder)
	}

	// PATCH updates one field and leaves the rest alone.
	var patched QuickReplyResponse
	testutil.Call(t, testHandler.UpdateQuickReply,
		withURLParam(newRequest(http.MethodPatch, "/api/quick-replies/"+first.ID, map[string]any{
			"name": quickReplyFixturePrefix + "crud-a-renamed",
		}), "id", first.ID)).Want(http.StatusOK).JSON(&patched)
	if patched.Name != quickReplyFixturePrefix+"crud-a-renamed" || patched.Content != "template A" {
		t.Fatalf("PATCH changed more than the sent field: %+v", patched)
	}

	// Delete removes the row.
	testutil.Call(t, testHandler.DeleteQuickReply,
		withURLParam(newRequest(http.MethodDelete, "/api/quick-replies/"+second.ID, nil), "id", second.ID)).Want(http.StatusOK)
	testutil.Call(t, testHandler.GetQuickReply,
		withURLParam(newRequest(http.MethodGet, "/api/quick-replies/"+second.ID, nil), "id", second.ID)).Want(http.StatusNotFound)
}

// TestQuickReplyUniquenessAndValidation covers the 409 on duplicate names and
// the 400 validation rules.
func TestQuickReplyUniquenessAndValidation(t *testing.T) {
	seedTestQuickReplies(t)
	existing := createTestQuickReply(t, quickReplyFixturePrefix+"dupe", "original")

	testutil.Call(t, testHandler.CreateQuickReply,
		newRequest(http.MethodPost, "/api/quick-replies", map[string]any{
			"name": existing.Name, "content": "other",
		})).Want(http.StatusConflict)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty name", map[string]any{"name": "  ", "content": "x"}},
		{"control char in name", map[string]any{"name": "bad\nname", "content": "x"}},
		{"name too long", map[string]any{"name": strings.Repeat("名", 65), "content": "x"}},
		{"empty content", map[string]any{"name": "ok", "content": "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Call(t, testHandler.CreateQuickReply,
				newRequest(http.MethodPost, "/api/quick-replies", tc.body)).Want(http.StatusBadRequest)
		})
	}

	// A 64-rune name is accepted (boundary).
	var ok QuickReplyResponse
	testutil.Call(t, testHandler.CreateQuickReply,
		newRequest(http.MethodPost, "/api/quick-replies", map[string]any{
			"name": quickReplyFixturePrefix + strings.Repeat("名", 54), "content": "boundary ok",
		})).Want(http.StatusCreated).JSON(&ok)
	cleanupQuickReply(t, ok.ID)
}

// allQuickReplyIDsInListOrder builds the full reorder payload: the fixture
// rows in the requested order, then every other row in its current list
// order — reorder requires the complete catalog.
func allQuickReplyIDsInListOrder(rows []QuickReplyResponse, head []string) []string {
	headSet := map[string]struct{}{}
	for _, id := range head {
		headSet[id] = struct{}{}
	}
	out := append([]string{}, head...)
	for _, r := range rows {
		if _, isHead := headSet[r.ID]; !isHead {
			out = append(out, r.ID)
		}
	}
	return out
}
