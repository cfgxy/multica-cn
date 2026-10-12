package handler

// RUYI-729 stage 1: daemon backpressure settings HTTP surface.
//
// Permission contract mirrors the scheduling freeze (RUYI-608): writes are
// human workspace owner/admin only (plain members and every machine
// credential fail closed), reads are member-visible. The card save must
// bump config_version server-side, stamp updated_by, and leave an
// ops-domain audit trail in the same transaction; delete restores the
// no-row state idempotently. The event list serves the Runtime page's
// trigger/recovery timeline newest-first.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func backpressureSettingsFixture(t *testing.T, name string) (runtimeID, daemonID string) {
	t.Helper()
	daemonID = fmt.Sprintf("bp-daemon-%s-%d", name, time.Now().UnixNano())
	runtimeID = dbfx.Runtime(t, "bp-rt-"+name, testutil.Cols{
		"runtime_mode": "local",
		"daemon_id":    daemonID,
		"visibility":   "public",
	})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM daemon_backpressure_settings WHERE daemon_id = $1`, daemonID)
		testPool.Exec(context.Background(), `DELETE FROM daemon_backpressure_event WHERE daemon_id = $1`, daemonID)
	})
	return runtimeID, daemonID
}

// validCardBody is a minimal in-range card: only the mem condition enabled.
func validCardBody() map[string]any {
	return map[string]any{
		"enabled":                 true,
		"mem_high_pct":            20,
		"mem_recovery_pct":        40,
		"swap_high_pct":           0,
		"swap_recovery_pct":       0,
		"psi_high_pct":            0,
		"psi_recovery_pct":        0,
		"sample_interval_seconds": 10,
		"window_size":             3,
	}
}

func TestGetRuntimeBackpressureSettings_DefaultsWhenUnconfigured(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, daemonID := backpressureSettingsFixture(t, "get-defaults")

	w := httptest.NewRecorder()
	testHandler.GetRuntimeBackpressureSettings(w, withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/runtimes/"+runtimeID+"/backpressure-settings", nil),
		"runtimeId", runtimeID))
	if w.Code != http.StatusOK {
		t.Fatalf("GET: got %d — %s", w.Code, w.Body.String())
	}
	var resp struct {
		DaemonID   string `json:"daemon_id"`
		Configured bool   `json:"configured"`
		Config     struct {
			MemHighPct     float64 `json:"mem_high_pct"`
			MemRecoveryPct float64 `json:"mem_recovery_pct"`
			ConfigVersion  int64   `json:"config_version"`
			Source         string  `json:"source"`
		} `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Configured {
		t.Fatal("fresh host must answer configured=false")
	}
	if resp.DaemonID != daemonID {
		t.Fatalf("daemon_id = %q, want %q", resp.DaemonID, daemonID)
	}
	// The default card carries the code defaults so the form has concrete
	// numbers to edit; version 0 marks it not-DB-managed.
	if resp.Config.MemHighPct != 15 || resp.Config.MemRecoveryPct != 25 {
		t.Fatalf("default card mismatch: %+v", resp.Config)
	}
	if resp.Config.ConfigVersion != 0 {
		t.Fatalf("default card version = %d, want 0", resp.Config.ConfigVersion)
	}
}

func TestPutRuntimeBackpressureSettings_PermissionMatrix(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, _ := backpressureSettingsFixture(t, "perm")
	memberID := createPermissionTestMember(t, "bp-perm-member@multica.test")

	put := func(userID, actorSource string) *httptest.ResponseRecorder {
		req := withURLParam(newRequestAs(userID, http.MethodPut,
			"/api/runtimes/"+runtimeID+"/backpressure-settings", validCardBody()), "runtimeId", runtimeID)
		if actorSource != "" {
			req.Header.Set("X-Actor-Source", actorSource)
		}
		w := httptest.NewRecorder()
		testHandler.PutRuntimeBackpressureSettings(w, req)
		return w
	}

	t.Run("workspace owner saves", func(t *testing.T) {
		if w := put(testUserID, ""); w.Code != http.StatusOK {
			t.Fatalf("owner PUT: got %d — %s", w.Code, w.Body.String())
		}
	})

	t.Run("plain member is rejected", func(t *testing.T) {
		if w := put(memberID, ""); w.Code != http.StatusForbidden {
			t.Fatalf("member PUT: got %d; want 403 — %s", w.Code, w.Body.String())
		}
	})

	t.Run("machine credential is rejected even for the owner", func(t *testing.T) {
		for _, source := range []string{"task_token", "cloud_pat", "oauth"} {
			if w := put(testUserID, source); w.Code != http.StatusForbidden {
				t.Fatalf("machine credential %s PUT: got %d; want 403 — %s", source, w.Code, w.Body.String())
			}
		}
	})
}

