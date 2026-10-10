package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// Faults are injected around real PostgreSQL transactions so rollback and
// ambiguous commit behavior exercise the same persistence contract as production.
type operationFaultStarter struct {
	txStarter
	auditFailures     int
	recoveryError     error
	ambiguousCommit   bool
	beforeBindingLock chan struct{}
}

func (s *operationFaultStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.txStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &operationFaultTx{Tx: tx, owner: s}, nil
}

type operationFaultTx struct {
	pgx.Tx
	owner *operationFaultStarter
}

func (t *operationFaultTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "INSERT INTO computer_audit") && t.owner.auditFailures > 0 {
		t.owner.auditFailures--
		return pgconn.CommandTag{}, &pgconn.PgError{Code: "40001", Message: "test-secret-must-not-be-logged"}
	}
	return t.Tx.Exec(ctx, sql, args...)
}

type operationErrorRow struct{ err error }

func (r operationErrorRow) Scan(...any) error { return r.err }
func (t *operationFaultTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if t.owner.beforeBindingLock != nil && strings.HasPrefix(sql, "SELECT id::text FROM computer_binding WHERE") {
		close(t.owner.beforeBindingLock)
	}
	if strings.HasPrefix(sql, "UPDATE computer_operation") && t.owner.recoveryError != nil {
		return operationErrorRow{t.owner.recoveryError}
	}
	return t.Tx.QueryRow(ctx, sql, args...)
}
func (t *operationFaultTx) Commit(ctx context.Context) error {
	if err := t.Tx.Commit(ctx); err != nil {
		return err
	}
	if t.owner.ambiguousCommit {
		t.owner.ambiguousCommit = false
		return errors.New("connection lost after commit")
	}
	return nil
}

