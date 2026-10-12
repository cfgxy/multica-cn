package handler

import (
	"context"
	"encoding/base64"
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

func TestComputerAccountCreationWithoutCredentials(t *testing.T) {
	t.Setenv("MULTICA_COMPUTER_SECRET_KEY", "")
	dir := t.TempDir()
	// Only account lookup and creation are permitted; credential/daemon commands fail.
	script := `#!/bin/sh
for arg in "$@"; do last="$arg"; done
case "$last" in
 *"getpwnam(sys.argv[1]); print"*) printf 'no\n'; exit 0 ;;
 *"useradd"*) cat >/dev/null; exit 0 ;;
esac
exit 99
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", filepath.Join(dir, "fake-key"))
	t.Setenv("MULTICA_COMPUTER_CLI_PATH", filepath.Join(dir, "must-not-be-read"))
	t.Setenv("MULTICA_COMPUTER_STATE_DIR", filepath.Join(dir, "locks"))
	t.Setenv("MULTICA_COMPUTER_SERVER_URL", "https://fake.invalid")
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "fake", "host": "fake.invalid", "port": 22, "ssh_user": "operator", "created_by": testUserID})
	var binding computerBinding
	testutil.Call(t, testHandler.ComputerBindings, computerTestRequest("POST", "/", map[string]string{"computer_id": machine, "workspace_id": testWorkspaceID, "username": "credentialless", "password": "fake-password", "action": "create_account"})).Want(202).JSON(&binding)
	t.Cleanup(func() {
		for _, table := range []string{"computer_audit", "computer_operation"} {
			_, _ = testPool.Exec(context.Background(), "DELETE FROM "+table+" WHERE binding_id=$1", binding.ID)
		}
		_, _ = testPool.Exec(context.Background(), "DELETE FROM computer_binding WHERE id=$1", binding.ID)
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		var state, account, op string
		var verified bool
		err := testPool.QueryRow(context.Background(), `SELECT b.state,b.account_state,b.verified,o.state FROM computer_binding b JOIN computer_operation o ON o.binding_id=b.id WHERE b.id=$1`, binding.ID).Scan(&state, &account, &verified, &op)
		if err != nil {
			t.Fatal(err)
		}
		if op == "succeeded" {
			if state != "pending" || account != "present" || !verified {
				t.Fatalf("account-only outcome: %s %s %v", state, account, verified)
			}
			break
		}
		if op == "failed" || time.Now().After(deadline) {
			t.Fatalf("account-only operation ended %s, binding %s", op, state)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestComputerCredentialTransferOwnerAndVerificationBoundary(t *testing.T) {
	_, binding := operationBinding(t)
	for _, fn := range []http.HandlerFunc{testHandler.ReadComputerCredentials, testHandler.WriteComputerCredentials} {
		req := bindingRequest("POST", binding, map[string]string{"password": "fake-password"})
		req.Header.Set("X-User-ID", "10000000-0000-0000-0000-000000000099")
		testutil.Call(t, fn, req).Want(404)
		testutil.Call(t, fn, bindingRequest("POST", binding, map[string]string{})).Want(400)
	}
	dbfx.Exec(t, `UPDATE computer_binding SET verified=false WHERE id=$1`, binding)
	testutil.Call(t, testHandler.ReadComputerCredentials, bindingRequest("POST", binding, map[string]string{"password": "fake-password"})).Want(409)
}

func TestComputerPartialCredentialsSaveWithoutPAT(t *testing.T) {
	t.Setenv("MULTICA_COMPUTER_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_credential WHERE user_id=$1`, testUserID)
	})
	testutil.Call(t, testHandler.ComputerSettings, computerTestRequest("PUT", "/", computer.Settings{GitName: "Only a name"})).Want(200)
	var response struct {
		Settings computer.Settings `json:"settings"`
	}
	testutil.Call(t, testHandler.ComputerSettings, computerTestRequest("GET", "/", nil)).Want(200).JSON(&response)
	if response.Settings.GitName != "Only a name" || response.Settings.MulticaPAT != "" {
		t.Fatal("partial template was not preserved")
	}
}

