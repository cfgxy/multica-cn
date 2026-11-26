package handler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func operationBinding(t *testing.T) (string, string) {
	t.Helper()
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "operations", "host": "fake.invalid", "port": 22, "ssh_user": "operator", "created_by": testUserID})
	binding := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": testUserID, "workspace_id": testWorkspaceID, "username": "alice", "verified": true, "state": "ready"})
	t.Cleanup(func() {
		for _, table := range []string{"computer_audit", "computer_runtime_asset", "computer_runtime_model_config", "computer_operation"} {
			_, _ = testPool.Exec(context.Background(), "DELETE FROM "+table+" WHERE binding_id=$1", binding)
		}
	})
	return machine, binding
}
func bindingRequest(method, binding string, body any) *http.Request {
	return withURLParam(computerTestRequest(method, "/", body), "id", binding)
}

func TestComputerOperationsSerializeAllKindsAndSurviveCancellation(t *testing.T) {
	_, binding := operationBinding(t)
	ctx, cancel := context.WithCancel(context.Background())
	op, err := testHandler.beginBindingOperation(ctx, testUserID, binding, "runtime_install", "codex", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = testHandler.beginBindingOperation(context.Background(), testUserID, binding, "runtime_discovery", "", ""); err == nil {
		t.Fatal("concurrent discovery accepted")
	}
	testHandler.runRemoteOperation(op, func(ctx context.Context) (string, error) {
		if ctx.Err() != nil {
			t.Fatal("request cancellation reached worker")
		}
		return "codex 1.2.3", nil
	})
	var list []remoteOperation
	testutil.Call(t, testHandler.ComputerBindingOperations, bindingRequest("GET", binding, nil)).Want(200).JSON(&list)
	if len(list) != 1 || list[0].State != "succeeded" || list[0].ActualVersion != "codex 1.2.3" || list[0].FinishedAt == nil {
		t.Fatalf("unexpected operations: %+v", list)
	}
	var audit int
	if err = testPool.QueryRow(context.Background(), `SELECT count(*) FROM computer_audit WHERE binding_id=$1 AND outcome='succeeded'`, binding).Scan(&audit); err != nil || audit != 1 {
		t.Fatalf("audit=%d, err=%v", audit, err)
	}
}

func TestComputerOperationRecoveryRequiresExpiredOperation(t *testing.T) {
	_, binding := operationBinding(t)
	op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "upgrade", "", "")
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE computer_operation SET state='running' WHERE id=$1`, op)
	req := func() *http.Request {
		return bindingRequest("POST", binding, map[string]string{"operation_id": op, "action": "acknowledge"})
	}
	testutil.Call(t, testHandler.RecoverComputerOperation, req()).Want(409)
	dbfx.Exec(t, `UPDATE computer_operation SET deadline_at=now()-interval '1 minute' WHERE id=$1`, op)
	var list []remoteOperation
	testutil.Call(t, testHandler.ComputerBindingOperations, bindingRequest("GET", binding, nil)).Want(200).JSON(&list)
	if list[0].State != "interrupted" || list[0].ErrorCode != "interrupted" {
		t.Fatalf("stale operation: %+v", list[0])
	}
	testutil.Call(t, testHandler.RecoverComputerOperation, req()).Want(200)
	op, err = testHandler.beginBindingOperation(context.Background(), testUserID, binding, "upgrade", "", "")
	if err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.RecoverComputerOperation, bindingRequest("POST", binding, map[string]string{"operation_id": op, "action": "cancel"})).Want(200)
	ran := false
	testHandler.runRemoteOperation(op, func(context.Context) (string, error) { ran = true; return "", nil })
	if ran {
		t.Fatal("cancelled work was executed")
	}
}

func TestComputerRuntimeProbeFailurePreservesLastKnownAsset(t *testing.T) {
	_, binding := operationBinding(t)
	op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "runtime_discovery", "", "")
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE computer_operation SET state='running' WHERE id=$1`, op)
	target := runtimeTargets()[0]
	if err := testHandler.saveRuntimeProbe(context.Background(), op, binding, target, computer.RuntimeProbe{State: "installed", Path: "/home/alice/.local/bin/omp", Version: "1.2.3"}, "latest", true); err != nil {
		t.Fatal(err)
	}
	if err := testHandler.saveRuntimeProbe(context.Background(), op, binding, target, computer.RuntimeProbe{State: "check_failed", Code: "ssh_timeout"}, "", false); err != nil {
		t.Fatal(err)
	}
	list, err := testHandler.runtimeAssets(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	a := list[0]
	if a.ProbeState != "check_failed" || a.ActualVersion != "1.2.3" || a.ExecutablePath == "" || a.RequestedVersion != "latest" || a.CheckedAt == nil || a.InstalledAt == nil {
		t.Fatalf("asset lost after failure: %+v", a)
	}
	if a.RegistrationState != "not_discovered" {
		t.Fatalf("registration invented: %+v", a)
	}
	dbfx.Runtime(t, "matching asset", testutil.Cols{"daemon_id": binding, "owner_id": testUserID, "provider": target.id, "status": "online", "last_seen_at": time.Now()})
	list, err = testHandler.runtimeAssets(context.Background(), binding)
	if err != nil || list[0].RegistrationState != "online" {
		t.Fatalf("registration: %+v %v", list, err)
	}
}

