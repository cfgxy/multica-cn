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
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/fileaccess"
)

func TestResolveFileAccessTargetByHost(t *testing.T) {
	runtimeID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	target := db.DaemonFileAccessTarget{ID: "target-a", RuntimeID: runtimeID}
	runtimeHost := uuidToString(runtimeID)
	tests := []struct {
		name    string
		host    string
		targets []db.DaemonFileAccessTarget
		wantID  string
		wantErr bool
	}{
		{name: "target id", host: target.ID, targets: []db.DaemonFileAccessTarget{target}, wantID: target.ID},
		{name: "runtime id with one target", host: runtimeHost, targets: []db.DaemonFileAccessTarget{target}, wantID: target.ID},
		{name: "missing host", targets: []db.DaemonFileAccessTarget{target}, wantErr: true},
		{name: "unknown host", host: "unknown", wantErr: true},
		{name: "ambiguous runtime id", host: runtimeHost, targets: []db.DaemonFileAccessTarget{target, {ID: "target-b", RuntimeID: runtimeID}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveFileAccessTargetByHost(tt.host, tt.targets)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected host resolution to fail")
				}
				if got.ID != "" {
					t.Fatalf("failed resolution returned target %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != tt.wantID {
				t.Fatalf("resolved target id = %q, want %q", got.ID, tt.wantID)
			}
		})
	}
}

func TestListRuntimeFilesRejectsMissingHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/runtimes/runtime-path/files/list", strings.NewReader(`{"path":"/tmp"}`))
	response := httptest.NewRecorder()
	(&Handler{}).ListRuntimeFiles(response, req)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), fileaccess.ErrorHostNotRegistered.Error()) {
		t.Fatalf("response = %q, want missing-host error", response.Body.String())
	}
}

