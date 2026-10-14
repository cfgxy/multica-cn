package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestComputerCredentialsOwnerIsolation(t *testing.T) {
	t.Setenv("MULTICA_COMPUTER_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	token, _ := insertTestPAT(t, time.Time{})
	s := computer.Settings{GitName: "Person", GitEmail: "person@example.com", GitLabURL: "https://git.example.com", GitLabToken: "fake-secret", MulticaPAT: token}
	body, _ := json.Marshal(s)
	testutil.Call(t, testHandler.ComputerSettings, computerTestRequest("PUT", "/api/me/computer-settings", strings.NewReader(string(body)))).Want(200)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_credential WHERE user_id=$1`, testUserID)
	})
	var own struct {
		Settings computer.Settings `json:"settings"`
	}
	testutil.Call(t, testHandler.ComputerSettings, computerTestRequest("GET", "/api/me/computer-settings", nil)).Want(200).JSON(&own)
	if own.Settings.GitLabToken != s.GitLabToken {
		t.Fatal("owner cannot read secret")
	}
	var cipher []byte
	if err := testPool.QueryRow(context.Background(), `SELECT ciphertext FROM computer_credential WHERE user_id=$1`, testUserID).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cipher), s.GitLabToken) {
		t.Fatal("plaintext stored")
	}
	req := computerTestRequest("GET", "/api/me/computer-settings", nil)
	req.Header.Set("X-User-ID", "10000000-0000-0000-0000-000000000099")
	var other struct {
		Settings computer.Settings `json:"settings"`
	}
	testutil.Call(t, testHandler.ComputerSettings, req).Want(200).JSON(&other)
	if other.Settings.GitLabToken != "" {
		t.Fatal("another human read secret")
	}
	req = computerTestRequest("PUT", "/api/me/computer-settings", strings.NewReader(string(body)))
	req.Header.Set("X-User-ID", "10000000-0000-0000-0000-000000000099")
	testutil.Call(t, testHandler.ComputerSettings, req).Want(http.StatusBadRequest)
}
func TestComputerRegistrationRequiresDeploymentOperator(t *testing.T) {
	t.Setenv("MULTICA_COMPUTER_OPERATOR_IDS", "")
	req := computerTestRequest("POST", "/api/computers", strings.NewReader(`{"name":"test","host":"example.com","port":22,"ssh_user":"operator"}`))
	testutil.Call(t, testHandler.Computers, req).Want(http.StatusForbidden)
}
func TestComputerRejectsInvalidUUIDBeforeRemote(t *testing.T) {
	req := computerTestRequest("POST", "/api/me/computer-bindings", strings.NewReader(`{"computer_id":"not-a-uuid"}`))
	testutil.Call(t, testHandler.ComputerBindings, req).Want(http.StatusBadRequest)
}

func computerTestRequest(method, path string, body any) *http.Request {
	return testutil.WithHeaders(testutil.JSONRequest(method, path, body), "X-User-ID", testUserID, "X-Workspace-ID", testWorkspaceID)
}

func TestComputerBindingSurvivesWorkspaceDeletionAsDetached(t *testing.T) {
	ws := dbfx.Workspace(t, "Computer test workspace", "computer-delete-test")
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "test", "host": "example.com", "port": 22, "ssh_user": "operator", "created_by": testUserID})
	binding := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": testUserID, "workspace_id": ws, "username": "alice", "verified": true, "state": "ready"})
	if err := testHandler.Queries.DeleteWorkspaceAdministration(context.Background(), parseUUID(ws)); err != nil {
		t.Fatal(err)
	}
	var detached bool
	var owner string
	if err := testPool.QueryRow(context.Background(), `SELECT workspace_id IS NULL AND state='detached',user_id::text FROM computer_binding WHERE id=$1`, binding).Scan(&detached, &owner); err != nil {
		t.Fatal(err)
	}
	if !detached || owner != testUserID {
		t.Fatal("OS account ownership must survive workspace deletion")
	}
}

func TestComputerProvisionRequiresMatchingLiveRegistration(t *testing.T) {
	t.Setenv("MULTICA_COMPUTER_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	dir := t.TempDir()
	ssh := `#!/bin/sh
for arg in "$@"; do last="$arg"; done
case "$last" in
  *"getpwnam(sys.argv[1]); print"*) printf 'no\n' ;;
esac
cat >/dev/null
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(ssh), 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "multica-fake")
	if err := os.WriteFile(binary, []byte("fake CLI, never executed"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", filepath.Join(dir, "fake-identity"))
	t.Setenv("MULTICA_COMPUTER_CLI_PATH", binary)
	t.Setenv("MULTICA_COMPUTER_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("MULTICA_COMPUTER_SERVER_URL", "https://test.invalid")
	token, _ := insertTestPAT(t, time.Time{})
	s := computer.Settings{GitName: "Human", GitEmail: "h@example.com", GitLabURL: "https://git.example.com", MulticaPAT: token}
	testutil.Call(t, testHandler.ComputerSettings, computerTestRequest("PUT", "/api/me/computer-settings", s)).Want(200)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_credential WHERE user_id=$1`, testUserID)
	})
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "fake", "host": "fake.invalid", "port": 22, "ssh_user": "operator", "created_by": testUserID})
	var b computerBinding
	testutil.Call(t, testHandler.ComputerBindings, computerTestRequest("POST", "/api/me/computer-bindings", map[string]string{"computer_id": machine, "workspace_id": testWorkspaceID, "username": "alice", "password": "fake-password", "action": "provision"})).Want(202).JSON(&b)
	// Register a daemon with the wrong identity first; systemd success is insufficient.
	dbfx.Runtime(t, "wrong-registration", testutil.Cols{"daemon_id": "wrong-daemon", "owner_id": testUserID, "status": "online", "last_seen_at": time.Now()})
	time.Sleep(250 * time.Millisecond)
	var state string
	if err := testPool.QueryRow(context.Background(), `SELECT state FROM computer_binding WHERE id=$1`, b.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "running" {
		t.Fatalf("premature state %q", state)
	}
	dbfx.Runtime(t, "matching-registration", testutil.Cols{"daemon_id": b.ID, "owner_id": testUserID, "status": "online", "last_seen_at": time.Now()})
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := testPool.QueryRow(context.Background(), `SELECT state FROM computer_binding WHERE id=$1`, b.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "ready" {
			break
		}
		if state == "failed" {
			t.Fatal("fake provision failed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state != "ready" {
		t.Fatalf("matching daemon did not complete: %s", state)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_audit WHERE binding_id=$1`, b.ID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_binding WHERE id=$1`, b.ID)
	})
}

func TestComputerBindingClaimIsExclusive(t *testing.T) {
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "claim-test", "host": "fake.invalid", "port": 22, "ssh_user": "operator", "created_by": testUserID})
	binding := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": testUserID, "workspace_id": testWorkspaceID, "username": "alice", "verified": true, "state": "failed"})
	other := "10000000-0000-0000-0000-000000000099"
	claim := func(owner string) error {
		var b computerBinding
		return testPool.QueryRow(context.Background(), claimComputerBindingSQL, machine, owner, testWorkspaceID, "alice", "provision").Scan(&b.ID, &b.ComputerID, &b.WorkspaceID, &b.Username, &b.State, &b.LastError, &b.HealthPort)
	}
	if err := claim(other); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("verified owner was replaceable: %v", err)
	}
	if err := claim(testUserID); err != nil {
		t.Fatal(err)
	}
	if err := claim(testUserID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("concurrent operation was accepted: %v", err)
	}
	dbfx.Exec(t, `UPDATE computer_binding SET verified=false,state='failed' WHERE id=$1`, binding)
	if err := claim(other); err != nil {
		t.Fatalf("failed unverified guess reserved another person's account: %v", err)
	}
	if err := claim(testUserID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("in-flight account claim was replaceable: %v", err)
	}
}

func TestComputerBindingConflictClassification(t *testing.T) {
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "conflict-test", "host": "fake.invalid", "port": 22, "ssh_user": "operator", "created_by": testUserID})
	other := "10000000-0000-0000-0000-000000000099"
	oldWorkspace := testWorkspaceID
	newWorkspace := dbfx.Workspace(t, "New workspace", "binding-conflict-test")
	binding := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": testUserID, "workspace_id": oldWorkspace, "username": "alice", "verified": true, "state": "ready"})
	check := func(uid, want string) {
		t.Helper()
		tx, err := testPool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		code, _, err := classifyComputerBindingConflict(context.Background(), tx, machine, "alice", uid, newWorkspace)
		if err != nil || code != want {
			t.Fatalf("conflict code = %q, err = %v; want %q", code, err, want)
		}
	}
	check(testUserID, "binding_workspace_conflict")
	check(other, "username_unavailable")
	dbfx.Exec(t, `UPDATE computer_binding SET state='running' WHERE id=$1`, binding)
	check(testUserID, "operation_busy")
	dbfx.Exec(t, `UPDATE computer_binding SET verified=false,state='failed' WHERE id=$1`, binding)
	check(other, "binding_conflict")
	check(testUserID, "binding_conflict")
	dbfx.Exec(t, `UPDATE computer_binding SET verified=true,state='running' WHERE id=$1`, binding)
	var operationID string
	if err := testPool.QueryRow(context.Background(), `INSERT INTO computer_operation(binding_id,computer_id,username,user_id,kind,state,deadline_at) VALUES($1,$2,'alice',$3,'provision','running',now()+interval '1 minute') RETURNING id::text`, binding, machine, testUserID).Scan(&operationID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_operation WHERE id=$1`, operationID)
	})
	var list []computerBinding
	findBinding := func() computerBinding {
		t.Helper()
		for _, item := range list {
			if item.ID == binding {
				return item
			}
		}
		t.Fatalf("binding %s absent from owner list", binding)
		return computerBinding{}
	}
	testutil.Call(t, testHandler.ComputerBindings, computerTestRequest(http.MethodGet, "/api/me/computer-bindings", nil)).Want(http.StatusOK).JSON(&list)
	if got := findBinding(); got.State != "running" || !got.OperationBusy {
		t.Fatalf("active operation projection: %+v", list)
	}
	dbfx.Exec(t, `UPDATE computer_operation SET deadline_at=now()-interval '1 minute' WHERE id=$1`, operationID)
	testutil.Call(t, testHandler.ComputerBindings, computerTestRequest(http.MethodGet, "/api/me/computer-bindings", nil)).Want(http.StatusOK).JSON(&list)
	if got := findBinding(); got.State != "interrupted" || got.OperationBusy {
		t.Fatalf("expired operation projection: %+v", list)
	}
	check(testUserID, "operation_recovery_required")
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var claimed computerBinding
	err = tx.QueryRow(context.Background(), claimComputerBindingSQL, machine, testUserID, newWorkspace, "alice", "provision").Scan(&claimed.ID, &claimed.ComputerID, &claimed.WorkspaceID, &claimed.Username, &claimed.State, &claimed.LastError, &claimed.HealthPort)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim with unacknowledged operation: id=%q err=%v", claimed.ID, err)
	}
}

