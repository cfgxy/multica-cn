package oauth

import "strings"

// ScopeForRequest maps a request's method and path onto the scope tier the
// MCP tool surface requires. This is the single enforcement point for the
// scope model: middleware.Auth calls it for every request presenting an
// OAuth access token.
//
// The map is the backend-visible shape of apps/mcp/src/rest.ts — every MCP
// tool call arrives here as one ordinary API request carrying the OAuth
// bearer token, so tiering the API surface tiers the tools. rest.ts is the
// source of truth; when a tool gains a new backend call this table must
// grow with it (the test in scope_request_test.go pins the full surface,
// so a missed entry fails a test instead of silently 403ing a tool).
//
// Fail-closed: a request outside these families is reported as unknown and
// rejected for every OAuth token, whatever its scope. An OAuth token is a
// credential for the MCP service, not a second browser session — the MCP
// surface deliberately excludes member/permission management, settings and
// account administration (see apps/mcp/src/tools.ts header), and a token
// must not reach them even when the user behind it could.
//
// Read = GET/HEAD on a known family. Write = any other method on a known
// family. Run = the dispatch and run-steering calls, which sit on the
// write-shaped paths a client could otherwise reach with mcp:write.
type ScopeRequirement int

const (
	// ScopeUnknown marks a request outside the MCP surface.
	ScopeUnknown ScopeRequirement = iota
	ScopeNeedRead
	ScopeNeedWrite
	ScopeNeedRun
)

// Scope returns the scope tier string the requirement maps to.
func (r ScopeRequirement) Scope() string {
	switch r {
	case ScopeNeedRead:
		return ScopeRead
	case ScopeNeedWrite:
		return ScopeWrite
	case ScopeNeedRun:
		return ScopeRun
	default:
		return ""
	}
}

// ScopeForRequest classifies one request. Never fails: an unrecognized
// request is ScopeUnknown, which callers must reject.
func ScopeForRequest(method, path string) ScopeRequirement {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	// Drop everything before "api" so a deployment mounting the backend
	// under a prefix classifies identically.
	for i, seg := range segments {
		if seg == "api" {
			segments = segments[i:]
			break
		}
	}
	if len(segments) < 2 || segments[0] != "api" {
		return ScopeUnknown
	}
	rest := segments[1:]

	switch {
	case family(rest, "workspaces", 1, 1),
		family(rest, "agents", 1, 1),
		family(rest, "projects", 1, 2),
		family(rest, "comments", 2, 2):
		return methodScope(method)
	case family(rest, "issues", 1, 5):
		return issueScope(method, rest)
	default:
		return ScopeUnknown
	}
}

// issueScope classifies the /api/issues family, which carries the run-shaped
// paths on top of the ordinary read/write issue surface.
func issueScope(method string, rest []string) ScopeRequirement {
	// Run family, checked before the generic shapes:
	//   POST /api/issues/quick-create              (dispatch_agent)
	//   POST /api/issues/{id}/tasks/{run}/cancel   (cancel_run)
	//   POST /api/issues/{id}/tasks/{run}/retry    (retry_run)
	if method == "POST" {
		if len(rest) == 2 && rest[1] == "quick-create" {
			return ScopeNeedRun
		}
		if len(rest) == 5 && rest[2] == "tasks" && (rest[4] == "cancel" || rest[4] == "retry") {
			return ScopeNeedRun
		}
	}
	return methodScope(method)
}

// methodScope reads on GET/HEAD, writes on everything else. The MCP surface
// has no method whose safety differs from this split.
func methodScope(method string) ScopeRequirement {
	if method == "GET" || method == "HEAD" {
		return ScopeNeedRead
	}
	return ScopeNeedWrite
}

// family reports whether rest starts with name and has between min and max
// remaining segments (inclusive).
func family(rest []string, name string, min, max int) bool {
	if len(rest) == 0 || rest[0] != name {
		return false
	}
	return len(rest) >= min && len(rest) <= max
}
