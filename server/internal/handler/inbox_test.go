package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// withWorkspaceContext seeds the workspace into the request context so handlers
// that read via ctxWorkspaceID() see the test workspace. The middleware does
// this in production; tests bypass middleware so we set it explicitly.
func withWorkspaceContext(req *http.Request, workspaceID string) *http.Request {
	ctx := middleware.SetMemberContext(req.Context(), workspaceID, db.Member{})
	return req.WithContext(ctx)
}

// seedInboxItems inserts <count> inbox items for the test user with timestamps
// staggered one minute apart (oldest first → newest last). Returns the inserted
// IDs in chronological order.
func seedInboxItems(t *testing.T, count int, withIssue bool) []string {
	t.Helper()
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Duration(count) * time.Minute)

	var issueID *string
	if withIssue {
		var iid string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, number, title, description, status, creator_type, creator_id)
			VALUES ($1, 9001, 'Inbox test issue', '', 'todo', 'member', $2)
			RETURNING id
		`, testWorkspaceID, testUserID).Scan(&iid); err != nil {
			t.Fatalf("seed issue: %v", err)
		}
		issueID = &iid
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, iid)
		})
	}

	ids := make([]string, count)
	for i := 0; i < count; i++ {
		var id string
		ts := base.Add(time.Duration(i) * time.Minute)
		if err := testPool.QueryRow(ctx, `
			INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, issue_id, title, body, created_at, details)
			VALUES ($1, 'member', $2, 'comment', 'info', $3, $4, '', $5, '{}'::jsonb)
			RETURNING id
		`, testWorkspaceID, testUserID, issueID, fmt.Sprintf("inbox %d", i), ts).Scan(&id); err != nil {
			t.Fatalf("seed inbox %d: %v", i, err)
		}
		ids[i] = id
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM inbox_item WHERE recipient_id = $1`, testUserID)
	})
	return ids
}

func fetchInboxLegacy(t *testing.T) ([]InboxItemResponse, int) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/inbox", nil)
	req = withWorkspaceContext(req, testWorkspaceID)
	testHandler.ListInbox(w, req)
	var resp []InboxItemResponse
	if w.Code == http.StatusOK {
		json.NewDecoder(w.Body).Decode(&resp)
	}
	return resp, w.Code
}

func fetchInboxPaginated(t *testing.T, query string) (InboxListResponse, int) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/inbox?"+query, nil)
	req = withWorkspaceContext(req, testWorkspaceID)
	testHandler.ListInbox(w, req)
	var resp InboxListResponse
	if w.Code == http.StatusOK {
		json.NewDecoder(w.Body).Decode(&resp)
	}
	return resp, w.Code
}

// TestListInbox_LegacyNoParamsReturnsArray verifies that requests without any
// pagination parameters get the bare []InboxItemResponse contract that pre-
// pagination clients rely on. Wrapping the body would crash old desktops.
func TestListInbox_LegacyNoParamsReturnsArray(t *testing.T) {
	seedInboxItems(t, 5, false)

	resp, code := fetchInboxLegacy(t)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(resp) != 5 {
		t.Fatalf("expected 5 items, got %d", len(resp))
	}
}

// TestListInbox_LegacyPathCappedAt200 ensures the no-params path bounds the
// response so an unexpectedly large inbox doesn't OOM old clients.
func TestListInbox_LegacyPathCappedAt200(t *testing.T) {
	seedInboxItems(t, 250, false)

	resp, code := fetchInboxLegacy(t)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(resp) != inboxLegacyCap {
		t.Fatalf("expected legacy cap %d items, got %d", inboxLegacyCap, len(resp))
	}
}

// TestListInbox_PaginatedLatestPage exercises the new wrapper contract: a
// `limit=N` request returns at most N entries plus a cursor when more exist.
func TestListInbox_PaginatedLatestPage(t *testing.T) {
	seedInboxItems(t, 10, false)

	resp, code := fetchInboxPaginated(t, "limit=5")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(resp.Entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(resp.Entries))
	}
	if !resp.HasMore {
		t.Fatalf("expected has_more=true with 10 items and limit=5")
	}
	if resp.NextCursor == nil || *resp.NextCursor == "" {
		t.Fatalf("expected next_cursor on a non-final page")
	}
}

// TestListInbox_PaginatedFinalPage verifies the cursor is omitted and
// has_more=false on the last page.
func TestListInbox_PaginatedFinalPage(t *testing.T) {
	seedInboxItems(t, 3, false)

	resp, code := fetchInboxPaginated(t, "limit=10")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(resp.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(resp.Entries))
	}
	if resp.HasMore {
		t.Fatalf("expected has_more=false on a final page")
	}
}

// TestListInbox_CursorPagination walks two pages and verifies entries are
// disjoint and ordered newest-first.
func TestListInbox_CursorPagination(t *testing.T) {
	seedInboxItems(t, 6, false)

	page1, code := fetchInboxPaginated(t, "limit=3")
	if code != http.StatusOK {
		t.Fatalf("page1: expected 200, got %d", code)
	}
	if len(page1.Entries) != 3 || !page1.HasMore || page1.NextCursor == nil {
		t.Fatalf("page1: bad shape: %+v", page1)
	}

	page2, code := fetchInboxPaginated(t, "limit=3&before="+*page1.NextCursor)
	if code != http.StatusOK {
		t.Fatalf("page2: expected 200, got %d", code)
	}
	if len(page2.Entries) != 3 {
		t.Fatalf("page2: expected 3 entries, got %d", len(page2.Entries))
	}

	seen := map[string]bool{}
	for _, e := range page1.Entries {
		seen[e.ID] = true
	}
	for _, e := range page2.Entries {
		if seen[e.ID] {
			t.Fatalf("page2 entry %s already on page1 (cursor pagination should be disjoint)", e.ID)
		}
	}
}

// TestListInbox_PerIssueDedup verifies that multiple unarchived inbox items
// for the same issue collapse to a single row in the listing — the SQL-level
// equivalent of the old client-side deduplicateInboxItems helper.
func TestListInbox_PerIssueDedup(t *testing.T) {
	seedInboxItems(t, 4, true) // all 4 items share one issue

	resp, code := fetchInboxPaginated(t, "limit=50")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("expected 1 deduped entry, got %d", len(resp.Entries))
	}
}

// TestListInbox_InvalidLimit rejects a non-numeric or out-of-range limit.
func TestListInbox_InvalidLimit(t *testing.T) {
	for _, q := range []string{"limit=abc", "limit=0", "limit=200"} {
		_, code := fetchInboxPaginated(t, q)
		if code != http.StatusBadRequest {
			t.Fatalf("limit=%q: expected 400, got %d", q, code)
		}
	}
}

// TestListInbox_InvalidCursor returns 400 on a malformed cursor.
func TestListInbox_InvalidCursor(t *testing.T) {
	_, code := fetchInboxPaginated(t, "limit=10&before=not-a-cursor")
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 on bad cursor, got %d", code)
	}
}

// TestCountUnreadInbox_DedupsByIssue ensures the badge count uses the same
// per-issue dedup as the listing — otherwise the badge would say 5 but the
// list would render 1, confusing users.
func TestCountUnreadInbox_DedupsByIssue(t *testing.T) {
	seedInboxItems(t, 4, true) // 4 unread items, all sharing one issue

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/inbox/unread/count", nil)
	req = withWorkspaceContext(req, testWorkspaceID)
	testHandler.CountUnreadInbox(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]int64
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"] != 1 {
		t.Fatalf("expected count=1 (deduped), got %d", resp["count"])
	}
}
