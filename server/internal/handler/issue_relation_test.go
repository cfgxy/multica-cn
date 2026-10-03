package handler

// RUYI-351 structured issue relation endpoint tests. The suite fixture
// (TestMain) provides testPool / testHandler / dbfx against a real database;
// fixtures ride the shared issue_dependency edge table so every assertion
// below exercises the same rows the API reads.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

type relationRef struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Status     string `json:"status"`
}

type relationsResponse struct {
	IssueID      string        `json:"issue_id"`
	Identifier   string        `json:"identifier"`
	Revision     int64         `json:"revision"`
	Parent       *relationRef  `json:"parent"`
	Blocks       []relationRef `json:"blocks"`
	BlockedBy    []relationRef `json:"blocked_by"`
	RelatesTo    []relationRef `json:"relates_to"`
	Supersedes   []relationRef `json:"supersedes"`
	SupersededBy []relationRef `json:"superseded_by"`
}

// relationIssue creates a plain issue through the public create endpoint and
// cleans it up with the test (issue deletion cascades its relation edges).
func relationIssue(t *testing.T, title string) IssueResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": title + " " + handlerFixtureSuffix,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create relation issue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() {
		w := httptest.NewRecorder()
		req := newRequest("DELETE", "/api/issues/"+issue.ID, nil)
		req = withURLParam(req, "id", issue.ID)
		testHandler.DeleteIssue(w, req)
	})
	return issue
}

func addRelation(t *testing.T, anchorID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+anchorID+"/relations", body)
	req = withURLParam(req, "id", anchorID)
	testHandler.AddIssueRelation(w, req)
	return w
}

func removeRelation(t *testing.T, anchorID, relType, targetID string, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	path := "/api/issues/" + anchorID + "/relations/" + relType + "/" + targetID
	if query != "" {
		path += "?" + query
	}
	req := newRequest("DELETE", path, nil)
	req = withURLParams(req, "id", anchorID, "relType", relType, "targetId", targetID)
	testHandler.RemoveIssueRelation(w, req)
	return w
}

func getRelations(t *testing.T, issueID string) relationsResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/issues/"+issueID+"/relations", nil)
	req = withURLParam(req, "id", issueID)
	testHandler.GetIssueRelations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get relations: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp relationsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode relations: %v", err)
	}
	return resp
}

func updateIssueRaw(t *testing.T, issueID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+issueID, body)
	req = withURLParam(req, "id", issueID)
	testHandler.UpdateIssue(w, req)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	return body
}

func wantCode(t *testing.T, stage string, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("%s: expected %d, got %d: %s", stage, status, w.Code, w.Body.String())
	}
}

func relationEdgeCount(t *testing.T, issueA, issueB string) int {
	t.Helper()
	return dbfx.Count(t, `SELECT count(*) FROM issue_dependency
		WHERE (issue_id = $1 AND depends_on_issue_id = $2) OR (issue_id = $2 AND depends_on_issue_id = $1)`,
		issueA, issueB)
}

func refsContain(refs []relationRef, id string) bool {
	for _, ref := range refs {
		if ref.ID == id {
			return true
		}
	}
	return false
}

