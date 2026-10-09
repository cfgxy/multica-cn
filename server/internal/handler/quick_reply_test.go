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

// Workspace quick-reply catalog tests (RUYI-435, RUYI-586).
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

// TestQuickReplySeedMatchesOwnerSpec pins the default templates to the
// RUYI-435/RUYI-586 owner specs verbatim — name AND body. The specs' texts
// are the product contract: a paraphrased seed would ship different words
// than every document and screenshot describing the feature. Runs against a
// wiped catalog so the assertions cover exactly the empty-workspace first
// seed: six defaults at positions 0..5 in spec order.
func TestQuickReplySeedMatchesOwnerSpec(t *testing.T) {
	ctx := context.Background()
	if _, err := testPool.Exec(ctx,
		`DELETE FROM quick_reply WHERE workspace_id = $1`, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("wipe catalog: %v", err)
	}
	seedTestQuickReplies(t)

	replies, err := testHandler.Queries.ListQuickReplies(ctx, parseUUID(testWorkspaceID))
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
		{"Dry Run + 深度 Review", "执行完整思想实验 / Dry Run，逐条覆盖关键用户路径、状态变化和边界场景；随后进行深度 Code Review，重点检查状态一致性、异常/竞态、回归风险及测试覆盖。发现问题则修复并补测；若 Dry Run + 深度 Review 均无问题，则可按 QA PASS 收口。"},
	}

	if len(replies) != len(want) {
		t.Fatalf("empty workspace Ensure should seed exactly %d defaults, got %d", len(want), len(replies))
	}
	// The list is position-ordered, so index equality also pins the append
	// semantics: defaults land at 0..n-1, new ones after the existing.
	for i, w := range want {
		got := replies[i]
		if got.Name != w.name {
			t.Errorf("seeded position %d: got %q, want %q", i, got.Name, w.name)
			continue
		}
		if got.Content != w.content {
			t.Errorf("seeded %q content drifted from the owner spec:\n  got:  %q\n  want: %q", w.name, got.Content, w.content)
		}
		if got.Position != float64(i) {
			t.Errorf("seeded %q position drifted: got %v, want %v", w.name, got.Position, i)
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
			r.Name == "确认并结单" || r.Name == "检查遗留事项" || r.Name == "Dry Run + 深度 Review" {
			byName[r.Name] = struct{}{}
		}
	}
	if len(byName) != 6 {
		t.Fatalf("expected exactly 6 seeded names after double seeding, got %d", len(byName))
	}
}

// TestQuickReplyEnsureMergesIntoExistingCatalog covers the upgrade path for a
// workspace that already carries the RUYI-435 defaults: Ensure appends the
// RUYI-586 template without disturbing the rows already there, and a
// same-name row an admin customized (name kept, content replaced) keeps its
// content — the (workspace_id, name) conflict skips the whole INSERT rather
// than overwriting.
func TestQuickReplyEnsureMergesIntoExistingCatalog(t *testing.T) {
	ctx := context.Background()
	seedTestQuickReplies(t)

	// Simulate a pre-RUYI-586 workspace: only the original five defaults.
	if _, err := testPool.Exec(ctx,
		`DELETE FROM quick_reply WHERE workspace_id = $1 AND name = $2`,
		parseUUID(testWorkspaceID), "Dry Run + 深度 Review"); err != nil {
		t.Fatalf("remove new default: %v", err)
	}
	before, err := testHandler.Queries.ListQuickReplies(ctx, parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("list pre-upgrade catalog: %v", err)
	}
	if len(before) != 5 {
		t.Fatalf("pre-upgrade catalog should hold exactly the 5 RUYI-435 defaults, got %d", len(before))
	}

	// An admin created a row with the new default's name before the upgrade
	// reached this workspace. Ensure must treat it as a conflict, not data.
	customContent := "qr-test admin-customized body"
	var customID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO quick_reply (workspace_id, name, content, position)
		VALUES ($1, $2, $3, 99)
		RETURNING id
	`, parseUUID(testWorkspaceID), "Dry Run + 深度 Review", customContent).Scan(&customID); err != nil {
		t.Fatalf("insert customized row: %v", err)
	}

	if err := quickreply.Ensure(ctx, testHandler.Queries, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("ensure on existing catalog: %v", err)
	}

	after, err := testHandler.Queries.ListQuickReplies(ctx, parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("list post-upgrade catalog: %v", err)
	}
	byName := map[string]db.QuickReply{}
	for _, r := range after {
		byName[r.Name] = r
	}
	if got, ok := byName["Dry Run + 深度 Review"]; !ok {
		t.Fatalf("post-upgrade catalog missing %q", "Dry Run + 深度 Review")
	} else if got.Content != customContent || got.Position != 99 {
		t.Fatalf("admin-customized row was overwritten: content %q position %v", got.Content, got.Position)
	}
	for _, b := range before {
		a, ok := byName[b.Name]
		if !ok {
			t.Fatalf("post-upgrade catalog lost existing default %q", b.Name)
		}
		if a.Content != b.Content || a.Position != b.Position {
			t.Fatalf("Ensure disturbed existing default %q: content %q→%q position %v→%v",
				b.Name, b.Content, a.Content, b.Position, a.Position)
		}
	}

	// Sweep the customized row and restore the canonical catalog so later
	// tests (and later fixture runs) see the true default again.
	if _, err := testPool.Exec(ctx, `DELETE FROM quick_reply WHERE id = $1`, parseUUID(customID)); err != nil {
		t.Fatalf("remove customized row: %v", err)
	}
	if err := quickreply.Ensure(ctx, testHandler.Queries, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("restore canonical default: %v", err)
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
	if resp.Total != 6 {
		t.Fatalf("self-heal after wipe should restore 6 defaults, got %d", resp.Total)
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