func TestComputerBindingDetailAndHistoryOwnerBoundary(t *testing.T) {
	_, binding := operationBinding(t)
	for _, handler := range []http.HandlerFunc{testHandler.ComputerBindingDetail, testHandler.ComputerBindingOperations, testHandler.ComputerBindingRuntimes} {
		req := bindingRequest("GET", binding, nil)
		req.Header.Set("X-User-ID", "10000000-0000-0000-0000-000000000099")
		testutil.Call(t, handler, req).Want(404)
	}
	var d bindingDetail
	testutil.Call(t, testHandler.ComputerBindingDetail, bindingRequest("GET", binding, nil)).Want(200).JSON(&d)
	if d.DaemonID != binding || d.AccountState != "unknown" || d.DaemonState != "unknown" || !d.Verified || d.OperationBusy {
		t.Fatalf("detail: %+v", d)
	}
	op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "runtime_discovery", "", "")
	if err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.ComputerBindingDetail, bindingRequest("GET", binding, nil)).Want(200).JSON(&d)
	if !d.OperationBusy || d.LatestOperation == nil || d.LatestOperation.ID != op || d.LatestOperation.State != "queued" {
		t.Fatalf("detail omits active operation: %+v", d)
	}
	testutil.Call(t, testHandler.RecoverComputerOperation, bindingRequest("POST", binding, map[string]string{"operation_id": op, "action": "cancel"})).Want(200)
}

func TestComputerArchivePreservesOwnershipAndUnblocksComputerDeletion(t *testing.T) {
	machine, binding := operationBinding(t)
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", testUserID)
	req := func() *http.Request {
		return bindingRequest("POST", binding, map[string]string{"action": "archive", "confirm_username": "alice"})
	}
	testutil.Call(t, testHandler.ComputerBindingLifecycle, req()).Want(409)
	dbfx.Exec(t, `UPDATE computer_binding SET state='removed' WHERE id=$1`, binding)
	testutil.Call(t, testHandler.ComputerBindingLifecycle, bindingRequest("POST", binding, map[string]string{"action": "archive", "confirm_username": "wrong"})).Want(400)
	testutil.Call(t, testHandler.ComputerBindingLifecycle, req()).Want(200)
	var archived, verified bool
	if err := testPool.QueryRow(context.Background(), `SELECT archived_at IS NOT NULL,verified FROM computer_binding WHERE id=$1`, binding).Scan(&archived, &verified); err != nil || !archived || !verified {
		t.Fatalf("ownership lost: %v", err)
	}
	testutil.Call(t, testHandler.DeleteAdminComputer, withURLParam(computerTestRequest("DELETE", "/", nil), "id", machine)).Want(200)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_audit WHERE computer_id=$1`, machine)
	})
}

func TestComputerDeleteUserFailureKeepsRecoverableBinding(t *testing.T) {
	_, binding := operationBinding(t)
	dir := t.TempDir()
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", filepath.Join(dir, "fake-key"))
	t.Setenv("MULTICA_COMPUTER_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("PATH", dir)
	fakeSSH := `#!/bin/sh
for command do :; done
/bin/cat >/dev/null
case "$command" in
  *"getpwnam(sys.argv[1]); print"*) printf 'yes\n';;
  *"sudo -n userdel -r alice"*) echo 'account still has running processes' >&2; exit 12;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(fakeSSH), 0700); err != nil {
		t.Fatal(err)
	}
	request := func() *http.Request {
		return bindingRequest("POST", binding, map[string]string{"action": "delete_user", "confirm_username": "alice", "password": "never-persist-this-password"})
	}
	testutil.Call(t, testHandler.ComputerBindingLifecycle, request()).Want(409)
	dbfx.Exec(t, `UPDATE computer_binding SET state='removed',account_state='present' WHERE id=$1`, binding)
	var receipt struct {
		ID string `json:"operation_id"`
	}
	testutil.Call(t, testHandler.ComputerBindingLifecycle, request()).Want(202).JSON(&receipt)
	deadline := time.Now().Add(5 * time.Second)
	var state, code, summary string
	for time.Now().Before(deadline) {
		if err := testPool.QueryRow(context.Background(), `SELECT state,error_code,error_summary FROM computer_operation WHERE id=$1`, receipt.ID).Scan(&state, &code, &summary); err != nil {
			t.Fatal(err)
		}
		if state == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state != "failed" || code != "account_delete_failed" || summary != computer.ErrorSummary(code) {
		t.Fatalf("delete result: %s %s %s", state, code, summary)
	}
	var d bindingDetail
	testutil.Call(t, testHandler.ComputerBindingDetail, bindingRequest("GET", binding, nil)).Want(200).JSON(&d)
	if d.State != "removed" || d.AccountState != "present" || d.ArchivedAt != nil {
		t.Fatalf("failed deletion lost recovery state: %+v", d)
	}
}
