package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

const marketplaceTestToken = "ghp-marketplace-should-never-be-echoed"

// marketplaceMcpItemKey is a catalog entry with a secret placeholder, which is
// what the install path's interesting cases hinge on.
const marketplaceMcpItemKey = "mcp:modelcontextprotocol/github"

func listMarketplaceForTest(t *testing.T, query string, mutate func(*http.Request)) (int, []MarketplaceItemResponse, string) {
	t.Helper()

	path := "/api/marketplace/items"
	if query != "" {
		path += "?" + query
	}
	req := newRequest(http.MethodGet, path, nil)
	req = withURLParam(req, "id", testWorkspaceID)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	testHandler.ListMarketplaceItems(w, req)

	var resp []MarketplaceItemResponse
	raw := w.Body.String()
	if w.Code == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return w.Code, resp, raw
}

func installMarketplaceForTest(t *testing.T, body any, mutate func(*http.Request)) (int, string) {
	t.Helper()

	req := newRequest(http.MethodPost, "/api/marketplace/install", body)
	req = withURLParam(req, "id", testWorkspaceID)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	testHandler.InstallMarketplaceItem(w, req)
	raw := w.Body.String()

	if w.Code == http.StatusCreated {
		var resp WorkspaceMcpServerResponse
		if err := json.Unmarshal([]byte(raw), &resp); err == nil && resp.ID != "" {
			t.Cleanup(func() {
				ctx := context.Background()
				testPool.Exec(ctx, `DELETE FROM agent_mcp_server WHERE server_id = $1`, resp.ID)
				testPool.Exec(ctx, `DELETE FROM workspace_mcp_server WHERE id = $1`, resp.ID)
			})
		}
	}
	return w.Code, raw
}

// The unified marketplace has to surface BOTH extension kinds; a listing that
// only ever returned one would be the static catalog this issue exists to
// avoid.
func TestListMarketplaceItems_ReturnsBothKinds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	code, resp, raw := listMarketplaceForTest(t, "", nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, raw)
	}
	kinds := map[string]int{}
	for _, item := range resp {
		kinds[item.Kind]++
	}
	if kinds[service.MarketplaceKindSkill] == 0 {
		t.Error("listing carries no skill entries")
	}
	if kinds[service.MarketplaceKindMcp] == 0 {
		t.Error("listing carries no MCP entries")
	}
}

func TestListMarketplaceItems_FiltersByKindAndQuery(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	code, resp, raw := listMarketplaceForTest(t, "kind=mcp", nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, raw)
	}
	if len(resp) == 0 {
		t.Fatal("kind=mcp returned nothing")
	}
	for _, item := range resp {
		if item.Kind != service.MarketplaceKindMcp {
			t.Fatalf("kind=mcp returned a %s entry: %s", item.Kind, item.Key)
		}
	}

	code, filtered, raw := listMarketplaceForTest(t, "kind=mcp&q=filesystem", nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, raw)
	}
	if len(filtered) == 0 {
		t.Fatal("search for a known entry returned nothing")
	}
	if len(filtered) >= len(resp) {
		t.Fatalf("search did not narrow the listing: %d of %d", len(filtered), len(resp))
	}

	code, _, raw = listMarketplaceForTest(t, "kind=plugin", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown kind, got %d: %s", code, raw)
	}
}

// The listing is what tells the UI whether to offer "install" or link through
// to the installed thing. Getting this wrong means offering an install that
// can only 409.
func TestListMarketplaceItems_ReportsInstalledState(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, before, _ := listMarketplaceForTest(t, "kind=mcp&q=filesystem", nil)
	if len(before) == 0 {
		t.Fatal("expected the filesystem entry in the catalog")
	}
	target := before[0]
	if target.Installed {
		t.Fatalf("entry %q already reports installed before the fixture ran", target.Key)
	}

	serverID := createWorkspaceMcpServerForTest(t, target.Name, `{"command":"npx"}`)

	_, after, raw := listMarketplaceForTest(t, "kind=mcp&q=filesystem", nil)
	var found *MarketplaceItemResponse
	for i := range after {
		if after[i].Key == target.Key {
			found = &after[i]
		}
	}
	if found == nil {
		t.Fatalf("entry vanished from the listing: %s", raw)
	}
	if !found.Installed {
		t.Fatalf("entry %q does not report installed after the server exists", target.Key)
	}
	if found.InstalledID != serverID {
		t.Fatalf("installed_id = %q, want the existing server %q", found.InstalledID, serverID)
	}
}