func TestComputerBindingsKeepSameHostAccountsPrivate(t *testing.T) {
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "shared-host", "host": "fake.invalid", "port": 22, "ssh_user": "operator", "created_by": testUserID})
	first := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": testUserID, "workspace_id": testWorkspaceID, "username": "alice-one", "verified": true, "state": "ready"})
	second := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": testUserID, "workspace_id": testWorkspaceID, "username": "alice-two", "verified": true, "state": "removed"})
	otherUser := dbfx.Insert(t, "user", testutil.Cols{"name": "Other Computer User", "email": "other-computer-user@example.test"})
	dbfx.Insert(t, "member", testutil.Cols{"workspace_id": testWorkspaceID, "user_id": otherUser, "role": "member"})
	bob := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": otherUser, "workspace_id": testWorkspaceID, "username": "bob-one", "verified": true, "state": "ready"})

	listFor := func(userID string) []computerBinding {
		t.Helper()
		req := computerTestRequest(http.MethodGet, "/api/me/computer-bindings", nil)
		req.Header.Set("X-User-ID", userID)
		var bindings []computerBinding
		testutil.Call(t, testHandler.ComputerBindings, req).Want(http.StatusOK).JSON(&bindings)
		return bindings
	}
	seen := map[string]computerBinding{}
	for _, binding := range listFor(testUserID) {
		if binding.ComputerID == machine {
			seen[binding.Username] = binding
		}
	}
	if !seen["alice-one"].Verified || !seen["alice-two"].Verified || seen["alice-one"].OperationBusy || seen["bob-one"].ID != "" {
		t.Fatalf("owner's same-host bindings = %v", seen)
	}
	bobSeen := false
	for _, binding := range listFor(otherUser) {
		if binding.ID == first || binding.ID == second {
			t.Fatalf("another member received private binding %s", binding.ID)
		}
		if binding.ID == bob && binding.Verified && !binding.OperationBusy {
			bobSeen = true
		}
	}
	if !bobSeen {
		t.Fatal("other member's own verified binding missing")
	}
	for _, handler := range []func(http.ResponseWriter, *http.Request){testHandler.ComputerBindingDetail, testHandler.ComputerBindingOperations} {
		req := computerTestRequest(http.MethodGet, "/api/me/computer-bindings/"+first, nil)
		req.Header.Set("X-User-ID", otherUser)
		req = withURLParam(req, "id", first)
		testutil.Call(t, handler, req).Want(http.StatusNotFound)
	}
}