func TestPutRuntimeBackpressureSettings_VersionBumpsAndAudits(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, daemonID := backpressureSettingsFixture(t, "version")

	put := func(body map[string]any) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		testHandler.PutRuntimeBackpressureSettings(w, withURLParam(newRequestAs(testUserID, http.MethodPut,
			"/api/runtimes/"+runtimeID+"/backpressure-settings", body), "runtimeId", runtimeID))
		return w
	}

	if w := put(validCardBody()); w.Code != http.StatusOK {
		t.Fatalf("first PUT: got %d — %s", w.Code, w.Body.String())
	}
	// Second save tightens the trigger; the server, not the client, owns
	// config_version — it must read back as 2.
	body := validCardBody()
	body["mem_high_pct"] = 30
	if w := put(body); w.Code != http.StatusOK {
		t.Fatalf("second PUT: got %d — %s", w.Code, w.Body.String())
	}

	var version int64
	var updatedBy string
	if err := testPool.QueryRow(context.Background(),
		`SELECT config_version, updated_by FROM daemon_backpressure_settings WHERE daemon_id = $1`,
		daemonID).Scan(&version, &updatedBy); err != nil {
		t.Fatalf("load saved row: %v", err)
	}
	if version != 2 {
		t.Fatalf("config_version = %d, want 2 (server-bumped per save)", version)
	}
	if updatedBy != testUserID {
		t.Fatalf("updated_by = %q, want the saving owner %q", updatedBy, testUserID)
	}

	// The audit trail is part of the save's transaction: both updates are
	// on record with the acting member.
	var auditCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_event WHERE workspace_id = $1 AND event_type = 'ops.backpressure_settings_updated' AND details->>'daemon_id' = $2`,
		testWorkspaceID, daemonID).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 2 {
		t.Fatalf("audit rows = %d, want 2 (one per save)", auditCount)
	}
}

func TestPutRuntimeBackpressureSettings_RejectsInvertedHysteresis(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, daemonID := backpressureSettingsFixture(t, "invalid")

	body := validCardBody()
	body["mem_high_pct"] = 50
	body["mem_recovery_pct"] = 20
	w := httptest.NewRecorder()
	testHandler.PutRuntimeBackpressureSettings(w, withURLParam(newRequestAs(testUserID, http.MethodPut,
		"/api/runtimes/"+runtimeID+"/backpressure-settings", body), "runtimeId", runtimeID))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("inverted hysteresis: got %d; want 400 — %s", w.Code, w.Body.String())
	}

	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM daemon_backpressure_settings WHERE daemon_id = $1`, daemonID).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 0 {
		t.Fatal("a rejected card must not leave a row behind")
	}
}

func TestDeleteRuntimeBackpressureSettings_RemovesAndIsIdempotent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, daemonID := backpressureSettingsFixture(t, "delete")

	// Seed a row first so the delete has something to remove.
	if w := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		testHandler.PutRuntimeBackpressureSettings(w, withURLParam(newRequestAs(testUserID, http.MethodPut,
			"/api/runtimes/"+runtimeID+"/backpressure-settings", validCardBody()), "runtimeId", runtimeID))
		return w
	}(); w.Code != http.StatusOK {
		t.Fatalf("seed PUT: got %d — %s", w.Code, w.Body.String())
	}

	del := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		testHandler.DeleteRuntimeBackpressureSettings(w, withURLParam(newRequestAs(testUserID, http.MethodDelete,
			"/api/runtimes/"+runtimeID+"/backpressure-settings", nil), "runtimeId", runtimeID))
		return w
	}
	if w := del(); w.Code != http.StatusOK {
		t.Fatalf("DELETE: got %d — %s", w.Code, w.Body.String())
	}
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM daemon_backpressure_settings WHERE daemon_id = $1`, daemonID).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 0 {
		t.Fatal("row must be gone after DELETE")
	}

	// Second delete: no row is a no-op that still answers 200 with the
	// default card — the daemon's boot config is the fallback, and the UI
	// needs a coherent answer either way.
	if w := del(); w.Code != http.StatusOK {
		t.Fatalf("idempotent DELETE: got %d — %s", w.Code, w.Body.String())
	}
}

func TestListRuntimeBackpressureEvents_NewestFirst(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, daemonID := backpressureSettingsFixture(t, "events")

	insertEvent := func(eventType, reason string, age time.Duration, deferred int64) {
		t.Helper()
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO daemon_backpressure_event
				(runtime_id, daemon_id, workspace_id, event_type, reason,
				 mem_available_pct, swap_used_pct, psi_some_avg10, psi_read_ok,
				 deferred_claims, effective_config, hits, occurred_at)
			VALUES ($1, $2, $3, $4, $5, 5, 10, 0, true, $6, '{}', '[]', $7)
		`, runtimeID, daemonID, testWorkspaceID, eventType, reason, deferred,
			time.Now().Add(-age)); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}
	insertEvent("entered", "mem", 2*time.Hour, 0)
	insertEvent("cleared", "", 1*time.Hour, 7)

	w := httptest.NewRecorder()
	testHandler.ListRuntimeBackpressureEvents(w, withURLParam(newRequestAs(testUserID, http.MethodGet,
		"/api/runtimes/"+runtimeID+"/backpressure-events", nil), "runtimeId", runtimeID))
	if w.Code != http.StatusOK {
		t.Fatalf("GET events: got %d — %s", w.Code, w.Body.String())
	}
	var resp struct {
		Events []struct {
			EventType      string `json:"event_type"`
			Reason         string `json:"reason"`
			DeferredClaims int64  `json:"deferred_claims"`
			EffectiveConf  struct {
				MemHighPct float64 `json:"mem_high_pct"`
			} `json:"effective_config"`
		} `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(resp.Events))
	}
	if resp.Events[0].EventType != "cleared" || resp.Events[1].EventType != "entered" {
		t.Fatalf("order = [%s %s], want cleared first (newest-first)",
			resp.Events[0].EventType, resp.Events[1].EventType)
	}
	if resp.Events[0].DeferredClaims != 7 {
		t.Fatalf("cleared deferred_claims = %d, want 7", resp.Events[0].DeferredClaims)
	}
	if resp.Events[1].Reason != "mem" {
		t.Fatalf("entered reason = %q, want mem", resp.Events[1].Reason)
	}
}