// The listing carries templates and placeholder NAMES, never values. A
// marketplace that echoed a stored entry would be a read-back channel for
// every credential the workspace MCP library deliberately keeps write-only.
func TestListMarketplaceItems_NeverEchoesStoredConfig(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	createWorkspaceMcpServerForTest(t, "github",
		`{"command":"npx","env":{"GITHUB_PERSONAL_ACCESS_TOKEN":"`+marketplaceTestToken+`"}}`)

	code, _, raw := listMarketplaceForTest(t, "", nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, raw)
	}
	if strings.Contains(raw, marketplaceTestToken) {
		t.Fatalf("listing echoed a stored credential: %s", raw)
	}
	// The config template is rendered server-side; shipping it to the client
	// is not needed and only widens what a compromised client can display.
	if strings.Contains(raw, "config_template") {
		t.Fatalf("listing exposes the raw config template: %s", raw)
	}
}

// Installing writes to the workspace's shared library, so it takes the same
// human owner/admin gate that adding an MCP server by hand takes. An agent
// running under an owner's PAT satisfies the router's role check but must
// still be refused.
func TestInstallMarketplaceItem_DeniesAgentActor(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	caller := createHandlerTestAgent(t, "marketplace-agent-actor", nil)
	taskID := insertHandlerTestTask(t, caller)

	code, raw := installMarketplaceForTest(t, map[string]any{
		"key":    marketplaceMcpItemKey,
		"values": map[string]string{"github_token": marketplaceTestToken},
	}, func(req *http.Request) {
		req.Header.Set("X-Actor-Source", "task_token")
		req.Header.Set("X-Agent-ID", caller)
		req.Header.Set("X-Task-ID", taskID)
	})
	if code != http.StatusForbidden {
		t.Fatalf("expected 403 for an agent actor, got %d: %s", code, raw)
	}
	if strings.Contains(raw, marketplaceTestToken) {
		t.Fatalf("rejection echoed the supplied secret: %s", raw)
	}
}

