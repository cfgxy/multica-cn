package handler

// Workspace backpressure settings API (RUYI-618). The owner gate itself
// lives in the route middleware (RequireWorkspaceRole "owner"); these tests
// exercise the handler contract: defaults until an owner saves, the save
// round-trip, and refusal of cards the daemons would reject at hot-apply.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type backpressureSettingsBody struct {
	Enabled               bool    `json:"enabled"`
	MemHighPct            float64 `json:"mem_high_pct"`
	MemRecoveryPct        float64 `json:"mem_recovery_pct"`
	SwapHighPct           float64 `json:"swap_high_pct"`
	SwapRecoveryPct       float64 `json:"swap_recovery_pct"`
	SampleIntervalSeconds int     `json:"sample_interval_seconds"`
	WindowSize            int     `json:"window_size"`
	Custom                bool    `json:"custom"`
}

func cleanupBackpressureRow(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		dbfx.Exec(t, `DELETE FROM workspace_backpressure_settings WHERE workspace_id = $1`, testWorkspaceID)
	})
}

func getBackpressureSettings(t *testing.T) backpressureSettingsBody {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.GetWorkspaceBackpressureSettings(w, newRequest("GET", "/api/workspace/backpressure-settings", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body backpressureSettingsBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET: decode: %v", err)
	}
	return body
}

func putBackpressureSettings(t *testing.T, card map[string]any) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.PutWorkspaceBackpressureSettings(w, newRequest("PUT", "/api/workspace/backpressure-settings", card))
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func validBackpressureCard() map[string]any {
	def := protocol.DefaultDaemonBackpressureConfig()
	return map[string]any{
		"enabled":                 true,
		"mem_high_pct":            20.0,
		"mem_recovery_pct":        30.0,
		"swap_high_pct":           def.SwapHighPct,
		"swap_recovery_pct":       def.SwapRecoveryPct,
		"psi_high_pct":            def.PSIHighPct,
		"psi_recovery_pct":        def.PSIRecoveryPct,
		"sample_interval_seconds": 10,
		"window_size":             4,
	}
}

func TestWorkspaceBackpressureSettings_DefaultsUntilSaved(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupBackpressureRow(t)

	body := getBackpressureSettings(t)
	def := protocol.DefaultDaemonBackpressureConfig()
	if body.Custom || body.Enabled != def.Enabled || body.MemHighPct != def.MemHighPct ||
		body.MemRecoveryPct != def.MemRecoveryPct || body.SampleIntervalSeconds != def.SampleIntervalSeconds ||
		body.WindowSize != def.WindowSize {
		t.Fatalf("unsaved workspace must report code defaults with custom=false, got %+v", body)
	}
}

func TestWorkspaceBackpressureSettings_SaveRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupBackpressureRow(t)

	code, saved := putBackpressureSettings(t, validBackpressureCard())
	if code != http.StatusOK {
		t.Fatalf("PUT: expected 200, got %d: %v", code, saved)
	}
	if saved["custom"] != true || saved["mem_high_pct"] != 20.0 || saved["sample_interval_seconds"] != float64(10) {
		t.Fatalf("PUT response must echo the saved card: %v", saved)
	}

	body := getBackpressureSettings(t)
	if !body.Custom || body.MemHighPct != 20 || body.MemRecoveryPct != 30 || body.SampleIntervalSeconds != 10 || body.WindowSize != 4 {
		t.Fatalf("GET after save must return the saved card: %+v", body)
	}
}

func TestWorkspaceBackpressureSettings_RejectsInvalidCard(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupBackpressureRow(t)

	card := validBackpressureCard()
	card["mem_high_pct"] = 80.0
	card["mem_recovery_pct"] = 30.0 // below high: inverted hysteresis
	code, body := putBackpressureSettings(t, card)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("inverted hysteresis must be refused with 422, got %d: %v", code, body)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Fatalf("422 body must carry the shared validation message: %v", body)
	}

	// Nothing persisted: the workspace still reports untouched defaults.
	bodyDef := getBackpressureSettings(t)
	if bodyDef.Custom {
		t.Fatalf("refused card must not persist, got custom=true: %+v", bodyDef)
	}
}
