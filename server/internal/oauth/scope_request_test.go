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
		{"GET", "/api/inbox", ScopeNeedRead},
		{"GET", "/api/inbox/archived", ScopeNeedRead},
		{"GET", "/api/inbox/unread-count", ScopeNeedRead},
		{"GET", "/api/inbox/unread-summary", ScopeNeedRead},
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
		{"GET", "/api/autopilots/ap-1", ScopeNeedRead},
		{"GET", "/api/autopilots/ap-1/runs", ScopeNeedRead},
		{"GET", "/api/autopilots/ap-1/runs/run-1", ScopeNeedRead},
		{"GET", "/api/quick-replies", ScopeNeedRead},
		{"GET", "/api/workspaces/ws-1/audit-events", ScopeNeedRead},
		{"POST", "/api/mcp/audit-start", ScopeNeedRead},
		{"POST", "/api/mcp/audit-finish", ScopeNeedRead},
		{"POST", "/api/mcp/pre-auth-rejections", ScopeNeedRead},
		{"POST", "/api/file-access/list", ScopeNeedRead},
		{"POST", "/api/file-access/metadata", ScopeNeedRead},
		{"POST", "/api/file-access/read", ScopeNeedRead},
		{"GET", "/api/file-access/operations/op-1", ScopeNeedRead},
		{"PUT", "/api/file-access/operations/op-1", ScopeUnknown},
		{"PUT", "/api/runtimes/rt-1/file-access-instance-bindings/i-1", ScopeUnknown},
		{"GET", "/api/workspaces/ws-1/execution-profiles", ScopeNeedRead},
		{"GET", "/api/workspaces/ws-1/execution-profiles/p-1", ScopeNeedRead},
		{"GET", "/api/workspaces/ws-1/execution-profiles/p-1/entries", ScopeNeedRead},
		{"GET", "/api/issues", ScopeNeedRead},
		{"GET", "/api/issues/i-1", ScopeNeedRead},
		{"GET", "/api/issues/search", ScopeNeedRead},
		{"GET", "/api/issues/i-1/comments", ScopeNeedRead},
		{"GET", "/api/issues/i-1/task-runs", ScopeNeedRead},
		{"GET", "/api/issues/i-1/tasks/r-1", ScopeNeedRead},
		{"GET", "/api/issues/i-1/tasks/r-1/activity", ScopeNeedRead},
		{"GET", "/api/issues/i-1/active-task", ScopeNeedRead},
		{"GET", "/api/issues/i-1/relations", ScopeNeedRead},
		{"GET", "/api/issues/i-1/relations/blocks/i-2", ScopeNeedRead},
		{"GET", "/api/workspaces/ws-1/decision-inbox", ScopeNeedRead},
		{"GET", "/api/issues/i-1/decisions", ScopeNeedRead},
		{"HEAD", "/api/issues", ScopeNeedRead},

		// Write surface.
		{"POST", "/api/issues", ScopeNeedWrite},
		{"POST", "/api/inbox/mark-all-read", ScopeNeedWrite},
		{"POST", "/api/inbox/archive-all", ScopeNeedWrite},
		{"POST", "/api/inbox/archive-all-read", ScopeNeedWrite},
		{"POST", "/api/inbox/archive-completed", ScopeNeedWrite},
		{"POST", "/api/inbox/item-1/read", ScopeNeedWrite},
		{"POST", "/api/inbox/item-1/unread", ScopeNeedWrite},
		{"POST", "/api/inbox/item-1/archive", ScopeNeedWrite},
		{"POST", "/api/inbox/item-1/unarchive", ScopeNeedWrite},
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

		// Decision-card write surface (RUYI-688): the batch answer is the one
		// write the decision tools drive. Card creation and cancel stay
		// off-surface — cards are raised by runs and withdrawn by members.
		{"POST", "/api/issues/i-1/decisions/answer-batch", ScopeNeedWrite},
		{"POST", "/api/issues/i-1/decisions", ScopeUnknown},
		{"POST", "/api/issues/i-1/decisions/d-1/answer", ScopeUnknown},
		{"POST", "/api/issues/i-1/decisions/d-1/cancel", ScopeUnknown},

		// Run surface: dispatch and run steering must NOT be reachable with
		// mcp:write — that is the whole point of the third tier. The run
		// shapes are POST-only: a GET on the same path classifies as an
		// ordinary read of the underlying resource.
		{"POST", "/api/issues/quick-create", ScopeNeedRun},
		{"POST", "/api/autopilots/ap-1/trigger", ScopeNeedRun},
		{"POST", "/api/issues/i-1/tasks/r-1/cancel", ScopeNeedRun},
		{"POST", "/api/issues/i-1/tasks/r-1/retry", ScopeNeedRun},
		{"GET", "/api/issues/quick-create", ScopeNeedRead},

		// Prompt legislation (RUYI-868): governance reads are read-only on the
		// wire; member proposal actions and the Owner's hash-guarded direct
		// revision pair ride mcp:write. Proposal decide-and-execute endpoints
		// remain off-surface so no OAuth scope can reach them.
		{"GET", "/api/prompt-legislation/governance/clauses", ScopeNeedRead},
		{"POST", "/api/prompt-legislation/governance/clauses", ScopeUnknown},
		// Note: the caller passes r.URL.Path (middleware/auth.go), so query
		// strings such as the get_clause window (?days=N) never reach this
		// classifier — only the path shape is ever matched.
		{"GET", "/api/prompt-legislation/governance/clauses/c-1", ScopeNeedRead},
		{"POST", "/api/prompt-legislation/governance/clauses/c-1/direct-revision-preview", ScopeNeedWrite},
		{"POST", "/api/prompt-legislation/governance/clauses/c-1/direct-revision", ScopeNeedWrite},
		{"GET", "/api/prompt-legislation/governance/clauses/search?q=mention", ScopeNeedRead},
		{"GET", "/api/prompt-legislation/governance/reconciliation", ScopeNeedRead},
		{"POST", "/api/prompt-legislation/proposals", ScopeNeedWrite},
		{"PATCH", "/api/prompt-legislation/proposals/p-1", ScopeNeedWrite},
		{"POST", "/api/prompt-legislation/proposals/p-1/submit", ScopeNeedWrite},
		{"POST", "/api/prompt-legislation/proposals/p-1/preview", ScopeNeedWrite},
		{"POST", "/api/prompt-legislation/proposals/p-1/rework", ScopeNeedWrite},
		{"GET", "/api/prompt-legislation/proposals", ScopeNeedRead},
		{"POST", "/api/prompt-legislation/proposals/p-1/approve", ScopeUnknown},
		{"POST", "/api/prompt-legislation/proposals/approve-batch", ScopeUnknown},
		{"POST", "/api/prompt-legislation/proposals/p-1/reject", ScopeUnknown},
		{"POST", "/api/prompt-legislation/proposals/p-1/restore", ScopeUnknown},
		{"POST", "/api/prompt-legislation/proposals/p-1/enact", ScopeUnknown},

		// Prompt-governance carrier reads (RUYI-868): version ledger and diff
		// stay reads; the owner carrier writes and the quality/quiz views are
		// off-surface.
		{"GET", "/api/prompt-governance/workspace/ws-1/versions", ScopeNeedRead},
		{"GET", "/api/prompt-governance/project/p-1/versions/2", ScopeNeedRead},
		{"GET", "/api/prompt-governance/squad/s-1/diff", ScopeNeedRead},
		{"POST", "/api/prompt-governance/workspace/ws-1/versions", ScopeUnknown},
		{"POST", "/api/prompt-governance/workspace/ws-1/versions/snapshot", ScopeUnknown},
		{"POST", "/api/prompt-governance/agent/ag-1/versions/1/switch", ScopeUnknown},
		{"GET", "/api/prompt-governance/workspace/ws-1/quality", ScopeUnknown},
		{"GET", "/api/prompt-governance/workspace/ws-1/quiz", ScopeUnknown},
		{"GET", "/api/prompt-governance/workspace/ws-1/baseline", ScopeUnknown},

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
