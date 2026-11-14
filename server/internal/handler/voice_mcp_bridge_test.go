package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/skillbundle"
)

func TestVoiceMCPBridge_UsesCanonicalToolsListAndCall(t *testing.T) {
	var methods []string
	var callers []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer caller-token" {
			t.Errorf("authorization = %q", got)
		}
		callers = append(callers, r.Header.Get("Authorization"))
		var request struct {
			Method string `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
			return
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
	if err != nil {
		t.Fatalf("workspace call: %v", err)
	}
	assertJSONEqual(t, workspaceResult, `{"content":[{"type":"text","text":"{\"total\":1,\"workspaces\":[{\"slug\":\"member\"}]}"}]}`)
	projectResult, err := bridge.call(context.Background(), "list_projects", json.RawMessage(`{"workspace":"member"}`))
	if err != nil || !strings.Contains(string(projectResult), "Member project") {
		t.Fatalf("project call result = %s, err = %v", projectResult, err)
	}
	if got := strings.Join(methods, ","); got != "initialize,tools/list,initialize,tools/call,initialize,tools/call" {
		t.Fatalf("MCP sequence = %q", got)
	}
	if got := strings.Join(callers, ","); got != "Bearer caller-token,Bearer caller-token,Bearer caller-token,Bearer caller-token,Bearer caller-token,Bearer caller-token" {
		t.Fatalf("MCP caller identity changed across discovery and calls: %q", got)
	}
}

func TestVoiceGateway_MCPToolCallReturnsStructuredResponseToProvider(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	memberID := createPermissionTestMember(t, "voice-mcp-member-"+time.Now().UTC().Format("20060102150405.000000000")+"@multica.test")
	ownerToken := voiceAuthCookie(t, testUserID).Value
	memberToken := voiceAuthCookie(t, memberID).Value
	usersByBearer := map[string]string{
		"Bearer " + ownerToken:  testUserID,
		"Bearer " + memberToken: memberID,
	}
	var mcpMu sync.Mutex
	toolCalls := map[string]int{}
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, knownCaller := usersByBearer[r.Header.Get("Authorization")]
		if !knownCaller {
			http.Error(w, "unauthorized caller", http.StatusUnauthorized)
			return
		}
		var request struct {
			Method string `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
			return
		}

		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name": "list_workspaces", "description": "list visible workspaces",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			}}}
		case "tools/call":
			if request.Params.Name != "list_workspaces" {
				http.Error(w, "unexpected tool name", http.StatusBadRequest)
				return
			}
			var arguments map[string]any
			if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil || len(arguments) != 0 {
				http.Error(w, "unexpected tool arguments", http.StatusBadRequest)
				return
			}
			mcpMu.Lock()
			toolCalls[r.Header.Get("Authorization")]++
			mcpMu.Unlock()

			var isMember bool
			if err := testPool.QueryRow(r.Context(), `
				SELECT EXISTS (
					SELECT 1 FROM member WHERE workspace_id = $1 AND user_id = $2
				)
			`, testWorkspaceID, userID).Scan(&isMember); err != nil {
				http.Error(w, "membership lookup failed", http.StatusInternalServerError)
				return
			}
			if isMember {
				result = map[string]any{
					"content": []map[string]any{{"type": "text", "text": `{"total":1}`}},
					"isError": false,
				}
			} else {
				result = map[string]any{
					"content": []map[string]any{{"type": "text", "text": "permission denied"}},
					"isError": true,
				}
			}
		default:
			http.Error(w, "unsupported JSON-RPC method", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}))
	defer mcpServer.Close()
	t.Setenv("MCP_URL", mcpServer.URL)

	provider := newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "MCP Tool Response", `{"model":"stubby-live-1"}`)
	dbfx.Exec(t, `UPDATE agent SET visibility = 'workspace', permission_mode = 'public_to' WHERE id = $1`, agentID)
	dbfx.InsertNoID(t, "agent_invocation_target", testutil.Cols{
		"agent_id": agentID, "target_type": "member", "target_id": memberID,
	}, "agent_id = $1 AND target_type = 'member' AND target_id = $2", agentID, memberID)
	dbfx.Cleanup(t, `DELETE FROM agent_invocation_target WHERE agent_id = $1 AND target_type = 'member' AND target_id = $2`, agentID, memberID)
	srv := voiceGatewayServer(t)

	tests := []struct {
		name             string
		callID           string
		token            string
		revokeMembership bool
		wantResult       string
	}{
		{
			name:       "owner succeeds",
			callID:     "owner-call",
			token:      ownerToken,
			wantResult: `{"content":[{"type":"text","text":"{\"total\":1}"}],"isError":false}`,
		},
		{
			name:             "caller without membership is denied",
			callID:           "member-call",
			token:            memberToken,
			revokeMembership: true,
			wantResult:       `{"content":[{"type":"text","text":"permission denied"}],"isError":true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := dialVoiceSessionFirstFrame(t, srv, agentID, "?workspace_id="+testWorkspaceID)
			if err := conn.WriteJSON(map[string]any{"type": "auth", "payload": map[string]any{"token": tt.token}}); err != nil {
				t.Fatalf("send auth frame: %v", err)
			}
			if first := readVoiceFrame(t, conn); !strings.Contains(string(first), "setupComplete") {
				t.Fatalf("expected setupComplete, got %s", first)
			}
			setups := provider.setupsRecorded(t)
			setup := setups[len(setups)-1]["setup"].(map[string]any)
			declared := false
			for _, rawGroup := range setup["tools"].([]any) {
				group := rawGroup.(map[string]any)
				for _, rawDecl := range group["functionDeclarations"].([]any) {
					decl := rawDecl.(map[string]any)
					declared = declared || decl["name"] == "list_workspaces"
				}
			}
			if !declared {
				t.Fatalf("provider setup omitted list_workspaces: %v", setup["tools"])
			}
			if tt.revokeMembership {
				dbfx.Exec(t, `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, memberID)
			}

			provider.sendDownstream(provider.gatewayConn(t), map[string]any{
				"toolCall": map[string]any{"functionCalls": []any{map[string]any{
					"id": tt.callID, "name": "list_workspaces", "args": map[string]any{},
				}}},
			})

			var responseFrames [][]byte
			waitFor(t, 3*time.Second, func() bool {
				provider.mu.Lock()
				defer provider.mu.Unlock()
				responseFrames = responseFrames[:0]
				for _, msg := range provider.clientMsgs {
					if strings.Contains(string(msg), `"toolResponse"`) && strings.Contains(string(msg), tt.callID) {
						responseFrames = append(responseFrames, append([]byte(nil), msg...))
					}
				}
				return len(responseFrames) == 1
			})
			// The client pump processes writes in order. Its marker can reach the
			// provider only after it has consumed the preceding echo; that makes the
			// final exact-count assertion stable rather than a first-response race.
			if err := conn.WriteJSON(map[string]any{
				"toolResponse": map[string]any{"functionResponses": []any{map[string]any{
					"id": tt.callID, "name": "list_workspaces", "response": json.RawMessage(tt.wantResult),
				}}},
			}); err != nil {
				t.Fatalf("send toolResponse echo: %v", err)
			}
			barrier := "mcp tool-response barrier " + tt.callID
			if err := conn.WriteJSON(map[string]any{
				"clientContent": map[string]any{
					"turns":        []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": barrier}}}},
					"turnComplete": true,
				},
			}); err != nil {
				t.Fatalf("send tool-response barrier: %v", err)
			}
			waitFor(t, 3*time.Second, func() bool {
				provider.mu.Lock()
				defer provider.mu.Unlock()
				for _, msg := range provider.clientMsgs {
					if strings.Contains(string(msg), barrier) {
						return true
					}
				}
				return false
			})

			provider.mu.Lock()
			responseFrames = responseFrames[:0]
			for _, msg := range provider.clientMsgs {
				if strings.Contains(string(msg), `"toolResponse"`) && strings.Contains(string(msg), tt.callID) {
					responseFrames = append(responseFrames, append([]byte(nil), msg...))
				}
			}
			provider.mu.Unlock()
			if len(responseFrames) != 1 {
				t.Fatalf("provider toolResponse count for %s = %d, want 1", tt.callID, len(responseFrames))
			}
			assertJSONEqual(t, responseFrames[0], `{"toolResponse":{"functionResponses":[{"id":"`+tt.callID+`","name":"list_workspaces","response":`+tt.wantResult+`}]}}`)

			bearer := "Bearer " + tt.token
			mcpMu.Lock()
			callCount := toolCalls[bearer]
			mcpMu.Unlock()
			if callCount != 1 {
				t.Fatalf("tools/call count for current voice caller = %d, want 1", callCount)
			}
		})
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
