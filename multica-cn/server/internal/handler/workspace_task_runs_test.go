package handler

import (
	"net/http"
	"sort"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GET /api/task-runs is the workspace-wide run view behind the MCP list_runs
// tool (RUYI-419): one filterable, paged read across every issue and agent.
// These tests pin the parts a caller builds automation on: the filter
// semantics inherited from the issue-scoped path (status CSV with the
// 'pending' alias, trigger buckets, unknown values match nothing), the
// issue-reference resolution (UUID or PREFIX-N, unresolvable = empty page,
// never 404), exact has_more paging, the RFC3339 time window, and workspace
// isolation.

// workspaceRunRow mirrors the handler's row shape. Decoded as its own type so
// a future field change in AgentTaskResponse that this view does not intend
// still shows up here as a decode difference.
type workspaceRunRow struct {
	AgentTaskResponse
	IssueTitle      string `json:"issue_title"`
	IssueIdentifier string `json:"issue_identifier"`
	Trigger         string `json:"trigger"`
}

type workspaceRunsPage struct {
	Runs       []workspaceRunRow `json:"runs"`
	Count      int               `json:"count"`
	HasMore    bool              `json:"has_more"`
	NextOffset *int              `json:"next_offset"`
}

func workspaceRunsCall(t *testing.T, query string) workspaceRunsPage {
	t.Helper()
	path := "/api/task-runs"
	if query != "" {
		path += "?" + query
	}
	var page workspaceRunsPage
	testutil.Call(t, testHandler.ListWorkspaceTaskRuns, newRequest(http.MethodGet, path, nil)).
		Want(http.StatusOK).JSON(&page)
	return page
}

// runsForAgent scopes a call to one fresh agent, so assertions are exact even
// if a previous test in the package left rows behind.
func runsForAgent(t *testing.T, agentID, query string) workspaceRunsPage {
	t.Helper()
	q := "agent_id=" + agentID
	if query != "" {
		q += "&" + query
	}
	return workspaceRunsCall(t, q)
}

func runIDs(page workspaceRunsPage) []string {
	ids := make([]string, 0, len(page.Runs))
	for _, r := range page.Runs {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	return ids
}

// wsrFixture wires one fresh agent and two issues; tests add their own tasks.
type wsrFixture struct {
	agentID string
	runtime string
	issueA  string
	issueB  string
}

func newWSRFixture(t *testing.T) wsrFixture {
	t.Helper()
	return wsrFixture{
		agentID: createHandlerTestAgent(t, "wsr-runs-agent", []byte("{}")),
		runtime: handlerTestRuntimeID(t),
		issueA:  dbfx.Issue(t, "wsr-runs-alpha"),
		issueB:  dbfx.Issue(t, "wsr-runs-beta"),
	}
}

func (f wsrFixture) task(t *testing.T, issueID, status string, over testutil.Cols) string {
	t.Helper()
	cols := testutil.Cols{
		"runtime_id": f.runtime,
		"issue_id":   issueID,
		"status":     status,
	}
	for k, v := range over {
		cols[k] = v
	}
	return dbfx.Task(t, f.agentID, cols)
}

// Defaults: the unfiltered page returns the workspace's runs newest-first
// across issues, and hydrates the cross-issue fields the issue-scoped read
// gets from its path parameter: issue_title, issue_identifier (workspace
// prefix), and the derived trigger source.
func TestListWorkspaceTaskRunsDefaultPage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	setWorkspaceIssuePrefixForTest(t, "WSR")

	f := newWSRFixture(t)
	done := f.task(t, f.issueA, "completed", nil)
	commented := f.task(t, f.issueB, "running", testutil.Cols{
		"trigger_comment_id": dbfx.Comment(t, f.issueB, "go ahead"),
	})

	page := runsForAgent(t, f.agentID, "")
	if page.HasMore || page.NextOffset != nil {
		t.Fatalf("single page expected has_more=false, got %+v", page)
	}
	if !sameIDs(runIDs(page), sortedCopy(commented, done)) {
		t.Fatalf("unexpected run set: %v", runIDs(page))
	}
	// Newest first: the running task was inserted last.
	if page.Runs[0].ID != commented || page.Runs[0].Trigger != "comment" {
		t.Fatalf("row 0 = %+v, want the comment-triggered running task first", page.Runs[0])
	}
	if page.Runs[0].IssueTitle != "wsr-runs-beta" {
		t.Fatalf("issue_title = %q", page.Runs[0].IssueTitle)
	}
	var number int32
	dbfx.QueryRow(t, `SELECT number FROM issue WHERE id = $1`, f.issueB).Scan(&number)
	if want := "WSR-" + strconv.Itoa(int(number)); page.Runs[0].IssueIdentifier != want {
		t.Fatalf("issue_identifier = %q, want %q", page.Runs[0].IssueIdentifier, want)
	}
	if page.Runs[1].Trigger != "other" {
		t.Fatalf("plain task trigger = %q, want other", page.Runs[1].Trigger)
	}
}

// Status filter semantics inherited from RUYI-292: a CSV of raw statuses,
// the 'pending' alias expanding to the four in-flight statuses, unknown
// values matching nothing (200, empty page) rather than erroring.
func TestListWorkspaceTaskRunsStatusFilter(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newWSRFixture(t)
	queued := f.task(t, f.issueA, "queued", nil)
	f.task(t, f.issueA, "running", nil)
	deferred := f.task(t, f.issueB, "deferred", nil)
	failed := f.task(t, f.issueB, "failed", nil)

	got := runsForAgent(t, f.agentID, "status=queued,failed")
	if !sameIDs(runIDs(got), sortedCopy(queued, failed)) {
		t.Fatalf("queued,failed → %v", runIDs(got))
	}

	got = runsForAgent(t, f.agentID, "status=pending")
	// The alias covers the not-yet-running in-flight statuses (RUYI-292);
	// running is deliberately its own status, not part of "pending".
	if !sameIDs(runIDs(got), sortedCopy(queued, deferred)) {
		t.Fatalf("pending alias → %v", runIDs(got))
	}

	got = runsForAgent(t, f.agentID, "status=nonsense")
	if len(got.Runs) != 0 || got.Count != 0 {
		t.Fatalf("unknown status must match nothing, got %v", runIDs(got))
	}
}

// Issue filter accepts a UUID or PREFIX-N (case-insensitive); an
// unresolvable reference is an empty page under 200 — a filter, not a
// resource path.
func TestListWorkspaceTaskRunsIssueFilter(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	setWorkspaceIssuePrefixForTest(t, "WSR")

	f := newWSRFixture(t)
	alpha := f.task(t, f.issueA, "completed", nil)
	beta := f.task(t, f.issueB, "completed", nil)

	got := runsForAgent(t, f.agentID, "issue="+f.issueA)
	if !sameIDs(runIDs(got), sortedCopy(alpha)) {
		t.Fatalf("uuid filter → %v", runIDs(got))
	}

	var number int32
	dbfx.QueryRow(t, `SELECT number FROM issue WHERE id = $1`, f.issueB).Scan(&number)
	got = runsForAgent(t, f.agentID, "issue=wsr-"+strconv.Itoa(int(number)))
	if !sameIDs(runIDs(got), sortedCopy(beta)) {
		t.Fatalf("lowercase identifier must resolve case-insensitively to the beta issue; got %v", runIDs(got))
	}

	got = runsForAgent(t, f.agentID, "issue=wsr-999999")
	if len(got.Runs) != 0 || got.Count != 0 {
		t.Fatalf("unresolvable identifier must be an empty page, got %v", runIDs(got))
	}
}

// Agent and project filters scope the view; a task whose issue belongs to
// another project is invisible through the project filter.
func TestListWorkspaceTaskRunsAgentAndProjectFilters(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newWSRFixture(t)
	projectID := dbfx.Project(t, "wsr-runs-project")
	inProject := dbfx.Issue(t, "wsr-runs-in-project", testutil.Cols{"project_id": projectID})
	f.task(t, inProject, "queued", nil)
	f.task(t, f.issueA, "queued", nil)

	got := runsForAgent(t, f.agentID, "project_id="+projectID)
	if got.Count != 1 {
		t.Fatalf("project filter → %d rows, want the single in-project run", got.Count)
	}
	if got.Runs[0].IssueTitle != "wsr-runs-in-project" {
		t.Fatalf("project filter row issue_title = %q", got.Runs[0].IssueTitle)
	}

	otherAgent := createHandlerTestAgent(t, "wsr-runs-agent-b", []byte("{}"))
	otherPage := runsForAgent(t, otherAgent, "project_id="+projectID)
	if otherPage.Count != 0 {
		t.Fatalf("other agent has no runs in that project, got %d", otherPage.Count)
	}
}

// Trigger filter buckets by evidence column with the raw
// trigger_evidence_kind fallback — the same contract the MCP tool documents.
func TestListWorkspaceTaskRunsTriggerFilter(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newWSRFixture(t)
	commented := f.task(t, f.issueA, "completed", testutil.Cols{
		"trigger_comment_id": dbfx.Comment(t, f.issueA, "run it"),
	})
	rule := f.task(t, f.issueB, "completed", testutil.Cols{
		"trigger_evidence_kind": "rule",
	})

	got := runsForAgent(t, f.agentID, "trigger=comment")
	if !sameIDs(runIDs(got), sortedCopy(commented)) {
		t.Fatalf("trigger=comment → %v", runIDs(got))
	}

	// Raw evidence-kind values (no dedicated bucket) fall back to equality.
	got = runsForAgent(t, f.agentID, "trigger=rule")
	if !sameIDs(runIDs(got), sortedCopy(rule)) {
		t.Fatalf("trigger=rule → %v", runIDs(got))
	}

	got = runsForAgent(t, f.agentID, "trigger=nonsense")
	if got.Count != 0 {
		t.Fatalf("unknown trigger must match nothing, got %v", runIDs(got))
	}
}

// Time window: RFC3339 bounds, half-open [created_after, created_before);
// a malformed timestamp is a 400, not a silent ignore.
func TestListWorkspaceTaskRunsTimeWindow(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newWSRFixture(t)
	early := f.task(t, f.issueA, "completed", testutil.Cols{
		"created_at": testutil.Raw("timestamptz '2026-05-14 10:00:00+00'"),
	})
	late := f.task(t, f.issueA, "completed", testutil.Cols{
		"created_at": testutil.Raw("timestamptz '2026-05-14 12:00:00+00'"),
	})

	got := runsForAgent(t, f.agentID, "created_after=2026-05-14T09:00:00Z&created_before=2026-05-14T11:00:00Z")
	if !sameIDs(runIDs(got), sortedCopy(early)) {
		t.Fatalf("early window → %v", runIDs(got))
	}

	got = runsForAgent(t, f.agentID, "created_after=2026-05-14T11:00:00Z")
	if !sameIDs(runIDs(got), sortedCopy(late)) {
		t.Fatalf("late window → %v", runIDs(got))
	}

	resp := testutil.Call(t, testHandler.ListWorkspaceTaskRuns,
		newRequest(http.MethodGet, "/api/task-runs?agent_id="+f.agentID+"&created_after=not-a-time", nil))
	resp.Want(http.StatusBadRequest)
}

// Paging: limit=2 over three rows yields has_more with next_offset pointing
// at the third row; out-of-range limit is a 400.
func TestListWorkspaceTaskRunsPagination(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newWSRFixture(t)
	first := f.task(t, f.issueA, "completed", testutil.Cols{
		"created_at": testutil.Raw("timestamptz '2026-05-14 10:00:00+00'"),
	})
	second := f.task(t, f.issueA, "completed", testutil.Cols{
		"created_at": testutil.Raw("timestamptz '2026-05-14 11:00:00+00'"),
	})
	third := f.task(t, f.issueA, "completed", testutil.Cols{
		"created_at": testutil.Raw("timestamptz '2026-05-14 12:00:00+00'"),
	})

	page := runsForAgent(t, f.agentID, "limit=2")
	if !page.HasMore || page.NextOffset == nil || *page.NextOffset != 2 {
		t.Fatalf("first page = has_more %v next_offset %v, want true/2", page.HasMore, page.NextOffset)
	}
	if !sameIDs(runIDs(page), sortedCopy(second, third)) {
		t.Fatalf("first page rows → %v, want the two newest", runIDs(page))
	}
	if page.Count != 2 {
		t.Fatalf("count = %d, want 2", page.Count)
	}

	page = runsForAgent(t, f.agentID, "limit=2&offset=2")
	if page.HasMore || page.NextOffset != nil {
		t.Fatalf("last page = has_more %v next_offset %v, want false/nil", page.HasMore, page.NextOffset)
	}
	if !sameIDs(runIDs(page), sortedCopy(first)) {
		t.Fatalf("last page rows → %v, want %s", runIDs(page), first)
	}

	testutil.Call(t, testHandler.ListWorkspaceTaskRuns,
		newRequest(http.MethodGet, "/api/task-runs?agent_id="+f.agentID+"&limit=201", nil)).Want(http.StatusBadRequest)
	testutil.Call(t, testHandler.ListWorkspaceTaskRuns,
		newRequest(http.MethodGet, "/api/task-runs?agent_id="+f.agentID+"&offset=-1", nil)).Want(http.StatusBadRequest)
}

// Isolation: the read joins on the caller's workspace, so a run whose issue
// belongs to a different workspace never crosses the boundary.
func TestListWorkspaceTaskRunsWorkspaceIsolation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newWSRFixture(t)
	mine := f.task(t, f.issueA, "queued", nil)

	otherWS := dbfx.Workspace(t, "wsr-runs-other", "wsr-runs-other")
	otherIssue := dbfx.Issue(t, "wsr-runs-foreign", testutil.Cols{"workspace_id": otherWS})
	f.task(t, otherIssue, "queued", nil)

	got := runsForAgent(t, f.agentID, "")
	if !sameIDs(runIDs(got), sortedCopy(mine)) {
		t.Fatalf("cross-workspace run leaked: %v", runIDs(got))
	}
}

// The trigger derivation is pure precedence — pin it without any database.
func TestDeriveRunTriggerPrecedence(t *testing.T) {
	id := func(valid bool) pgtype.UUID {
		u, _ := uuid.NewRandom()
		return pgtype.UUID{Bytes: u, Valid: valid}
	}
	cases := []struct {
		name string
		task db.AgentTaskQueue
		want string
	}{
		{"autopilot beats everything", db.AgentTaskQueue{
			AutopilotRunID: id(true), RetryOfTaskID: id(true), RerunOfTaskID: id(true), TriggerCommentID: id(true),
		}, "autopilot"},
		{"system retry beats rerun and comment", db.AgentTaskQueue{
			RetryOfTaskID: id(true), RerunOfTaskID: id(true), TriggerCommentID: id(true),
		}, "system_retry"},
		{"rerun beats comment", db.AgentTaskQueue{RerunOfTaskID: id(true), TriggerCommentID: id(true)}, "rerun"},
		{"comment alone", db.AgentTaskQueue{TriggerCommentID: id(true)}, "comment"},
		{"no evidence", db.AgentTaskQueue{}, "other"},
	}
	for _, tc := range cases {
		if got := deriveRunTrigger(tc.task); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
