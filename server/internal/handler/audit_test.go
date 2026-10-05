package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// The decoded wire shape of one audit events page. Declared locally (not via
// auditEventDTO) so these tests pin the JSON contract rather than the Go
// struct: the wire is what REST clients and the MCP search_audit_events tool
// consume.
type auditEventsPageWire struct {
	Events       []auditEventWire `json:"events"`
	NextCursor   *string          `json:"next_cursor"`
	NextCursorID *string          `json:"next_cursor_id"`
}

type auditEventWire struct {
	ID         string  `json:"id"`
	Domain     string  `json:"domain"`
	EventType  string  `json:"event_type"`
	OccurredAt string  `json:"occurred_at"`
	ActorType  string  `json:"actor_type"`
	IssueID    *string `json:"issue_id"`
	TaskID     *string `json:"task_id"`
}

func fetchWorkspaceAuditEvents(t *testing.T, query string) (auditEventsPageWire, int) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/workspaces/"+testWorkspaceID+"/audit-events?"+query, nil)
	req = withURLParam(req, "id", testWorkspaceID)
	testHandler.ListAuditEvents(w, req)
	var page auditEventsPageWire
	if w.Code == http.StatusOK {
		if err := json.NewDecoder(w.Body).Decode(&page); err != nil {
			t.Fatalf("decode audit page: %v", err)
		}
	}
	return page, w.Code
}

func fetchIssueAuditEvents(t *testing.T, issueID, query string) (auditEventsPageWire, int) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/issues/"+issueID+"/audit-events?"+query, nil)
	req = withURLParam(req, "id", issueID)
	testHandler.ListIssueAuditEvents(w, req)
	var page auditEventsPageWire
	if w.Code == http.StatusOK {
		if err := json.NewDecoder(w.Body).Decode(&page); err != nil {
			t.Fatalf("decode audit page: %v", err)
		}
	}
	return page, w.Code
}

// seedAuditEvent inserts one audit_event row in the suite workspace and
// returns its id. audit_event carries no foreign keys, so issue_id/task_id can
// be any UUID — a query-surface test does not need the referenced rows.
func seedAuditEvent(t *testing.T, occurredAt time.Time, issueID, taskID string, overrides testutil.Cols) string {
	t.Helper()
	cols := testutil.Cols{
		"id":           uuid.NewString(), // audit_event.id has no default
		"workspace_id": testWorkspaceID,
		"domain":       "issue",
		"event_type":   "issue.updated", // CHECK: event_type must start with the domain
		"actor_type":   "member",
		"occurred_at":  occurredAt,
	}
	if issueID != "" {
		cols["issue_id"] = issueID
	}
	if taskID != "" {
		cols["task_id"] = taskID
	}
	for k, v := range overrides {
		cols[k] = v
	}
	return dbfx.Insert(t, "audit_event", cols)
}