func TestComputerOperationFinishRetriesWithoutReplayingWork(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failures  int
		ambiguous bool
		want      string
		audits    int
	}{
		{"transient audit failure", 1, false, "succeeded", 1},
		{"persistent audit failure", 10, false, "running", 0},
		{"ambiguous commit", 0, true, "succeeded", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, binding := operationBinding(t)
			op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "runtime_install", "codex", "1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			h := *testHandler
			h.TxStarter = &operationFaultStarter{txStarter: h.TxStarter, auditFailures: tc.failures, ambiguousCommit: tc.ambiguous}
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(old)
			runs := 0
			h.runRemoteOperation(op, func(context.Context) (string, error) { runs++; return "1.2.3", nil })
			var state string
			var audits int
			if err := testPool.QueryRow(context.Background(), `SELECT state,(SELECT count(*) FROM computer_audit WHERE binding_id=$2) FROM computer_operation WHERE id=$1`, op, binding).Scan(&state, &audits); err != nil {
				t.Fatal(err)
			}
			if runs != 1 || state != tc.want || audits != tc.audits {
				t.Fatalf("runs=%d state=%s audits=%d", runs, state, audits)
			}
			if !strings.Contains(logs.String(), op) || strings.Contains(logs.String(), "test-secret-must-not-be-logged") {
				t.Fatalf("missing diagnostic or leaked secret: %s", logs.String())
			}
			if tc.want == "running" {
				// Recovery remains explicit, and failed attempts never left an audit behind.
				testHandler.finishRemoteOperation(op, "succeeded", "", "1.2.3")
				if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM computer_audit WHERE binding_id=$1`, binding).Scan(&audits); err != nil || audits != 1 {
					t.Fatalf("retry audit=%d err=%v", audits, err)
				}
			}
		})
	}
}

func TestComputerRecoverySeparatesDatabaseFailureFromConflict(t *testing.T) {
	_, binding := operationBinding(t)
	op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "upgrade", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"no matching row", fmt.Errorf("wrapped: %w", pgx.ErrNoRows), 409},
		{"timeout", context.DeadlineExceeded, 500},
		{"connection failure", errors.New("connection failed"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := *testHandler
			h.TxStarter = &operationFaultStarter{txStarter: h.TxStarter, recoveryError: tc.err}
			testutil.Call(t, h.RecoverComputerOperation, bindingRequest("POST", binding, map[string]string{"operation_id": op, "action": "cancel"})).Want(tc.status)
		})
	}
}

func TestComputerInstallReceiptStopsAtExpiredOperation(t *testing.T) {
	for _, state := range []string{"queued", "running", "succeeded", "failed"} {
		t.Run(state, func(t *testing.T) {
			_, binding := operationBinding(t)
			op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "runtime_install", "codex", "latest")
			if err != nil {
				t.Fatal(err)
			}
			dbfx.Exec(t, `UPDATE computer_operation SET state=$2,error_code='installer_failed',deadline_at=now()-interval '1 minute' WHERE id=$1`, op, state)
			req := bindingRequest("POST", binding, nil)
			ctx, cancel := context.WithTimeout(req.Context(), time.Second)
			defer cancel()
			handler := func(w http.ResponseWriter, r *http.Request) { testHandler.runtimeInstallReceipt(w, r, op) }
			var result map[string]any
			status := 502
			if state == "succeeded" {
				status = 200
			}
			testutil.Call(t, handler, req.WithContext(ctx)).Want(status).JSON(&result)
			if state == "queued" || state == "running" {
				if result["code"] != "interrupted" || result["operation_id"] != op {
					t.Fatalf("unexpected result: %+v", result)
				}
				// A display projection must not release the active operation's safety lock.
				if _, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "upgrade", "", ""); err == nil {
					t.Fatal("expired receipt released safety lock")
				}
			} else if state == "failed" && result["code"] != "installer_failed" {
				t.Fatalf("terminal result overwritten: %+v", result)
			}
		})
	}
}

func TestComputerRecoveredWorkerCannotChangeNewOperation(t *testing.T) {
	_, binding := operationBinding(t)
	op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "upgrade", "", "")
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE computer_binding SET state='running' WHERE id=$1`, binding)
	dbfx.Exec(t, `UPDATE computer_operation SET state='running',deadline_at=now()-interval '1 minute' WHERE id=$1`, op)
	testutil.Call(t, testHandler.RecoverComputerOperation, bindingRequest("POST", binding, map[string]string{"operation_id": op, "action": "acknowledge"})).Want(200)
	next, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "upgrade", "", "")
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE computer_binding SET state='running' WHERE id=$1`, binding)
	dbfx.Exec(t, `UPDATE computer_operation SET state='running' WHERE id=$1`, next)
	wrote := false
	err = testHandler.withRunningOperation(context.Background(), op, func(tx pgx.Tx) error {
		wrote = true
		_, err := tx.Exec(context.Background(), `UPDATE computer_binding SET state='ready' WHERE id=$1`, binding)
		return err
	})
	if !errors.Is(err, errOperationSuperseded) || wrote {
		t.Fatalf("late binding write: wrote=%v err=%v", wrote, err)
	}
	err = testHandler.saveRuntimeProbe(context.Background(), op, binding, runtimeTargets()[0], computer.RuntimeProbe{State: "installed", Version: "old"}, "old", true)
	if !errors.Is(err, errOperationSuperseded) {
		t.Fatalf("late asset write: %v", err)
	}
	testHandler.finishRemoteOperation(op, "succeeded", "", "old")
	var oldState, newState, bindingState string
	var audits int
	err = testPool.QueryRow(context.Background(), `SELECT o.state,n.state,b.state,(SELECT count(*) FROM computer_audit WHERE binding_id=b.id) FROM computer_operation o JOIN computer_binding b ON b.id=o.binding_id JOIN computer_operation n ON n.id=$2 WHERE o.id=$1`, op, next).Scan(&oldState, &newState, &bindingState, &audits)
	if err != nil || oldState != "interrupted" || newState != "running" || bindingState != "running" || audits != 1 {
		t.Fatalf("old=%s new=%s binding=%s audits=%d err=%v", oldState, newState, bindingState, audits, err)
	}
}

func TestComputerOperationPanicIsDiagnosableWithoutSecret(t *testing.T) {
	_, binding := operationBinding(t)
	op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "upgrade", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(old)
	testHandler.runRemoteOperation(op, func(context.Context) (string, error) { panic("secret-in-panic-value") })
	var state, code string
	if err := testPool.QueryRow(context.Background(), `SELECT state,error_code FROM computer_operation WHERE id=$1`, op).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "interrupted" || code != "interrupted" {
		t.Fatalf("panic result: %s/%s", state, code)
	}
	if !strings.Contains(logs.String(), op) || !strings.Contains(logs.String(), "computer_operation_recovery_test.go") || strings.Contains(logs.String(), "secret-in-panic-value") {
		t.Fatalf("unsafe or missing panic log: %s", logs.String())
	}
}

func TestComputerWrappedStartErrorsKeepConflictStatus(t *testing.T) {
	for _, sentinel := range []error{errOperationBusy, errBindingNotReady} {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			operationStartError(w, fmt.Errorf("wrapped: %w", sentinel))
		}
		var body map[string]string
		testutil.Call(t, handler, testutil.JSONRequest("POST", "/", nil)).Want(409).JSON(&body)
		if body["code"] != sentinel.Error() {
			t.Fatalf("wrong code: %+v", body)
		}
	}
}

func TestComputerRecoverySerializesWithWorkerWrite(t *testing.T) {
	_, binding := operationBinding(t)
	op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "upgrade", "", "")
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE computer_operation SET state='running' WHERE id=$1`, op)
	dbfx.Exec(t, `UPDATE computer_binding SET state='running' WHERE id=$1`, binding)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		done <- testHandler.withRunningOperation(ctx, op, func(tx pgx.Tx) error {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := tx.Exec(ctx, `UPDATE computer_binding SET state='ready' WHERE id=$1`, binding)
			if err != nil {
				return err
			}
			// Finish while holding both locks; recovery must see this terminal result.
			_, err = tx.Exec(ctx, `UPDATE computer_operation SET state='succeeded',deadline_at=now()-interval '1 minute' WHERE id=$1`, op)
			return err
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker failed to acquire lock")
	}
	recovery := make(chan *testutil.Response, 1)
	attempting := make(chan struct{})
	h := *testHandler
	h.TxStarter = &operationFaultStarter{txStarter: h.TxStarter, beforeBindingLock: attempting}
	go func() {
		req := bindingRequest("POST", binding, map[string]string{"operation_id": op, "action": "acknowledge"}).WithContext(ctx)
		req = testutil.WithURLParams(req, "id", binding)
		recovery <- testutil.Call(t, h.RecoverComputerOperation, req)
	}()
	select {
	case <-attempting:
	case <-ctx.Done():
		t.Fatal("recovery did not attempt binding lock")
	}
	select {
	case <-recovery:
		t.Fatal("recovery passed a held binding lock")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var result *testutil.Response
	select {
	case result = <-recovery:
	case <-ctx.Done():
		t.Fatal("recovery did not finish")
	}
	if result.Code != 409 {
		t.Fatalf("recovery overwrote committed result: %d %s", result.Code, result.Body.String())
	}
	var state string
	if err := testPool.QueryRow(ctx, `SELECT state FROM computer_binding WHERE id=$1`, binding).Scan(&state); err != nil || state != "ready" {
		t.Fatalf("state=%s err=%v", state, err)
	}
}

func TestComputerDeleteUserThenReprovision(t *testing.T) {
	machine, binding := operationBinding(t)
	dbfx.Exec(t, `UPDATE computer_binding SET state='removed',account_state='present' WHERE id=$1`, binding)
	dir := t.TempDir()
	account := filepath.Join(dir, "account-exists")
	if err := os.WriteFile(account, []byte("present"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_ACCOUNT_FILE", account)
	t.Setenv("MULTICA_COMPUTER_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", filepath.Join(dir, "fake-key"))
	t.Setenv("MULTICA_COMPUTER_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("MULTICA_COMPUTER_SERVER_URL", "https://test.invalid")
	binary := filepath.Join(dir, "fake-cli")
	if err := os.WriteFile(binary, []byte("fake CLI, never executed"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_COMPUTER_CLI_PATH", binary)
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	fakeSSH := `#!/bin/sh
for command do :; done
/bin/cat >/dev/null
case "$command" in
 *"getpwnam(sys.argv[1]); print"*) if test -f "$TEST_ACCOUNT_FILE"; then printf 'yes\n'; else printf 'no\n'; fi;;
 "sudo -n userdel -r alice") /bin/rm "$TEST_ACCOUNT_FILE";;
 *"useradd"*) printf present > "$TEST_ACCOUNT_FILE";;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(fakeSSH), 0700); err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		ID string `json:"operation_id"`
	}
	testutil.Call(t, testHandler.ComputerBindingLifecycle, bindingRequest("POST", binding, map[string]string{"action": "delete_user", "confirm_username": "alice", "password": "fake-password"})).Want(202).JSON(&receipt)
	waitForCompletion := func(op string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var state string
			if err := testPool.QueryRow(context.Background(), `SELECT state FROM computer_operation WHERE id=$1`, op).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state == "succeeded" {
				return
			}
			if state != "queued" && state != "running" {
				t.Fatalf("operation %s ended in %s", op, state)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("operation did not finish")
	}
	waitForCompletion(receipt.ID)
	var detail bindingDetail
	testutil.Call(t, testHandler.ComputerBindingDetail, bindingRequest("GET", binding, nil)).Want(200).JSON(&detail)
	if detail.State != "removed" || detail.AccountState != "missing" || detail.ArchivedAt != nil {
		t.Fatalf("deleted account state: %+v", detail)
	}
	if _, err := os.Stat(account); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fake account not deleted: %v", err)
	}
	token, _ := insertTestPAT(t, time.Time{})
	settings := computer.Settings{GitName: "Human", GitEmail: "h@example.com", GitLabURL: "https://git.example.com", MulticaPAT: token}
	testutil.Call(t, testHandler.ComputerSettings, computerTestRequest("PUT", "/", settings)).Want(200)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_credential WHERE user_id=$1`, testUserID)
	})
	dbfx.Runtime(t, "recreated-account-daemon", testutil.Cols{"daemon_id": binding, "owner_id": testUserID, "status": "online", "last_seen_at": time.Now()})
	var recreated computerBinding
	testutil.Call(t, testHandler.ComputerBindings, computerTestRequest("POST", "/", map[string]string{"computer_id": machine, "workspace_id": testWorkspaceID, "username": "alice", "password": "fake-password", "action": "provision"})).Want(202).JSON(&recreated)
	if recreated.ID != binding {
		t.Fatal("reprovision lost binding identity")
	}
	var op string
	if err := testPool.QueryRow(context.Background(), `SELECT id::text FROM computer_operation WHERE binding_id=$1 AND kind='provision'`, binding).Scan(&op); err != nil {
		t.Fatal(err)
	}
	waitForCompletion(op)
	testutil.Call(t, testHandler.ComputerBindingDetail, bindingRequest("GET", binding, nil)).Want(200).JSON(&detail)
	if detail.State != "ready" || detail.AccountState != "present" {
		t.Fatalf("recreated account state: %+v", detail)
	}
	if _, err := os.Stat(account); err != nil {
		t.Fatalf("account not recreated: %v", err)
	}
}

type panicComputerRunner struct{}

func (panicComputerRunner) Run(context.Context, []string, string) (string, error) {
	panic("secret-in-inner-panic")
}

func TestComputerBindingPanicRecordsInterruptedOperation(t *testing.T) {
	machine, binding := operationBinding(t)
	op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "remove", "", "")
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE computer_binding SET state='running' WHERE id=$1`, binding)
	remote := computer.SSHRemote{Host: "fake.invalid", Port: 22, User: "operator", KeyPath: "/fake/key", RunCmd: panicComputerRunner{}}
	b := computerBinding{ID: binding, ComputerID: machine, WorkspaceID: testWorkspaceID, Username: "alice", State: "running"}
	store := &computer.FileStore{Dir: filepath.Join(t.TempDir(), "state")}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(old)
	testHandler.runRemoteOperation(op, func(ctx context.Context) (string, error) {
		return "", testHandler.runComputerOperation(ctx, op, testUserID, b, remote, store, computer.Request{Password: "fake-password"}, "remove")
	})
	var state, code, bindingState string
	err = testPool.QueryRow(context.Background(), `SELECT o.state,o.error_code,b.state FROM computer_operation o JOIN computer_binding b ON b.id=o.binding_id WHERE o.id=$1`, op).Scan(&state, &code, &bindingState)
	if err != nil || state != "interrupted" || code != "interrupted" || bindingState != "failed" {
		t.Fatalf("operation=%s/%s binding=%s err=%v", state, code, bindingState, err)
	}
	if !strings.Contains(logs.String(), op) || !strings.Contains(logs.String(), "computer_binding.go") || strings.Contains(logs.String(), "secret-in-inner-panic") {
		t.Fatalf("missing or unsafe log: %s", logs.String())
	}
}
