package oauth

import "strings"

// ScopeForRequest maps a request's method and path onto the scope tier the
// MCP tool surface requires. This is the single enforcement point for the
// scope model: middleware.Auth calls it for every request presenting an
// OAuth access token.
//
// The table below is the backend-visible shape of apps/mcp/src/rest.ts —
// every MCP tool call arrives here as one ordinary API request carrying the
// OAuth bearer token, so tiering the API surface tiers the tools. rest.ts is
// the source of truth: every path template it emits is enumerated as one
// shape here, and scope_request_test.go pins each one, so a tool that gains
// a new backend call fails a test here instead of silently 403ing in
// production.
//
// Shapes are enumerated exactly, not as prefix ranges: the backend routes
// several non-MCP sub-resources under the same families (workspace members
// and invitations, agent env and webhooks, runtime credentials and usage,
// squad member management) and only exact shapes keep them off the surface.
//
// Fail-closed: a request matching no shape is reported as unknown and
// rejected for every OAuth token, whatever its scope. An OAuth token is a
// credential for the MCP service, not a second browser session — the MCP
// surface deliberately excludes member/permission management, settings and
// account administration (see apps/mcp/src/tools.ts header), and a token
// must not reach them even when the user behind it could.
//
// Read = GET/HEAD on a known shape. Write = any other method on a known
// shape. Run = the dispatch and run-steering calls, which sit on the
// write-shaped paths a client could otherwise reach with mcp:write. Some
// shapes restrict the methods they admit: read-only surfaces (listed but
// never written by any tool) reject every non-GET method, and the project
// shape rejects DELETE — project deletion is a hard delete the MCP envelope
// deliberately does not expose.
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

type shapeKind uint8

const (
	// shapeReadWrite: GET/HEAD require mcp:read, any other method mcp:write.
	shapeReadWrite shapeKind = iota
	// shapeReadOnly: GET/HEAD require mcp:read; every other method is
	// off-surface. Used where the tools read a resource but no tool writes it.
	shapeReadOnly
	// shapeNoDelete: read/write, except DELETE which is off-surface — for the
	// one family whose deletion is a hard delete the MCP envelope excludes.
	shapeNoDelete
)

type scopeShape struct {
	// segments are the path segments after "/api"; "*" matches any single
	// segment.
	segments []string
	kind     shapeKind
}

// runShapes are matched before the surface shapes, on POST only, so dispatch
// and run steering stay out of mcp:write's reach:
//
//	POST /api/issues/quick-create              (dispatch_agent)
//	POST /api/issues/{id}/tasks/{run}/cancel   (cancel_run)
//	POST /api/issues/{id}/tasks/{run}/retry    (retry_run)
var runShapes = [][]string{
	{"issues", "quick-create"},
	{"issues", "*", "tasks", "*", "cancel"},
	{"issues", "*", "tasks", "*", "retry"},
}

// surfaceShapes is the complete MCP surface. Order within the list does not
// matter: shapes are same-length exact matches, so at most one can match a
// given request.
var surfaceShapes = []scopeShape{
	// Workspaces: the list, plus the two sub-resources the tools drive —
	// execution profiles and the audit trail. Everything else under
	// /api/workspaces/{id} (members, invitations, plugins, …) stays
	// off-surface.
	{[]string{"workspaces"}, shapeReadOnly},
	{[]string{"workspaces", "*", "execution-profiles"}, shapeReadWrite},
	{[]string{"workspaces", "*", "execution-profiles", "*"}, shapeReadWrite},
	{[]string{"workspaces", "*", "execution-profiles", "*", "entries"}, shapeReadWrite},
	{[]string{"workspaces", "*", "execution-profiles", "*", "entries", "*"}, shapeReadWrite},
	{[]string{"workspaces", "*", "execution-profiles", "*", "activate"}, shapeReadWrite},
	{[]string{"workspaces", "*", "audit-events"}, shapeReadOnly},

	// Agents: list/create, per-agent read/update, archive/restore.
	{[]string{"agents"}, shapeReadWrite},
	{[]string{"agents", "*"}, shapeReadWrite},
	{[]string{"agents", "*", "archive"}, shapeReadWrite},
	{[]string{"agents", "*", "restore"}, shapeReadWrite},

	// Projects: list/create, per-project read/update (no DELETE), and the
	// resource bindings.
	{[]string{"projects"}, shapeReadWrite},
	{[]string{"projects", "*"}, shapeNoDelete},
	{[]string{"projects", "*", "resources"}, shapeReadWrite},
	{[]string{"projects", "*", "resources", "*"}, shapeReadWrite},

	// Issues: the full issue surface including comments, relations and run
	// lookups; the runShapes above are matched first.
	{[]string{"issues"}, shapeReadWrite},
	{[]string{"issues", "*"}, shapeReadWrite},
	{[]string{"issues", "*", "comments"}, shapeReadWrite},
	{[]string{"issues", "*", "relations"}, shapeReadWrite},
	{[]string{"issues", "*", "relations", "*", "*"}, shapeReadWrite},
	{[]string{"issues", "*", "active-task"}, shapeReadOnly},
	{[]string{"issues", "*", "task-runs"}, shapeReadOnly},
	{[]string{"issues", "*", "tasks", "*"}, shapeReadOnly},

	{[]string{"comments", "*"}, shapeReadWrite},

	// Squads: list/create, per-squad read/update/archive. The members route
	// is read-only — adding, removing or re-roleing a member is permission
	// management and stays off-surface.
	{[]string{"squads"}, shapeReadWrite},
	{[]string{"squads", "*"}, shapeReadWrite},
	{[]string{"squads", "*", "members"}, shapeReadOnly},

	// Runtimes and the workspace-wide run view. The runtime list is read-only
	// (creating runtimes is daemon administration); model listing is the one
	// write, and only the detail read exists per model entry.
	{[]string{"runtimes"}, shapeReadOnly},
	{[]string{"runtimes", "*", "models"}, shapeReadWrite},
	{[]string{"runtimes", "*", "models", "*"}, shapeReadOnly},
	{[]string{"task-runs"}, shapeReadOnly},

	{[]string{"quick-replies"}, shapeReadWrite},
	{[]string{"quick-replies", "*"}, shapeReadWrite},
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

	if method == "POST" {
		for _, shape := range runShapes {
			if matchSegments(shape, rest) {
				return ScopeNeedRun
			}
		}
	}
	for _, shape := range surfaceShapes {
		if !matchSegments(shape.segments, rest) {
			continue
		}
		switch shape.kind {
		case shapeReadOnly:
			if method == "GET" || method == "HEAD" {
				return ScopeNeedRead
			}
			return ScopeUnknown
		case shapeNoDelete:
			if method == "DELETE" {
				return ScopeUnknown
			}
			return methodScope(method)
		default:
			return methodScope(method)
		}
	}
	return ScopeUnknown
}

// methodScope reads on GET/HEAD, writes on everything else. The MCP surface
// has no method whose safety differs from this split.
func methodScope(method string) ScopeRequirement {
	if method == "GET" || method == "HEAD" {
		return ScopeNeedRead
	}
	return ScopeNeedWrite
}

// matchSegments reports whether the request path (after "/api") matches the
// shape: equal length, literal segments equal, "*" any single segment.
func matchSegments(shape, rest []string) bool {
	if len(shape) != len(rest) {
		return false
	}
	for i, seg := range shape {
		if seg != "*" && seg != rest[i] {
			return false
		}
	}
	return true
}
