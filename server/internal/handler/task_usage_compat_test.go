package handler

import (
	"encoding/json"
	"testing"
)

// TestReportTaskUsageNewDaemonToOldServerCompat is the second RUYI-154
// compatibility case: a new daemon build reports turns / compactions /
// max_context_tokens, but the receiving server predates this feature and
// decodes into a payload shape that has never heard of those fields.
//
// oldTaskUsagePayload is a frozen snapshot of TaskUsagePayload as it existed
// before RUYI-154 — deliberately NOT kept in sync with the real struct, so
// this test keeps proving the OLD shape's behavior rather than silently
// degrading into testing the current one.
//
// encoding/json's decoder silently drops JSON object keys that have no
// matching destination field (no error, no partial-decode failure), which is
// the whole compatibility guarantee this test pins: it must never regress to
// an error return, and the fields the old server DOES know about must decode
// untouched.
func TestReportTaskUsageNewDaemonToOldServerCompat(t *testing.T) {
	type oldTaskUsagePayload struct {
		Provider         string `json:"provider"`
		Model            string `json:"model"`
		InputTokens      int64  `json:"input_tokens"`
		OutputTokens     int64  `json:"output_tokens"`
		CacheReadTokens  int64  `json:"cache_read_tokens"`
		CacheWriteTokens int64  `json:"cache_write_tokens"`
		CostUSDTicks     int64  `json:"cost_usd_ticks"`
		ContextTokens    int64  `json:"context_tokens"`
	}

	// A new daemon's wire payload: the pre-existing fields plus the three
	// RUYI-154 additions the old server's struct has no field for.
	newDaemonBody := []byte(`{
		"provider": "anthropic",
		"model": "claude-opus-5",
		"input_tokens": 1000,
		"output_tokens": 100,
		"cache_read_tokens": 500,
		"cache_write_tokens": 0,
		"cost_usd_ticks": 42,
		"context_tokens": 80000,
		"turns": 12,
		"compactions": 3,
		"max_context_tokens": 150000
	}`)

	var old oldTaskUsagePayload
	if err := json.Unmarshal(newDaemonBody, &old); err != nil {
		t.Fatalf("old server must not error decoding a new daemon's payload, got: %v", err)
	}

	if old.Provider != "anthropic" || old.Model != "claude-opus-5" {
		t.Errorf("provider/model = %q/%q, want anthropic/claude-opus-5", old.Provider, old.Model)
	}
	if old.InputTokens != 1000 || old.OutputTokens != 100 ||
		old.CacheReadTokens != 500 || old.CacheWriteTokens != 0 {
		t.Errorf("token counts = %+v, want 1000/100/500/0 — fields the old server knows about must not be disturbed by the unknown ones", old)
	}
	if old.CostUSDTicks != 42 {
		t.Errorf("cost_usd_ticks = %d, want 42", old.CostUSDTicks)
	}
	if old.ContextTokens != 80000 {
		t.Errorf("context_tokens = %d, want 80000", old.ContextTokens)
	}
}

// TestTaskUsagePayloadOldDaemonMissingRunStatsDecodesToZero is the decode-level
// half of the first compatibility case (old daemon -> new server): a JSON
// object that never mentions turns/compactions/max_context_tokens must decode
// into TaskUsagePayload with those fields at their zero value, not an error —
// which is what lets authoritativeTurns/authoritativeCompactions/
// authoritativeMaxContextTokens treat "field absent" identically to "field
// present but 0" (see internal/handler/daemon.go).
func TestTaskUsagePayloadOldDaemonMissingRunStatsDecodesToZero(t *testing.T) {
	oldDaemonBody := []byte(`{
		"provider": "anthropic",
		"model": "claude-opus-5",
		"input_tokens": 1000,
		"output_tokens": 100,
		"cache_read_tokens": 0,
		"cache_write_tokens": 0
	}`)

	var payload TaskUsagePayload
	if err := json.Unmarshal(oldDaemonBody, &payload); err != nil {
		t.Fatalf("new server must not error decoding an old daemon's payload, got: %v", err)
	}

	if payload.InputTokens != 1000 {
		t.Errorf("input_tokens = %d, want 1000", payload.InputTokens)
	}
	if payload.Turns != 0 || payload.Compactions != 0 || payload.MaxContextTokens != 0 {
		t.Errorf("run stats = turns=%d compactions=%d max_ctx=%d, want all 0 (absent, not fabricated)",
			payload.Turns, payload.Compactions, payload.MaxContextTokens)
	}
	if authoritativeTurns(payload.Turns).Valid {
		t.Error("authoritativeTurns(0) is Valid, want invalid/NULL for an absent field")
	}
	if authoritativeCompactions(payload.Compactions).Valid {
		t.Error("authoritativeCompactions(0) is Valid, want invalid/NULL for an absent field")
	}
	if authoritativeMaxContextTokens(payload.MaxContextTokens).Valid {
		t.Error("authoritativeMaxContextTokens(0) is Valid, want invalid/NULL for an absent field")
	}
}
