package oauth

import "testing"

// The map below pins the backend-visible shape of apps/mcp/src/rest.ts (see
// scope_request.go). Every path template that file emits has one line here,
// so the table and the client move together or a test says so.
func TestScopeForRequestPinsTheMCPSurface(t *testing.T) {
	cases := []struct {
		method, path string
		want         ScopeRequirement
	}{
		// Read surface.
		{"GET", "/api/mcp", ScopeUnknown}, // /api/mcp itself is Node's, never classified here
		{"GET", "/api/workspaces", ScopeNeedRead},
		{"GET", "/api/agents", ScopeNeedRead},
		{"GET", "/api/agents/ag-1", ScopeNeedRead},
		{"GET", "/api/projects", ScopeNeedRead},
		{"GET", "/api/projects/p-1", ScopeNeedRead},
		{"GET", "/api/projects/p-1/resources", ScopeNeedRead},
		{"GET", "/api/squads", ScopeNeedRead},
		{"GET", "/api/squads/s-1", ScopeNeedRead},
		{"GET", "/api/squads/s-1/members", ScopeNeedRead},
		{"GET", "/api/runtimes", ScopeNeedRead},
		{"GET", "/api/runtimes/rt-1/models", ScopeNeedRead},
		{"GET", "/api/runtimes/rt-1/models/m-1", ScopeNeedRead},
		{"GET", "/api/task-runs", ScopeNeedRead},
		{"GET", "/api/quick-replies", ScopeNeedRead},
		{"GET", "/api/workspaces/ws-1/audit-events", ScopeNeedRead},
		{"GET", "/api/workspaces/ws-1/execution-profiles", ScopeNeedRead},
		{"GET", "/api/workspaces/ws-1/execution-profiles/p-1", ScopeNeedRead},
		{"GET", "/api/workspaces/ws-1/execution-profiles/p-1/entries", ScopeNeedRead},
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
		{"PUT", "/api/comments/c-1", ScopeNeedWrite},
		{"DELETE", "/api/comments/c-1", ScopeNeedWrite},
		{"POST", "/api/issues/i-1/relations", ScopeNeedWrite},
		{"DELETE", "/api/issues/i-1/relations/blocks/i-2", ScopeNeedWrite},
		{"POST", "/api/agents", ScopeNeedWrite},
		{"PUT", "/api/agents/ag-1", ScopeNeedWrite},
		{"POST", "/api/agents/ag-1/archive", ScopeNeedWrite},
		{"POST", "/api/agents/ag-1/restore", ScopeNeedWrite},
		{"POST", "/api/projects", ScopeNeedWrite},
		{"PUT", "/api/projects/p-1", ScopeNeedWrite},
		{"POST", "/api/projects/p-1/resources", ScopeNeedWrite},
		{"PUT", "/api/projects/p-1/resources/r-1", ScopeNeedWrite},
		{"DELETE", "/api/projects/p-1/resources/r-1", ScopeNeedWrite},
		{"POST", "/api/squads", ScopeNeedWrite},
		{"PUT", "/api/squads/s-1", ScopeNeedWrite},
		{"DELETE", "/api/squads/s-1", ScopeNeedWrite},
		{"POST", "/api/quick-replies", ScopeNeedWrite},
		{"PATCH", "/api/quick-replies/q-1", ScopeNeedWrite},
		{"DELETE", "/api/quick-replies/q-1", ScopeNeedWrite},
		{"POST", "/api/runtimes/rt-1/models", ScopeNeedWrite},
		{"POST", "/api/workspaces/ws-1/execution-profiles", ScopeNeedWrite},
		{"PATCH", "/api/workspaces/ws-1/execution-profiles/p-1", ScopeNeedWrite},
		{"DELETE", "/api/workspaces/ws-1/execution-profiles/p-1", ScopeNeedWrite},
		{"PUT", "/api/workspaces/ws-1/execution-profiles/p-1/entries", ScopeNeedWrite},
		{"DELETE", "/api/workspaces/ws-1/execution-profiles/p-1/entries/ag-1", ScopeNeedWrite},
		{"POST", "/api/workspaces/ws-1/execution-profiles/p-1/activate", ScopeNeedWrite},

		// Run surface: dispatch and run steering must NOT be reachable with
		// mcp:write — that is the whole point of the third tier. The run
		// shapes are POST-only: a GET on the same path classifies as an
		// ordinary read of the underlying resource.
		{"POST", "/api/issues/quick-create", ScopeNeedRun},
		{"POST", "/api/issues/i-1/tasks/r-1/cancel", ScopeNeedRun},
		{"POST", "/api/issues/i-1/tasks/r-1/retry", ScopeNeedRun},
		{"GET", "/api/issues/quick-create", ScopeNeedRead},

		// Deliberately tightened shapes: reachable before this table was
		// enumerated, but no MCP tool drives them and the envelope excludes
		// them — workspace creation, project hard delete, and squad member
		// management (permission management, admin-gated server-side).
		{"POST", "/api/workspaces", ScopeUnknown},
		{"DELETE", "/api/projects/p-1", ScopeUnknown},
		{"POST", "/api/squads/s-1/members", ScopeUnknown},
		{"PATCH", "/api/squads/s-1/members/m-1", ScopeUnknown},
		{"DELETE", "/api/squads/s-1/members/m-1", ScopeUnknown},

		// Off-surface: shapes rest.ts never calls fail closed, so a future
		// backend call added without growing the table fails here instead of
		// silently 403ing a tool. Workspace selection goes through query and
		// body parameters, which is why there is no bare /api/workspaces/{id}
		// path; the sub-resources below are real backend routes a range-based
		// matcher once swept in by accident.
		{"GET", "/api/workspaces/ws-1", ScopeUnknown},
		{"GET", "/api/workspaces/ws-1/members", ScopeUnknown},
		{"GET", "/api/workspaces/ws-1/invitations", ScopeUnknown},
		{"POST", "/api/workspaces/ws-1/leave", ScopeUnknown},
		{"GET", "/api/workspaces/ws-1/runtime-profiles", ScopeUnknown},
		{"GET", "/api/projects/p-1/issues", ScopeUnknown},
		{"GET", "/api/comments/c-1/reactions", ScopeUnknown},
		{"PUT", "/api/agents/ag-1/env", ScopeUnknown},
		{"GET", "/api/agents/ag-1/webhooks", ScopeUnknown},
		{"POST", "/api/agents/ag-1/cancel-tasks", ScopeUnknown},
		{"PATCH", "/api/runtimes/rt-1", ScopeUnknown},
		{"DELETE", "/api/runtimes/rt-1", ScopeUnknown},
		{"GET", "/api/runtimes/rt-1/usage", ScopeUnknown},
		{"GET", "/api/runtimes/rt-1/credentials", ScopeUnknown},

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