// TestIssueRelationsLifecycleAllTypes walks all five relation kinds through
// establish → query → modify → dissolve, plus the parent set / remount /
// clear path. It is the implementer self-test the RUYI-351 acceptance asks
// to close the loop.
func TestIssueRelationsLifecycleAllTypes(t *testing.T) {
	a := relationIssue(t, "relation lifecycle anchor")
	b := relationIssue(t, "relation lifecycle target")

	// parent: establish → query → remount → clear.
	if w := updateIssueRaw(t, a.ID, map[string]any{"parent_issue_id": b.ID}); w.Code != http.StatusOK {
		t.Fatalf("set parent: got %d: %s", w.Code, w.Body.String())
	}
	if got := getRelations(t, a.ID); got.Parent == nil || got.Parent.ID != b.ID {
		t.Fatalf("expected parent %s, got %+v", b.ID, got.Parent)
	}
	c := relationIssue(t, "relation lifecycle remount")
	if w := updateIssueRaw(t, a.ID, map[string]any{"parent_issue_id": c.ID}); w.Code != http.StatusOK {
		t.Fatalf("remount parent: got %d: %s", w.Code, w.Body.String())
	}
	if got := getRelations(t, a.ID); got.Parent == nil || got.Parent.ID != c.ID {
		t.Fatalf("expected remounted parent %s, got %+v", c.ID, got.Parent)
	}
	if w := updateIssueRaw(t, a.ID, map[string]any{"parent_issue_id": nil}); w.Code != http.StatusOK {
		t.Fatalf("clear parent: got %d: %s", w.Code, w.Body.String())
	}
	if got := getRelations(t, a.ID); got.Parent != nil {
		t.Fatalf("expected cleared parent, got %+v", got.Parent)
	}

	// blocks: forward edge visible from both sides.
	wantCode(t, "add blocks", addRelation(t, a.ID, map[string]any{"type": "blocks", "target_issue_id": b.ID}), http.StatusCreated)
	if got := getRelations(t, a.ID); !refsContain(got.Blocks, b.ID) {
		t.Fatalf("expected blocks to contain %s, got %+v", b.ID, got)
	}
	if got := getRelations(t, b.ID); !refsContain(got.BlockedBy, a.ID) {
		t.Fatalf("expected blocked_by to contain %s, got %+v", a.ID, got)
	}
	// Remove from the target's inverse frame — the same canonical edge.
	wantCode(t, "remove blocked_by from target", removeRelation(t, b.ID, "blocked_by", a.ID, ""), http.StatusOK)
	if got := getRelations(t, a.ID); len(got.Blocks) != 0 {
		t.Fatalf("expected blocks emptied, got %+v", got)
	}
	wantCode(t, "remove missing edge", removeRelation(t, a.ID, "blocks", b.ID, ""), http.StatusNotFound)

	// blocked_by (inverse frame at write time): A blocked_by B stores B blocks A.
	wantCode(t, "add blocked_by", addRelation(t, a.ID, map[string]any{"type": "blocked_by", "target_issue_id": b.ID}), http.StatusCreated)
	if got := getRelations(t, a.ID); !refsContain(got.BlockedBy, b.ID) {
		t.Fatalf("expected blocked_by to contain %s, got %+v", b.ID, got)
	}
	if got := getRelations(t, b.ID); !refsContain(got.Blocks, a.ID) {
		t.Fatalf("expected blocks to contain %s, got %+v", a.ID, got)
	}
	wantCode(t, "remove blocked_by", removeRelation(t, a.ID, "blocked_by", b.ID, ""), http.StatusOK)
	if relationEdgeCount(t, a.ID, b.ID) != 0 {
		t.Fatal("expected zero edges after blocked_by removal")
	}

	// relates_to.
	wantCode(t, "add relates_to", addRelation(t, a.ID, map[string]any{"type": "relates_to", "target_issue_id": b.ID}), http.StatusCreated)
	if got := getRelations(t, b.ID); !refsContain(got.RelatesTo, a.ID) {
		t.Fatalf("expected relates_to to contain %s, got %+v", a.ID, got)
	}
	wantCode(t, "remove relates_to", removeRelation(t, b.ID, "relates_to", a.ID, ""), http.StatusOK)

	// supersedes + superseded_by.
	wantCode(t, "add supersedes", addRelation(t, a.ID, map[string]any{"type": "supersedes", "target_issue_id": b.ID}), http.StatusCreated)
	if got := getRelations(t, b.ID); !refsContain(got.SupersededBy, a.ID) {
		t.Fatalf("expected superseded_by to contain %s, got %+v", a.ID, got)
	}
	wantCode(t, "remove supersedes", removeRelation(t, a.ID, "supersedes", b.ID, ""), http.StatusOK)

	wantCode(t, "add superseded_by", addRelation(t, a.ID, map[string]any{"type": "superseded_by", "target_issue_id": b.ID}), http.StatusCreated)
	if got := getRelations(t, b.ID); !refsContain(got.Supersedes, a.ID) {
		t.Fatalf("expected supersedes to contain %s, got %+v", a.ID, got)
	}
	// The edge lives on A's superseded_by frame; B's inverse frame is supersedes.
	wantCode(t, "remove superseded_by", removeRelation(t, a.ID, "superseded_by", b.ID, ""), http.StatusOK)
	if relationEdgeCount(t, a.ID, b.ID) != 0 {
		t.Fatal("expected zero edges after full lifecycle")
	}
}

