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
	ID           string  `json:"id"`
	Domain       string  `json:"domain"`
	EventType    string  `json:"event_type"`
	OccurredAt   string  `json:"occurred_at"`
	ActorType    string  `json:"actor_type"`
	IssueID      *string `json:"issue_id"`
	TaskID       *string `json:"task_id"`
	InvocationID *string `json:"invocation_id"`
	Sensitive    bool    `json:"sensitive"`
}

func fetchWorkspaceAuditEvents(t *testing.T, query string) (auditEventsPageWire, int) {
	return fetchWorkspaceAuditEventsAs(t, testUserID, query)
}

func fetchWorkspaceAuditEventsAs(t *testing.T, userID, query string) (auditEventsPageWire, int) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/workspaces/"+testWorkspaceID+"/audit-events?"+query, nil)
	req.Header.Set("X-User-ID", userID)
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

func TestParseListAuditEventsInvocationFilters(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/?time_basis=invocation_start&since=2026-10-17T10%3A00%3A00.123456789Z&actor_type=member&client_name=Codex&tool_name=read_file_chunk&result_status=success&target_type=host&target_id=host-7", nil)
	response := httptest.NewRecorder()
	params, ok := parseListAuditEventsParams(response, request)
	if !ok || response.Body.Len() != 0 {
		t.Fatalf("valid invocation filters rejected: ok=%v status=%d body=%s", ok, response.Code, response.Body.String())
	}
	if !params.timeBasis.Valid || params.timeBasis.String != "invocation_start" || !params.since.Valid || !params.filterStart ||
		params.clientName.String != "Codex" || params.toolName.String != "read_file_chunk" || params.resultStatus.String != "success" ||
		params.targetType.String != "host" || params.targetID.String != "host-7" {
		t.Fatalf("valid invocation filters parsed incorrectly: %+v", params)
	}

	legacyResponse := httptest.NewRecorder()
	legacyParams, ok := parseListAuditEventsParams(legacyResponse, httptest.NewRequest(http.MethodGet, "/?since=2026-10-17T10%3A00%3A00Z", nil))
	if !ok || legacyParams.filterStart || legacyParams.timeBasis.Valid {
		t.Fatalf("legacy event-time request changed semantics: ok=%v params=%+v", ok, legacyParams)
	}

	for _, query := range []string{
		"time_basis=event_time",
		"result_status=maybe",
		"target_type=host",
	} {
		response := httptest.NewRecorder()
		_, ok := parseListAuditEventsParams(response, httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		if ok || response.Code != http.StatusBadRequest {
			t.Errorf("invalid query %q: ok=%v status=%d", query, ok, response.Code)
		}
	}
}

func fetchIssueAuditEvents(t *testing.T, issueID, query string) (auditEventsPageWire, int) {
	return fetchIssueAuditEventsAs(t, testUserID, issueID, query)
}

func fetchIssueAuditEventsAs(t *testing.T, userID, issueID, query string) (auditEventsPageWire, int) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/issues/"+issueID+"/audit-events?"+query, nil)
	req.Header.Set("X-User-ID", userID)
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

func TestSensitiveAuditEventsAreOwnerAdminOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	invocationID := uuid.NewString()
	sensitiveID := seedAuditEvent(t, time.Now().UTC(), "", "", testutil.Cols{
		"domain": "mcp", "event_type": "mcp.invocation_finished", "invocation_id": invocationID,
		"sensitive": true, "details": testutil.Raw("'{\"path\":\"/private/report.txt\"}'::jsonb"),
	})
	ordinaryInvocationID := uuid.NewString()
	ordinaryID := seedAuditEvent(t, time.Now().UTC().Add(time.Second), "", "", testutil.Cols{
		"domain": "mcp", "event_type": "mcp.invocation_finished", "invocation_id": ordinaryInvocationID,
		"details": testutil.Raw("'{\"tool\":\"search\"}'::jsonb"),
	})
	memberID := dbfx.User(t, "audit reader "+handlerFixtureSuffix, "audit-reader-"+handlerFixtureSuffix+"@example.test")
	dbfx.Member(t, testWorkspaceID, memberID, "member")
	adminID := dbfx.User(t, "audit admin "+handlerFixtureSuffix, "audit-admin-"+handlerFixtureSuffix+"@example.test")
	dbfx.Member(t, testWorkspaceID, adminID, "admin")

	ownerPage, code := fetchWorkspaceAuditEvents(t, "invocation_id="+invocationID)
	if code != http.StatusOK || len(ownerPage.Events) != 1 || ownerPage.Events[0].ID != sensitiveID || !ownerPage.Events[0].Sensitive {
		t.Fatalf("owner sensitive query: status=%d events=%+v", code, ownerPage.Events)
	}
	adminPage, code := fetchWorkspaceAuditEventsAs(t, adminID, "invocation_id="+invocationID)
	if code != http.StatusOK || len(adminPage.Events) != 1 || adminPage.Events[0].ID != sensitiveID {
		t.Fatalf("admin sensitive query: status=%d events=%+v", code, adminPage.Events)
	}
	memberPage, code := fetchWorkspaceAuditEventsAs(t, memberID, "invocation_id="+invocationID)
	if code != http.StatusOK || len(memberPage.Events) != 0 {
		t.Fatalf("member sensitive query must be empty: status=%d events=%+v", code, memberPage.Events)
	}
	memberPage, code = fetchWorkspaceAuditEventsAs(t, memberID, "")
	if code != http.StatusOK || len(memberPage.Events) == 0 || containsID(eventIDs(memberPage), sensitiveID) || !containsID(eventIDs(memberPage), ordinaryID) {
		t.Fatalf("member list must hide sensitive rows and retain standard rows: status=%d ids=%v", code, eventIDs(memberPage))
	}
	if _, code = fetchWorkspaceAuditEventsAs(t, memberID, "invocation_id=invalid"); code != http.StatusBadRequest {
		t.Fatalf("invalid invocation id: got %d, want 400", code)
	}

	issueID := dbfx.Issue(t, "sensitive audit route "+handlerFixtureSuffix)
	issueInvocationID := uuid.NewString()
	issueSensitiveID := seedAuditEvent(t, time.Now().UTC(), issueID, "", testutil.Cols{
		"domain": "mcp", "event_type": "mcp.invocation_finished", "invocation_id": issueInvocationID,
		"sensitive": true, "details": testutil.Raw("'{\"path\":\"/private/issue.txt\"}'::jsonb"),
	})
	issuePage, code := fetchIssueAuditEventsAs(t, memberID, issueID, "invocation_id="+issueInvocationID)
	if code != http.StatusOK || len(issuePage.Events) != 0 {
		t.Fatalf("member issue audit query must hide sensitive rows: status=%d events=%+v", code, issuePage.Events)
	}
	issuePage, code = fetchIssueAuditEventsAs(t, adminID, issueID, "invocation_id="+issueInvocationID)
	if code != http.StatusOK || len(issuePage.Events) != 1 || issuePage.Events[0].ID != issueSensitiveID {
		t.Fatalf("admin issue audit query: status=%d events=%+v", code, issuePage.Events)
	}
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

func TestListAuditEventsMCPInvocationFilters(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	base := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Second)
	invocationID := uuid.NewString()
	actorID := uuid.NewString()
	startID := seedAuditEvent(t, base, "", "", testutil.Cols{
		"domain": "mcp", "event_type": "mcp.invocation_started", "invocation_id": invocationID,
		"actor_type": "member", "actor_id": actorID,
		"details": testutil.Raw(`'{"reported_client_name":"Codex","reported_client_version":"1.2","tool_name":"read_file_chunk","target_type":"host","target_id":"host-7"}'::jsonb`),
	})
	finishID := seedAuditEvent(t, base.Add(2*time.Hour), "", "", testutil.Cols{
		"domain": "mcp", "event_type": "mcp.invocation_finished", "invocation_id": invocationID,
		"actor_type": "member", "actor_id": actorID,
		"details": testutil.Raw(`'{"status":"success"}'::jsonb`),
	})

	query := "time_basis=invocation_start" +
		"&since=" + url.QueryEscape(base.Format(time.RFC3339Nano)) +
		"&until=" + url.QueryEscape(base.Add(time.Minute).Format(time.RFC3339Nano)) +
		"&actor_type=member&actor_id=" + actorID +
		"&client_name=Codex&client_version=1.2&tool_name=read_file_chunk" +
		"&result_status=success&target_type=host&target_id=host-7"
	page, code := fetchWorkspaceAuditEvents(t, query)
	if code != http.StatusOK || len(page.Events) != 2 || !containsID(eventIDs(page), startID) || !containsID(eventIDs(page), finishID) {
		t.Fatalf("invocation filters must match both event rows using start time across finish boundary: status=%d ids=%v", code, eventIDs(page))
	}

	page, code = fetchWorkspaceAuditEvents(t, query+"&tool_name=other_tool")
	if code != http.StatusOK || len(page.Events) != 0 {
		t.Fatalf("invocation filters must combine with AND: status=%d ids=%v", code, eventIDs(page))
	}

	if _, code = fetchWorkspaceAuditEvents(t, "time_basis=event_time"); code != http.StatusBadRequest {
		t.Fatalf("unsupported time_basis must be rejected: got %d", code)
	}
	if _, code = fetchWorkspaceAuditEvents(t, "result_status=maybe"); code != http.StatusBadRequest {
		t.Fatalf("unknown result_status must be rejected: got %d", code)
	}
	if _, code = fetchWorkspaceAuditEvents(t, "target_type=host"); code != http.StatusBadRequest {
		t.Fatalf("target_type without target_id must be rejected: got %d", code)
	}
}