// The end of the install closes the loop this issue is about: the entry has to
// land in the workspace MCP library as an ordinary row, discoverable by the
// existing binding path, with the secret stored and never returned.
func TestInstallMarketplaceItem_StoresMcpEntryWriteOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	code, raw := installMarketplaceForTest(t, map[string]any{
		"key":    marketplaceMcpItemKey,
		"name":   "github-marketplace",
		"values": map[string]string{"github_token": marketplaceTestToken},
	}, nil)
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", code, raw)
	}
	if strings.Contains(raw, marketplaceTestToken) {
		t.Fatalf("install response echoed the secret: %s", raw)
	}

	var resp WorkspaceMcpServerResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Name != "github-marketplace" {
		t.Fatalf("name = %q, want the supplied name", resp.Name)
	}
	if resp.Transport != "stdio" {
		t.Fatalf("transport = %q, want stdio", resp.Transport)
	}

	// The stored row must carry the rendered entry, or the server would start
	// unauthenticated at run time — far from the install that appeared to work.
	var stored string
	if err := testPool.QueryRow(context.Background(),
		`SELECT config::text FROM workspace_mcp_server WHERE id = $1`, resp.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored config: %v", err)
	}
	if !strings.Contains(stored, marketplaceTestToken) {
		t.Fatalf("stored entry did not receive the supplied secret: %s", stored)
	}
	if strings.Contains(stored, "${") {
		t.Fatalf("stored entry keeps an unfilled placeholder: %s", stored)
	}

	// Installed, but bound to nobody: a library entry reaches an agent only
	// when someone binds it, which is the contract ResolveAgentMcpConfig
	// depends on.
	var bindings int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_mcp_server WHERE server_id = $1`, resp.ID).Scan(&bindings); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if bindings != 0 {
		t.Fatalf("install bound the server to %d agent(s) without being asked", bindings)
	}

	// And the closed loop: the listing now reports it as installed.
	_, items, listRaw := listMarketplaceForTest(t, "kind=mcp&q=github", nil)
	var reported bool
	for _, item := range items {
		if item.Key == marketplaceMcpItemKey && item.Installed {
			reported = true
		}
	}
	if reported {
		// Installed under a custom name, so the catalog-name lookup should NOT
		// report it — asserting the opposite would hide a false positive.
		t.Fatalf("entry installed under a different name reported as installed: %s", listRaw)
	}
}

func TestInstallMarketplaceItem_RejectsDuplicateName(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	createWorkspaceMcpServerForTest(t, "github", `{"command":"npx"}`)

	code, raw := installMarketplaceForTest(t, map[string]any{
		"key":    marketplaceMcpItemKey,
		"values": map[string]string{"github_token": marketplaceTestToken},
	}, nil)
	if code != http.StatusConflict {
		t.Fatalf("expected 409 for a duplicate name, got %d: %s", code, raw)
	}
	if strings.Contains(raw, marketplaceTestToken) {
		t.Fatalf("conflict response echoed the secret: %s", raw)
	}
}

// A missing required secret must fail at install rather than store an entry
// that authenticates as nobody.
func TestInstallMarketplaceItem_RejectsInvalidConfiguration(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{
			name: "unknown item",
			body: map[string]any{"key": "mcp:does-not-exist"},
			want: http.StatusNotFound,
		},
		{
			name: "missing required value",
			body: map[string]any{"key": marketplaceMcpItemKey},
			want: http.StatusBadRequest,
		},
		{
			name: "unknown value key",
			body: map[string]any{
				"key":    marketplaceMcpItemKey,
				"values": map[string]string{"githbu_token": marketplaceTestToken},
			},
			want: http.StatusBadRequest,
		},
		{
			name: "invalid server name",
			body: map[string]any{
				"key":    marketplaceMcpItemKey,
				"name":   "not a valid name",
				"values": map[string]string{"github_token": marketplaceTestToken},
			},
			want: http.StatusBadRequest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, raw := installMarketplaceForTest(t, tc.body, nil)
			if code != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, code, raw)
			}
			if strings.Contains(raw, marketplaceTestToken) {
				t.Fatalf("rejection echoed the supplied secret: %s", raw)
			}
		})
	}
}

// A supplied value must not be able to rewrite the entry it sits in. Without
// JSON-encoding the substitution, a value carrying a quote could append a
// `command` the catalog never declared and the admin never approved.
func TestInstallMarketplaceItem_HostileValueCannotRewriteEntry(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	code, raw := installMarketplaceForTest(t, map[string]any{
		"key":    marketplaceMcpItemKey,
		"name":   "github-hostile",
		"values": map[string]string{"github_token": `x","command":"/bin/sh`},
	}, nil)
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", code, raw)
	}
	var resp WorkspaceMcpServerResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var stored string
	if err := testPool.QueryRow(context.Background(),
		`SELECT config->>'command' FROM workspace_mcp_server WHERE id = $1`, resp.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored command: %v", err)
	}
	if stored != "npx" {
		t.Fatalf("command = %q; the supplied value escaped its string and rewrote the entry", stored)
	}
}

// An unreachable or malformed skill source must surface as an upstream error
// rather than a half-created skill. The catalog's real sources are external,
// so this drives the failure path with a source the fetcher rejects before
// any network call.
func TestInstallMarketplaceItem_SkillSourceFailureCreatesNothing(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	item := service.MarketplaceItem{
		Key:       "skill:test/unreachable",
		Kind:      service.MarketplaceKindSkill,
		Name:      "marketplace-unreachable-skill",
		SourceURL: "https://not-a-supported-host.example/some/skill",
	}
	req := newRequest(http.MethodPost, "/api/marketplace/install", nil)
	req = withURLParam(req, "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.installMarketplaceSkill(w, req, testWorkspaceID,
		parseUUID(testWorkspaceID), parseUUID(testUserID), testUserID, item)

	if w.Code == http.StatusOK || w.Code == http.StatusCreated {
		t.Fatalf("unsupported source was accepted: %d %s", w.Code, w.Body.String())
	}

	var skills int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM skill WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, item.Name).Scan(&skills); err != nil {
		t.Fatalf("count skills: %v", err)
	}
	if skills != 0 {
		t.Fatalf("a failed source install left %d skill row(s) behind", skills)
	}
}
