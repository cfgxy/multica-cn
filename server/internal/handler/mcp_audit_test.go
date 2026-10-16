package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const mcpAuditTestCredential = "test-mcp-audit-service-credential"

func callMCPAudit(t *testing.T, handler http.HandlerFunc, userID string, body any, credential string) (int, mcpAuditWriteResponse) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/audit", bytes.NewReader(encoded))
	req.Header.Set("X-User-ID", userID)
	if credential != "" {
		req.Header.Set("X-Multica-MCP-Service-Credential", credential)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	var response mcpAuditWriteResponse
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	return recorder.Code, response
}

func TestMCPAuditWriteRequiresPrivateCredentialAndRejectsUnknownFields(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	previous := testHandler.cfg.MCPAuditServiceCredential
	testHandler.cfg.MCPAuditServiceCredential = mcpAuditTestCredential
	t.Cleanup(func() { testHandler.cfg.MCPAuditServiceCredential = previous })

	request := map[string]any{
		"invocation_id": uuid.NewString(), "workspace_id": testWorkspaceID,
		"tool_name": "list_issues", "actor_id": uuid.NewString(),
	}
	if status, _ := callMCPAudit(t, testHandler.StartMCPInvocationAudit, testUserID, request, "wrong"); status != http.StatusUnauthorized {
		t.Fatalf("wrong service credential: got %d, want 401", status)
	}
	if status, _ := callMCPAudit(t, testHandler.StartMCPInvocationAudit, testUserID, request, mcpAuditTestCredential); status != http.StatusBadRequest {
		t.Fatalf("unknown actor field: got %d, want 400", status)
	}
}

func TestMCPAuditInvocationStartFinishAreBoundToActorAndIdempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	previous := testHandler.cfg.MCPAuditServiceCredential
	testHandler.cfg.MCPAuditServiceCredential = mcpAuditTestCredential
	t.Cleanup(func() { testHandler.cfg.MCPAuditServiceCredential = previous })

	id := uuid.New()
	start := map[string]any{
		"invocation_id": id.String(), "workspace_id": testWorkspaceID,
		"tool_name": "read_file", "target_type": "host", "target_id": "local",
		"normalized_path": "/workspace/private/report.txt",
	}
	if status, response := callMCPAudit(t, testHandler.StartMCPInvocationAudit, testUserID, start, mcpAuditTestCredential); status != http.StatusCreated || response.Status != "recorded" {
		t.Fatalf("start audit: status=%d response=%+v", status, response)
	}
	if status, response := callMCPAudit(t, testHandler.StartMCPInvocationAudit, testUserID, start, mcpAuditTestCredential); status != http.StatusConflict || response.Status != "already_recorded" {
		t.Fatalf("duplicate start: status=%d response=%+v", status, response)
	}
	stored, err := testHandler.Queries.GetMCPInvocationAuditStart(t.Context(), pgtype.UUID{Bytes: id, Valid: true})
	if err != nil || !stored.Sensitive {
		t.Fatalf("sensitive start row not stored: row=%+v err=%v", stored, err)
	}

	otherUser := dbfx.User(t, "mcp audit other "+handlerFixtureSuffix, "mcp-audit-other-"+handlerFixtureSuffix+"@example.test")
	finish := map[string]any{"invocation_id": id.String(), "status": "failure", "duration_ms": 27, "error_code": "unavailable"}
	if status, _ := callMCPAudit(t, testHandler.FinishMCPInvocationAudit, otherUser, finish, mcpAuditTestCredential); status != http.StatusForbidden {
		t.Fatalf("cross-actor finish: got %d, want 403", status)
	}
	if status, response := callMCPAudit(t, testHandler.FinishMCPInvocationAudit, testUserID, finish, mcpAuditTestCredential); status != http.StatusOK || response.Status != "recorded" {
		t.Fatalf("finish audit: status=%d response=%+v", status, response)
	}
	if status, response := callMCPAudit(t, testHandler.FinishMCPInvocationAudit, testUserID, finish, mcpAuditTestCredential); status != http.StatusOK || response.Status != "recorded" {
		t.Fatalf("duplicate finish must be idempotent: status=%d response=%+v", status, response)
	}
	var count int64
	err = testPool.QueryRow(t.Context(), `SELECT count(*) FROM audit_event WHERE invocation_id = $1`, pgtype.UUID{Bytes: id, Valid: true}).Scan(&count)
	if err != nil || count != 2 {
		t.Fatalf("invocation should have exactly start+finish rows: count=%d err=%v", count, err)
	}
}

