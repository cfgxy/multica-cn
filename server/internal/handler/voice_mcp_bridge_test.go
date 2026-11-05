package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/skillbundle"
)

func TestVoiceMCPBridge_UsesCanonicalToolsListAndCall(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer caller-token" {
			t.Errorf("authorization = %q", got)
		}
		var request struct {
			Method string `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, request.Method)
		result := any(map[string]any{})
		switch request.Method {
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{
				{"name": "list_workspaces", "description": "canonical MCP workspace tool", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
				{"name": "list_projects", "description": "canonical MCP project tool", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"workspace": map[string]any{"type": "string"}}, "required": []string{"workspace"}}},
			}}
		case "tools/call":
			switch request.Params.Name {
			case "list_workspaces":
				result = map[string]any{"content": []map[string]any{{"type": "text", "text": `{"total":1,"workspaces":[{"slug":"member"}]}`}}}
			case "list_projects":
				if got := string(request.Params.Arguments); !strings.Contains(got, `"workspace":"member"`) {
					t.Errorf("project arguments = %s", got)
				}
				result = map[string]any{"content": []map[string]any{{"type": "text", "text": `{"total":1,"projects":[{"title":"Member project"}]}`}}}
			default:
				t.Errorf("tool name = %q", request.Params.Name)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}))
	defer server.Close()

	t.Setenv("MCP_URL", server.URL)
	bridge, err := newVoiceMCPBridge("caller-token")
	if err != nil {
		t.Fatal(err)
	}
	tools, err := bridge.tools(context.Background())
	if err != nil || len(tools) != 2 || tools[0].Name != "list_workspaces" || tools[1].Name != "list_projects" {
		t.Fatalf("tools = %+v, err = %v", tools, err)
	}
	declarations := voiceMCPFunctionDeclarations(tools)
	if len(declarations) != 2 || declarations[0]["name"] != "list_workspaces" || declarations[1]["name"] != "list_projects" {
		t.Fatalf("Gemini declarations = %#v", declarations)
	}
	workspaceResult, err := bridge.call(context.Background(), "list_workspaces", nil)
	if err != nil || !strings.Contains(string(workspaceResult), `"slug":"member"`) {
		t.Fatalf("workspace call result = %s, err = %v", workspaceResult, err)
	}
	projectResult, err := bridge.call(context.Background(), "list_projects", json.RawMessage(`{"workspace":"member"}`))
	if err != nil || !strings.Contains(string(projectResult), "Member project") {
		t.Fatalf("project call result = %s, err = %v", projectResult, err)
	}
	if got := strings.Join(methods, ","); got != "initialize,tools/list,initialize,tools/call,initialize,tools/call" {
		t.Fatalf("MCP sequence = %q", got)
	}
}

func TestNewVoiceMCPBridgeRequiresConfiguredEndpoint(t *testing.T) {
	previous, had := os.LookupEnv("MCP_URL")
	os.Unsetenv("MCP_URL")
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("MCP_URL", previous)
		}
	})
	if _, err := newVoiceMCPBridge("caller-token"); err == nil {
		t.Fatal("expected missing MCP_URL to refuse the bridge")
	}
}

func TestVoiceSkillCatalogCarriesPinnedBundleIdentity(t *testing.T) {
	decl := voiceSkillFunctionDeclaration()
	parameters, ok := decl["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("load_skill parameters = %T, want object", decl["parameters"])
	}
	properties, ok := parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("load_skill properties = %T, want object", parameters["properties"])
	}
	required, ok := parameters["required"].([]string)
	if !ok || len(required) != 3 {
		t.Fatalf("load_skill required = %#v, want id/source/hash", parameters["required"])
	}
	for _, field := range []string{"id", "source", "hash"} {
		if _, ok := properties[field]; !ok {
			t.Errorf("load_skill declaration omitted %q", field)
		}
		if !containsString(required, field) {
			t.Errorf("load_skill does not require %q: %v", field, required)
		}
	}

	catalog := voiceSkillCatalogContext([]service.AgentSkillRefData{{
		ID:          "builtin:multica-mentioning",
		Source:      skillbundle.SourceBuiltin,
		Hash:        "sha256:bundle-hash",
		Name:        "multica-mentioning",
		Description: "mention workflow",
	}})
	for _, want := range []string{"### Available agent skills", `"id":"builtin:multica-mentioning"`, `"source":"builtin"`, `"hash":"sha256:bundle-hash"`} {
		if !strings.Contains(catalog, want) {
			t.Errorf("catalog omitted %q: %s", want, catalog)
		}
	}
}

func voiceSkillCatalogRef(t *testing.T, agentID, name string, source string) ([]service.AgentSkillRefData, service.AgentSkillRefData) {
	t.Helper()
	_, refs, err := testHandler.TaskService.LoadAgentSkillBundles(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatalf("load skill catalog: %v", err)
	}
	for _, ref := range refs {
		if ref.Name == name && ref.Source == source {
			return refs, ref
		}
	}
	t.Fatalf("skill catalog did not include %q from %q: %+v", name, source, refs)
	return nil, service.AgentSkillRefData{}
}

func voiceSkillArgs(t *testing.T, ref service.AgentSkillRefData, hash string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"id": ref.ID, "source": ref.Source, "hash": hash})
	if err != nil {
		t.Fatalf("marshal skill args: %v", err)
	}
	return raw
}

type voiceSkillResponseFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type voiceSkillResponse struct {
	ID      string                   `json:"id"`
	Source  string                   `json:"source"`
	Hash    string                   `json:"hash"`
	Content *string                  `json:"content"`
	Files   []voiceSkillResponseFile `json:"files"`
	Error   *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func decodeVoiceSkillResponse(t *testing.T, raw json.RawMessage) voiceSkillResponse {
	t.Helper()
	var response voiceSkillResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode voice skill response: %v (%s)", err, raw)
	}
	return response
}

func TestExecuteVoiceSkillLoadsOnlyCatalogPinnedBundles(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := seedUsableVoiceSetup(t, "Voice Skill Files", `{"model":"stubby-live-1"}`)
	skillName := "voice-skill-" + time.Now().UTC().Format("20060102150405.000000000")
	skillID := createImportTargetSkill(t, skillName, testUserID, map[string]string{"references/usage.md": "use the checked procedure"})
	const workspaceSkillContent = "# original\n\nUse the checked procedure."
	if _, err := testPool.Exec(context.Background(), `UPDATE skill SET content = $1 WHERE id = $2`, workspaceSkillContent, skillID); err != nil {
		t.Fatalf("set multiline skill content: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`, agentID, skillID); err != nil {
		t.Fatalf("assign skill: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_skill WHERE agent_id = $1 AND skill_id = $2`, agentID, skillID)
	})

	catalog, workspaceRef := voiceSkillCatalogRef(t, agentID, skillName, skillbundle.SourceWorkspace)
	session := db.LiveSession{
		WorkspaceID: parseUUID(testWorkspaceID),
		AgentID:     parseUUID(agentID),
		UserID:      parseUUID(testUserID),
	}
	result, err := testHandler.executeVoiceSkill(context.Background(), session, catalog, voiceSkillArgs(t, workspaceRef, workspaceRef.Hash))
	if err != nil {
		t.Fatalf("executeVoiceSkill: %v", err)
	}
	workspace := decodeVoiceSkillResponse(t, result)
	if workspace.ID != workspaceRef.ID || workspace.Source != skillbundle.SourceWorkspace || workspace.Hash != workspaceRef.Hash ||
		workspace.Content == nil || *workspace.Content != workspaceSkillContent || len(workspace.Files) != 1 ||
		workspace.Files[0].Path != "references/usage.md" || workspace.Files[0].Content != "use the checked procedure" {
		t.Errorf("workspace skill response = %+v, want pinned multiline content and files", workspace)
	}

	builtins := testHandler.TaskService.BuiltinSkills()
	if len(builtins) == 0 {
		t.Fatal("embedded builtin skills are required for voice catalog")
	}
	_, builtinRef := voiceSkillCatalogRef(t, agentID, builtins[0].Name, skillbundle.SourceBuiltin)
	builtin, err := testHandler.executeVoiceSkill(context.Background(), session, catalog, voiceSkillArgs(t, builtinRef, builtinRef.Hash))
	builtinResponse := decodeVoiceSkillResponse(t, builtin)
	if err != nil || builtinResponse.ID != builtinRef.ID || builtinResponse.Source != skillbundle.SourceBuiltin || builtinResponse.Hash != builtinRef.Hash ||
		builtinResponse.Content == nil || *builtinResponse.Content != builtins[0].Content {
		t.Errorf("builtin skill response = %+v, err = %v", builtinResponse, err)
	}

	hashMismatch, err := testHandler.executeVoiceSkill(context.Background(), session, catalog, voiceSkillArgs(t, workspaceRef, "sha256:stale"))
	hashMismatchResponse := decodeVoiceSkillResponse(t, hashMismatch)
	if err != nil || hashMismatchResponse.Error == nil || hashMismatchResponse.Error.Code != "conflict" || hashMismatchResponse.Content != nil {
		t.Errorf("hash mismatch response = %+v, err = %v", hashMismatchResponse, err)
	}
	const changedSkillContent = "# changed after catalog injection"
	if _, err := testPool.Exec(context.Background(), `UPDATE skill SET content = $1 WHERE id = $2`, changedSkillContent, skillID); err != nil {
		t.Fatalf("change skill after catalog injection: %v", err)
	}
	staleBundle, err := testHandler.executeVoiceSkill(context.Background(), session, catalog, voiceSkillArgs(t, workspaceRef, workspaceRef.Hash))
	staleResponse := decodeVoiceSkillResponse(t, staleBundle)
	if err != nil || staleResponse.Error == nil || staleResponse.Error.Code != "conflict" || staleResponse.Content != nil {
		t.Errorf("changed skill with stale catalog hash response = %+v, err = %v; want a content-free conflict", staleResponse, err)
	}

	unboundID := createImportTargetSkill(t, "voice-unbound-"+time.Now().UTC().Format("20060102150405.000000000"), testUserID, nil)
	unboundRef := workspaceRef
	unboundRef.ID = unboundID
	denied, err := testHandler.executeVoiceSkill(context.Background(), session, catalog, voiceSkillArgs(t, unboundRef, unboundRef.Hash))
	deniedResponse := decodeVoiceSkillResponse(t, denied)
	if err != nil || deniedResponse.Error == nil || deniedResponse.Error.Code != "not_found" || deniedResponse.Content != nil {
		t.Errorf("unassigned skill response = %+v, err = %v", deniedResponse, err)
	}
}

