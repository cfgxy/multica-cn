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

	testHandler.recordBackpressure(ctx, pgtype.UUID{}, bpReport(true, 9.9, 85, 3))
	testHandler.recordBackpressure(ctx, mustParseUUID(t, runtimeID), nil)

	if _, present := readStoredBackpressure(t, ctx, runtimeID); present {
		t.Fatal("nil/invalid inputs must not write a backpressure report")
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
	rtUUID := mustParseUUID(t, runtimeID)

	// First report lands in metadata with the watermarks intact.
	testHandler.recordBackpressure(ctx, rtUUID, bpReport(true, 8.4, 82, 5))
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
	testHandler.recordBackpressure(ctx, rtUUID, bpReport(true, 8.4, 82, 5))
	if got := storedRecordedAt(t, ctx, runtimeID); got != firstAt {
		t.Fatalf("unchanged report rewrote the row: recorded_at %q -> %q", firstAt, got)
	}

	// A watermark drift (state unchanged) must still refresh the stored report.
	testHandler.recordBackpressure(ctx, rtUUID, bpReport(true, 7.9, 84, 9))
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
	rtUUID := mustParseUUID(t, runtimeID)

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// First report, inactive: a write but no transition — no audit line.
	testHandler.recordBackpressure(ctx, rtUUID, bpReport(false, 40, 30, 0))

	testHandler.recordBackpressure(ctx, rtUUID, bpReport(true, 8.1, 83, 12))
	entered := logs.String()
	if !containsField(entered, "daemon backpressure ENTERED", runtimeID) {
		t.Fatalf("missing ENTERED audit log, got: %s", entered)
	}

	logs.Reset()
	testHandler.recordBackpressure(ctx, rtUUID, bpReport(false, 30.2, 55, 12))
	cleared := logs.String()
	if !containsField(cleared, "daemon backpressure CLEARED", runtimeID) {
		t.Fatalf("missing CLEARED audit log, got: %s", cleared)
	}
	if !bytes.Contains([]byte(cleared), []byte("deferred_claims=12")) {
		t.Fatalf("CLEARED log missing deferred_claims, got: %s", cleared)
	}

	// Same-state follow-up (watermark drift only) must not log a transition.
	logs.Reset()
	testHandler.recordBackpressure(ctx, rtUUID, bpReport(false, 29.7, 54, 12))
	if logs.Len() != 0 {
		t.Fatalf("watermark drift logged a transition: %s", logs.String())
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

func mustParseUUID(t *testing.T, id string) pgtype.UUID {
	t.Helper()
	u, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", id, err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}