func TestPutRuntimeFileAccessTargetDBBacked(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "file access target owner", testutil.Cols{"visibility": "public"})
	targetID := "files-" + uuid.NewString()
	body := func(id string) map[string]any {
		return map[string]any{"id": id, "enabled": true, "roots": []string{"/srv/data"}}
	}
	request := func(id string) *http.Request {
		return testutil.WithURLParams(newRequest(http.MethodPut, "/api/runtimes/"+runtimeID+"/file-access-targets", body(id)), "runtimeId", runtimeID)
	}

	testutil.Call(t, testHandler.PutRuntimeFileAccessTarget, request(targetID)).Want(http.StatusOK)
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM daemon_file_access_target WHERE id = $1`, targetID) })
	if got := dbfx.Count(t, `SELECT count(*) FROM daemon_file_access_target WHERE id = $1 AND runtime_id = $2`, targetID, runtimeID); got != 1 {
		t.Fatalf("registered target count = %d, want 1", got)
	}
	memberID := dbfx.User(t, "File target registration member", "file-target-member+"+uuid.NewString()+"@multica.test")
	dbfx.Member(t, testWorkspaceID, memberID, "member")
	memberTargetID := "member-target-" + uuid.NewString()
	memberRequest := testutil.WithURLParams(newRequestAs(memberID, http.MethodPut, "/api/runtimes/"+runtimeID+"/file-access-targets", body(memberTargetID)), "runtimeId", runtimeID)
	testutil.Call(t, testHandler.PutRuntimeFileAccessTarget, memberRequest).Want(http.StatusForbidden)
	if got := dbfx.Count(t, `SELECT count(*) FROM daemon_file_access_target WHERE id = $1`, memberTargetID); got != 0 {
		t.Fatalf("non-admin target registration was persisted; count = %d", got)
	}

	otherRuntimeID := dbfx.Runtime(t, "file access target other runtime")
	otherRequest := testutil.WithURLParams(newRequest(http.MethodPut, "/api/runtimes/"+otherRuntimeID+"/file-access-targets", body(targetID)), "runtimeId", otherRuntimeID)
	testutil.Call(t, testHandler.PutRuntimeFileAccessTarget, otherRequest).Want(http.StatusConflict)
	if got := dbfx.Count(t, `SELECT count(*) FROM daemon_file_access_target WHERE id = $1 AND runtime_id = $2`, targetID, runtimeID); got != 1 {
		t.Fatalf("conflicting registration changed original binding; count = %d", got)
	}

	invalid := body("bad-root")
	invalid["roots"] = []string{"relative/path"}
	invalidRequest := testutil.WithURLParams(newRequest(http.MethodPut, "/api/runtimes/"+runtimeID+"/file-access-targets", invalid), "runtimeId", runtimeID)
	testutil.Call(t, testHandler.PutRuntimeFileAccessTarget, invalidRequest).Want(http.StatusBadRequest)
	if got := dbfx.Count(t, `SELECT count(*) FROM daemon_file_access_target WHERE id = 'bad-root'`); got != 0 {
		t.Fatalf("invalid target was persisted; count = %d", got)
	}
}

func TestSubmitRuntimeFileAccessDBBackedRejectionsAndQueue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "file access submission runtime", testutil.Cols{"visibility": "public"})
	targetID := "files-" + uuid.NewString()
	memberOnlyID := "member-only-" + uuid.NewString()
	disabledID := "disabled-" + uuid.NewString()
	registerFileAccessTarget(t, runtimeID, targetID, true, []string{"/srv/data"}, []string{testWorkspaceID}, nil)
	deniedUserID := dbfx.User(t, "File ACL denied", "file-acl-denied+"+uuid.NewString()+"@multica.test")
	dbfx.Member(t, testWorkspaceID, deniedUserID, "member")
	registerFileAccessTarget(t, runtimeID, memberOnlyID, true, []string{"/srv/data"}, []string{testWorkspaceID}, []string{testUserID})
	registerFileAccessTarget(t, runtimeID, disabledID, false, []string{"/srv/data"}, []string{testWorkspaceID}, nil)

	unknown := submitFilesRequest(runtimeID, "missing-"+uuid.NewString(), "/srv/data/a.txt", testUserID)
	wantFileAccessError(t, testHandler.ListRuntimeFiles, unknown, http.StatusNotFound, fileaccess.ErrorHostNotRegistered.Error())
	wantFileAccessError(t, testHandler.ListRuntimeFiles, submitFilesRequest(runtimeID, disabledID, "/srv/data/a.txt", testUserID), http.StatusForbidden, fileaccess.ErrorTargetDisabled.Error())
	wantFileAccessError(t, testHandler.ListRuntimeFiles, submitFilesRequest(runtimeID, memberOnlyID, "/srv/data/a.txt", deniedUserID), http.StatusForbidden, fileaccess.ErrorPermissionDenied.Error())
	wantFileAccessError(t, testHandler.ListRuntimeFiles, submitFilesRequest(runtimeID, targetID, "/etc/passwd", testUserID), http.StatusForbidden, fileaccess.ErrorPathNotAllowed.Error())
	if got := dbfx.Count(t, `SELECT count(*) FROM daemon_file_operation WHERE runtime_id = $1`, runtimeID); got != 0 {
		t.Fatalf("rejected requests queued %d operations, want 0", got)
	}

	dbfx.Exec(t, `UPDATE agent_runtime SET status = 'offline' WHERE id = $1`, runtimeID)
	wantFileAccessError(t, testHandler.ListRuntimeFiles, submitFilesRequest(runtimeID, targetID, "/srv/data/a.txt", testUserID), http.StatusConflict, fileaccess.ErrorTargetUnreachable.Error())
	dbfx.Exec(t, `UPDATE agent_runtime SET status = 'online' WHERE id = $1`, runtimeID)

	for i := 0; i < 5; i++ {
		insertFileOperation(t, runtimeID, targetID, "file_list", "pending", 0)
	}
	wantFileAccessError(t, testHandler.ListRuntimeFiles, submitFilesRequest(runtimeID, targetID, "/srv/data/a.txt", testUserID), http.StatusTooManyRequests, "rate_limited")
	if got := dbfx.Count(t, `SELECT count(*) FROM daemon_file_operation WHERE runtime_id = $1 AND status = 'pending'`, runtimeID); got != 5 {
		t.Fatalf("quota rejection left %d pending operations, want 5", got)
	}
	dbfx.Exec(t, `UPDATE daemon_file_operation SET status = 'failed' WHERE runtime_id = $1 AND status = 'pending'`, runtimeID)

	var response struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
		TargetID    string `json:"target_id"`
		Path        string `json:"path"`
	}
	testutil.Call(t, testHandler.ListRuntimeFiles, submitFilesRequest(runtimeID, targetID, "/srv/data/./a.txt", testUserID)).Want(http.StatusAccepted).JSON(&response)
	if response.OperationID == "" || response.Status != "pending" || response.TargetID != targetID || response.Path != "/srv/data/a.txt" {
		t.Fatalf("queued operation response = %+v", response)
	}
	var storedPath string
	dbfx.QueryRow(t, `SELECT request->>'path' FROM daemon_file_operation WHERE id = $1`, response.OperationID).Scan(&storedPath)
	if storedPath != response.Path {
		t.Fatalf("stored path = %q, response path = %q", storedPath, response.Path)
	}
}

func TestGetRuntimeFileOperationIsolatedByRuntimeAndACL(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "file operation query runtime", testutil.Cols{"visibility": "public"})
	otherRuntimeID := dbfx.Runtime(t, "file operation other runtime", testutil.Cols{"visibility": "public"})
	targetID := "query-target-" + uuid.NewString()
	registerFileAccessTarget(t, runtimeID, targetID, true, []string{"/srv/data"}, []string{testWorkspaceID}, []string{testUserID})
	opID := insertFileOperation(t, runtimeID, targetID, "file_list", "pending", 0)

	allowedRequest := testutil.WithURLParams(newRequest(http.MethodGet, "/api/runtimes/"+runtimeID+"/file-operations/"+opID, nil), "runtimeId", runtimeID, "operationId", opID)
	var operation struct {
		OperationID string `json:"operation_id"`
		TargetID    string `json:"target_id"`
		Status      string `json:"status"`
	}
	testutil.Call(t, testHandler.GetRuntimeFileOperation, allowedRequest).Want(http.StatusOK).JSON(&operation)
	if operation.OperationID != opID || operation.TargetID != targetID || operation.Status != "pending" {
		t.Fatalf("authorized operation response = %+v", operation)
	}

	request := testutil.WithURLParams(newRequest(http.MethodGet, "/api/runtimes/"+otherRuntimeID+"/file-operations/"+opID, nil), "runtimeId", otherRuntimeID, "operationId", opID)
	wantFileAccessError(t, testHandler.GetRuntimeFileOperation, request, http.StatusNotFound, "operation not found")

	deniedID := dbfx.User(t, "File operation ACL denied", "file-operation-acl+"+uuid.NewString()+"@multica.test")
	dbfx.Member(t, testWorkspaceID, deniedID, "member")
	deniedRequest := testutil.WithURLParams(newRequestAs(deniedID, http.MethodGet, "/api/runtimes/"+runtimeID+"/file-operations/"+opID, nil), "runtimeId", runtimeID, "operationId", opID)
	wantFileAccessError(t, testHandler.GetRuntimeFileOperation, deniedRequest, http.StatusNotFound, "operation not found")
}

func TestMCPFileAccessQueuesAuditedOperationAndEnforcesTargetACL(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	const instanceID = "mcp-file-access-test"
	const instanceCredential = "test-mcp-file-access-instance-credential"
	previousAuditCredential := testHandler.cfg.MCPAuditServiceCredential
	previousInstanceCredentials := testHandler.cfg.MCPFileAccessInstanceCredentials
	testHandler.cfg.MCPAuditServiceCredential = mcpAuditTestCredential
	testHandler.cfg.MCPFileAccessInstanceCredentials = map[string]string{instanceID: instanceCredential}
	t.Cleanup(func() {
		testHandler.cfg.MCPAuditServiceCredential = previousAuditCredential
		testHandler.cfg.MCPFileAccessInstanceCredentials = previousInstanceCredentials
	})

	runtimeID := dbfx.Runtime(t, "MCP file access runtime", testutil.Cols{"visibility": "public"})
	targetID := "mcp-files-" + uuid.NewString()
	registerFileAccessTarget(t, runtimeID, targetID, true, []string{"/srv/data"}, []string{testWorkspaceID}, []string{testUserID})

	startInvocation := func(actorID, toolName string) string {
		t.Helper()
		invocationID := uuid.NewString()
		body, err := json.Marshal(map[string]any{
			"invocation_id": invocationID, "workspace_id": testWorkspaceID,
			"tool_name": toolName, "instance_id": instanceID,
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/mcp/audit-start", bytes.NewReader(body))
		request.Header.Set("X-User-ID", actorID)
		request.Header.Set("X-Multica-MCP-Service-Credential", mcpAuditTestCredential)
		request.Header.Set("X-Multica-MCP-Instance-ID", instanceID)
		request.Header.Set("X-Multica-MCP-Instance-Credential", instanceCredential)
		recorder := httptest.NewRecorder()
		testHandler.StartMCPInvocationAudit(recorder, request)
		var response mcpAuditWriteResponse
		_ = json.Unmarshal(recorder.Body.Bytes(), &response)
		if recorder.Code != http.StatusCreated || response.Status != "recorded" {
			t.Fatalf("start MCP file invocation: status=%d response=%+v", recorder.Code, response)
		}
		return invocationID
	}

	invocationID := startInvocation(testUserID, "list_files")
	writeRequest := newRequest(http.MethodPost, "/api/file-access/list", map[string]any{
		"invocation_id": invocationID, "host": targetID, "path": "/srv/data/./report.txt",
	})
	writeRequest.Header.Set("X-Multica-MCP-Instance-ID", instanceID)
	writeRequest.Header.Set("X-Multica-MCP-Instance-Credential", instanceCredential)
	var queued struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}
	testutil.Call(t, testHandler.ListMCPFiles, writeRequest).Want(http.StatusAccepted).JSON(&queued)
	if queued.OperationID == "" || queued.Status != "pending" {
		t.Fatalf("queued MCP file operation = %+v", queued)
	}
	var storedTargetID, storedPath, storedInvocationID string
	if err := testPool.QueryRow(t.Context(), `
		SELECT target_id, request->>'path', invocation_id::text
		FROM daemon_file_operation WHERE id = $1
	`, queued.OperationID).Scan(&storedTargetID, &storedPath, &storedInvocationID); err != nil {
		t.Fatal(err)
	}
	if storedTargetID != targetID || storedPath != "/srv/data/report.txt" || storedInvocationID != invocationID {
		t.Fatalf("stored operation target/path/invocation = %q/%q/%q", storedTargetID, storedPath, storedInvocationID)
	}

	if _, err := testPool.Exec(t.Context(), `UPDATE daemon_file_operation SET status = 'succeeded', result = $2 WHERE id = $1`, queued.OperationID,
		[]byte(`{"entries":[{"name":"report.txt"}],"private_daemon_field":"must-not-project"}`)); err != nil {
		t.Fatal(err)
	}
	readRequest := newRequest(http.MethodGet, "/api/file-access/operations/"+queued.OperationID+"?invocation_id="+invocationID, nil)
	readRequest.Header.Set("X-Multica-MCP-Instance-ID", instanceID)
	readRequest.Header.Set("X-Multica-MCP-Instance-Credential", instanceCredential)
	readRequest = withURLParam(readRequest, "operationId", queued.OperationID)
	var projected struct {
		OperationID string         `json:"operation_id"`
		Status      string         `json:"status"`
		Result      map[string]any `json:"result"`
	}
	testutil.Call(t, testHandler.GetMCPFileOperation, readRequest).Want(http.StatusOK).JSON(&projected)
	if projected.OperationID != queued.OperationID || projected.Status != "succeeded" || projected.Result["entries"] == nil {
		t.Fatalf("MCP file operation projection = %+v", projected)
	}
	if _, leaked := projected.Result["private_daemon_field"]; leaked {
		t.Fatalf("MCP file result exposed an unapproved daemon field: %+v", projected.Result)
	}

	finishBody, err := json.Marshal(map[string]any{
		"invocation_id": invocationID, "operation_id": queued.OperationID,
		"status": "success", "duration_ms": 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	finishRequest := httptest.NewRequest(http.MethodPost, "/api/mcp/audit-finish", bytes.NewReader(finishBody))
	finishRequest.Header.Set("X-User-ID", testUserID)
	finishRequest.Header.Set("X-Multica-MCP-Service-Credential", mcpAuditTestCredential)
	finishRequest.Header.Set("X-Multica-MCP-Instance-ID", instanceID)
	finishRequest.Header.Set("X-Multica-MCP-Instance-Credential", instanceCredential)
	finishRecorder := httptest.NewRecorder()
	testHandler.FinishMCPInvocationAudit(finishRecorder, finishRequest)
	var finishResponse mcpAuditWriteResponse
	_ = json.Unmarshal(finishRecorder.Body.Bytes(), &finishResponse)
	if finishRecorder.Code != http.StatusOK || finishResponse.Status != "recorded" {
		t.Fatalf("finish MCP file invocation: status=%d response=%+v", finishRecorder.Code, finishResponse)
	}
	var finishDetailsJSON []byte
	if err := testPool.QueryRow(t.Context(), `
		SELECT details FROM audit_event WHERE invocation_id = $1 AND event_type = 'mcp.invocation_finished'
	`, invocationID).Scan(&finishDetailsJSON); err != nil {
		t.Fatal(err)
	}
	var finishDetails map[string]any
	if err := json.Unmarshal(finishDetailsJSON, &finishDetails); err != nil {
		t.Fatal(err)
	}
	if finishDetails["target_id"] != targetID || finishDetails["normalized_path"] != "/srv/data/report.txt" || finishDetails["operation_id"] != queued.OperationID {
		t.Fatalf("finish audit did not derive fields from the stored operation: %+v", finishDetails)
	}

	deniedUserID := dbfx.User(t, "MCP file ACL denied", "mcp-file-acl-denied+"+uuid.NewString()+"@multica.test")
	dbfx.Member(t, testWorkspaceID, deniedUserID, "member")
	deniedInvocationID := startInvocation(deniedUserID, "list_files")
	deniedRequest := newRequestAs(deniedUserID, http.MethodPost, "/api/file-access/list", map[string]any{
		"invocation_id": deniedInvocationID, "host": targetID, "path": "/srv/data/secret.txt",
	})
	deniedRequest.Header.Set("X-Multica-MCP-Instance-ID", instanceID)
	deniedRequest.Header.Set("X-Multica-MCP-Instance-Credential", instanceCredential)
	wantFileAccessError(t, testHandler.ListMCPFiles, deniedRequest, http.StatusForbidden, "permission_denied")
	if got := dbfx.Count(t, `SELECT count(*) FROM daemon_file_operation WHERE invocation_id = $1`, deniedInvocationID); got != 0 {
		t.Fatalf("ACL-denied MCP invocation queued %d operations, want 0", got)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM audit_event WHERE invocation_id = ANY($1::uuid[])`, []string{invocationID, deniedInvocationID})
	})
}

