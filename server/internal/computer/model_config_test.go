package computer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/sub2api"
)

func TestModelTemplatesEscapeAndPreserveUserFields(t *testing.T) {
	key := sub2api.Resolved{APIKeyToken: "test-\"key\\with-special", Endpoints: []sub2api.Endpoint{{APIType: "openai-responses", BaseURL: "https://model.example/v1", DefaultModel: "test-model"}, {APIType: "anthropic-messages", BaseURL: "https://model.example", DefaultModel: "test-model"}, {APIType: "openai-chat-completions", BaseURL: "https://model.example/v1", DefaultModel: "test-model"}}, Models: []sub2api.Model{{ID: "test-model", APITypes: []string{"openai-responses", "anthropic-messages", "openai-chat-completions"}}}}
	for _, kind := range []string{"claude", "codex", "opencode", "openclaw", "grok", "kimi", "omp", "pi"} {
		t.Run(kind, func(t *testing.T) {
			template, err := BuiltinModelTemplate(kind)
			if err != nil {
				t.Fatal(err)
			}
			files, _, _, err := RenderModelFiles(template, key, "", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = ValidateModelFiles(template, files); err != nil {
				t.Fatal(err)
			}
			key.APIKeyToken = "rotated-test-key"
			changed, _, _, err := RenderModelFiles(template, key, "", "", files)
			if err != nil {
				t.Fatal(err)
			}
			if len(changed) != len(files) {
				t.Fatal("file set changed")
			}
		})
	}
	template, _ := BuiltinModelTemplate("codex")
	files, _, _, err := RenderModelFiles(template, key, "test-model", "openai-responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	files[".codex/config.toml"] += "# user comment\n[custom]\nsetting = 'preserve me'\n"
	template.Files[0].Content = "invalid replacement template"
	files, _, _, err = RenderModelFiles(template, key, "test-model", "openai-responses", files)
	if err != nil || !strings.Contains(files[".codex/config.toml"], "# user comment\n[custom]\nsetting = 'preserve me'") {
		t.Fatal("existing user content lost")
	}
}

type configScriptRunner struct {
	root   string
	script string
}

func (r configScriptRunner) Run(ctx context.Context, argv []string, stdin string) (string, error) {
	script := r.script
	if script == "" {
		script = runtimeConfigFilesScript
	}
	if strings.Count(script, "    main()\n") != 1 {
		return "", fmt.Errorf("unsafe script test injection")
	}
	script = strings.Replace(script, "    main()\n", "    main("+pythonLiteral(r.root)+")\n", 1)
	cmd := exec.CommandContext(ctx, "python3", "-c", script)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	return string(out), err
}
func pythonLiteral(s string) string { b, _ := json.Marshal(s); return string(b) }
func testConfigRemote(t *testing.T) (SSHRemote, string) {
	t.Helper()
	root := t.TempDir()
	return SSHRemote{Host: "example.test", Port: 22, User: "operator", KeyPath: "test-key", RunCmd: configScriptRunner{root: root}}, root
}
func TestModelConfigConflictAndCreateOnly(t *testing.T) {
	remote, root := testConfigRemote(t)
	paths := []string{".codex/auth.json", ".codex/config.toml"}
	first, err := remote.ReadModelConfig("tester", paths)
	if err != nil || first.Exists() {
		t.Fatalf("initial read: %v", err)
	}
	files := map[string]string{paths[0]: `{"OPENAI_API_KEY":"test-secret"}`, paths[1]: "model='a'\n"}
	saved, err := remote.SaveModelConfig("tester", paths, files, first.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(root, paths[0]))
	if info.Mode().Perm() != 0600 {
		t.Fatal("secret is not private")
	}
	_, err = remote.SaveModelConfig("tester", paths, files, first.Revision, false)
	if ErrorCode(err, "") != "model_config_conflict" {
		t.Fatalf("stale revision accepted: %v", err)
	}
	_, err = remote.SaveModelConfig("tester", paths, files, saved.Revision, true)
	if ErrorCode(err, "") != "model_config_exists" {
		t.Fatal("create replaced existing files")
	}
}
func TestModelConfigRejectsSymlinkAndRollsBackSecondFile(t *testing.T) {
	remote, root := testConfigRemote(t)
	paths := []string{".codex/auth.json", ".codex/config.toml"}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, ".codex")); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ReadModelConfig("tester", paths); err == nil {
		t.Fatal("followed symlink")
	}
	os.Remove(filepath.Join(root, ".codex"))
	first, _ := remote.ReadModelConfig("tester", paths)
	initial := map[string]string{paths[0]: "original-auth", paths[1]: "original-config"}
	saved, err := remote.SaveModelConfig("tester", paths, initial, first.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	failureScript := strings.Replace(runtimeConfigFilesScript, "        for entry in entries:\n            parent = directory", "        for entry in entries:\n            if entry['path'].endswith('config.toml'):\n                raise ValueError('model_config_invalid')\n            parent = directory", 1)
	remote.RunCmd = configScriptRunner{root: root, script: failureScript}
	_, err = remote.SaveModelConfig("tester", paths, map[string]string{paths[0]: "new-auth", paths[1]: "new-config"}, saved.Revision, false)
	if err == nil {
		t.Fatal("injected write failure ignored")
	}
	remote.RunCmd = configScriptRunner{root: root}
	after, err := remote.ReadModelConfig("tester", paths)
	if err != nil || after.Revision != saved.Revision {
		t.Fatalf("rollback failed: %v", err)
	}
}
func TestModelConnectionUsesFakeExecutableAndRequiresReply(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "empty"}[success], func(t *testing.T) {
			root := t.TempDir()
			executable := filepath.Join(root, "fake-codex")
			output := `{"type":"turn.completed"}`
			if success {
				output = `{"type":"item.completed","item":{"type":"agent_message","text":"MULTICA_CONNECTION_OK"}}` + "\n" + output
			}
			script := "#!/usr/bin/env python3\nprint(" + pythonLiteral(output) + ")\n"
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if strings.Count(modelConnectionScript, "    main()\n") != 1 {
				t.Fatal("unsafe CLI test injection")
			}
			code := strings.Replace(modelConnectionScript, "    main()\n", "    main("+pythonLiteral(root)+", "+pythonLiteral(executable)+")\n", 1)
			cmd := exec.Command("python3", "-c", code)
			cmd.Stdin = strings.NewReader(`{"kind":"codex","model":"model-a","model_selector":"model-a"}`)
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			var result ModelConnectionTest
			if json.Unmarshal(out, &result) != nil {
				t.Fatal("invalid test result")
			}
			if (result.Status == "succeeded") != success {
				t.Fatalf("wrong outcome: %s", out)
			}
		})
	}
}

