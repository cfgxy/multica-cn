package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func patchRuntimeConcurrency(actorID, runtimeID string, value int32) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := newRequestAs(actorID, http.MethodPatch, "/api/runtimes/"+runtimeID, map[string]any{
		"max_concurrent_tasks": value,
	})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.UpdateAgentRuntime(w, req)
	return w
}

func TestUpdateAgentRuntimeConcurrencyLimit(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID, ownerID, memberID := runtimeVisibilityFixture(t)

	w := patchRuntimeConcurrency(ownerID, runtimeID, 8)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH runtime capacity: status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var response AgentRuntimeResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode runtime response: %v", err)
	}
	if response.MaxConcurrentTasks != 8 {
		t.Fatalf("runtime max_concurrent_tasks = %d, want 8", response.MaxConcurrentTasks)
	}

	w = patchRuntimeConcurrency(ownerID, runtimeID, -1)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("negative runtime limit: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	w = patchRuntimeConcurrency(memberID, runtimeID, 4)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-owner runtime limit update: status = %d, want 403: %s", w.Code, w.Body.String())
	}
}
