package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestInstanceAdminBootstrapPrecedence(t *testing.T) {
	old, exists := os.LookupEnv("MULTICA_INSTANCE_ADMIN_IDS")
	t.Cleanup(func() {
		if exists {
			os.Setenv("MULTICA_INSTANCE_ADMIN_IDS", old)
		} else {
			os.Unsetenv("MULTICA_INSTANCE_ADMIN_IDS")
		}
	})
	os.Unsetenv("MULTICA_INSTANCE_ADMIN_IDS")
	t.Setenv("MULTICA_COMPUTER_OPERATOR_IDS", testUserID)
	if !isInstanceAdmin(testUserID) {
		t.Fatal("legacy bootstrap lost")
	}
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", "")
	if isInstanceAdmin(testUserID) || isInstanceAdmin("") {
		t.Fatal("explicit empty must revoke")
	}
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", "10000000-0000-0000-0000-000000000099")
	if isInstanceAdmin(testUserID) {
		t.Fatal("legacy fallback must not extend explicit list")
	}
}
func TestInstanceAdminBoundaryAndAudit(t *testing.T) {
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", "")
	// testUserID is a workspace owner, but still cannot administer the instance.
	for _, fn := range []http.HandlerFunc{testHandler.AdminComputers, testHandler.AdminComputerBindings, testHandler.AdminComputerAudit, testHandler.UpdateAdminComputer} {
		testutil.Call(t, fn, computerTestRequest("GET", "/", nil)).Want(403)
	}
	testutil.Call(t, testHandler.Computers, computerTestRequest("POST", "/api/computers", map[string]any{"name": "test"})).Want(403)
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", testUserID)
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", adminTestKey(t))
	var created struct {
		ID string `json:"id"`
	}
	body := map[string]any{"name": "admin-test", "host": "fake.invalid", "port": 22, "ssh_user": "operator"}
	// An unreachable computer is never stored.
	testutil.Call(t, testHandler.AdminComputers, computerTestRequest("POST", "/api/admin/computers", body)).Want(422)
	withProbeOK(t)
	testutil.Call(t, testHandler.AdminComputers, computerTestRequest("POST", "/api/admin/computers", body)).Want(201).JSON(&created)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM computer_audit WHERE computer_id=$1`, created.ID)
		testPool.Exec(context.Background(), `DELETE FROM computer WHERE id=$1`, created.ID)
	})
	patch := func(body any) *http.Request {
		return withURLParam(computerTestRequest("PATCH", "/", body), "id", created.ID)
	}
	testutil.Call(t, testHandler.UpdateAdminComputer, patch(map[string]any{"host": "new.invalid"})).Want(200)
	binding := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": created.ID, "user_id": testUserID, "workspace_id": testWorkspaceID, "username": "alice"})
	_ = binding
	testutil.Call(t, testHandler.UpdateAdminComputer, patch(map[string]any{"host": "wrong.invalid"})).Want(409)
	testutil.Call(t, testHandler.UpdateAdminComputer, patch(map[string]any{"enabled": false})).Want(200)
	var machines []adminComputer
	testutil.Call(t, testHandler.AdminComputers, computerTestRequest("GET", "/", nil)).Want(200).JSON(&machines)
	found := false
	for _, m := range machines {
		if m.ID == created.ID {
			found = true
			if m.Enabled || m.Host != "new.invalid" {
				t.Fatal("bad update")
			}
		}
	}
	if !found {
		t.Fatal("disabled machine missing from admin")
	}
	var audit []map[string]any
	testutil.Call(t, testHandler.AdminComputerAudit, computerTestRequest("GET", "/", nil)).Want(200).JSON(&audit)
	found = false
	for _, a := range audit {
		if a["computer_id"] == created.ID && a["action"] == "disable" {
			found = true
		}
	}
	if !found {
		t.Fatal("admin mutation missing audit")
	}
	testutil.Call(t, testHandler.AdminComputerBindings, computerTestRequest("GET", "/", nil)).Want(200)
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", "")
	testutil.Call(t, testHandler.UpdateAdminComputer, patch(map[string]any{"enabled": true})).Want(403)
	rec := testutil.Call(t, testHandler.Computers, computerTestRequest("GET", "/", nil)).Want(200)
	var public []json.RawMessage
	rec.JSON(&public)
	for _, m := range public {
		var v struct{ ID string }
		json.Unmarshal(m, &v)
		if v.ID == created.ID {
			t.Fatal("disabled machine selectable")
		}
	}
}

// adminTestKey supplies a key path so probing is configured. The probe still
// fails to connect, which is what these tests assert on.
func adminTestKey(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, []byte("not-a-real-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// withProbeOK makes the connectivity probe pass without a real machine.
func withProbeOK(t *testing.T) {
	t.Helper()
	old := probeMachine
	probeMachine = func(computer.Machine) (computer.ProbeResult, error) {
		return computer.ProbeResult{OK: true, Checks: []computer.ProbeCheck{{Name: "ssh", OK: true}}}, nil
	}
	t.Cleanup(func() { probeMachine = old })
}

// A failing check must block registration outright under the chosen policy.
func TestRegisterRejectsComputerThatFailsCheck(t *testing.T) {
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", testUserID)
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", adminTestKey(t))
	old := probeMachine
	probeMachine = func(computer.Machine) (computer.ProbeResult, error) {
		return computer.ProbeResult{OK: false, Checks: []computer.ProbeCheck{{Name: "sudo", OK: false, Detail: "passwordless sudo unavailable"}}}, nil
	}
	t.Cleanup(func() { probeMachine = old })
	var res struct {
		Code  string               `json:"code"`
		Probe computer.ProbeResult `json:"probe"`
	}
	testutil.Call(t, testHandler.AdminComputers, computerTestRequest("POST", "/", map[string]any{"name": "rejected", "host": "10.9.9.9", "port": 22, "ssh_user": "operator"})).Want(422).JSON(&res)
	if res.Code != "computer_check_failed" || len(res.Probe.Checks) != 1 || res.Probe.Checks[0].Detail == "" {
		t.Fatalf("failure reason not returned: %+v", res)
	}
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM computer WHERE name='rejected'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("rejected Computer was stored anyway")
	}
}

func TestRegisterRequiresProbeConfiguration(t *testing.T) {
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", testUserID)
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", "")
	body := map[string]any{"name": "unconfigured", "host": "fake.invalid", "port": 22, "ssh_user": "operator"}
	testutil.Call(t, testHandler.AdminComputers, computerTestRequest("POST", "/api/admin/computers", body)).Want(503)
	testutil.Call(t, testHandler.CheckAdminComputerDraft, computerTestRequest("POST", "/api/admin/computers/check", body)).Want(503)
}

func TestCheckDraftRejectsBadInputAndNonAdmin(t *testing.T) {
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", adminTestKey(t))
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", "")
	testutil.Call(t, testHandler.CheckAdminComputerDraft, computerTestRequest("POST", "/", map[string]any{"name": "x", "host": "10.0.0.1", "port": 22, "ssh_user": "operator"})).Want(403)
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", testUserID)
	// Probing must not become a way to smuggle ssh options through the host.
	testutil.Call(t, testHandler.CheckAdminComputerDraft, computerTestRequest("POST", "/", map[string]any{"name": "x", "host": "-oProxyCommand=touch /tmp/pwn", "port": 22, "ssh_user": "operator"})).Want(400)
	testutil.Call(t, testHandler.CheckAdminComputerDraft, computerTestRequest("POST", "/", map[string]any{"name": "x", "host": "10.0.0.1", "port": 0, "ssh_user": "operator"})).Want(400)
	// A configured but unreachable computer is a verdict, not a server error.
	var res computer.ProbeResult
	testutil.Call(t, testHandler.CheckAdminComputerDraft, computerTestRequest("POST", "/", map[string]any{"name": "x", "host": "fake.invalid", "port": 22, "ssh_user": "operator"})).Want(200).JSON(&res)
	if res.OK || len(res.Checks) == 0 || res.Checks[0].Name != "ssh" {
		t.Fatalf("unexpected probe: %+v", res)
	}
}

func TestDeleteComputerRefusesWhileBoundAndAudits(t *testing.T) {
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", testUserID)
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "to-delete", "host": "delete.invalid", "port": 22, "ssh_user": "operator", "created_by": testUserID})
	del := func() *http.Request { return withURLParam(computerTestRequest("DELETE", "/", nil), "id", machine) }
	binding := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": testUserID, "workspace_id": testWorkspaceID, "username": "alice"})
	testutil.Call(t, testHandler.DeleteAdminComputer, del()).Want(409)
	if _, err := testPool.Exec(context.Background(), `DELETE FROM computer_binding WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.DeleteAdminComputer, del()).Want(200)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM computer_audit WHERE computer_id=$1`, machine)
	})
	// Deleting twice is a 404, not a silent success.
	testutil.Call(t, testHandler.DeleteAdminComputer, del()).Want(404)
	var gone bool
	if err := testPool.QueryRow(context.Background(), `SELECT NOT EXISTS(SELECT 1 FROM computer WHERE id=$1)`, machine).Scan(&gone); err != nil {
		t.Fatal(err)
	}
	if !gone {
		t.Fatal("computer row survived delete")
	}
	// The audit trail must outlive the Computer it describes.
	var audited bool
	if err := testPool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM computer_audit WHERE computer_id=$1 AND action='delete' AND user_id=$2)`, machine, testUserID).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if !audited {
		t.Fatal("delete not audited")
	}
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", "")
	testutil.Call(t, testHandler.DeleteAdminComputer, del()).Want(403)
}