func TestComputerCredentialTransferDraftAndAudit(t *testing.T) {
	_, binding := operationBinding(t)
	dbfx.Exec(t, `UPDATE computer_binding SET state='pending' WHERE id=$1`, binding)
	dir := t.TempDir()
	t.Setenv("MULTICA_TEST_CREDENTIAL_PAYLOAD", filepath.Join(dir, "payload"))
	script := `#!/bin/sh
for arg in "$@"; do last="$arg"; done
case "$last" in
 *"def git_value"*) printf '%s' '{"git_name":"Manual","git_email":"manual@example.com","gitlab_url":"","gitlab_token":"","git_ssh_key":"","git_known_hosts":"","model_env":"OPENAI_API_KEY=remote-fake-secret","multica_pat":""}'; exit 0 ;;
 *"parts=sys.stdin.buffer"*) cat > "$MULTICA_TEST_CREDENTIAL_PAYLOAD"; exit 0 ;;
 *"pam_start"*) cat >/dev/null; exit 0 ;;
esac
exit 99
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", filepath.Join(dir, "fake-key"))
	t.Setenv("MULTICA_COMPUTER_STATE_DIR", filepath.Join(dir, "locks"))
	t.Setenv("MULTICA_COMPUTER_SERVER_URL", "https://fake.invalid")
	var before, after int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM computer_credential WHERE user_id=$1`, testUserID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Settings computer.Settings `json:"settings"`
	}
	readResponse := testutil.Call(t, testHandler.ReadComputerCredentials, bindingRequest("POST", binding, map[string]string{"password": "fake-password"})).Want(200)
	readResponse.JSON(&response)
	if readResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential read can be cached")
	}
	if response.Settings.GitName != "Manual" || response.Settings.ModelEnv != "OPENAI_API_KEY=remote-fake-secret" {
		t.Fatal("server settings not returned to owner")
	}
	writeResponse := testutil.Call(t, testHandler.WriteComputerCredentials, bindingRequest("POST", binding, map[string]any{"password": "fake-password", "settings": computer.Settings{GitName: "New name"}})).Want(200)
	if writeResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential write can be cached")
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM computer_credential WHERE user_id=$1`, testUserID).Scan(&after); err != nil || before != after {
		t.Fatal("transfer changed the personal template")
	}
	body, err := os.ReadFile(filepath.Join(dir, "payload"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "New name") || !strings.Contains(string(body), "remote-fake-secret") {
		t.Fatal("write did not preserve blank remote fields")
	}
	var operations []remoteOperation
	testutil.Call(t, testHandler.ComputerBindingOperations, bindingRequest("GET", binding, nil)).Want(200).JSON(&operations)
	encoded, _ := json.Marshal(operations)
	if len(operations) != 2 || strings.Contains(string(encoded), "remote-fake-secret") || strings.Contains(string(encoded), "fake-password") {
		t.Fatal("invalid or secret-bearing operation history")
	}
	for _, op := range operations {
		if op.State != "succeeded" {
			t.Fatalf("operation failed: %s", op.ErrorCode)
		}
	}
	var auditCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM computer_audit WHERE binding_id=$1 AND outcome='succeeded'`, binding).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("audit count: %d", auditCount)
	}
}