func TestModelJSONCEditingPreservesCommentsAndCustomFields(t *testing.T) {
	content := "{\n // user note\n \"token\": \"old\", \"custom\": true,\n}\n"
	updated, err := patchModelFields("jsonc", content, map[string]string{"token": "new-\"secret"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseModelFile("jsonc", updated)
	if err != nil || parsed["token"] != "new-\"secret" || parsed["custom"] != true || !strings.Contains(updated, "// user note") {
		t.Fatal("JSONC content or comments changed")
	}
}

func TestModelConnectionACPAssemblesChunksAndDeniesTools(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "fake-acp")
	script := `#!/usr/bin/env python3
import json,sys
for line in sys.stdin:
    r=json.loads(line)
    method=r.get('method')
    if method=='initialize':
        print(json.dumps({'jsonrpc':'2.0','id':1,'result':{'protocolVersion':1}}),flush=True)
    elif method=='session/new':
        print(json.dumps({'jsonrpc':'2.0','id':2,'result':{'sessionId':'fixture'}}),flush=True)
    elif method=='session/set_model':
        assert r['params']['modelId']=='multica'
        print(json.dumps({'jsonrpc':'2.0','id':4,'result':{}}),flush=True)
    elif method=='session/prompt':
        print(json.dumps({'jsonrpc':'2.0','id':77,'method':'session/request_permission','params':{}}),flush=True)
    elif r.get('id')==77:
        assert r['result']['outcome']['outcome']=='cancelled'
        for text in ['MULTICA_','CONNECTION_','OK']:
            print(json.dumps({'jsonrpc':'2.0','method':'session/update','params':{'update':{'sessionUpdate':'agent_message_chunk','content':{'type':'text','text':text}}}}),flush=True)
        print(json.dumps({'jsonrpc':'2.0','id':3,'result':{'stopReason':'end_turn'}}),flush=True)
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if strings.Count(modelConnectionScript, "    main()\n") != 1 {
		t.Fatal("unsafe CLI test injection")
	}
	code := strings.Replace(modelConnectionScript, "    main()\n", "    main("+pythonLiteral(root)+", "+pythonLiteral(executable)+")\n", 1)
	cmd := exec.Command("python3", "-c", code)
	cmd.Stdin = strings.NewReader(`{"kind":"kimi","model":"model-a","model_selector":"multica"}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var result ModelConnectionTest
	if json.Unmarshal(out, &result) != nil || result.Status != "succeeded" {
		t.Fatal("ACP test did not assemble reply chunks")
	}
}

func TestModelStaticCheckDetectsMissingFieldsAndCrossFileModelMismatch(t *testing.T) {
	template, err := BuiltinModelTemplate("pi")
	if err != nil {
		t.Fatal(err)
	}
	key := sub2api.Resolved{APIKeyToken: "fixture", Endpoints: []sub2api.Endpoint{{APIType: "openai-responses", BaseURL: "https://model.example/v1", DefaultModel: "model-a"}}, Models: []sub2api.Model{{ID: "model-a", APITypes: []string{"openai-responses"}}}}
	files, _, _, err := RenderModelFiles(template, key, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckModelFiles(template, files); err != nil {
		t.Fatal(err)
	}
	files[".pi/agent/settings.json"] = `{"defaultProvider":"multica","defaultModel":"other-model"}`
	if err = CheckModelFiles(template, files); err == nil {
		t.Fatal("cross-file model mismatch accepted")
	}
	files[".pi/agent/settings.json"] = `{}`
	if err = CheckModelFiles(template, files); ErrorCode(err, "") != "model_config_mapping_missing" {
		t.Fatal("missing managed fields accepted")
	}
}

func TestModelSelectionSwitchesNativeProviderAndOAuthMode(t *testing.T) {
	template, _ := BuiltinModelTemplate("codex")
	key := sub2api.Resolved{APIKeyToken: "assigned-fixture-key", Endpoints: []sub2api.Endpoint{{APIType: "openai-responses", BaseURL: "https://model.example/v1", DefaultModel: "model-a"}}, Models: []sub2api.Model{{ID: "model-a", APITypes: []string{"openai-responses"}}}}
	initial, _, _, err := RenderModelFiles(template, key, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	initial[".codex/config.toml"] = strings.Replace(initial[".codex/config.toml"], `model_provider = "multica"`, `model_provider = "other"`, 1) + "\n[custom]\nkeep=true\n"
	initial[".codex/auth.json"] = `{"OPENAI_API_KEY":null,"auth_mode":"chatgpt","tokens":{"access_token":"old-fixture-oauth"},"custom":"preserve"}`
	changed, _, _, err := RenderModelFiles(template, key, "", "", initial)
	if err != nil {
		t.Fatal(err)
	}
	root, err := parseModelFile("toml", changed[".codex/config.toml"])
	if err != nil || root["model_provider"] != "multica" || !strings.Contains(changed[".codex/config.toml"], "keep=true") {
		t.Fatal("native provider selection not updated")
	}
	auth, _ := parseModelFile("json", changed[".codex/auth.json"])
	if auth["auth_mode"] != nil || auth["tokens"] != nil || auth["OPENAI_API_KEY"] != "assigned-fixture-key" || auth["custom"] != "preserve" {
		t.Fatal("OAuth took precedence or custom auth fields were lost")
	}
	delete(initial, ".codex/auth.json")
	if _, _, _, err = RenderModelFiles(template, key, "", "", initial); err != nil {
		t.Fatal("missing auth file could not be initialized without replacing existing config")
	}
}

func TestConfiguredModelIsCanonicalAndConsistent(t *testing.T) {
	for _, kind := range []string{"openclaw", "omp"} {
		template, err := BuiltinModelTemplate(kind)
		if err != nil {
			t.Fatal(err)
		}
		key := sub2api.Resolved{APIKeyToken: "fixture", Endpoints: []sub2api.Endpoint{{APIType: "openai-responses", BaseURL: "https://model.example/v1"}}, Models: []sub2api.Model{{ID: "model-a", APITypes: []string{"openai-responses"}}}}
		files, _, _, err := RenderModelFiles(template, key, "model-a", "openai-responses", nil)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 1000; i++ {
			if ConfiguredModel(template, files) != "model-a" {
				t.Fatal("model ID depends on map iteration")
			}
		}
		if ConfiguredModelSelector(template, files) != "multica/model-a" {
			t.Fatal("native model selector lost")
		}
	}
}

func TestModelJournalCrashRecoveryPreservesExternalEdits(t *testing.T) {
	for _, mode := range []string{"partial-journal", "external-edit"} {
		t.Run(mode, func(t *testing.T) {
			remote, root := testConfigRemote(t)
			paths := []string{".codex/config.toml"}
			first, err := remote.ReadModelConfig("tester", paths)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := remote.SaveModelConfig("tester", paths, map[string]string{paths[0]: "original"}, first.Revision, true)
			if err != nil {
				t.Fatal(err)
			}
			var old, replacement string
			if mode == "partial-journal" {
				old = "        pending = True"
				replacement = "        pending = True\n        os._exit(9)"
			} else {
				old = "        private_write(journal_fd, \"committed.json\", json.dumps({\"entries\": entries, \"committed\": True}).encode())"
				replacement = "        private_write(journal_fd, \"committed.json\", json.dumps({\"entries\": entries, \"committed\": True}).encode())\n        os._exit(9)"
			}
			if strings.Count(runtimeConfigFilesScript, old) != 1 {
				t.Fatal("crash injection no longer matches")
			}
			remote.RunCmd = configScriptRunner{root: root, script: strings.Replace(runtimeConfigFilesScript, old, replacement, 1)}
			if _, err = remote.SaveModelConfig("tester", paths, map[string]string{paths[0]: "new"}, saved.Revision, false); err == nil {
				t.Fatal("crash did not occur")
			}
			remote.RunCmd = configScriptRunner{root: root}
			if mode == "external-edit" {
				if err = os.WriteFile(filepath.Join(root, paths[0]), []byte("external-user-edit"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err = remote.ReadModelConfig("tester", paths); ErrorCode(err, "") != "model_config_conflict" {
					t.Fatalf("expected recovery conflict: %v", err)
				}
			}
			after, err := remote.ReadModelConfig("tester", paths)
			if err != nil {
				t.Fatal(err)
			}
			want := "original"
			if mode == "external-edit" {
				want = "external-user-edit"
			}
			if after.Contents()[paths[0]] != want {
				t.Fatal("recovery overwrote an external edit or lost the original")
			}
		})
	}
}

func TestModelJournalRecoveryAfterConfigDirectoryRemoval(t *testing.T) {
	remote, root := testConfigRemote(t)
	path := ".codex/config.toml"
	first, err := remote.ReadModelConfig("tester", []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = remote.SaveModelConfig("tester", []string{path}, map[string]string{path: "original"}, first.Revision, true); err != nil {
		t.Fatal(err)
	}
	old := "        pending = True"
	replacement := "        pending = True\n        os._exit(9)"
	if strings.Count(runtimeConfigFilesScript, old) != 1 {
		t.Fatal("crash injection no longer matches")
	}
	remote.RunCmd = configScriptRunner{root: root, script: strings.Replace(runtimeConfigFilesScript, old, replacement, 1)}
	saved, err := remote.ReadModelConfig("tester", []string{path})
	if err != nil || saved.Contents()[path] != "original" {
		t.Fatalf("initial read after save failed: %v", err)
	}
	if _, err = remote.SaveModelConfig("tester", []string{path}, map[string]string{path: "new"}, saved.Revision, false); err == nil {
		t.Fatal("crash did not occur")
	}
	if err = os.RemoveAll(filepath.Join(root, ".codex")); err != nil {
		t.Fatal(err)
	}
	remote.RunCmd = configScriptRunner{root: root}
	if _, err = remote.ReadModelConfig("tester", []string{path}); ErrorCode(err, "") != "model_config_conflict" {
		t.Fatalf("missing config directory should be a recoverable conflict: %v", err)
	}
	// Recovery consumes the journal. A later read must see an empty config
	// rather than repeatedly returning credential_transfer_failed.
	after, err := remote.ReadModelConfig("tester", []string{path})
	if err != nil || after.Contents()[path] != "" {
		t.Fatalf("journal was not consumed after conflict: %v", err)
	}
}

func TestModelJournalCleansPrivateStagingAtEachCrashPoint(t *testing.T) {
	for _, point := range []string{
		`        pending = True`,
		`                private_write(parent, entry["staged"], value.encode())`,
		`                    private_write(parent, entry["backup"], current["files"][entry["path"]].encode())`,
		`                os.replace(entry["staged"], entry["path"].split("/")[-1], src_dir_fd=parent, dst_dir_fd=parent)`,
	} {
		t.Run(strings.TrimSpace(point), func(t *testing.T) {
			remote, root := testConfigRemote(t)
			paths := []string{".codex/auth.json", ".codex/config.toml"}
			first, err := remote.ReadModelConfig("tester", paths)
			if err != nil {
				t.Fatal(err)
			}
			original := map[string]string{paths[0]: "old-key", paths[1]: "old-model"}
			saved, err := remote.SaveModelConfig("tester", paths, original, first.Revision, true)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(runtimeConfigFilesScript, point) != 1 {
				t.Fatal("unsafe crash injection")
			}
			indent := point[:len(point)-len(strings.TrimLeft(point, " "))]
			remote.RunCmd = configScriptRunner{root: root, script: strings.Replace(runtimeConfigFilesScript, point, point+"\n"+indent+"os._exit(9)", 1)}
			if _, err := remote.SaveModelConfig("tester", paths, map[string]string{paths[0]: "new-key", paths[1]: "new-model"}, saved.Revision, false); err == nil {
				t.Fatal("crash did not occur")
			}
			remote.RunCmd = configScriptRunner{root: root}
			after, err := remote.ReadModelConfig("tester", paths)
			if err != nil || after.Revision != saved.Revision {
				t.Fatalf("recovery lost original configuration: %v", err)
			}
			assertNoModelJournalArtifacts(t, root)
		})
	}
}

func assertNoModelJournalArtifacts(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".multica-new-") || strings.HasPrefix(name, ".multica-old-") || name == "pending.json" || name == "committed.json" || name == "pending.tmp" {
			t.Errorf("orphaned transaction file: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestModelJournalConflictsDoNotBlockOtherRuntimes(t *testing.T) {
	for _, change := range []string{"external-edit", "non-utf8", "hardlink", "directory"} {
		t.Run(change, func(t *testing.T) {
			remote, root := testConfigRemote(t)
			path := ".codex/auth.json"
			first, err := remote.ReadModelConfig("tester", []string{path})
			if err != nil {
				t.Fatal(err)
			}
			saved, err := remote.SaveModelConfig("tester", []string{path}, map[string]string{path: "old-key"}, first.Revision, true)
			if err != nil {
				t.Fatal(err)
			}
			point := `                os.replace(entry["staged"], entry["path"].split("/")[-1], src_dir_fd=parent, dst_dir_fd=parent)`
			if strings.Count(runtimeConfigFilesScript, point) != 1 {
				t.Fatal("unsafe crash injection")
			}
			remote.RunCmd = configScriptRunner{root: root, script: strings.Replace(runtimeConfigFilesScript, point, point+"\n                os._exit(9)", 1)}
			if _, err := remote.SaveModelConfig("tester", []string{path}, map[string]string{path: "new-key"}, saved.Revision, false); err == nil {
				t.Fatal("crash did not occur")
			}
			full := filepath.Join(root, path)
			switch change {
			case "external-edit":
				err = os.WriteFile(full, []byte("external"), 0600)
			case "non-utf8":
				err = os.WriteFile(full, []byte{0xff, 0xfe}, 0600)
			case "hardlink":
				err = os.Link(full, filepath.Join(root, "user-link"))
			case "directory":
				if err = os.Remove(full); err == nil {
					err = os.Mkdir(full, 0700)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			remote.RunCmd = configScriptRunner{root: root}
			other := []string{".grok/config.toml"}
			if _, err := remote.ReadModelConfig("tester", other); ErrorCode(err, "") != "model_config_conflict" {
				t.Fatalf("wanted recoverable conflict: %v", err)
			}
			if _, err := remote.ReadModelConfig("tester", other); err != nil {
				t.Fatalf("other runtime still blocked: %v", err)
			}
			assertNoModelJournalArtifacts(t, root)
			if change == "external-edit" {
				content, err := os.ReadFile(full)
				if err != nil || string(content) != "external" {
					t.Fatal("external edit was overwritten")
				}
			}
		})
	}
}

func TestModelFieldsNeverReplaceObjectsOrArrays(t *testing.T) {
	for _, format := range []string{"json", "jsonc", "yaml"} {
		for _, content := range []string{`{"key":{"nested":"private"}}`, `{"key":["private"]}`} {
			if _, err := patchModelFields(format, content, map[string]string{"key": "replacement"}); ErrorCode(err, "") != "model_config_mapping_missing" {
				t.Fatalf("%s accepted a non-scalar binding: %v", format, err)
			}
		}
	}
}