func TestComputerBindingSummaryRespectsWorkspaceAccess(t *testing.T) {
	machine := dbfx.Insert(t, "computer", testutil.Cols{"name": "summary-host", "host": "fake.invalid", "port": 22, "ssh_user": "operator", "created_by": testUserID})
	privateWorkspace := dbfx.Insert(t, "workspace", testutil.Cols{"name": "Private workspace name", "slug": "binding-summary-private"})
	binding := dbfx.Insert(t, "computer_binding", testutil.Cols{"computer_id": machine, "user_id": testUserID, "workspace_id": privateWorkspace, "username": "summary-user", "verified": true, "state": "pending"})
	operation := dbfx.Insert(t, "computer_operation", testutil.Cols{"binding_id": binding, "computer_id": machine, "username": "summary-user", "user_id": testUserID, "kind": "runtime_install", "state": "running", "deadline_at": time.Now().Add(-time.Minute)})
	var list []computerBinding
	testutil.Call(t, testHandler.ComputerBindings, computerTestRequest(http.MethodGet, "/api/me/computer-bindings", nil)).Want(200).JSON(&list)
	var found computerBinding
	for _, b := range list {
		if b.ID == binding {
			found = b
		}
	}
	if found.WorkspaceName != "" || found.WorkspaceAccess != "unavailable" || found.OperationBusy || found.LatestOperation == nil || found.LatestOperation.ID != operation || found.LatestOperation.State != "interrupted" || found.LatestOperation.FinishedAt != nil {
		t.Fatalf("summary leaked workspace or lost expired operation: %+v", found)
	}
	req := withURLParam(computerTestRequest(http.MethodGet, "/api/me/computer-bindings/"+binding, nil), "id", binding)
	var detail bindingDetail
	testutil.Call(t, testHandler.ComputerBindingDetail, req).Want(200).JSON(&detail)
	if detail.WorkspaceName != "" || detail.WorkspaceAccess != "unavailable" {
		t.Fatalf("detail disclosed workspace: %+v", detail)
	}
	dbfx.Insert(t, "member", testutil.Cols{"workspace_id": privateWorkspace, "user_id": testUserID, "role": "member"})
	testutil.Call(t, testHandler.ComputerBindings, computerTestRequest(http.MethodGet, "/api/me/computer-bindings", nil)).Want(200).JSON(&list)
	for _, b := range list {
		if b.ID == binding && (b.WorkspaceName != "Private workspace name" || b.WorkspaceAccess != "accessible") {
			t.Fatalf("member lost workspace name: %+v", b)
		}
	}
	dbfx.Exec(t, `UPDATE computer_binding SET workspace_id=NULL,state='detached' WHERE id=$1`, binding)
	testutil.Call(t, testHandler.ComputerBindings, computerTestRequest(http.MethodGet, "/api/me/computer-bindings", nil)).Want(200).JSON(&list)
	for _, b := range list {
		if b.ID == binding && (b.WorkspaceName != "" || b.WorkspaceAccess != "none") {
			t.Fatalf("detached summary: %+v", b)
		}
	}
}
