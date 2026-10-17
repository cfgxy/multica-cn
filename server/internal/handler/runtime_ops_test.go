// RUYI-645: DB-backed tests for the read-only node-ops channel — matrix
// groups 2 (member-API gates + status mapping), 4 (receipt/epoch + sweep)
// and 5 (inventory report forms), plus the concurrent-claim no-double-claim
// assertion. Whitelist/argv semantics live in pkg/opskind (group 1).
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// createOpsChannelTestRuntime inserts an online (or given-status) runtime
// carrying ops_caps metadata plus a successful daemon_service_inventory row
// that manages multica-server.service — the fixture every gate test needs.
func createOpsChannelTestRuntime(t *testing.T, status string) (runtimeID, daemonID string) {
	t.Helper()

	suffix := time.Now().UnixNano()
	daemonID = fmt.Sprintf("ops-channel-daemon-%d", suffix)
	runtimeName := fmt.Sprintf("runtime-ops-channel-%d", suffix)

	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at, visibility
		)
		VALUES ($1, $2, $3, 'local', 'claude', $4, 'Ops Channel Test', '{"ops_caps":["systemd","docker"]}'::jsonb, $5, now(), 'public')
		RETURNING id
	`, testWorkspaceID, daemonID, runtimeName, status, testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("create ops channel runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO daemon_service_inventory (daemon_id, collect_status, services, last_success_at)
		VALUES ($1, 'ok',
			'[{"name":"multica-server.service","type":"systemd","status":"active","detail":"multica server"}]'::jsonb,
			now())
	`, daemonID); err != nil {
		t.Fatalf("create ops channel inventory: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM daemon_service_inventory WHERE daemon_id = $1`, daemonID)
	})

	return runtimeID, daemonID
}

// insertOpsChannelCommand inserts one command row directly so receipt and
// sweep tests control lifecycle state without going through the gates.
func insertOpsChannelCommand(t *testing.T, workspaceID, runtimeID, daemonID, kind string) string {
	t.Helper()

	var commandID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO daemon_ops_command (
			id, workspace_id, runtime_id, daemon_id, kind, args, status, timeout_s,
			submitted_by_type, submitted_by_id, submission_key
		)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, '{"unit":"multica-server.service"}'::jsonb,
			'pending', 30, 'member', $5, 'seed-' || gen_random_uuid()::text)
		RETURNING id
	`, workspaceID, runtimeID, daemonID, kind, testUserID).Scan(&commandID); err != nil {
		t.Fatalf("insert ops command: %v", err)
	}
	cleanupOpsChannelCommand(t, commandID)
	return commandID
}

// cleanupOpsChannelCommand registers removal of one command row plus its
// audit trail. Every ops audit event (submitted/finalized/expired/timeout)
// carries the command id in details, so the audit delete is an exact match
// rather than a workspace-wide sweep that could eat unrelated rows.
func cleanupOpsChannelCommand(t *testing.T, commandID string) {
	t.Helper()
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM audit_event WHERE details->>'command_id' = $1`, commandID)
		testPool.Exec(context.Background(), `DELETE FROM daemon_ops_command WHERE id = $1`, commandID)
	})
}

func opsSubmitRequest(userID, runtimeID string, body map[string]any) *http.Request {
	return withURLParams(
		newRequestAsUser(userID, http.MethodPost, "/api/runtimes/"+runtimeID+"/ops/commands", body),
		"runtimeId", runtimeID,
	)
}

func decodeOpsResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return out
}