func TestDisabledComputerVisibleOnlyForOwnerCleanup(t *testing.T) {
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", "")
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "disabled-owned", "host": "private.invalid", "port": 22, "ssh_user": "operator", "created_by": testUserID, "enabled": false})
	dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": testUserID, "workspace_id": testWorkspaceID, "username": "alice", "verified": true})
	var list []adminComputer
	testutil.Call(t, testHandler.Computers, computerTestRequest("GET", "/", nil)).Want(200).JSON(&list)
	found := false
	for _, m := range list {
		if m.ID == machine {
			found = true
			if m.Enabled || m.Host != "" || m.Port != 0 || m.SSHUser != "" {
				t.Fatal("disabled cleanup list leaked connection or availability")
			}
		}
	}
	if !found {
		t.Fatal("owner cannot choose disabled machine for removal")
	}
	req := computerTestRequest("GET", "/", nil)
	req.Header.Set("X-User-ID", "10000000-0000-0000-0000-000000000099")
	testutil.Call(t, testHandler.Computers, req).Want(200).JSON(&list)
	for _, m := range list {
		if m.ID == machine {
			t.Fatal("non-owner can see disabled Computer")
		}
	}
}

func TestOwnedRuntimeUserVersionAndAudit(t *testing.T) {
	t.Setenv("MULTICA_COMPUTER_SERVER_URL", "https://cache.test.invalid")
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", testUserID)
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", adminTestKey(t))
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "runtime-test", "host": "fake.invalid", "port": 22, "ssh_user": "operator", "created_by": testUserID})
	binding := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": testUserID, "workspace_id": testWorkspaceID, "username": "alice", "state": "ready", "verified": true})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM computer_audit WHERE computer_id=$1`, machine)
	})
	dir := t.TempDir()
	// SSH is a test-owned executable; these tests never resolve vendor CLIs.
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(`#!/bin/sh
for command do :; done
case "$command" in
  *"runuser -u alice"*|*"runuser -u 'alice'"*) ;;
  *) exit 44;;
esac
case "$command" in
  *"--version"*) printf '{"path":"/home/alice/.grok/bin/grok","version":"1.2.3","state":"installed","code":""}\n';;
  *) exit 45;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	request := func(method, url string, body any) *http.Request {
		return withURLParam(computerTestRequest(method, url, body), "id", binding)
	}
	var runtimes []struct {
		ID               string `json:"id"`
		InstalledVersion string `json:"installed_version"`
		VersionRequired  bool   `json:"version_required"`
		SupportsVersion  bool   `json:"supports_version"`
		ProbeError       string `json:"probe_error"`
	}
	testutil.Call(t, testHandler.ComputerBindingRuntimes, request("GET", "/", nil)).Want(200).JSON(&runtimes)
	if len(runtimes) != 8 {
		t.Fatalf("runtimes = %+v", runtimes)
	}
	for _, rt := range runtimes {
		if rt.InstalledVersion != "" || rt.ProbeError != "" {
			t.Fatalf("wrong target user: %+v", rt)
		}
		if rt.VersionRequired || (rt.SupportsVersion != (rt.ID != "openclaw")) {
			t.Fatalf("wrong version capability: %+v", rt)
		}
	}
	for _, tc := range []struct {
		runtime, version string
		status           int
	}{
		{"omp", "1.2.3\n", 400},
		{"unknown", "1.2.3", 400},
		{"grok", "", 200},
		{"grok", "latest", 200},
	} {
		testutil.Call(t, testHandler.ComputerBindingRuntimeInstall, request("POST", "/", map[string]string{"runtime_id": tc.runtime, "version": tc.version})).Want(tc.status)
	}
	testutil.Call(t, testHandler.ComputerBindingRuntimeInstall, request("POST", "/", map[string]string{"runtime_id": "codex"})).Want(200)
	var requested string
	if err := testPool.QueryRow(context.Background(), `SELECT requested_version FROM computer_operation WHERE binding_id=$1 AND runtime_id='codex' ORDER BY created_at DESC LIMIT 1`, binding).Scan(&requested); err != nil || requested != "latest" {
		t.Fatalf("omitted version was not recorded as latest: %q %v", requested, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM computer_audit WHERE computer_id=$1 AND action='runtime_install:grok@latest' AND outcome='succeeded'`, machine).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if count != 2 {
		t.Fatal("asynchronous install audit missing")
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM computer_operation WHERE binding_id=$1`, binding)
		testPool.Exec(context.Background(), `DELETE FROM computer_runtime_asset WHERE binding_id=$1`, binding)
		testPool.Exec(context.Background(), `DELETE FROM computer_runtime_model_config WHERE binding_id=$1`, binding)
	})
	// Transport failures remain distinct from executables missing on PATH.
	other := request("GET", "/", nil)
	other.Header.Set("X-User-ID", "10000000-0000-0000-0000-000000000099")
	testutil.Call(t, testHandler.ComputerBindingRuntimes, other).Want(404)
	otherInstall := request("POST", "/", map[string]string{"runtime_id": "grok", "version": "latest"})
	otherInstall.Header.Set("X-User-ID", "10000000-0000-0000-0000-000000000099")
	testutil.Call(t, testHandler.ComputerBindingRuntimeInstall, otherInstall).Want(404)
}

func TestAdminCheckLinuxUserAuthorizationAndRemoteAccount(t *testing.T) {
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", testUserID)
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", adminTestKey(t))
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "linux-user-check", "host": "fake.invalid", "port": 22, "ssh_user": "operator", "created_by": testUserID})
	binding := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": testUserID, "workspace_id": testWorkspaceID, "username": "alice"})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM computer_audit WHERE computer_id=$1`, machine)
	})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(`#!/bin/sh
for command do :; done
case "$command" in
  *"getent passwd 'alice'"*) printf 'present\nactive\n';;
  *) exit 44;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	request := func(id string) *http.Request {
		return withURLParam(computerTestRequest("POST", "/", nil), "id", id)
	}
	testutil.Call(t, testHandler.AdminCheckLinuxUser, request("invalid")).Want(400)
	testutil.Call(t, testHandler.AdminCheckLinuxUser, request("10000000-0000-0000-0000-000000000099")).Want(404)
	var result struct {
		Present bool `json:"present"`
	}
	testutil.Call(t, testHandler.AdminCheckLinuxUser, request(binding)).Want(200).JSON(&result)
	if !result.Present {
		t.Fatal("remote account was not reported present")
	}
	var detail bindingDetail
	testutil.Call(t, testHandler.AdminBindingDetail, request(binding)).Want(200).JSON(&detail)
	if detail.DaemonState != "running" || detail.LastSeenAt != nil {
		t.Fatalf("service state must not depend on runtime registration: %+v", detail)
	}
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", "")
	testutil.Call(t, testHandler.AdminCheckLinuxUser, request(binding)).Want(403)
}

func TestAdminSshPublicKeyDerivation(t *testing.T) {
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", testUserID)
	key := filepath.Join(t.TempDir(), "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("generate test key: %v: %s", err, out)
	}
	expected, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(key + ".pub"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", key)
	var result struct {
		Pubkey string `json:"pubkey"`
	}
	testutil.Call(t, testHandler.AdminSshPubKey, computerTestRequest("GET", "/", nil)).Want(200).JSON(&result)
	want := strings.Fields(string(expected))
	got := strings.Fields(result.Pubkey)
	if len(got) < 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatal("derived public key does not match")
	}
}