// eventIDs extracts the sorted-free id list of a page for set comparisons.
func eventIDs(page auditEventsPageWire) []string {
	ids := make([]string, len(page.Events))
	for i, e := range page.Events {
		ids[i] = e.ID
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// P1 rework: the workspace-level search dropped the issue_id filter on the
// floor — the MCP search_audit_events tool passes issue_id through this
// endpoint and silently got the whole workspace trail back. A passed filter
// must filter, and an invalid one must 400 like every other dimension
// (invalid-filter-widens is worse than a rejection).
func TestListAuditEventsWorkspaceIssueIDFilter(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueA := uuid.NewString()
	issueB := uuid.NewString()
	base := time.Now().UTC().Add(-time.Hour)
	idsA := []string{
		seedAuditEvent(t, base.Add(1*time.Second), issueA, "", nil),
		seedAuditEvent(t, base.Add(2*time.Second), issueA, "", nil),
		seedAuditEvent(t, base.Add(3*time.Second), issueA, "", nil),
	}
	idB := seedAuditEvent(t, base.Add(4*time.Second), issueB, "", nil)

	// Unfiltered: a superset of both trails (the workspace may also carry
	// rows written by fixture setup — assert membership, not exact counts).
	page, code := fetchWorkspaceAuditEvents(t, "")
	if code != http.StatusOK {
		t.Fatalf("unfiltered: expected 200, got %d", code)
	}
	ids := eventIDs(page)
	if len(ids) < 4 || !containsID(ids, idsA[0]) || !containsID(ids, idsA[2]) || !containsID(ids, idB) {
		t.Fatalf("unfiltered: expected at least the 4 seeded rows, got %d: %v", len(ids), ids)
	}

	// issue_id=A: exactly the three A rows, nothing from B.
	page, code = fetchWorkspaceAuditEvents(t, "issue_id="+issueA)
	if code != http.StatusOK {
		t.Fatalf("issue_id=A: expected 200, got %d", code)
	}
	ids = eventIDs(page)
	if len(ids) != 3 {
		t.Fatalf("issue_id=A: expected exactly 3 rows, got %d: %v", len(ids), ids)
	}
	for _, want := range idsA {
		if !containsID(ids, want) {
			t.Fatalf("issue_id=A: missing seeded row %s in %v", want, ids)
		}
	}
	for _, e := range page.Events {
		if e.IssueID == nil || *e.IssueID != issueA {
			t.Fatalf("issue_id=A: row %s leaked from another issue: %v", e.ID, e.IssueID)
		}
	}

	// issue_id=B: exactly the one B row.
	page, code = fetchWorkspaceAuditEvents(t, "issue_id="+issueB)
	if code != http.StatusOK {
		t.Fatalf("issue_id=B: expected 200, got %d", code)
	}
	if ids = eventIDs(page); len(ids) != 1 || ids[0] != idB {
		t.Fatalf("issue_id=B: expected exactly [%s], got %v", idB, ids)
	}

	// Invalid UUID: 400, mirroring the other dimensions' rejection posture.
	if _, code = fetchWorkspaceAuditEvents(t, "issue_id=not-a-uuid"); code != http.StatusBadRequest {
		t.Fatalf("issue_id=not-a-uuid: expected 400, got %d", code)
	}
}

// P2-1 rework: occurred_at was serialized at second precision while the keyset
// cursor (next_cursor) is nanosecond. A client paging with occurred_at as the
// cursor dropped every row sharing the cursor's truncated second. The DTO must
// carry full precision so the response's own occurred_at is a safe cursor.
func TestListAuditEventsOccurredAtCursorTraversal(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	// Three rows within two adjacent seconds: after second-truncation the
	// walk from row 2 skips row 1 entirely.
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	want := []string{
		seedAuditEvent(t, base.Add(1500*time.Millisecond), "", "", nil), // newest
		seedAuditEvent(t, base.Add(900*time.Millisecond), "", "", nil),
		seedAuditEvent(t, base.Add(100*time.Millisecond), "", "", nil), // oldest
	}

	// First page: full-precision occurred_at on the wire.
	page, code := fetchWorkspaceAuditEvents(t, "limit=1")
	if code != http.StatusOK || len(page.Events) != 1 {
		t.Fatalf("first page: expected 200 with 1 event, got %d / %d events", code, len(page.Events))
	}
	if !strings.Contains(page.Events[0].OccurredAt, ".") {
		t.Fatalf("occurred_at lost sub-second precision: %q", page.Events[0].OccurredAt)
	}

	// Walk with the response's own occurred_at as the cursor — the client
	// pattern the truncation broke. The first page's row counts as visited.
	visited := map[string]bool{page.Events[0].ID: true}
	cursor, cursorID := page.Events[0].OccurredAt, *page.NextCursorID
	for pages := 0; pages < 100 && cursor != ""; pages++ {
		q := "limit=1&cursor=" + url.QueryEscape(cursor) + "&cursor_id=" + url.QueryEscape(cursorID)
		page, code = fetchWorkspaceAuditEvents(t, q)
		if code != http.StatusOK {
			t.Fatalf("cursor page: expected 200, got %d", code)
		}
		if len(page.Events) == 0 {
			break
		}
		visited[page.Events[0].ID] = true
		if page.NextCursor != nil {
			cursor, cursorID = page.Events[0].OccurredAt, *page.NextCursorID
		} else {
			cursor = ""
		}
	}
	for _, id := range want {
		if !visited[id] {
			t.Fatalf("cursor traversal missed seeded row %s (visited %d rows)", id, len(visited))
		}
	}
}

// Regression guard for the shared filter parser: adding issue_id must not
// disturb the other dimensions, and the issue-level endpoint must stay pinned
// to its route issue.
func TestListAuditEventsFilterMatrixAndIssueEndpoint(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	base := time.Now().UTC().Add(-3 * time.Hour)
	taskX := uuid.NewString()
	idDomain := seedAuditEvent(t, base.Add(1*time.Second), "", "", testutil.Cols{"domain": "task", "event_type": "task.matrix_domain"})
	idType := seedAuditEvent(t, base.Add(2*time.Second), "", "", testutil.Cols{"event_type": "issue.matrix_event"})
	idActor := seedAuditEvent(t, base.Add(3*time.Second), "", "", testutil.Cols{"actor_type": "agent"})
	idTask := seedAuditEvent(t, base.Add(4*time.Second), "", taskX, nil)
	idWindow := seedAuditEvent(t, base.Add(5*time.Second), "", "", nil)

	assertSingle := func(label, query, wantID string) {
		t.Helper()
		page, code := fetchWorkspaceAuditEvents(t, query)
		if code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", label, code)
		}
		ids := eventIDs(page)
		// Filtered facets are unique to this test's rows; the match set is
		// exact even if fixture setup wrote extra rows elsewhere.
		if len(ids) != 1 || ids[0] != wantID {
			t.Fatalf("%s: expected exactly [%s], got %v", label, wantID, ids)
		}
	}
	assertSingle("domain", "domain=task", idDomain)
	assertSingle("event_type", "event_type=issue.matrix_event", idType)
	assertSingle("actor_type", "actor_type=agent", idActor)
	assertSingle("task_id", "task_id="+taskX, idTask)
	assertSingle(
		"time window",
		"since="+url.QueryEscape(base.Add(4500*time.Millisecond).Format(time.RFC3339Nano))+
			"&until="+url.QueryEscape(base.Add(5500*time.Millisecond).Format(time.RFC3339Nano)),
		idWindow)

	// Issue-level endpoint: pinned to the route issue regardless of any stray
	// issue_id query param — the route owns the dimension.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "audit query surface " + handlerFixtureSuffix,
		"status": "todo",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)
	t.Cleanup(func() {
		ctx := context.Background()
		testPool.Exec(ctx, `DELETE FROM activity_log WHERE issue_id = $1`, issue.ID)
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, issue.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issue.ID)
	})

	issueTrail := uuid.NewString()
	otherTrail := uuid.NewString()
	idIn := seedAuditEvent(t, base.Add(6*time.Second), issue.ID, "", nil)
	seedAuditEvent(t, base.Add(7*time.Second), issueTrail, "", nil)
	idOut := seedAuditEvent(t, base.Add(8*time.Second), otherTrail, "", nil)

	page, code := fetchIssueAuditEvents(t, issue.ID, "")
	if code != http.StatusOK {
		t.Fatalf("issue endpoint: expected 200, got %d", code)
	}
	ids := eventIDs(page)
	if len(ids) != 1 || ids[0] != idIn {
		t.Fatalf("issue endpoint: expected exactly [%s], got %v", idIn, ids)
	}
	page, code = fetchIssueAuditEvents(t, issue.ID, "issue_id="+otherTrail)
	if code != http.StatusOK {
		t.Fatalf("issue endpoint with stray param: expected 200, got %d", code)
	}
	if ids = eventIDs(page); len(ids) != 1 || ids[0] != idIn {
		t.Fatalf("issue endpoint with stray param: route pin must hold, got %v", ids)
	}
	if containsID(ids, idOut) {
		t.Fatalf("issue endpoint leaked row %s from another issue", idOut)
	}
}
