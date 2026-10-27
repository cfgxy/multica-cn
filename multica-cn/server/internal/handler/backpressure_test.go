package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// readStoredBackpressure returns the backpressure report persisted in the
// runtime's metadata bag, or (nil, false) when the key is absent.
func readStoredBackpressure(t *testing.T, ctx context.Context, runtimeID string) (*protocol.DaemonBackpressureReport, bool) {
	t.Helper()
	var raw []byte
	if err := testPool.QueryRow(ctx,
		`SELECT metadata->'backpressure' FROM agent_runtime WHERE id = $1`, runtimeID,
	).Scan(&raw); err != nil {
		t.Fatalf("read backpressure metadata: %v", err)
	}
	if len(raw) == 0 {
		return nil, false
	}
	var report protocol.DaemonBackpressureReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("stored backpressure is not a valid report: %v", err)
	}
	return &report, true
}

// storedRecordedAt reads metadata->'backpressure'->>'recorded_at' so tests can
// tell a rewritten report from a deduped no-op.
func storedRecordedAt(t *testing.T, ctx context.Context, runtimeID string) string {
	t.Helper()
	var at *string
	if err := testPool.QueryRow(ctx,
		`SELECT metadata->'backpressure'->>'recorded_at' FROM agent_runtime WHERE id = $1`, runtimeID,
	).Scan(&at); err != nil {
		t.Fatalf("read recorded_at: %v", err)
	}
	if at == nil {
		return ""
	}
	return *at
}

func bpReport(active bool, memPct, swapPct float64, deferred int64) *protocol.DaemonBackpressureReport {
	return &protocol.DaemonBackpressureReport{
		Active:          active,
		Reason:          "mem",
		MemAvailablePct: memPct,
		SwapUsedPct:     swapPct,
		DeferredClaims:  deferred,
	}
}

func TestRecordBackpressure_NilAndInvalidNoop(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := dbfx.Runtime(t, "BP noop rt", testutil.Cols{
		"device_info": "backpressure noop fixture",
	})

	testHandler.recordBackpressure(ctx, db.AgentRuntime{}, bpReport(true, 9.9, 85, 3))
	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), nil)

	if _, present := readStoredBackpressure(t, ctx, runtimeID); present {
		t.Fatal("nil/invalid inputs must not write a backpressure report")
	}
}