func TestReportFileOperationResultRequiresCurrentClaimLease(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "file operation receipt runtime")
	pendingID := insertFileOperation(t, runtimeID, "receipt-target", "file_list", "pending", 0)
	expiredID := insertFileOperation(t, runtimeID, "receipt-target", "file_list", "expired", 1)
	staleID := insertFileOperation(t, runtimeID, "receipt-target", "file_list", "claimed", 3)
	validID := insertFileOperation(t, runtimeID, "receipt-target", "file_list", "claimed", 3)

	report := func(id string, epoch int64) *http.Request {
		request := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/file-operations/"+id+"/result", map[string]any{
			"status": "succeeded", "lease_epoch": epoch, "result": map[string]any{"entries": []any{}},
		}, testWorkspaceID, "file-operation-receipt-test")
		return testutil.WithURLParams(request, "runtimeId", runtimeID, "operationId", id)
	}
	wantFileAccessError(t, testHandler.ReportFileOperationResult, report(pendingID, 1), http.StatusConflict, "file operation lease is stale")
	wantFileAccessError(t, testHandler.ReportFileOperationResult, report(expiredID, 1), http.StatusConflict, "file operation lease is stale")
	wantFileAccessError(t, testHandler.ReportFileOperationResult, report(staleID, 2), http.StatusConflict, "file operation lease is stale")
	testutil.Call(t, testHandler.ReportFileOperationResult, report(validID, 3)).Want(http.StatusOK)

	var status string
	dbfx.QueryRow(t, `SELECT status FROM daemon_file_operation WHERE id = $1`, validID).Scan(&status)
	if status != "succeeded" {
		t.Fatalf("valid lease receipt status = %q, want succeeded", status)
	}
	for _, id := range []string{pendingID, expiredID, staleID} {
		dbfx.QueryRow(t, `SELECT status FROM daemon_file_operation WHERE id = $1`, id).Scan(&status)
		if status == "succeeded" {
			t.Fatalf("rejected receipt changed operation %s to succeeded", id)
		}
	}
}

