package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// RUYI-397: resource_weight is the claim-budget multiplier. The API surface
// mirrors max_concurrent_tasks: omitted/null defaults to 1, 1..10 accepted,
// anything else rejected with the valid range spelled out.
func TestCreateAgent_ResourceWeightBoundsAndDefault(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	tests := []struct {
		name     string
		value    any
		provided bool
		wantCode int
		want     int32
	}{
		{name: "omitted defaults to one", provided: false, wantCode: http.StatusCreated, want: 1},
		{name: "null defaults to one", value: nil, provided: true, wantCode: http.StatusCreated, want: 1},
		{name: "minimum accepted", value: 1, provided: true, wantCode: http.StatusCreated, want: 1},
		{name: "maximum accepted", value: 10, provided: true, wantCode: http.StatusCreated, want: 10},
		{name: "zero rejected", value: 0, provided: true, wantCode: http.StatusBadRequest},
		{name: "negative rejected", value: -1, provided: true, wantCode: http.StatusBadRequest},
		{name: "above maximum rejected", value: 11, provided: true, wantCode: http.StatusBadRequest},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentName := fmt.Sprintf("weight-create-%d", i)
			body := map[string]any{
				"name":       agentName,
				"runtime_id": handlerTestRuntimeID(t),
			}
			if tt.provided {
				body["resource_weight"] = tt.value
			}

			w := httptest.NewRecorder()
			testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", body))
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.wantCode, w.Body.String())
			}

			if tt.wantCode == http.StatusBadRequest {
				if !strings.Contains(w.Body.String(), "between 1 and 10") {
					t.Fatalf("error should explain the 1-10 range: %s", w.Body.String())
				}
				return
			}

			var response AgentResponse
			if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.ResourceWeight != tt.want {
				t.Fatalf("resource_weight = %d, want %d", response.ResourceWeight, tt.want)
			}
		})
	}
}

func TestUpdateAgent_ResourceWeightBoundsAndOmission(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "weight-update", nil)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET resource_weight = 7 WHERE id = $1`, agentID,
	); err != nil {
		t.Fatalf("seed resource_weight: %v", err)
	}

	readPersisted := func() int32 {
		t.Helper()
		var got int32
		if err := testPool.QueryRow(context.Background(),
			`SELECT resource_weight FROM agent WHERE id = $1`, agentID,
		).Scan(&got); err != nil {
			t.Fatalf("read resource_weight: %v", err)
		}
		return got
	}

	for _, value := range []int32{0, -1, 11} {
		t.Run(fmt.Sprintf("rejects_%d", value), func(t *testing.T) {
			w := httptest.NewRecorder()
			req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
				"resource_weight": value,
			}), "id", agentID)
			testHandler.UpdateAgent(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "between 1 and 10") {
				t.Fatalf("error should explain the 1-10 range: %s", w.Body.String())
			}
			if got := readPersisted(); got != 7 {
				t.Fatalf("rejected update persisted %d, want existing value 7", got)
			}
		})
	}

	for _, value := range []int32{1, 10} {
		t.Run(fmt.Sprintf("accepts_%d", value), func(t *testing.T) {
			w := httptest.NewRecorder()
			req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
				"resource_weight": value,
			}), "id", agentID)
			testHandler.UpdateAgent(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
			}
			if got := readPersisted(); got != value {
				t.Fatalf("persisted resource_weight = %d, want %d", got, value)
			}
		})
	}

	t.Run("omitted preserves existing value", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
			"description": "weight unchanged",
		}), "id", agentID)
		testHandler.UpdateAgent(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		if got := readPersisted(); got != 10 {
			t.Fatalf("omitted update persisted %d, want the previously set 10", got)
		}
	})
}
