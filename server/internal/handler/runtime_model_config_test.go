package handler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestRuntimeModelConfigPreviewSaveTestAndSecretBoundary(t *testing.T) {
	_, mode, _ := modelSourceFixture(t)
	_, binding := operationBinding(t)
	testutil.Call(t, testHandler.AdminUserModelKeyGrants, withURLParam(computerTestRequest("PUT", "/", map[string]any{"ids": []string{"9007199254740993"}}), "id", testUserID)).Want(200)
	root := t.TempDir()
	transport := t.TempDir()
	t.Setenv("MULTICA_MODEL_TEST_HOME", root)
	t.Setenv("MULTICA_MODEL_TEST_CLI", filepath.Join(transport, "fake-cli"))
	t.Setenv("OPENAI_API_KEY", "fixture-operator-token-must-not-be-used")
	t.Setenv("MULTICA_COMPUTER_SSH_KEY", filepath.Join(transport, "fake-key"))
	t.Setenv("MULTICA_COMPUTER_STATE_DIR", filepath.Join(transport, "locks"))
	t.Setenv("PATH", transport+":"+os.Getenv("PATH"))
	ssh := `#!/usr/bin/env python3
import json,os,shlex,subprocess,sys
command=sys.argv[-1]
if 'pam_start' in command:
    sys.stdin.read()
    sys.exit(0)
args=shlex.split(command)
if args[:6]!=['sudo','-n','runuser','-u','alice','--']:
    sys.exit(99)
script=args[-1]
assert script.count('    main()\n')==1
home=json.dumps(os.environ['MULTICA_MODEL_TEST_HOME'])
if 'MULTICA_CONNECTION_OK' in script:
    script=script.replace('    main()\n','    main('+home+', '+json.dumps(os.environ['MULTICA_MODEL_TEST_CLI'])+')\n')
else:
    script=script.replace('    main()\n','    main('+home+')\n')
sys.exit(subprocess.run([sys.executable,'-c',script],input=sys.stdin.buffer.read()).returncode)
`
	cli := `#!/usr/bin/env python3
import json,os,sys
assert sys.argv[sys.argv.index('--model')+1]=='model-a'
assert 'OPENAI_API_KEY' not in os.environ
with open(os.path.join(os.environ['HOME'],'.codex/auth.json')) as f:
    assert json.load(f)['OPENAI_API_KEY']=='fixture-model-secret'
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'MULTICA_CONNECTION_OK'}}))
print(json.dumps({'type':'turn.completed'}))
`
	for name, script := range map[string]string{"ssh": ssh, "fake-cli": cli} {
		if err := os.WriteFile(filepath.Join(transport, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	var read runtimeConfigResponse
	rr := testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "read", runtimeConfigRequest{Password: "fixture-password"})).Want(200)
	rr.JSON(&read)
	if rr.Header().Get("Cache-Control") != "no-store" || read.Snapshot.Exists() {
		t.Fatal("unsafe or unexpected initial read")
	}
	var preview runtimeConfigResponse
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "preview", runtimeConfigRequest{Password: "fixture-password", ExpectedRevision: read.Snapshot.Revision, APIKeyID: "9007199254740993", ModelID: "model-a", APIType: "openai-responses"})).Want(200).JSON(&preview)
	if !strings.Contains(preview.Snapshot.Contents()[".codex/auth.json"], "fixture-model-secret") {
		t.Fatal("preview did not resolve key")
	}
	if _, err := os.Stat(filepath.Join(root, ".codex/auth.json")); !os.IsNotExist(err) {
		t.Fatal("preview wrote a file")
	}
	input := runtimeConfigRequest{Password: "fixture-password", ExpectedRevision: read.Snapshot.Revision, APIKeyID: "9007199254740993", ModelID: "model-a", APIType: "openai-responses", Files: preview.Snapshot.Contents(), CreateOnly: true}
	mode.Store(3)
	var rotated map[string]string
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "save", input)).Want(409).JSON(&rotated)
	if rotated["code"] != "model_key_changed" {
		t.Fatal("rotated key did not require a new preview")
	}
	if _, err := os.Stat(filepath.Join(root, ".codex/auth.json")); !os.IsNotExist(err) {
		t.Fatal("stale preview was written")
	}
	mode.Store(0)
	var saved runtimeConfigResponse
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "save", input)).Want(200).JSON(&saved)
	if !saved.Saved || saved.APIKeyID != "9007199254740993" {
		t.Fatal("save attribution missing")
	}
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "save", input)).Want(409)
	var tested runtimeConfigResponse
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "test", runtimeConfigRequest{Password: "fixture-password", ExpectedRevision: saved.Snapshot.Revision, ModelID: "must-not-override-saved-model"})).Want(200).JSON(&tested)
	if tested.Test == nil || tested.Test.Status != "succeeded" || tested.Test.Model != "model-a" {
		t.Fatalf("test did not use saved configuration: %+v", tested.Test)
	}
	// Database metadata and history must never contain secrets or rendered files.
	var metadata []byte
	if err := testPool.QueryRow(t.Context(), `SELECT row_to_json(c) FROM computer_runtime_model_config c WHERE binding_id=$1`, binding).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	var operations []remoteOperation
	testutil.Call(t, testHandler.ComputerBindingOperations, bindingRequest("GET", binding, nil)).Want(200).JSON(&operations)
	history, _ := json.Marshal(operations)
	for _, payload := range [][]byte{metadata, history} {
		for _, secret := range []string{"fixture-model-secret", "fixture-password", "fixture-directory-secret", "fixture-operator-token"} {
			if strings.Contains(string(payload), secret) {
				t.Fatal("secret persisted outside Linux private files")
			}
		}
	}
	// Saving clears every stale test field, not just its status.
	input.ExpectedRevision = saved.Snapshot.Revision
	input.CreateOnly = false
	if _, err := testPool.Exec(t.Context(), `UPDATE computer_runtime_model_config SET last_test_status='failed',last_test_error_code='model_auth_failed' WHERE binding_id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "save", input)).Want(200).JSON(&saved)
	var errorCode, testStatus string
	if err := testPool.QueryRow(t.Context(), `SELECT last_test_error_code,last_test_status FROM computer_runtime_model_config WHERE binding_id=$1`, binding).Scan(&errorCode, &testStatus); err != nil || errorCode != "" || testStatus != "untested" {
		t.Fatal("save retained stale test failure")
	}
	// Revoked grants do not remain attributed in either the list or editor.
	testutil.Call(t, testHandler.AdminUserModelKeyGrants, withURLParam(computerTestRequest("PUT", "/", map[string]any{"ids": []string{}}), "id", testUserID)).Want(200)
	assets, err := testHandler.runtimeAssets(t.Context(), binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.ID == "codex" && (asset.ModelKeyName != "" || asset.ModelConfigStatus != "authorization_invalid") {
			t.Fatal("revoked grant still attributed")
		}
	}
	var revoked runtimeConfigResponse
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "read", runtimeConfigRequest{Password: "fixture-password"})).Want(200).JSON(&revoked)
	if revoked.APIKeyID != "" || revoked.ConfigStatus != "authorization_invalid" {
		t.Fatal("read returned revoked key")
	}
	testutil.Call(t, testHandler.AdminUserModelKeyGrants, withURLParam(computerTestRequest("PUT", "/", map[string]any{"ids": []string{"9007199254740993"}}), "id", testUserID)).Want(200)
	// A CLI test proves connectivity, not a new authorization. Source changes
	// invalidate attribution without deleting the already-written native files.
	dbfx.Exec(t, `UPDATE computer_runtime_model_config SET source_id=NULL,upstream_id=NULL,status='authorization_invalid' WHERE binding_id=$1 AND runtime_kind='codex'`, binding)
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "test", runtimeConfigRequest{Password: "fixture-password", ExpectedRevision: saved.Snapshot.Revision})).Want(200)
	var configStatus string
	if err := testPool.QueryRow(t.Context(), `SELECT status FROM computer_runtime_model_config WHERE binding_id=$1 AND runtime_kind='codex'`, binding).Scan(&configStatus); err != nil || configStatus != "authorization_invalid" {
		t.Fatalf("CLI test restored invalid authorization: status=%s err=%v", configStatus, err)
	}
	// Reinstall after deletion writes fresh files and must replace stale key,
	// model, revision and test metadata even if a previous revision was tracked.
	remote := computer.SSHRemote{Host: "fake.invalid", Port: 22, User: "operator", KeyPath: filepath.Join(transport, "fake-key"), Context: t.Context()}
	template, err := computer.BuiltinModelTemplate("codex")
	if err != nil {
		t.Fatal(err)
	}
	template.DefaultModel = "model-a"
	testutil.Call(t, testHandler.AdminRuntimeConfigTemplates, withURLParam(computerTestRequest("PUT", "/", template), "kind", "codex")).Want(200)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM runtime_config_template WHERE runtime_kind='codex' AND updated_by=$1`, testUserID)
	})
	for _, path := range []string{".codex/auth.json", ".codex/config.toml"} {
		if err := os.Remove(filepath.Join(root, path)); err != nil {
			t.Fatal(err)
		}
	}
	dbfx.Exec(t, `UPDATE computer_runtime_model_config SET upstream_id='stale-key',file_revision='stale-revision',model_id='old-model',last_test_status='failed',last_test_error_code='model_auth_failed' WHERE binding_id=$1 AND runtime_kind='codex'`, binding)
	if err := testHandler.initializeRuntimeModelConfig(t.Context(), testUserID, binding, "alice", "codex", remote); err != nil {
		t.Fatal(err)
	}
	var initialized runtimeConfigResponse
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "read", runtimeConfigRequest{Password: "fixture-password"})).Want(200).JSON(&initialized)
	if initialized.APIKeyID != "9007199254740993" || initialized.ModelID != "model-a" || initialized.ConfigStatus != "saved" || initialized.Test != nil {
		t.Fatal("reinstallation kept stale key or test metadata")
	}
	// Installation after a user edit must preserve the existing native file.
	custom := saved.Snapshot.Contents()[".codex/config.toml"] + "\n# user-custom-setting\n"
	if err := os.WriteFile(filepath.Join(root, ".codex/config.toml"), []byte(custom), 0600); err != nil {
		t.Fatal(err)
	}

	if err := testHandler.initializeRuntimeModelConfig(t.Context(), testUserID, binding, "alice", "codex", remote); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(root, ".codex/config.toml"))
	if err != nil || string(after) != custom {
		t.Fatal("installation replaced user configuration")
	}
	var edited runtimeConfigResponse
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "read", runtimeConfigRequest{Password: "fixture-password"})).Want(200).JSON(&edited)
	req := modelConfigRequest("POST", binding, "codex", "test", runtimeConfigRequest{Password: "fixture-password", ExpectedRevision: edited.Snapshot.Revision})
	req.Header.Set("Prefer", "respond-async")
	var accepted runtimeConfigResponse
	testutil.Call(t, testHandler.RuntimeModelConfig, req).Want(202).JSON(&accepted)
	if accepted.OperationID == "" || accepted.Test != nil || accepted.Snapshot.Exists() {
		t.Fatal("async acknowledgement contains result or secrets")
	}
	deadline := time.Now().Add(10 * time.Second)
	var completed runtimeConfigResponse
	for {
		testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "result", runtimeConfigRequest{OperationID: accepted.OperationID})).Want(200).JSON(&completed)
		if completed.OperationState != "running" && completed.OperationState != "queued" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("async test did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Test == nil || completed.Test.Status != "succeeded" || completed.Snapshot.Exists() {
		t.Fatal("polling missing test result or returned files")
	}
	var savedID *string
	if err := testPool.QueryRow(t.Context(), `SELECT upstream_id FROM computer_runtime_model_config WHERE binding_id=$1`, binding).Scan(&savedID); err != nil || savedID != nil {
		t.Fatal("SSH edit retained old key attribution")
	}
	// A test whose metadata cannot be stored must not be reported as success.
	if _, err = testPool.Exec(t.Context(), `ALTER TABLE computer_runtime_model_config ADD CONSTRAINT fixture_test_metadata_failure CHECK (last_test_status='untested') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `ALTER TABLE computer_runtime_model_config DROP CONSTRAINT IF EXISTS fixture_test_metadata_failure`)
	})
	var failure map[string]string
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "test", runtimeConfigRequest{Password: "fixture-password", ExpectedRevision: edited.Snapshot.Revision})).Want(422).JSON(&failure)
	if failure["code"] != "model_config_metadata_failed" {
		t.Fatal("metadata write failure masked")
	}
	if _, err = testPool.Exec(t.Context(), `ALTER TABLE computer_runtime_model_config DROP CONSTRAINT fixture_test_metadata_failure`); err != nil {
		t.Fatal(err)
	}
	// Repairing a present file does not require inventing a missing credential.
	if err = os.Remove(filepath.Join(root, ".codex/auth.json")); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "read", runtimeConfigRequest{Password: "fixture-password"})).Want(200).JSON(&edited)
	saveRequest := modelConfigRequest("POST", binding, "codex", "save", runtimeConfigRequest{Password: "fixture-password", ExpectedRevision: edited.Snapshot.Revision, Files: map[string]string{".codex/config.toml": custom}})
	saveRequest.Header.Set("Prefer", "respond-async")
	testutil.Call(t, testHandler.RuntimeModelConfig, saveRequest).Want(202).JSON(&accepted)
	deadline = time.Now().Add(10 * time.Second)
	for {
		testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "result", runtimeConfigRequest{OperationID: accepted.OperationID})).Want(200).JSON(&completed)
		if completed.OperationState != "running" && completed.OperationState != "queued" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("async save did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !completed.Saved || completed.ConfigStatus != "needs_configuration" || completed.Snapshot.Exists() || len(completed.Snapshot.Files) != 2 {
		t.Fatal("async save did not return safe incomplete-file metadata")
	}
	if _, err = testPool.Exec(t.Context(), `UPDATE computer_runtime_model_config SET file_manifest=jsonb_set(file_manifest,'{files,0,path}','".ssh/config.json"') WHERE binding_id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.RuntimeModelConfig, modelConfigRequest("POST", binding, "codex", "read", runtimeConfigRequest{Password: "fixture-password"})).Want(422)
}