func registerFileAccessTarget(t *testing.T, runtimeID, id string, enabled bool, roots, workspaceIDs, memberIDs []string) {
	t.Helper()
	rootJSON, err := json.Marshal(roots)
	if err != nil {
		t.Fatal(err)
	}
	workspaceJSON, err := json.Marshal(workspaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	memberJSON, err := json.Marshal(memberIDs)
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Insert(t, "daemon_file_access_target", testutil.Cols{
		"id": id, "workspace_id": testWorkspaceID, "runtime_id": runtimeID, "enabled": enabled,
		"roots": rootJSON, "workspace_acl": workspaceJSON, "member_acl": memberJSON,
	})
}

func wantFileAccessError(t *testing.T, handler http.HandlerFunc, request *http.Request, status int, message string) {
	t.Helper()
	response := testutil.Call(t, handler, request).Want(status)
	if !strings.Contains(response.Body.String(), message) {
		t.Fatalf("response = %q, want error %q", response.Body.String(), message)
	}
}

func submitFilesRequest(runtimeID, targetID, path, userID string) *http.Request {
	request := newRequestAs(userID, http.MethodPost, "/api/runtimes/"+runtimeID+"/files/list", map[string]any{
		"host": targetID, "path": path,
	})
	return withURLParam(request, "runtimeId", runtimeID)
}

func insertFileOperation(t *testing.T, runtimeID, targetID, kind, status string, leaseEpoch int64) string {
	t.Helper()
	return dbfx.Insert(t, "daemon_file_operation", testutil.Cols{
		"id": uuid.NewString(), "workspace_id": testWorkspaceID, "runtime_id": runtimeID,
		"target_id": targetID, "kind": kind, "request": []byte(`{"path":"/srv/data/a.txt"}`),
		"status": status, "lease_epoch": leaseEpoch,
	})
}