func TestSubmitOpsCommand_MemberForbidden(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, _ := createOpsChannelTestRuntime(t, "online")
	memberUserID := createRuntimeLocalSkillTestMember(t, "member")

	w := httptest.NewRecorder()
	testHandler.SubmitRuntimeOpsCommand(w, opsSubmitRequest(memberUserID, runtimeID, map[string]any{
		"kind": "systemctl_status",
		"args": map[string]any{"unit": "multica-server.service"},
	}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubmitOpsCommand_UnknownKindReturns400(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, _ := createOpsChannelTestRuntime(t, "online")

	w := httptest.NewRecorder()
	testHandler.SubmitRuntimeOpsCommand(w, opsSubmitRequest(testUserID, runtimeID, map[string]any{
		"kind": "rm_rf_slash",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubmitOpsCommand_OfflineRuntimeReturns409(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, _ := createOpsChannelTestRuntime(t, "offline")

	w := httptest.NewRecorder()
	testHandler.SubmitRuntimeOpsCommand(w, opsSubmitRequest(testUserID, runtimeID, map[string]any{
		"kind": "systemctl_status",
		"args": map[string]any{"unit": "multica-server.service"},
	}))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubmitOpsCommand_CapabilityMissingReturns409(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Fixture caps are ["systemd","docker"]; launchctl_list needs darwin+launchd.
	runtimeID, _ := createOpsChannelTestRuntime(t, "online")

	w := httptest.NewRecorder()
	testHandler.SubmitRuntimeOpsCommand(w, opsSubmitRequest(testUserID, runtimeID, map[string]any{
		"kind": "launchctl_list",
	}))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubmitOpsCommand_UnitNotInInventoryReturns400(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, _ := createOpsChannelTestRuntime(t, "online")

	w := httptest.NewRecorder()
	testHandler.SubmitRuntimeOpsCommand(w, opsSubmitRequest(testUserID, runtimeID, map[string]any{
		"kind": "journalctl_tail",
		"args": map[string]any{"unit": "totally-unmanaged.service", "lines": 100},
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubmitOpsCommand_TimeoutOutOfRangeReturns400(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, _ := createOpsChannelTestRuntime(t, "online")

	w := httptest.NewRecorder()
	testHandler.SubmitRuntimeOpsCommand(w, opsSubmitRequest(testUserID, runtimeID, map[string]any{
		"kind":      "systemctl_status",
		"args":      map[string]any{"unit": "multica-server.service"},
		"timeout_s": 999,
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubmitOpsCommand_IdempotentReplayAndConflict(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, _ := createOpsChannelTestRuntime(t, "online")
	body := map[string]any{
		"kind":           "systemctl_status",
		"args":           map[string]any{"unit": "multica-server.service"},
		"submission_key": "replay-case-1",
	}

	w := httptest.NewRecorder()
	testHandler.SubmitRuntimeOpsCommand(w, opsSubmitRequest(testUserID, runtimeID, body))
	if w.Code != http.StatusCreated {
		t.Fatalf("first submit: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	first := decodeOpsResponse(t, w)
	firstID, _ := first["id"].(string)
	if firstID == "" {
		t.Fatalf("first submit missing id: %s", w.Body.String())
	}
	// The submit path inserts server-side, so the row (and its
	// ops.command_submitted audit event) must be torn down explicitly.
	cleanupOpsChannelCommand(t, firstID)

	w = httptest.NewRecorder()
	testHandler.SubmitRuntimeOpsCommand(w, opsSubmitRequest(testUserID, runtimeID, body))
	if w.Code != http.StatusOK {
		t.Fatalf("replay: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	replay := decodeOpsResponse(t, w)
	if replay["id"] != firstID {
		t.Fatalf("replay id = %v, want original %s", replay["id"], firstID)
	}
	if replay["idempotent"] != true {
		t.Fatalf("replay idempotent = %v, want true", replay["idempotent"])
	}

	// Same key, different args → conflict, never a silent second command.
	w = httptest.NewRecorder()
	testHandler.SubmitRuntimeOpsCommand(w, opsSubmitRequest(testUserID, runtimeID, map[string]any{
		"kind":           "ps_snapshot",
		"args":           map[string]any{},
		"submission_key": "replay-case-1",
	}))
	if w.Code != http.StatusConflict {
		t.Fatalf("same-key different-args: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubmitOpsCommand_QuotaExceededReturns429(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, daemonID := createOpsChannelTestRuntime(t, "online")
	for i := 0; i < service.OpsCommandQuotaPerRuntime; i++ {
		insertOpsChannelCommand(t, testWorkspaceID, runtimeID, daemonID, "ps_snapshot")
	}

	w := httptest.NewRecorder()
	testHandler.SubmitRuntimeOpsCommand(w, opsSubmitRequest(testUserID, runtimeID, map[string]any{
		"kind": "df_report",
	}))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubmitOpsCommand_WakesDaemonOnFreshSubmission(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, _ := createOpsChannelTestRuntime(t, "online")
	recorder := &runtimeLocalSkillPendingWorkRecorder{}
	h := *testHandler
	h.DaemonPendingWork = recorder

	w := httptest.NewRecorder()
	h.SubmitRuntimeOpsCommand(w, opsSubmitRequest(testUserID, runtimeID, map[string]any{
		"kind": "systemctl_status",
		"args": map[string]any{"unit": "multica-server.service"},
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	created := decodeOpsResponse(t, w)
	commandID, _ := created["id"].(string)
	if commandID == "" {
		t.Fatalf("submit missing id: %s", w.Body.String())
	}
	cleanupOpsChannelCommand(t, commandID)
	want := runtimeID + ":" + protocol.PendingWorkKindOpsCommand
	if len(recorder.hints) != 1 || recorder.hints[0] != want {
		t.Fatalf("pending-work hints = %v, want [%s]", recorder.hints, want)
	}
}

// TestOpsClaim_ConcurrentNoDoubleClaim is the added acceptance point: two
// server instances heartbeating the same runtime at once must never hand
// out the same command, and claimants must never block each other. Eight
// concurrent claims over five pending rows must yield exactly five distinct
// claimed commands.
func TestOpsClaim_ConcurrentNoDoubleClaim(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, daemonID := createOpsChannelTestRuntime(t, "online")
	for i := 0; i < 5; i++ {
		insertOpsChannelCommand(t, testWorkspaceID, runtimeID, daemonID, "ps_snapshot")
	}

	rt, err := testHandler.Queries.GetAgentRuntime(context.Background(), mustParseUUID(t, runtimeID))
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}

	const claimants = 8
	var (
		mu       sync.Mutex
		claimed  []string
		wg       sync.WaitGroup
		startBar = make(chan struct{})
	)
	for i := 0; i < claimants; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startBar
			rows, err := service.ClaimOpsCommands(context.Background(), testHandler.Queries, rt.ID, service.OpsCommandMaxPerBeat)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, row := range rows {
				claimed = append(claimed, uuidToString(row.ID))
			}
		}()
	}
	close(startBar)
	wg.Wait()

	seen := make(map[string]bool, len(claimed))
	for _, id := range claimed {
		if seen[id] {
			t.Fatalf("command %s handed out twice across %d concurrent claims", id, claimants)
		}
		seen[id] = true
	}
	if len(claimed) != 5 {
		t.Fatalf("total claimed = %d, want all 5 pending rows", len(claimed))
	}

	var stillPending int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM daemon_ops_command WHERE runtime_id = $1 AND status = 'pending'`,
		mustParseUUID(t, runtimeID)).Scan(&stillPending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if stillPending != 0 {
		t.Fatalf("%d rows still pending after concurrent claims", stillPending)
	}
}

// TestOpsReceipt_EpochMismatchAndLateReceipt covers assertion 3: a receipt
// with the wrong lease epoch is rejected 409 without touching state, the
// correct epoch finalizes, and a late receipt after finalization is also 409.
func TestOpsReceipt_EpochMismatchAndLateReceipt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, daemonID := createOpsChannelTestRuntime(t, "online")
	commandID := insertOpsChannelCommand(t, testWorkspaceID, runtimeID, daemonID, "systemctl_status")

	rt, err := testHandler.Queries.GetAgentRuntime(context.Background(), mustParseUUID(t, runtimeID))
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	rows, err := service.ClaimOpsCommands(context.Background(), testHandler.Queries, rt.ID, 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("claim: rows=%d err=%v", len(rows), err)
	}
	epoch := rows[0].LeaseEpoch

	receiptRequest := func(epoch int64, status string) *http.Request {
		return withURLParams(
			newDaemonTokenRequest(http.MethodPost,
				"/api/daemon/runtimes/"+runtimeID+"/ops-commands/"+commandID+"/result",
				map[string]any{"status": status, "stdout": "active\n", "lease_epoch": epoch},
				testWorkspaceID, daemonID),
			"runtimeId", runtimeID, "commandId", commandID,
		)
	}

	w := httptest.NewRecorder()
	testHandler.ReportOpsCommandResult(w, receiptRequest(epoch+999, "succeeded"))
	if w.Code != http.StatusConflict {
		t.Fatalf("wrong epoch: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.ReportOpsCommandResult(w, receiptRequest(epoch, "succeeded"))
	if w.Code != http.StatusOK {
		t.Fatalf("correct epoch: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Late receipt for an already-terminal command: rejected, state intact.
	w = httptest.NewRecorder()
	testHandler.ReportOpsCommandResult(w, receiptRequest(epoch, "failed"))
	if w.Code != http.StatusConflict {
		t.Fatalf("late receipt: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var dbStatus, resultStdout string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, result->>'stdout' FROM daemon_ops_command WHERE id = $1
	`, mustParseUUID(t, commandID)).Scan(&dbStatus, &resultStdout); err != nil {
		t.Fatalf("read command: %v", err)
	}
	if dbStatus != "succeeded" || resultStdout != "active\n" {
		t.Fatalf("final state = %s stdout=%q, want succeeded with echoed output", dbStatus, resultStdout)
	}
}

func TestOpsReceipt_SucceedsWritesAudit(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, daemonID := createOpsChannelTestRuntime(t, "online")
	commandID := insertOpsChannelCommand(t, testWorkspaceID, runtimeID, daemonID, "ps_snapshot")

	rt, err := testHandler.Queries.GetAgentRuntime(context.Background(), mustParseUUID(t, runtimeID))
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	rows, err := service.ClaimOpsCommands(context.Background(), testHandler.Queries, rt.ID, 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("claim: rows=%d err=%v", len(rows), err)
	}

	w := httptest.NewRecorder()
	testHandler.ReportOpsCommandResult(w, withURLParams(
		newDaemonTokenRequest(http.MethodPost,
			"/api/daemon/runtimes/"+runtimeID+"/ops-commands/"+commandID+"/result",
			map[string]any{"status": "succeeded", "exit_code": 0, "stdout": "root 1 0.0 ps\n", "lease_epoch": rows[0].LeaseEpoch},
			testWorkspaceID, daemonID),
		"runtimeId", runtimeID, "commandId", commandID,
	))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var audits int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM audit_event
		WHERE runtime_id = $1 AND event_type = 'ops.command_finalized' AND actor_type = 'system'
	`, mustParseUUID(t, runtimeID)).Scan(&audits); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audits != 1 {
		t.Fatalf("audit_event finalized rows = %d, want 1", audits)
	}
}

// TestOpsSweep_ExpiredPendingNeverRedelivered covers assertion 5: a pending
// row past its TTL finalizes to expired, and a later claim (e.g. after node
// recovery) delivers nothing.
func TestOpsSweep_ExpiredPendingNeverRedelivered(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, daemonID := createOpsChannelTestRuntime(t, "online")
	commandID := insertOpsChannelCommand(t, testWorkspaceID, runtimeID, daemonID, "df_report")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE daemon_ops_command SET created_at = now() - interval '10 minutes' WHERE id = $1`,
		mustParseUUID(t, commandID)); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	expired, timedOut := service.SweepOpsCommands(context.Background(), testHandler.Queries)
	if expired < 1 {
		t.Fatalf("expired = %d, want >= 1 (timedOut=%d)", expired, timedOut)
	}

	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM daemon_ops_command WHERE id = $1`,
		mustParseUUID(t, commandID)).Scan(&status); err != nil {
		t.Fatalf("read command: %v", err)
	}
	if status != "expired" {
		t.Fatalf("status = %s, want expired", status)
	}

	rt, err := testHandler.Queries.GetAgentRuntime(context.Background(), mustParseUUID(t, runtimeID))
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	rows, err := service.ClaimOpsCommands(context.Background(), testHandler.Queries, rt.ID, service.OpsCommandMaxPerBeat)
	if err != nil {
		t.Fatalf("claim after expiry: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("claim after expiry returned %d rows, want 0 (never re-dispatch)", len(rows))
	}
}

func TestOpsSweep_ClaimedPastLeaseTimesOut(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, daemonID := createOpsChannelTestRuntime(t, "online")
	commandID := insertOpsChannelCommand(t, testWorkspaceID, runtimeID, daemonID, "df_report")
	if _, err := testPool.Exec(context.Background(), `
		UPDATE daemon_ops_command
		SET status = 'claimed', lease_epoch = 1, claimed_at = now() - interval '10 minutes'
		WHERE id = $1
	`, mustParseUUID(t, commandID)); err != nil {
		t.Fatalf("force claimed: %v", err)
	}

	_, timedOut := service.SweepOpsCommands(context.Background(), testHandler.Queries)
	if timedOut < 1 {
		t.Fatalf("timedOut = %d, want >= 1", timedOut)
	}

	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM daemon_ops_command WHERE id = $1`,
		mustParseUUID(t, commandID)).Scan(&status); err != nil {
		t.Fatalf("read command: %v", err)
	}
	if status != "timeout" {
		t.Fatalf("status = %s, want timeout", status)
	}
}

func TestGetOpsInventory_NoReportForm(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// No daemon_service_inventory row for this daemon: the surface must say
	// unavailable with "no report received", never an empty ok list.
	daemonID := fmt.Sprintf("ops-no-report-daemon-%d", time.Now().UnixNano())
	var runtimeID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', 'claude', 'online', 'Ops No Report', '{"ops_caps":["systemd"]}'::jsonb, $4, now())
		RETURNING id
	`, testWorkspaceID, daemonID, fmt.Sprintf("runtime-ops-no-report-%d", time.Now().UnixNano()), testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	w := httptest.NewRecorder()
	testHandler.GetRuntimeOpsInventory(w, withURLParams(
		newRequestAsUser(testUserID, http.MethodGet, "/api/runtimes/"+runtimeID+"/ops/inventory", nil),
		"runtimeId", runtimeID,
	))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	out := decodeOpsResponse(t, w)
	if out["collect_status"] != "unavailable" || out["error"] != "no report received" {
		t.Fatalf("inventory = %v, want unavailable / no report received", out)
	}
}

func TestGetOpsInventory_ReturnsReportedServices(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, _ := createOpsChannelTestRuntime(t, "online")

	w := httptest.NewRecorder()
	testHandler.GetRuntimeOpsInventory(w, withURLParams(
		newRequestAsUser(testUserID, http.MethodGet, "/api/runtimes/"+runtimeID+"/ops/inventory", nil),
		"runtimeId", runtimeID,
	))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	out := decodeOpsResponse(t, w)
	if out["collect_status"] != "ok" {
		t.Fatalf("collect_status = %v, want ok", out["collect_status"])
	}
	services, ok := out["services"].([]any)
	if !ok || len(services) != 1 {
		t.Fatalf("services = %v, want 1 entry", out["services"])
	}
	first, _ := services[0].(map[string]any)
	if first["name"] != "multica-server.service" {
		t.Fatalf("service name = %v, want multica-server.service", first["name"])
	}
	if _, ok := out["last_success_at"]; !ok {
		t.Fatalf("last_success_at missing from inventory response: %v", out)
	}
}

func TestReportDaemonInventory_OkAndUnavailableForms(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	_, daemonID := createOpsChannelTestRuntime(t, "online")

	inventoryRequest := func(collectStatus string, services any) *http.Request {
		return newDaemonTokenRequest(http.MethodPost, "/api/daemon/inventory", map[string]any{
			"daemon_id":      daemonID,
			"collect_status": collectStatus,
			"services":       services,
		}, testWorkspaceID, daemonID)
	}

	w := httptest.NewRecorder()
	testHandler.ReportDaemonInventory(w, inventoryRequest("ok", []map[string]any{{
		"name": "multica-server.service", "type": "systemd", "status": "active", "detail": "report ok",
	}}))
	if w.Code != http.StatusOK {
		t.Fatalf("ok report: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var collectStatus string
	var servicesRaw []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT collect_status, services FROM daemon_service_inventory WHERE daemon_id = $1
	`, daemonID).Scan(&collectStatus, &servicesRaw); err != nil {
		t.Fatalf("read inventory: %v", err)
	}
	if collectStatus != "ok" {
		t.Fatalf("collect_status = %s, want ok", collectStatus)
	}

	// An unavailable report must overwrite with an empty services array, so
	// the Web surface renders the unavailable form, not stale rows.
	w = httptest.NewRecorder()
	testHandler.ReportDaemonInventory(w, inventoryRequest("unavailable", []map[string]any{{
		"name": "stale.service",
	}}))
	if w.Code != http.StatusOK {
		t.Fatalf("unavailable report: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT collect_status, services FROM daemon_service_inventory WHERE daemon_id = $1
	`, daemonID).Scan(&collectStatus, &servicesRaw); err != nil {
		t.Fatalf("read inventory: %v", err)
	}
	if collectStatus != "unavailable" || string(servicesRaw) != "[]" {
		t.Fatalf("after unavailable: status=%s services=%s, want unavailable/[]", collectStatus, servicesRaw)
	}
}

func TestReportDaemonInventory_UnknownDaemonReturns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	testHandler.ReportDaemonInventory(w, newDaemonTokenRequest(http.MethodPost, "/api/daemon/inventory", map[string]any{
		"daemon_id":      fmt.Sprintf("ops-unknown-daemon-%d", time.Now().UnixNano()),
		"collect_status": "ok",
		"services":       []map[string]any{},
	}, testWorkspaceID, "ops-unknown-daemon-caller"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestDaemonHeartbeat_HTTPResponseCarriesPendingOpsCommands pins the QA escape
// (RUYI-645): claimOpsCommands attaches claimed commands to the shared ack, but
// the HTTP heartbeat handler re-projects the ack into a response map and dropped
// pending_ops_commands — HTTP-heartbeating daemons never saw their claimed rows,
// so every command died in sweep timeout. The WS path returns the ack struct
// directly and was unaffected; this regression guards the HTTP projection.
func TestDaemonHeartbeat_HTTPResponseCarriesPendingOpsCommands(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID, daemonID := createOpsChannelTestRuntime(t, "online")
	commandID := insertOpsChannelCommand(t, testWorkspaceID, runtimeID, daemonID, "ps_snapshot")

	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", map[string]any{
		"runtime_id": runtimeID,
	}, testWorkspaceID, daemonID)
	w := testutil.Call(t, testHandler.DaemonHeartbeat, req).Want(http.StatusOK)

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode heartbeat response %q: %v", w.Body.String(), err)
	}
	raw, ok := resp["pending_ops_commands"]
	if !ok {
		t.Fatalf("heartbeat response dropped pending_ops_commands: %s", w.Body.String())
	}
	dispatches, ok := raw.([]any)
	if !ok || len(dispatches) != 1 {
		t.Fatalf("pending_ops_commands = %#v, want exactly one dispatch", raw)
	}
	dispatch, _ := dispatches[0].(map[string]any)
	if dispatch["command_id"] != commandID {
		t.Fatalf("dispatch command_id = %v, want %s", dispatch["command_id"], commandID)
	}
	if dispatch["kind"] != "ps_snapshot" {
		t.Fatalf("dispatch kind = %v, want ps_snapshot", dispatch["kind"])
	}
	if _, ok := dispatch["lease_epoch"].(float64); !ok {
		t.Fatalf("dispatch lease_epoch missing or not a number: %#v", dispatch["lease_epoch"])
	}

	// The claim runs inside processHeartbeat, so the row must already be
	// claimed after this beat — proving the response projection was the only
	// loss point on the HTTP path.
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM daemon_ops_command WHERE id = $1`, commandID).Scan(&status); err != nil {
		t.Fatalf("read command row: %v", err)
	}
	if status != "claimed" {
		t.Fatalf("command status = %q after HTTP heartbeat, want claimed", status)
	}
}