func TestMCPFinishAuditFailureIsQueuedAndDrained(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	id := uuid.New()
	invocationID := pgtype.UUID{Bytes: id, Valid: true}
	actorID, err := util.ParseUUID(testUserID)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, err := util.ParseUUID(testWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	start := service.MCPInvocationAuditEvent(service.MCPInvocationAuditContext{
		InvocationID: invocationID, WorkspaceID: workspaceID,
		ActorType: service.AuditActorMember, ActorID: actorID,
	}, service.AuditMCPInvocationStarted, map[string]any{"tool_name": "list_issues"}, false)
	if inserted, err := service.AppendMCPInvocationAuditEvent(ctx, testHandler.Queries, start); err != nil || !inserted {
		t.Fatalf("persist audit start: inserted=%t err=%v", inserted, err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM mcp_invocation_audit_outbox WHERE invocation_id = $1`, invocationID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM audit_event WHERE invocation_id = $1`, invocationID)
	})

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "test_mcp_audit_reject_" + suffix
	triggerName := "test_mcp_audit_reject_" + suffix
	function := `CREATE FUNCTION ` + functionName + `() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.invocation_id = '` + id.String() + `'::uuid AND NEW.event_type = 'mcp.invocation_finished' THEN
        RAISE EXCEPTION 'synthetic audit insert failure';
    END IF;
    RETURN NEW;

END $$`
	if _, err := testPool.Exec(ctx, function); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `CREATE TRIGGER `+triggerName+` BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION `+functionName+`() `); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DROP TRIGGER IF EXISTS `+triggerName+` ON audit_event`)
		_, _ = testPool.Exec(context.Background(), `DROP FUNCTION IF EXISTS `+functionName+`() `)
	})
	finish := service.MCPInvocationAuditEvent(service.MCPInvocationAuditContext{
		InvocationID: invocationID, WorkspaceID: workspaceID,
		ActorType: service.AuditActorMember, ActorID: actorID,
	}, service.AuditMCPInvocationFinished, map[string]any{"status": "success", "duration_ms": 5}, false)
	if status := service.FinishMCPInvocationAudit(ctx, testHandler.Queries, finish); status != "queued" {
		t.Fatalf("finish write failure status = %q, want queued", status)
	}
	var queued int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM mcp_invocation_audit_outbox WHERE invocation_id = $1`, invocationID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("finish failure should persist one outbox row: count=%d err=%v", queued, err)
	}
	if _, err := testPool.Exec(ctx, `DROP TRIGGER `+triggerName+` ON audit_event`); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `DROP FUNCTION `+functionName+`() `); err != nil {
		t.Fatal(err)
	}
	service.DrainMCPInvocationAuditOutbox(ctx, testHandler.Queries, 10)
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE invocation_id = $1 AND event_type = 'mcp.invocation_finished'`, invocationID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("outbox drain should restore the finish event: count=%d err=%v", queued, err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM mcp_invocation_audit_outbox WHERE invocation_id = $1`, invocationID).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("successful retry should remove outbox row: count=%d err=%v", queued, err)
	}
}

func TestWorkspaceLeafCleanupDeletesMCPAuditOutbox(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	workspaceID := dbfx.Workspace(t, "MCP audit outbox cleanup", "mcp-audit-outbox-"+strings.ReplaceAll(uuid.NewString(), "-", ""))
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	invocationID := uuid.New()
	actorID, err := util.ParseUUID(testUserID)
	if err != nil {
		t.Fatal(err)
	}
	event := service.MCPInvocationAuditEvent(service.MCPInvocationAuditContext{
		InvocationID: pgtype.UUID{Bytes: invocationID, Valid: true},
		WorkspaceID:  workspaceUUID, ActorType: service.AuditActorMember, ActorID: actorID,
	}, service.AuditMCPInvocationFinished, map[string]any{"status": "failure", "duration_ms": 2}, false)
	if err := testHandler.Queries.QueueMCPInvocationAuditEvent(t.Context(), db.QueueMCPInvocationAuditEventParams{
		ID: event.ID, WorkspaceID: event.WorkspaceID, EventType: event.EventType,
		OccurredAt: pgtype.Timestamptz{Time: event.OccurredAt, Valid: true},
		ActorType:  event.ActorType, ActorID: event.ActorID, InvocationID: event.InvocationID,
		Details: event.Details, ExpiresAt: event.ExpiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := testHandler.Queries.DeleteWorkspaceLeafData(t.Context(), workspaceUUID); err != nil {
		t.Fatalf("delete workspace leaf data: %v", err)
	}
	var count int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM mcp_invocation_audit_outbox WHERE workspace_id = $1`, workspaceUUID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("workspace outbox rows after cleanup = %d, err=%v", count, err)
	}
}
