package oauth

import "testing"

// The map below pins the backend-visible shape of apps/mcp/src/rest.ts (see
// scope_request.go). When a tool gains a backend call, that file changes and
// one of these lines fails — the table and the client move together or a test
// says so.
func TestScopeForRequestPinsTheMCPSurface(t *testing.T) {
	cases := []struct {
		method, path string
		want         ScopeRequirement
	}{
		// Read surface.
		{"GET", "/api/mcp", ScopeUnknown}, // /api/mcp itself is Node's, never classified here
		{"GET", "/api/workspaces", ScopeNeedRead},
		{"GET", "/api/agents", ScopeNeedRead},
		{"GET", "/api/projects", ScopeNeedRead},
		{"GET", "/api/projects/p-1", ScopeNeedRead},
		{"GET", "/api/issues", ScopeNeedRead},
		{"GET", "/api/issues/i-1", ScopeNeedRead},
		{"GET", "/api/issues/search", ScopeNeedRead},
		{"GET", "/api/issues/i-1/comments", ScopeNeedRead},
		{"GET", "/api/issues/i-1/task-runs", ScopeNeedRead},
		{"GET", "/api/issues/i-1/tasks/r-1", ScopeNeedRead},
		{"GET", "/api/issues/i-1/active-task", ScopeNeedRead},
		{"GET", "/api/issues/i-1/relations", ScopeNeedRead},
		{"GET", "/api/issues/i-1/relations/blocks/i-2", ScopeNeedRead},
		{"HEAD", "/api/issues", ScopeNeedRead},

		// Write surface.
		{"POST", "/api/issues", ScopeNeedWrite},
		{"PATCH", "/api/issues/i-1", ScopeNeedWrite},
		{"PUT", "/api/issues/i-1", ScopeNeedWrite},
		{"DELETE", "/api/issues/i-1", ScopeNeedWrite},
		{"POST", "/api/issues/i-1/comments", ScopeNeedWrite},
		{"POST", "/api/issues/i-1/relations", ScopeNeedWrite},
		{"DELETE", "/api/issues/i-1/relations/blocks/i-2", ScopeNeedWrite},
		{"DELETE", "/api/comments/c-1", ScopeNeedWrite},
		{"POST", "/api/workspaces", ScopeNeedWrite},
		{"POST", "/api/projects", ScopeNeedWrite},

		// Run surface: dispatch and run steering must NOT be reachable with
		// mcp:write — that is the whole point of the third tier.
		{"POST", "/api/issues/quick-create", ScopeNeedRun},
		{"POST", "/api/issues/i-1/tasks/r-1/cancel", ScopeNeedRun},
		{"POST", "/api/issues/i-1/tasks/r-1/retry", ScopeNeedRun},

		// Off-surface: shapes rest.ts never calls fail closed, so a future
		// backend call added without growing the table fails here instead of
		// silently 403ing a tool. Workspace selection goes through query and
		// body parameters, which is why there are no id-prefixed
		// workspaces/agents paths on the surface.
		{"GET", "/api/workspaces/ws-1", ScopeUnknown},
		{"GET", "/api/agents/ag-1", ScopeUnknown},
		{"GET", "/api/projects/p-1/issues", ScopeUnknown},
		{"GET", "/api/comments/c-1/reactions", ScopeUnknown},

		// Fail-closed outside the MCP families — member/permission management,
		// settings and account administration stay unreachable for every
		// OAuth scope, because the MCP surface deliberately excludes them.
		{"GET", "/api/members", ScopeUnknown},
		{"GET", "/api/admin/users", ScopeUnknown},
		{"GET", "/api/tokens", ScopeUnknown},
		{"POST", "/api/cli-token", ScopeUnknown},
		{"GET", "/healthz", ScopeUnknown},
		{"GET", "/api", ScopeUnknown},
		{"GET", "/", ScopeUnknown},
	}
	for _, c := range cases {
		if got := ScopeForRequest(c.method, c.path); got != c.want {
			t.Fatalf("ScopeForRequest(%q, %q) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestScopeRequirementNames(t *testing.T) {
	if got := ScopeNeedRead.Scope(); got != ScopeRead {
		t.Fatalf("ScopeNeedRead.Scope() = %q", got)
	}
	if got := ScopeNeedWrite.Scope(); got != ScopeWrite {
		t.Fatalf("ScopeNeedWrite.Scope() = %q", got)
	}
	if got := ScopeNeedRun.Scope(); got != ScopeRun {
		t.Fatalf("ScopeNeedRun.Scope() = %q", got)
	}
	if got := ScopeUnknown.Scope(); got != "" {
		t.Fatalf("ScopeUnknown.Scope() = %q, want empty", got)
	}
}