// TestIssueRelationsBidirectionalConsistency: a relation named from either
// side is visible from both sides, in the frame each side speaks.
func TestIssueRelationsBidirectionalConsistency(t *testing.T) {
	a := relationIssue(t, "bidirectional A")
	b := relationIssue(t, "bidirectional B")
	c := relationIssue(t, "bidirectional C")

	// relates_to named from B's side shows on A too.
	wantCode(t, "add relates_to from B", addRelation(t, b.ID, map[string]any{"type": "relates_to", "target_issue_id": a.ID}), http.StatusCreated)
	if got := getRelations(t, a.ID); !refsContain(got.RelatesTo, b.ID) {
		t.Fatalf("A expected relates_to with B, got %+v", got)
	}
	if got := getRelations(t, b.ID); !refsContain(got.RelatesTo, a.ID) {
		t.Fatalf("B expected relates_to with A, got %+v", got)
	}

	// blocks established toward C shows as blocked_by on C.
	wantCode(t, "add blocks toward C", addRelation(t, a.ID, map[string]any{"type": "blocks", "target_issue_id": c.ID}), http.StatusCreated)
	if got := getRelations(t, c.ID); !refsContain(got.BlockedBy, a.ID) {
		t.Fatalf("C expected blocked_by with A, got %+v", got)
	}

	// supersedes established from B's inverse frame (B superseded_by A) is
	// visible as supersedes on A.
	wantCode(t, "add superseded_by", addRelation(t, b.ID, map[string]any{"type": "superseded_by", "target_issue_id": a.ID}), http.StatusCreated)
	if got := getRelations(t, a.ID); !refsContain(got.Supersedes, b.ID) {
		t.Fatalf("A expected supersedes with B, got %+v", got)
	}
	if got := getRelations(t, b.ID); !refsContain(got.SupersededBy, a.ID) {
		t.Fatalf("B expected superseded_by with A, got %+v", got)
	}
}

// TestIssueRelationDuplicateRejected: an existing edge (in either direction
// for the symmetric relates_to) answers 409 relation_exists and stores
// nothing extra.
func TestIssueRelationDuplicateRejected(t *testing.T) {
	a := relationIssue(t, "duplicate A")
	b := relationIssue(t, "duplicate B")

	wantCode(t, "first add", addRelation(t, a.ID, map[string]any{"type": "relates_to", "target_issue_id": b.ID}), http.StatusCreated)
	w := addRelation(t, a.ID, map[string]any{"type": "relates_to", "target_issue_id": b.ID})
	wantCode(t, "duplicate add", w, http.StatusConflict)
	if body := decodeBody(t, w); body["code"] != "relation_exists" {
		t.Fatalf("expected code relation_exists, got %v", body["code"])
	}
	// Same unordered pair named from B must also collide.
	w = addRelation(t, b.ID, map[string]any{"type": "relates_to", "target_issue_id": a.ID})
	wantCode(t, "reverse add", w, http.StatusConflict)
	if relationEdgeCount(t, a.ID, b.ID) != 1 {
		t.Fatal("expected exactly one stored edge")
	}

	wantCode(t, "directional duplicate", addRelation(t, a.ID, map[string]any{"type": "blocks", "target_issue_id": b.ID}), http.StatusCreated)
	w = addRelation(t, a.ID, map[string]any{"type": "blocks", "target_issue_id": b.ID})
	wantCode(t, "duplicate blocks", w, http.StatusConflict)
}

// TestIssueRelationInputValidation: self relations and unknown types answer
// structured 400s; a target outside the workspace is rejected rather than
// linked across the boundary.
func TestIssueRelationInputValidation(t *testing.T) {
	a := relationIssue(t, "validation anchor")
	b := relationIssue(t, "validation target")

	w := addRelation(t, a.ID, map[string]any{"type": "blocks", "target_issue_id": a.ID})
	wantCode(t, "self relation", w, http.StatusBadRequest)

	w = addRelation(t, a.ID, map[string]any{"type": "enemies_with", "target_issue_id": b.ID})
	wantCode(t, "unknown type", w, http.StatusBadRequest)

	// Cross-workspace target: the anchor resolves in testWorkspaceID, the
	// target lives in a second workspace and must be invisible as a target.
	otherWS := dbfx.Workspace(t, "relation other ws", "relation-other-"+handlerFixtureSuffix)
	otherIssue := dbfx.Issue(t, "relation foreign target", testutil.Cols{"workspace_id": otherWS})
	w = addRelation(t, a.ID, map[string]any{"type": "blocks", "target_issue_id": otherIssue})
	wantCode(t, "cross-workspace target", w, http.StatusBadRequest)
	if body := decodeBody(t, w); body["error"] != "target issue not found in this workspace" {
		t.Fatalf("unexpected error: %v", body["error"])
	}
}

