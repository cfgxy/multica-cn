package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func patchRuntimeConcurrency(actorID, runtimeID string, value int32) *httptest.ResponseRecorder {
	return patchRuntime(actorID, runtimeID, map[string]any{
		"max_concurrent_tasks": value,
	})
}

func patchRuntime(actorID, runtimeID string, body any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := newRequestAs(actorID, http.MethodPatch, "/api/runtimes/"+runtimeID, body)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.UpdateAgentRuntime(w, req)
	return w
}

func TestUpdateAgentRuntimeConcurrencyLimit(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID, ownerID, memberID := runtimeVisibilityFixture(t)

	for _, value := range []int32{0, 1, 50, 51} {
		w := patchRuntimeConcurrency(ownerID, runtimeID, value)
		if w.Code != http.StatusOK {
			t.Fatalf("runtime limit %d: status = %d, want 200: %s", value, w.Code, w.Body.String())
		}
		var response AgentRuntimeResponse
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("decode runtime response for %d: %v", value, err)
		}
		if response.MaxConcurrentTasks != value {
			t.Fatalf("runtime max_concurrent_tasks for %d = %d, want %d", value, response.MaxConcurrentTasks, value)
		}
	}

	for _, value := range []int32{-1, -2} {
		w := patchRuntimeConcurrency(ownerID, runtimeID, value)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("runtime limit %d: status = %d, want 400: %s", value, w.Code, w.Body.String())
		}
	}

	w := patchRuntime(ownerID, runtimeID, map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("omitted runtime limit: status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var response AgentRuntimeResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode omitted runtime limit response: %v", err)
	}
	if response.MaxConcurrentTasks != 51 {
		t.Fatalf("omitted runtime max_concurrent_tasks = %d, want 51", response.MaxConcurrentTasks)
	}

	w = patchRuntimeConcurrency(memberID, runtimeID, 4)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-owner runtime limit update: status = %d, want 403: %s", w.Code, w.Body.String())
	}
}