// TestExecuteVoiceSkillRechecksInvokePermission keeps a long-lived voice
// session from exposing an already-advertised skill after its caller's invoke
// grant is revoked.
func TestExecuteVoiceSkillRechecksInvokePermission(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := seedUsableVoiceSetup(t, "Voice Skill Revoke", `{"model":"stubby-live-1"}`)
	ownerID := createPermissionTestMember(t, "voice-skill-owner-"+time.Now().UTC().Format("20060102150405.000000000")+"@multica.test")
	dbfx.Exec(t, `UPDATE agent SET owner_id = $2, visibility = 'workspace', permission_mode = 'public_to' WHERE id = $1`, agentID, ownerID)
	dbfx.InsertNoID(t, "agent_invocation_target", testutil.Cols{
		"agent_id":    agentID,
		"target_type": "member",
		"target_id":   testUserID,
	}, "agent_id = $1 AND target_type = 'member' AND target_id = $2", agentID, testUserID)
	dbfx.Cleanup(t, `DELETE FROM agent_invocation_target WHERE agent_id = $1`, agentID)

	skillName := "voice-revocable-skill-" + time.Now().UTC().Format("20060102150405.000000000")
	skillID := createImportTargetSkill(t, skillName, ownerID, nil)
	dbfx.InsertNoID(t, "agent_skill", testutil.Cols{"agent_id": agentID, "skill_id": skillID}, "agent_id = $1 AND skill_id = $2", agentID, skillID)
	dbfx.Cleanup(t, `DELETE FROM agent_skill WHERE agent_id = $1 AND skill_id = $2`, agentID, skillID)

	catalog, ref := voiceSkillCatalogRef(t, agentID, skillName, skillbundle.SourceWorkspace)
	session := db.LiveSession{
		WorkspaceID: parseUUID(testWorkspaceID),
		AgentID:     parseUUID(agentID),
		UserID:      parseUUID(testUserID),
	}
	if result, err := testHandler.executeVoiceSkill(context.Background(), session, catalog, voiceSkillArgs(t, ref, ref.Hash)); err != nil || !strings.Contains(string(result), `"content"`) {
		t.Fatalf("load skill before revoke = %s, err = %v", result, err)
	}

	dbfx.Exec(t, `UPDATE agent SET permission_mode = 'private' WHERE id = $1`, agentID)
	result, err := testHandler.executeVoiceSkill(context.Background(), session, catalog, voiceSkillArgs(t, ref, ref.Hash))
	if err != nil || !strings.Contains(string(result), `"forbidden"`) || strings.Contains(string(result), `"content"`) {
		t.Fatalf("load skill after invoke revoke = %s, err = %v; want a content-free forbidden result", result, err)
	}
}