// TestIssueRelationRevisionConflict: a stale expected_revision answers the
// structured revision_conflict error and leaves no edge behind; the fresh
// revision succeeds; delete honors the same guard.
func TestIssueRelationRevisionConflict(t *testing.T) {
	a := relationIssue(t, "conflict anchor")
	b := relationIssue(t, "conflict target")

	// Move the anchor off revision 1 so a stale expectation is expressible.
	if w := updateIssueRaw(t, a.ID, map[string]any{"priority": "high"}); w.Code != http.StatusOK {
		t.Fatalf("bump anchor: got %d: %s", w.Code, w.Body.String())
	}
	stale := int64(1)
	w := addRelation(t, a.ID, map[string]any{"type": "blocks", "target_issue_id": b.ID, "expected_revision": stale})
	wantCode(t, "stale add", w, http.StatusConflict)
	body := decodeBody(t, w)
	if body["code"] != "revision_conflict" {
		t.Fatalf("expected code revision_conflict, got %v", body)
	}
	if body["actual_revision"] == nil || body["expected_revision"] != float64(1) {
		t.Fatalf("conflict body missing revisions: %v", body)
	}
	if relationEdgeCount(t, a.ID, b.ID) != 0 {
		t.Fatal("rolled-back add must not store an edge")
	}

	fresh := int64(2)
	w = addRelation(t, a.ID, map[string]any{"type": "blocks", "target_issue_id": b.ID, "expected_revision": fresh})
	wantCode(t, "fresh add", w, http.StatusCreated)
	added := decodeBody(t, w)
	if got, ok := added["issue"].(map[string]any); !ok || got["revision"] != float64(3) {
		t.Fatalf("expected anchor revision 3 in response, got %v", added["issue"])
	}

	// Target revision advanced too: B was created at revision 1.
	if got := getRelations(t, b.ID); got.Revision != 2 {
		t.Fatalf("expected target revision 2 after add, got %d", got.Revision)
	}

	// Delete honors the guard: stale query revision → 409, edge survives.
	w = removeRelation(t, a.ID, "blocks", b.ID, "expected_revision=2")
	wantCode(t, "stale remove", w, http.StatusConflict)
	if relationEdgeCount(t, a.ID, b.ID) != 1 {
		t.Fatal("rolled-back remove must keep the edge")
	}
	w = removeRelation(t, a.ID, "blocks", b.ID, "expected_revision=3")
	wantCode(t, "fresh remove", w, http.StatusOK)
	if relationEdgeCount(t, a.ID, b.ID) != 0 {
		t.Fatal("expected edge removed")
	}
}

// TestIssueRelationCascadeOnIssueDelete: deleting either endpoint removes
// the edge (001-era FK cascade) — no dangling references survive.
func TestIssueRelationCascadeOnIssueDelete(t *testing.T) {
	a := relationIssue(t, "cascade anchor")
	b := relationIssue(t, "cascade target")

	wantCode(t, "add blocks", addRelation(t, a.ID, map[string]any{"type": "blocks", "target_issue_id": b.ID}), http.StatusCreated)

	// Delete the TARGET: the edge must vanish from the anchor's view.
	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/issues/"+b.ID, nil)
	req = withURLParam(req, "id", b.ID)
	testHandler.DeleteIssue(w, req)
	wantCode(t, "delete target", w, http.StatusNoContent)
	if got := getRelations(t, a.ID); len(got.Blocks) != 0 {
		t.Fatalf("expected blocks emptied after target delete, got %+v", got)
	}
	if relationEdgeCount(t, a.ID, b.ID) != 0 {
		t.Fatal("dangling edge survived target delete")
	}

	// Delete the ANCHOR: the inverse view on the surviving issue empties too.
	c := relationIssue(t, "cascade target two")
	wantCode(t, "add relates_to", addRelation(t, a.ID, map[string]any{"type": "relates_to", "target_issue_id": c.ID}), http.StatusCreated)
	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/issues/"+a.ID, nil)
	req = withURLParam(req, "id", a.ID)
	testHandler.DeleteIssue(w, req)
	wantCode(t, "delete anchor", w, http.StatusNoContent)
	if got := getRelations(t, c.ID); len(got.RelatesTo) != 0 {
		t.Fatalf("expected relates_to emptied after anchor delete, got %+v", got)
	}
}
