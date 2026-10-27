package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Project.instructions (RUYI-46) is the per-project agent prompt: the create
// and update APIs must accept it, echo it back, honor the three-state update
// contract shared with description (absent = keep, null = clear, value = set),
// and reject oversized values with a 4xx before any DB work.
func TestProjectInstructionsLifecycle(t *testing.T) {
	const instructions = "Always write Go. Commit messages follow Conventional Commits."

	// Create echoes instructions back.
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title":        "instructions project",
		"instructions": instructions,
	}))
	created := decodeProject(t, w, http.StatusCreated)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, created.ID)
	})
	if created.Instructions == nil || *created.Instructions != instructions {
		t.Fatalf("create instructions = %v, want %q", created.Instructions, instructions)
	}

	// GET persists it.
	w = httptest.NewRecorder()
	getReq := withURLParam(newRequest("GET", "/api/projects/"+created.ID, nil), "id", created.ID)
	testHandler.GetProject(w, getReq)
	got := decodeProject(t, w, http.StatusOK)
	if got.Instructions == nil || *got.Instructions != instructions {
		t.Fatalf("get instructions = %v, want %q", got.Instructions, instructions)
	}

	// Update to a new value.
	w = httptest.NewRecorder()
	putReq := withURLParam(newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"instructions": "Updated instructions.",
	}), "id", created.ID)
	testHandler.UpdateProject(w, putReq)
	updated := decodeProject(t, w, http.StatusOK)
	if updated.Instructions == nil || *updated.Instructions != "Updated instructions." {
		t.Fatalf("update instructions = %v, want %q", updated.Instructions, "Updated instructions.")
	}

	// An absent key leaves the prior value untouched (same contract as dates).
	w = httptest.NewRecorder()
	putReq = withURLParam(newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"title": "instructions project renamed",
	}), "id", created.ID)
	testHandler.UpdateProject(w, putReq)
	untouched := decodeProject(t, w, http.StatusOK)
	if untouched.Instructions == nil || *untouched.Instructions != "Updated instructions." {
		t.Fatalf("absent key must not change instructions, got %v", untouched.Instructions)
	}

	// An explicit null clears it.
	w = httptest.NewRecorder()
	putReq = withURLParam(newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"instructions": nil,
	}), "id", created.ID)
	testHandler.UpdateProject(w, putReq)
	cleared := decodeProject(t, w, http.StatusOK)
	if cleared.Instructions != nil {
		t.Fatalf("explicit null must clear instructions, got %v", *cleared.Instructions)
	}
}

func TestCreateProjectInstructionsOverLimitReturns400(t *testing.T) {
	oversized := strings.Repeat("字", maxProjectInstructionsLen+1) // 32001 runes
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title":        "oversized instructions",
		"instructions": oversized,
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized instructions, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateProjectInstructionsOverLimitReturns400(t *testing.T) {
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "oversize update seed",
	}))
	created := decodeProject(t, w, http.StatusCreated)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, created.ID)
	})

	oversized := strings.Repeat("a", maxProjectInstructionsLen+1)
	w = httptest.NewRecorder()
	putReq := withURLParam(newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"instructions": oversized,
	}), "id", created.ID)
	testHandler.UpdateProject(w, putReq)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized instructions update, got %d: %s", w.Code, w.Body.String())
	}

	// The failed update must not have modified the stored value.
	w = httptest.NewRecorder()
	getReq := withURLParam(newRequest("GET", "/api/projects/"+created.ID, nil), "id", created.ID)
	testHandler.GetProject(w, getReq)
	got := decodeProject(t, w, http.StatusOK)
	if got.Instructions != nil {
		t.Fatalf("rejected update must not persist instructions, got %q", *got.Instructions)
	}
}

// Exactly at the cap is accepted; the limit counts runes, not bytes, so a
// CJK payload of the same rune length passes even though it is larger in
// bytes on the wire.
func TestProjectInstructionsAtCapAccepted(t *testing.T) {
	atCap := strings.Repeat("字", maxProjectInstructionsLen) // exactly 32000 runes
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title":        "at-cap instructions",
		"instructions": atCap,
	}))
	created := decodeProject(t, w, http.StatusCreated)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, created.ID)
	})
	if created.Instructions == nil || len(*created.Instructions) != len(atCap) {
		t.Fatalf("at-cap instructions must be accepted (got len %v)", created.Instructions)
	}
}

func TestCreateProjectWithoutInstructionsReturnsNull(t *testing.T) {
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "no instructions project",
	}))
	rawBody := w.Body.String()
	created := decodeProject(t, w, http.StatusCreated)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, created.ID)
	})
	if created.Instructions != nil {
		t.Fatalf("instructions must default to null, got %q", *created.Instructions)
	}
	// The JSON key must still be present so clients can rely on it.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawBody), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	if _, ok := raw["instructions"]; !ok {
		t.Errorf("response JSON must carry the instructions key")
	}
}
