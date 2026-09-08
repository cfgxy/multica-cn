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

func TestCreateAgent_SessionGateBoundsAndDefaults(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	tests := []struct {
		name        string
		body        map[string]any
		wantCode    int
		wantTokens  int64
		wantPct     int32
		wantErrPart string
	}{
		{
			name:       "omitted uses the product defaults",
			body:       map[string]any{},
			wantCode:   http.StatusCreated,
			wantTokens: 400_000,
			wantPct:    80,
		},
		{
			// Omission must not decode to Go's zero value: zero disables the
			// gate, so every agent created by a client that never heard of
			// these fields would silently ship with it turned off.
			name:       "explicit null uses the defaults too",
			body:       map[string]any{"session_max_context_tokens": nil, "session_compact_pct": nil},
			wantCode:   http.StatusCreated,
			wantTokens: 400_000,
			wantPct:    80,
		},
		{
			name:       "zero ceiling is accepted and means disabled",
			body:       map[string]any{"session_max_context_tokens": 0},
			wantCode:   http.StatusCreated,
			wantTokens: 0,
			wantPct:    80,
		},
		{
			name:       "custom values are persisted",
			body:       map[string]any{"session_max_context_tokens": 200_000, "session_compact_pct": 60},
			wantCode:   http.StatusCreated,
			wantTokens: 200_000,
			wantPct:    60,
		},
		{
			// The frozen range is 0 or [100_000, 2_000_000]. 99_999 is the
			// value one below the floor, i.e. the one an off-by-one in the
			// comparison would let through.
			name:        "ceiling one below the minimum is rejected",
			body:        map[string]any{"session_max_context_tokens": 99_999},
			wantCode:    http.StatusBadRequest,
			wantErrPart: "session_max_context_tokens",
		},
		{
			name:       "the minimum ceiling itself is accepted",
			body:       map[string]any{"session_max_context_tokens": 100_000},
			wantCode:   http.StatusCreated,
			wantTokens: 100_000,
			wantPct:    80,
		},
		{
			name:       "the maximum ceiling itself is accepted",
			body:       map[string]any{"session_max_context_tokens": 2_000_000},
			wantCode:   http.StatusCreated,
			wantTokens: 2_000_000,
			wantPct:    80,
		},
		{
			name:        "ceiling one above the maximum is rejected",
			body:        map[string]any{"session_max_context_tokens": 2_000_001},
			wantCode:    http.StatusBadRequest,
			wantErrPart: "session_max_context_tokens",
		},
		{
			name:        "percentage below the minimum is rejected",
			body:        map[string]any{"session_compact_pct": 9},
			wantCode:    http.StatusBadRequest,
			wantErrPart: "session_compact_pct",
		},
		{
			name:        "percentage above 100 is rejected",
			body:        map[string]any{"session_compact_pct": 101},
			wantCode:    http.StatusBadRequest,
			wantErrPart: "session_compact_pct",
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{
				"name":       fmt.Sprintf("session-gate-create-%d", i),
				"runtime_id": handlerTestRuntimeID(t),
			}
			for k, v := range tt.body {
				body[k] = v
			}

			w := httptest.NewRecorder()
			testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", body))
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.wantCode, w.Body.String())
			}
			if tt.wantCode == http.StatusBadRequest {
				if !strings.Contains(w.Body.String(), tt.wantErrPart) {
					t.Fatalf("error should name the offending field %q: %s", tt.wantErrPart, w.Body.String())
				}
				return
			}

			var response AgentResponse
			if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			t.Cleanup(func() {
				testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, response.ID)
			})
			if response.SessionMaxContextTokens != tt.wantTokens {
				t.Errorf("session_max_context_tokens = %d, want %d", response.SessionMaxContextTokens, tt.wantTokens)
			}
			if response.SessionCompactPct != tt.wantPct {
				t.Errorf("session_compact_pct = %d, want %d", response.SessionCompactPct, tt.wantPct)
			}
		})
	}
}

func TestUpdateAgent_SessionGateBoundsAndOmission(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "session-gate-update", nil)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET session_max_context_tokens = 250000, session_compact_pct = 70 WHERE id = $1`, agentID,
	); err != nil {
		t.Fatalf("seed session gate settings: %v", err)
	}

	readPersisted := func() (int64, int32) {
		t.Helper()
		var tokens int64
		var pct int32
		if err := testPool.QueryRow(context.Background(),
			`SELECT session_max_context_tokens, session_compact_pct FROM agent WHERE id = $1`, agentID,
		).Scan(&tokens, &pct); err != nil {
			t.Fatalf("read session gate settings: %v", err)
		}
		return tokens, pct
	}

	update := func(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.UpdateAgent(w, withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, body), "id", agentID))
		return w
	}

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{name: "ceiling below the minimum", body: map[string]any{"session_max_context_tokens": 1}},
		{name: "ceiling one below the minimum", body: map[string]any{"session_max_context_tokens": 99_999}},
		{name: "ceiling above the maximum", body: map[string]any{"session_max_context_tokens": 2_000_001}},
		{name: "negative ceiling", body: map[string]any{"session_max_context_tokens": -1}},
		{name: "percentage below the minimum", body: map[string]any{"session_compact_pct": 0}},
		{name: "percentage above 100", body: map[string]any{"session_compact_pct": 101}},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			if w := update(t, tc.body); w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			// A rejected update must leave the stored configuration alone —
			// a half-applied settings write is worse than a refused one.
			if tokens, pct := readPersisted(); tokens != 250_000 || pct != 70 {
				t.Fatalf("rejected update mutated state: got (%d, %d), want (250000, 70)", tokens, pct)
			}
		})
	}

	t.Run("accepts zero as disabled", func(t *testing.T) {
		if w := update(t, map[string]any{"session_max_context_tokens": 0}); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		if tokens, _ := readPersisted(); tokens != 0 {
			t.Fatalf("persisted ceiling = %d, want 0", tokens)
		}
	})

	t.Run("accepts new values", func(t *testing.T) {
		if w := update(t, map[string]any{"session_max_context_tokens": 500_000, "session_compact_pct": 90}); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		if tokens, pct := readPersisted(); tokens != 500_000 || pct != 90 {
			t.Fatalf("persisted (%d, %d), want (500000, 90)", tokens, pct)
		}
	})

	t.Run("omitted preserves existing values", func(t *testing.T) {
		if w := update(t, map[string]any{"description": "session gate unchanged"}); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		if tokens, pct := readPersisted(); tokens != 500_000 || pct != 90 {
			t.Fatalf("omitted fields changed state: got (%d, %d), want (500000, 90)", tokens, pct)
		}
	})
}