func TestComputerCredentialFailureCodes(t *testing.T) {
	for _, tc := range []struct {
		name, pamExit, readExit, code string
		status                        int
	}{
		{"password", "42", "99", "password_mismatch", 403},
		{"ssh", "255", "99", "ssh_unreachable", 502},
		{"policy", "43", "99", "account_unavailable", 422},
		{"invalid_file", "0", "1", "credential_transfer_failed", 422},
		{"read_transport", "0", "255", "ssh_unreachable", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, binding := operationBinding(t)
			dir := t.TempDir()
			script := "#!/bin/sh\nfor arg in \"$@\"; do last=\"$arg\"; done\ncase \"$last\" in\n *pam_start*) cat >/dev/null; exit " + tc.pamExit + ";;\n *'def git_value'*) printf 'fake-secret-do-not-expose'; exit " + tc.readExit + ";;\nesac\nexit 99\n"
			if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
			t.Setenv("MULTICA_COMPUTER_SSH_KEY", filepath.Join(dir, "fake"))
			t.Setenv("MULTICA_COMPUTER_STATE_DIR", filepath.Join(dir, "locks"))
			t.Setenv("MULTICA_COMPUTER_SERVER_URL", "https://fake.invalid")
			for _, fn := range []http.HandlerFunc{testHandler.ReadComputerCredentials, testHandler.WriteComputerCredentials} {
				response := testutil.Call(t, fn, bindingRequest("POST", binding, map[string]string{"password": "fake-password"})).Want(tc.status)
				var body map[string]string
				response.JSON(&body)
				if body["code"] != tc.code || strings.Contains(response.Body.String(), "fake-secret") || response.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("unsafe or wrong failure response: %v", body)
				}
			}
			if tc.name == "password" {
				for i := 0; i < 3; i++ {
					testutil.Call(t, testHandler.ReadComputerCredentials, bindingRequest("POST", binding, map[string]string{"password": "fake-password"})).Want(403)
				}
				var body map[string]string
				testutil.Call(t, testHandler.ReadComputerCredentials, bindingRequest("POST", binding, map[string]string{"password": "fake-password"})).Want(429).JSON(&body)
				if body["code"] != "password_locked" {
					t.Fatal("missing lockout code")
				}
			}
		})
	}
}

func TestComputerUpgradeRejectsPasswordBeforeCredentialRead(t *testing.T) {
	machine, binding := operationBinding(t)
	dir := t.TempDir()
	store := &computer.FileStore{Dir: filepath.Join(dir, "locks")}
	for attempt := 0; attempt < 6; attempt++ {
		op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "upgrade", "", "")
		if err != nil {
			t.Fatal(err)
		}
		dbfx.Exec(t, `UPDATE computer_binding SET state='running' WHERE id=$1`, binding)
		read := false
		remote := computer.SSHRemote{Host: "fake.invalid", User: "operator", Port: 22, KeyPath: "fake", DaemonID: binding,
			RunCmd: credentialTestRunner(func(argv []string, stdin string) (string, error) {
				command := argv[len(argv)-1]
				if strings.Contains(command, "getpwnam(sys.argv[1]); print") {
					return "yes", nil
				}
				if strings.Contains(command, "pam_start") {
					return "", exec.Command("sh", "-c", "exit 42").Run()
				}
				read = true
				return "", nil
			}),
		}
		testHandler.runRemoteOperation(op, func(ctx context.Context) (string, error) {
			return "", testHandler.runComputerOperation(ctx, op, testUserID, computerBinding{ID: binding, ComputerID: machine, Username: "alice", WorkspaceID: testWorkspaceID}, remote, store, computer.Request{ComputerID: machine, Username: "alice", Password: "wrong", PreserveFiles: true}, "upgrade")
		})
		var code string
		if err := testPool.QueryRow(context.Background(), `SELECT error_code FROM computer_operation WHERE id=$1`, op).Scan(&code); err != nil {
			t.Fatal(err)
		}
		expected := "password_mismatch"
		if attempt >= 4 {
			expected = "password_locked"
		}
		if read || code != expected {
			t.Fatalf("upgrade before authentication: read=%v code=%s", read, code)
		}
	}
}

type credentialTestRunner func([]string, string) (string, error)

func (r credentialTestRunner) Run(_ context.Context, argv []string, stdin string) (string, error) {
	return r(argv, stdin)
}