// D2 rework evidence (RUYI-729): a genuine recovery (active→inactive report
// on a live heartbeat, no restart) must land a cleared row in
// daemon_backpressure_event through the transition detector. The QA round
// never saw a cleared row only because the D1 defect kept the daemon
// reporting active:true forever — the transition never happened. This test
// pins the recovery half of the chain so the D1 fix's end-to-end behavior
// (fallback → inactive report → cleared row) has a checked seam.
func TestRecordBackpressure_RecoveryTransitionWritesClearedEventRow(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := dbfx.Runtime(t, "BP cleared rt", testutil.Cols{
		"device_info": "backpressure cleared fixture",
	})
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM daemon_backpressure_event WHERE runtime_id = $1`,
			mustParseUUID(t, runtimeID))
	})
	eventRows := func() []string {
		t.Helper()
		rows, err := testPool.Query(ctx,
			`SELECT event_type FROM daemon_backpressure_event WHERE runtime_id = $1 ORDER BY occurred_at`,
			mustParseUUID(t, runtimeID))
		if err != nil {
			t.Fatalf("query event rows: %v", err)
		}
		defer rows.Close()
		var types []string
		for rows.Next() {
			var et string
			if err := rows.Scan(&et); err != nil {
				t.Fatalf("scan event_type: %v", err)
			}
			types = append(types, et)
		}
		return types
	}

	// Hold starts (deferred claims climbing), then a genuine recovery on the
	// same live runtime: exactly one entered row and one cleared row.
	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), bpReport(true, 8.1, 83, 36))
	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), bpReport(false, 30.2, 55, 93))

	types := eventRows()
	if len(types) != 2 || types[0] != "entered" || types[1] != "cleared" {
		t.Fatalf("event rows = %v, want [entered cleared]", types)
	}
	var deferred int64
	if err := testPool.QueryRow(ctx,
		`SELECT deferred_claims FROM daemon_backpressure_event WHERE runtime_id = $1 AND event_type = 'cleared'`,
		mustParseUUID(t, runtimeID),
	).Scan(&deferred); err != nil {
		t.Fatalf("read cleared row: %v", err)
	}
	if deferred != 93 {
		t.Fatalf("cleared deferred_claims = %d, want 93", deferred)
	}

	// A same-state follow-up report is watermark drift only — no new rows.
	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), bpReport(false, 29.7, 54, 93))
	if got := eventRows(); len(got) != 2 {
		t.Fatalf("event rows after drift = %v, want unchanged [entered cleared]", got)
	}
}

func TestRecordBackpressure_PersistDedupeAndDrift(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := dbfx.Runtime(t, "BP persist rt", testutil.Cols{
		"device_info": "backpressure persist fixture",
	})
	// First report lands in metadata with the watermarks intact.
	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), bpReport(true, 8.4, 82, 5))
	stored, present := readStoredBackpressure(t, ctx, runtimeID)
	if !present {
		t.Fatal("first report was not persisted")
	}
	if !stored.Active || stored.Reason != "mem" || stored.MemAvailablePct != 8.4 || stored.SwapUsedPct != 82 || stored.DeferredClaims != 5 {
		t.Fatalf("stored report mismatch: %+v", stored)
	}
	if storedRecordedAt(t, ctx, runtimeID) == "" {
		t.Fatal("server did not stamp recorded_at")
	}

	// An identical report must dedupe to a no-op (recorded_at unchanged).
	firstAt := storedRecordedAt(t, ctx, runtimeID)
	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), bpReport(true, 8.4, 82, 5))
	if got := storedRecordedAt(t, ctx, runtimeID); got != firstAt {
		t.Fatalf("unchanged report rewrote the row: recorded_at %q -> %q", firstAt, got)
	}

	// A watermark drift (state unchanged) must still refresh the stored report.
	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), bpReport(true, 7.9, 84, 9))
	stored, _ = readStoredBackpressure(t, ctx, runtimeID)
	if stored.MemAvailablePct != 7.9 || stored.SwapUsedPct != 84 || stored.DeferredClaims != 9 {
		t.Fatalf("drifted report was not refreshed: %+v", stored)
	}
}

func TestRecordBackpressure_TransitionAuditLogs(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := dbfx.Runtime(t, "BP audit rt", testutil.Cols{
		"device_info": "backpressure audit fixture",
	})
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// First report, inactive: a write but no transition — no audit line.
	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), bpReport(false, 40, 30, 0))

	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), bpReport(true, 8.1, 83, 12))
	entered := logs.String()
	if !containsField(entered, "daemon backpressure ENTERED", runtimeID) {
		t.Fatalf("missing ENTERED audit log, got: %s", entered)
	}

	logs.Reset()
	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), bpReport(false, 30.2, 55, 12))
	cleared := logs.String()
	if !containsField(cleared, "daemon backpressure CLEARED", runtimeID) {
		t.Fatalf("missing CLEARED audit log, got: %s", cleared)
	}
	if !bytes.Contains([]byte(cleared), []byte("deferred_claims=12")) {
		t.Fatalf("CLEARED log missing deferred_claims, got: %s", cleared)
	}

	// Same-state follow-up (watermark drift only) must not log a transition.
	logs.Reset()
	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), bpReport(false, 29.7, 54, 12))
	if logs.Len() != 0 {
		t.Fatalf("watermark drift logged a transition: %s", logs.String())
	}
}

func TestRecordBackpressure_FirstReportNoPriorKey_EntersAudit(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := dbfx.Runtime(t, "BP first-enter rt", testutil.Cols{
		"device_info": "backpressure first-enter fixture",
	})
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// First-ever report on a runtime whose metadata has no backpressure key
	// (fresh registration or a post-restart re-registered row): the write
	// must land AND the ENTERED transition must be audited. A NULL previous
	// report means wasActive=false, not a storage failure.
	testHandler.recordBackpressure(ctx, bpRuntimeRow(t, ctx, runtimeID), bpReport(true, 8.1, 83, 12))

	if _, present := readStoredBackpressure(t, ctx, runtimeID); !present {
		t.Fatal("first report was not persisted")
	}
	captured := logs.String()
	if !containsField(captured, "daemon backpressure ENTERED", runtimeID) {
		t.Fatalf("missing ENTERED audit log on first report, got: %s", captured)
	}
	if strings.Contains(captured, "record backpressure failed") {
		t.Fatalf("first report logged a spurious failure, got: %s", captured)
	}
}

func containsField(hay, msg, runtimeID string) bool {
	return strings.Contains(hay, msg) && strings.Contains(hay, "runtime_id="+runtimeID)
}

func TestDaemonHeartbeatHTTP_BackpressureRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := dbfx.Runtime(t, "BP heartbeat rt", testutil.Cols{
		"device_info": "backpressure heartbeat fixture",
	})

	beat := func(body map[string]any) {
		t.Helper()
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", body,
			testWorkspaceID, "bp-heartbeat-daemon")
		testutil.Call(t, testHandler.DaemonHeartbeat, req).Want(http.StatusOK)
	}

	beat(map[string]any{"runtime_id": runtimeID,
		"backpressure": map[string]any{"active": true, "reason": "mem+swap",
			"mem_available_pct": 9.5, "swap_used_pct": 88.1, "deferred_claims": 4}})
	stored, present := readStoredBackpressure(t, ctx, runtimeID)
	if !present {
		t.Fatal("heartbeat with backpressure did not persist the report")
	}
	if !stored.Active || stored.Reason != "mem+swap" || stored.MemAvailablePct != 9.5 {
		t.Fatalf("stored heartbeat report mismatch: %+v", stored)
	}

	// A legacy heartbeat without the field must neither clear nor rewrite it.
	beat(map[string]any{"runtime_id": runtimeID})
	stored, present = readStoredBackpressure(t, ctx, runtimeID)
	if !present || !stored.Active || stored.Reason != "mem+swap" {
		t.Fatalf("legacy heartbeat clobbered the stored report: present=%v %+v", present, stored)
	}
}

func TestClaimTasksByRuntime_BackpressurePersisted(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "BP claim rt")

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/tasks/claim",
		map[string]any{"daemon_id": batchClaimTestDaemonID, "runtime_ids": []string{runtimeID},
			"max_tasks": 0,
			"backpressure": map[string]any{"active": true, "reason": "swap",
				"mem_available_pct": 22, "swap_used_pct": 91.3, "deferred_claims": 1}},
		testWorkspaceID, batchClaimTestDaemonID)
	testHandler.ClaimTasksByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	stored, present := readStoredBackpressure(t, ctx, runtimeID)
	if !present {
		var meta []byte
		_ = testPool.QueryRow(ctx, `SELECT metadata FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&meta)
		t.Fatalf("batch claim with backpressure did not persist the report; metadata=%s body=%s logs=%s",
			meta, w.Body.String(), logs.String())
	}
	if !stored.Active || stored.Reason != "swap" || stored.SwapUsedPct != 91.3 {
		t.Fatalf("stored claim report mismatch: %+v", stored)
	}
}

// bpRuntimeRow loads the fixture runtime row: recordBackpressure (RUYI-729)
// reads ID, workspace_id and daemon_id off it, so tests feed it the real row
// instead of a bare uuid.
func bpRuntimeRow(t *testing.T, ctx context.Context, runtimeID string) db.AgentRuntime {
	t.Helper()
	var rt db.AgentRuntime
	if err := testPool.QueryRow(ctx,
		`SELECT id, workspace_id, daemon_id FROM agent_runtime WHERE id = $1`,
		mustParseUUID(t, runtimeID),
	).Scan(&rt.ID, &rt.WorkspaceID, &rt.DaemonID); err != nil {
		t.Fatalf("load runtime row %s: %v", runtimeID, err)
	}
	return rt
}

func mustParseUUID(t *testing.T, id string) pgtype.UUID {
	t.Helper()
	u, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", id, err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}
