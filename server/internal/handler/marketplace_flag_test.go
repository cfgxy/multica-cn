package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// callMarketplaceWithFlagOff drives both marketplace entry points with
// marketplace_v1 disabled. The handler-level gate is what closes them: the
// routes stay registered, exactly like the Plugin surface, so an old client
// receives a clean 503 instead of a 404 that reads like a broken deployment.
func callMarketplaceWithFlagOff(t *testing.T) (listCode int, listBody string, installCode int, installBody string) {
	t.Helper()
	withMarketplaceV1Flag(t, testHandler, false)

	listReq := withURLParam(newRequest(http.MethodGet, "/api/marketplace/items", nil), "id", testWorkspaceID)
	listRec := httptest.NewRecorder()
	testHandler.ListMarketplaceItems(listRec, listReq)

	installReq := withURLParam(newRequest(http.MethodPost, "/api/marketplace/install", map[string]any{
		"key":    marketplaceMcpItemKey,
		"name":   "github-flag-off",
		"values": map[string]string{"github_token": marketplaceTestToken},
	}), "id", testWorkspaceID)
	installRec := httptest.NewRecorder()
	testHandler.InstallMarketplaceItem(installRec, installReq)

	return listRec.Code, listRec.Body.String(), installRec.Code, installRec.Body.String()
}

// Closing the marketplace has to actually close it. The listing must not leak
// the catalog and the install must not reach the workspace library — including
// with a secret in the body, which must be neither stored nor echoed.
func TestMarketplaceEndpointsClosedWhenFlagOff(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	listCode, listBody, installCode, installBody := callMarketplaceWithFlagOff(t)

	if listCode != http.StatusServiceUnavailable {
		t.Fatalf("list with flag off: expected 503, got %d: %s", listCode, listBody)
	}
	if installCode != http.StatusServiceUnavailable {
		t.Fatalf("install with flag off: expected 503, got %d: %s", installCode, installBody)
	}
	if strings.Contains(installBody, marketplaceTestToken) {
		t.Fatalf("closed install echoed the supplied secret: %s", installBody)
	}

	var items []MarketplaceItemResponse
	if err := json.Unmarshal([]byte(listBody), &items); err == nil && len(items) > 0 {
		t.Fatalf("closed listing still returned %d catalog entries: %s", len(items), listBody)
	}

	var count int
	if err := testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM workspace_mcp_server WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "github-flag-off").Scan(&count); err != nil {
		t.Fatalf("count library entries: %v", err)
	}
	if count != 0 {
		t.Fatalf("closed install wrote %d workspace MCP entries", count)
	}
}

// The acceptance condition the flag exists to make verifiable: turning the
// marketplace off removes a front door, not a capability. Every entry point the
// marketplace merely drives — skill listing, skill import, the workspace MCP
// library, and agent binding — stays reachable with marketplace_v1 disabled.
func TestUnderlyingEntryPointsSurviveMarketplaceFlagOff(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withMarketplaceV1Flag(t, testHandler, false)

	agentID := createHandlerTestAgent(t, "marketplace-flag-off-agent", nil)

	t.Run("skill listing", func(t *testing.T) {
		req := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/skills", nil), "id", testWorkspaceID)
		w := httptest.NewRecorder()
		testHandler.ListSkills(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ListSkills with marketplace off: expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	// Import is asserted through its own validation, not a live fetch: a 400 on
	// a bad on_conflict proves the endpoint still runs its own logic, which a
	// 503 from a marketplace gate would have pre-empted.
	t.Run("skill import", func(t *testing.T) {
		req := withURLParam(newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/skills/import", map[string]any{
			"url":         "https://clawhub.dev/skills/example",
			"on_conflict": "not-a-strategy",
		}), "id", testWorkspaceID)
		w := httptest.NewRecorder()
		testHandler.ImportSkill(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("ImportSkill with marketplace off: expected 400 from its own validation, got %d: %s", w.Code, w.Body.String())
		}
	})

	// Created on the parent t: the helper registers a cleanup that deletes the
	// row, and a subtest's cleanup would fire before the binding check runs.
	code, created, raw := createWorkspaceMcpServerViaAPI(t, map[string]any{
		"name":   "manual-entry-flag-off",
		"config": map[string]any{"command": "npx", "args": []string{"-y", "mcp-server"}},
	}, nil)
	if code != http.StatusCreated {
		t.Fatalf("CreateWorkspaceMcpServer with marketplace off: expected 201, got %d: %s", code, raw)
	}

	t.Run("workspace mcp library", func(t *testing.T) {
		listCode, servers, listRaw := listWorkspaceMcpServersForTest(t, nil)
		if listCode != http.StatusOK {
			t.Fatalf("ListWorkspaceMcpServers with marketplace off: expected 200, got %d: %s", listCode, listRaw)
		}
		if !containsMcpServer(servers, created.ID) {
			t.Fatalf("manually created entry %s missing from the library listing: %s", created.ID, listRaw)
		}
	})

	t.Run("agent binding", func(t *testing.T) {
		bound := addAgentMcpServerForTest(t, agentID, created.ID)
		if !containsMcpServer(bound, created.ID) {
			t.Fatalf("binding with marketplace off did not attach %s", created.ID)
		}
	})
}

func containsMcpServer(servers []WorkspaceMcpServerResponse, id string) bool {
	for _, s := range servers {
		if s.ID == id {
			return true
		}
	}
	return false
}
