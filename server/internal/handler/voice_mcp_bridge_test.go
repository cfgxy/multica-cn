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

	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
				Name string `json:"name"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, request.Method)
		result := any(map[string]any{})
		switch request.Method {
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "list_workspaces", "description": "real MCP tool", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "tools/call":
			if request.Params.Name != "list_workspaces" {
				t.Errorf("tool name = %q", request.Params.Name)
			}
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "real result"}}}
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
	if err != nil || len(tools) != 1 || tools[0].Name != "list_workspaces" {
		t.Fatalf("tools = %+v, err = %v", tools, err)
	}
	result, err := bridge.call(context.Background(), "list_workspaces", nil)
	if err != nil || !strings.Contains(string(result), "real result") {
		t.Fatalf("call result = %s, err = %v", result, err)
	}
	if got := strings.Join(methods, ","); got != "initialize,tools/list,initialize,tools/call" {
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

func TestExecuteVoiceSkillLoadsAssignedFiles(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := seedUsableVoiceSetup(t, "Voice Skill Files", `{"model":"stubby-live-1"}`)
	skillName := "voice-skill-" + time.Now().UTC().Format("20060102150405.000000000")
	skillID := createImportTargetSkill(t, skillName, testUserID, map[string]string{"references/usage.md": "use the checked procedure"})
	if _, err := testPool.Exec(context.Background(), `INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`, agentID, skillID); err != nil {
		t.Fatalf("assign skill: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_skill WHERE agent_id = $1 AND skill_id = $2`, agentID, skillID)
	})

	result, err := testHandler.executeVoiceSkill(context.Background(), db.LiveSession{AgentID: parseUUID(agentID)}, json.RawMessage(`{"name":"`+skillName+`"}`))
	if err != nil {
		t.Fatalf("executeVoiceSkill: %v", err)
	}
	for _, want := range []string{"# original", "references/usage.md", "use the checked procedure"} {
		if !strings.Contains(string(result), want) {
			t.Errorf("skill result omitted %q: %s", want, result)
		}
	}

	denied, err := testHandler.executeVoiceSkill(context.Background(), db.LiveSession{AgentID: parseUUID(agentID)}, json.RawMessage(`{"name":"not-assigned"}`))
	if err != nil || !strings.Contains(string(denied), "not_found") {
		t.Errorf("unassigned skill = %s, err = %v", denied, err)
	}
}
