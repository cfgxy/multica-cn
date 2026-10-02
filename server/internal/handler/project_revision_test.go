package handler

// RUYI-354: project revision (optimistic lock) contract.
//
// The MCP project tools read a project, then write back with the revision
// they saw. These tests pin what the tools rely on: every response carries
// revision, a successful update increments it, a stale expected_revision
// answers 409 revision_conflict without mutating the row — plus the negative
// guarantee that metadata writes never spawn agent runs.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// createRevisionProject seeds a project owned by the fixture owner and
// registers its cleanup; every test in this file starts from it.
func createRevisionProject(t *testing.T, title string) ProjectResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": title,
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed CreateProject: %d %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatalf("decode CreateProject: %v", err)
	}
	t.Cleanup(func() {
		req := newRequest("DELETE", "/api/projects/"+project.ID, nil)
		req = withURLParam(req, "id", project.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	})
	return project
}

func getProjectForTest(t *testing.T, projectID string) ProjectResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/projects/"+projectID, nil)
	req = withURLParam(req, "id", projectID)
	testHandler.GetProject(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetProject: %d %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatalf("decode GetProject: %v", err)
	}
	return project
}

// create → get → update keeps the same project id and increments revision.
func TestProjectRevisionLifecycle(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler suite fixture unavailable")
	}
	created := createRevisionProject(t, "revision lifecycle")
	if created.Revision != 1 {
		t.Fatalf("expected created revision 1, got %d", created.Revision)
	}

	got := getProjectForTest(t, created.ID)
	if got.ID != created.ID {
		t.Fatalf("get returned a different project id: %s != %s", got.ID, created.ID)
	}
	if got.Revision != 1 {
		t.Fatalf("expected read revision 1, got %d", got.Revision)
	}

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"title":             "revision lifecycle v2",
		"expected_revision": got.Revision,
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateProject(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateProject with current revision: %d %s", w.Code, w.Body.String())
	}
	var updated ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode UpdateProject: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("update returned a different project id: %s != %s", updated.ID, created.ID)
	}
	if updated.Title != "revision lifecycle v2" {
		t.Errorf("expected updated title, got %q", updated.Title)
	}
	if updated.Revision != got.Revision+1 {
		t.Errorf("expected revision %d after update, got %d", got.Revision+1, updated.Revision)
	}
}

// A stale expected_revision answers 409 revision_conflict with the expected
// and actual revisions, and leaves the row untouched.
func TestUpdateProjectStaleRevisionConflict(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler suite fixture unavailable")
	}
	created := createRevisionProject(t, "revision conflict")
	stale := created.Revision + 5

	// Advance the real revision so the held value is genuinely stale.
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/projects/"+created.ID, map[string]any{"title": "conflict v2"})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateProject(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("advance update: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"title":             "conflict must not land",
		"expected_revision": stale,
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateProject(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for stale revision, got %d: %s", w.Code, w.Body.String())
	}
	var conflict struct {
		Error            string `json:"error"`
		Code             string `json:"code"`
		ResourceType     string `json:"resource_type"`
		ResourceID       string `json:"resource_id"`
		ExpectedRevision int64  `json:"expected_revision"`
		ActualRevision   int64  `json:"actual_revision"`
	}
	if err := json.NewDecoder(w.Body).Decode(&conflict); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if conflict.Code != "revision_conflict" {
		t.Errorf("expected code revision_conflict, got %q", conflict.Code)
	}
	if conflict.ResourceType != "project" || conflict.ResourceID != created.ID {
		t.Errorf("unexpected resource identity: %s %s", conflict.ResourceType, conflict.ResourceID)
	}
	if conflict.ExpectedRevision != stale {
		t.Errorf("expected_revision %d, got %d", stale, conflict.ExpectedRevision)
	}
	if conflict.ActualRevision != created.Revision+1 {
		t.Errorf("actual_revision %d, got %d", created.Revision+1, conflict.ActualRevision)
	}

	current := getProjectForTest(t, created.ID)
	if current.Title != "conflict v2" || current.Revision != created.Revision+1 {
		t.Errorf("conflicting write mutated the row: title %q revision %d", current.Title, current.Revision)
	}
}

// A matching expected_revision still lands (the lock does not over-reject),
// and a non-positive one is a clean 400 like the issue contract.
func TestUpdateProjectExpectedRevisionValidation(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler suite fixture unavailable")
	}
	created := createRevisionProject(t, "revision validation")

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"title":             "validation v2",
		"expected_revision": 0,
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateProject(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for expected_revision 0, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"title":             "validation v3",
		"expected_revision": created.Revision,
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateProject(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for matching revision, got %d: %s", w.Code, w.Body.String())
	}
	var updated ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode UpdateProject: %v", err)
	}
	if updated.Revision != created.Revision+1 {
		t.Errorf("expected revision %d, got %d", created.Revision+1, updated.Revision)
	}
}

// Negative assertion: a project metadata update must not create any agent
// task — metadata writes publish a WS event and nothing else.
func TestProjectMetadataUpdateSpawnsNoAgentTask(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler suite fixture unavailable")
	}
	created := createRevisionProject(t, "metadata update spawns no runs")
	taskCount := func() int {
		return dbfx.Count(t,
			`SELECT count(*) FROM agent_task_queue q JOIN issue i ON i.id = q.issue_id WHERE i.workspace_id = $1`,
			testWorkspaceID)
	}

	before := taskCount()
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"title":       "no-runs v2",
		"description": "metadata only",
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateProject(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateProject: %d %s", w.Code, w.Body.String())
	}
	if after := taskCount(); after != before {
		t.Fatalf("project metadata update changed agent task count: before %d after %d", before, after)
	}
}

// Permission semantics of the mounted route chain: an authenticated user who
// is not a workspace member gets the structured 404 the middleware writes,
// and a missing identity gets 401 — both JSON `{"error": ...}` bodies the MCP
// client surfaces as structured errors.
func TestProjectWriteByNonMemberStructuredError(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler suite fixture unavailable")
	}
	created := createRevisionProject(t, "permission gate")

	// The same middleware the router mounts in front of /api/projects.
	gated := middleware.RequireWorkspaceMember(db.New(testPool))(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			testHandler.UpdateProject(w, r)
		}))

	otherUserID := dbfx.User(t, "proj-nonmember", "proj-nonmember-"+handlerFixtureSuffix+"@multica.ai")

	serve := func(userID string) *httptest.ResponseRecorder {
		req := newRequestAs(userID, "PUT", "/api/projects/"+created.ID, map[string]any{"title": "x"})
		req = withURLParam(req, "id", created.ID)
		w := httptest.NewRecorder()
		gated.ServeHTTP(w, req)
		return w
	}

	w := serve(otherUserID)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-member, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode middleware error body: %v", err)
	}
	if body["error"] != "workspace not found" {
		t.Errorf("expected structured workspace-not-found error, got %v", body)
	}

	// The middleware rejects before the handler runs, so no route context is
	// needed; an unauthenticated call must still answer the structured 401.
	req := newRequestAs("", "PUT", "/api/projects/"+created.ID, map[string]any{"title": "x"})
	req = withURLParam(req, "id", created.ID)
	w = httptest.NewRecorder()
	gated.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing identity, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode 401 body: %v", err)
	}
	if body["error"] != "user not authenticated" {
		t.Errorf("expected structured unauthenticated error, got %v", body)
	}
}