func TestListAuditEventsMCPInvocationFiltersRespectVisibilityAndWorkspace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	memberID := dbfx.User(t, "audit filter member "+handlerFixtureSuffix, "audit-filter-member-"+handlerFixtureSuffix+"@example.test")
	dbfx.Member(t, testWorkspaceID, memberID, "member")
	base := time.Now().UTC().Add(-5 * time.Hour).Truncate(time.Second)
	invocationID := uuid.NewString()
	startID := seedAuditEvent(t, base, "", "", testutil.Cols{
		"domain": "mcp", "event_type": "mcp.invocation_started", "invocation_id": invocationID,
		"sensitive": true,
		"details":   testutil.Raw(`'{"reported_client_name":"Hidden","tool_name":"read_file_chunk","target_type":"host","target_id":"secret-host"}'::jsonb`),
	})
	finishID := seedAuditEvent(t, base.Add(time.Second), "", "", testutil.Cols{
		"domain": "mcp", "event_type": "mcp.invocation_finished", "invocation_id": invocationID,
		"sensitive": true, "details": testutil.Raw(`'{"status":"success"}'::jsonb`),
	})
	query := "client_name=Hidden&tool_name=read_file_chunk&result_status=success&target_type=host&target_id=secret-host"
	page, code := fetchWorkspaceAuditEventsAs(t, memberID, query)
	if code != http.StatusOK || len(page.Events) != 0 {
		t.Fatalf("member must not infer hidden invocation from filters: status=%d ids=%v", code, eventIDs(page))
	}
	page, code = fetchWorkspaceAuditEvents(t, query)
	if code != http.StatusOK || len(page.Events) != 2 || !containsID(eventIDs(page), startID) || !containsID(eventIDs(page), finishID) {
		t.Fatalf("owner should see matching sensitive invocation rows: status=%d ids=%v", code, eventIDs(page))
	}

	foreignInvocation := uuid.NewString()
	foreignWorkspaceID := uuid.NewString()
	foreignStartID := uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO audit_event (id, workspace_id, domain, event_type, occurred_at, actor_type, actor_id, details, invocation_id)
		VALUES ($1, $2, 'mcp', 'mcp.invocation_started', $3, 'member', $4, '{"tool_name":"read_file_chunk","target_type":"host","target_id":"host-7"}', $5)
	`, foreignStartID, foreignWorkspaceID, base, uuid.NewString(), foreignInvocation); err != nil {
		t.Fatalf("seed foreign workspace invocation start: %v", err)
	}
	foreignEventID := seedAuditEvent(t, base.Add(2*time.Second), "", "", testutil.Cols{
		"domain": "mcp", "event_type": "mcp.invocation_finished", "invocation_id": foreignInvocation,
		"details": testutil.Raw(`'{"status":"success"}'::jsonb`),
	})
	page, code = fetchWorkspaceAuditEvents(t, "tool_name=read_file_chunk&result_status=success&target_type=host&target_id=host-7")
	if code != http.StatusOK || containsID(eventIDs(page), foreignEventID) {
		t.Fatalf("invocation correlation must not cross workspace: status=%d ids=%v", code, eventIDs(page))
	}
}
