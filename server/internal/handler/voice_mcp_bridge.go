package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RUYI-874 link ③: the controlled backend bridge that lets the live voice
// model call the Multica MCP tool face through the gateway.
//
// The voice provider does not support native tool execution end-to-end in
// the relay: the gateway owns the provider credential and the session, so a
// client-side toolResponse pump would be the only channel. Instead the
// bridge EXECUTES server-side and echoes the function response back to the
// provider on the session's own user identity:
//
//   - the tool face is the Multica MCP registration (apps/mcp): the same
//     names, argument semantics and result projections, not a parallel
//     implementation;
//   - identity is the live session's user (live_session.user_id) — every
//     executor runs the read-only, workspace-membership-scoped queries the
//     Multica MCP REST endpoints use, so the permission boundary is exactly
//     the session user's own access;
//   - results are real rows, never synthesized: a failure comes back as a
//     structured {error:{code,message}} payload the model can read, which
//     is the one shape that keeps "voice answers fabricating tool results"
//     out of the system.
//
// The bridge is read-only by construction (no issue/project/agent writes):
// a voice turn must not mutate platform state on the user's behalf.

// voiceMCPTool is one bridge-executable tool of the Multica MCP face.
type voiceMCPTool struct {
	name        string
	description string
	params      map[string]any // JSON-schema style parameters object
	exec        func(h *Handler, ctx context.Context, session db.LiveSession, workspaceID pgtype.UUID, args map[string]any) (json.RawMessage, error)
}

// voiceMCPBridgeTools is the bridge's v1 tool face: the high-frequency
// read-only tools of the Multica MCP registration. list_workspaces and
// list_projects carry the acceptance anchor (real data, no synthesis).
func voiceMCPBridgeTools() []voiceMCPTool {
	return []voiceMCPTool{
		{
			name:        "list_workspaces",
			description: "List the Multica workspaces the session user belongs to, with id, name, slug, description and issue prefix. Entry point for the workspace-scoped tools: their `workspace` argument takes the returned slug or id.",
			params:      map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}},
			exec:        execVoiceListWorkspaces,
		},
		{
			name:        "list_projects",
			description: "List the projects in one workspace: id, title, status and issue counts. Read-only; paginated (default limit 100).",
			params: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace": map[string]any{"type": "string", "description": "Workspace slug or UUID."},
					"limit":     map[string]any{"type": "number", "description": "Page size, 1-100 (default 100)."},
					"offset":    map[string]any{"type": "number", "description": "Rows to skip (default 0)."},
				},
				"required": []string{"workspace"},
			},
			exec: execVoiceListProjects,
		},
		{
			name:        "get_project",
			description: "Read one project's title, status, priority, instructions and dates from a workspace. Read-only.",
			params: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace":  map[string]any{"type": "string", "description": "Workspace slug or UUID."},
					"project_id": map[string]any{"type": "string", "description": "Project UUID, from list_projects."},
				},
				"required": []string{"workspace", "project_id"},
			},
			exec: execVoiceGetProject,
		},
		{
			name:        "list_agents",
			description: "List the active agents in one workspace with id, name, description, status and visibility. Read-only.",
			params: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace": map[string]any{"type": "string", "description": "Workspace slug or UUID."},
				},
				"required": []string{"workspace"},
			},
			exec: execVoiceListAgents,
		},
		{
			name:        "get_issue",
			description: "Read one issue's identifier, title, status, priority, assignee and dates from a workspace. Read-only.",
			params: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace": map[string]any{"type": "string", "description": "Workspace slug or UUID."},
					"issue_id":  map[string]any{"type": "string", "description": "Issue UUID or workspace identifier (PREFIX-NUMBER)."},
				},
				"required": []string{"workspace", "issue_id"},
			},
			exec: execVoiceGetIssue,
		},
	}
}

// voiceMCPToolDeclarations composes the Gemini setup `tools` entry: one
// functionDeclarations group per bridge tool, in registration order.
func voiceMCPToolDeclarations() []map[string]any {
	decls := make([]map[string]any, 0, len(voiceMCPBridgeTools()))
	for _, tool := range voiceMCPBridgeTools() {
		decls = append(decls, map[string]any{
			"name":        tool.name,
			"description": tool.description,
			"parameters":  tool.params,
		})
	}
	return decls
}

// execVoiceToolError is a bridge execution failure with a stable machine
// code — the shape the Multica MCP tool surface returns for defined
// failures. It is returned, not raised: the caller marshals it into the
// function response so the model sees a readable, structured denial instead
// of a fabricated result.
type execVoiceToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *execVoiceToolError) Error() string { return e.Code + ": " + e.Message }

// resolveVoiceWorkspace enforces the bridge's permission boundary for a
// workspace-scoped call: the workspace must exist and the session user must
// be a member of it. The result is the workspace UUID every executor
// queries under.
func (h *Handler) resolveVoiceWorkspace(ctx context.Context, session db.LiveSession, raw string) (pgtype.UUID, *execVoiceToolError) {
	ws, err := h.Queries.GetWorkspaceBySlug(ctx, raw)
	if err != nil {
		if id, perr := util.ParseUUID(raw); perr == nil {
			ws, err = h.Queries.GetWorkspace(ctx, id)
		}
	}
	if err != nil {
		return pgtype.UUID{}, &execVoiceToolError{Code: "not_found", Message: "workspace '" + raw + "' not found"}
	}
	if _, err := h.getWorkspaceMember(ctx, uuidToString(session.UserID), uuidToString(ws.ID)); err != nil {
		return pgtype.UUID{}, &execVoiceToolError{Code: "permission_denied", Message: "the session user is not a member of workspace '" + raw + "'"}
	}
	return ws.ID, nil
}

// execVoiceListWorkspaces — Multica MCP list_workspaces, same projection
// (id, name, slug, description, issue prefix), membership-scoped by the
// session user's rows.
func execVoiceListWorkspaces(h *Handler, ctx context.Context, session db.LiveSession, _ pgtype.UUID, _ map[string]any) (json.RawMessage, error) {
	if !session.UserID.Valid {
		return nil, &execVoiceToolError{Code: "unauthenticated", Message: "the voice session has no user identity"}
	}
	workspaces, err := h.Queries.ListWorkspaces(ctx, session.UserID)
	if err != nil {
		return nil, &execVoiceToolError{Code: "internal", Message: "failed to list workspaces"}
	}
	entries := make([]map[string]any, 0, len(workspaces))
	for _, ws := range workspaces {
		entry := map[string]any{
			"id":           uuidToString(ws.ID),
			"name":         ws.Name,
			"slug":         ws.Slug,
			"issue_prefix": ws.IssuePrefix,
		}
		if ws.Description.Valid {
			entry["description"] = ws.Description.String
		}
		entries = append(entries, entry)
	}
	return json.Marshal(map[string]any{
		"total":      len(workspaces),
		"workspaces": entries,
	})
}

// execVoiceListProjects — Multica MCP list_projects: workspace-scoped
// paginated projection with the real issue counts from the stats query.
func execVoiceListProjects(h *Handler, ctx context.Context, session db.LiveSession, workspaceID pgtype.UUID, args map[string]any) (json.RawMessage, error) {
	limit, offset := voiceIntArg(args, "limit", 100), voiceIntArg(args, "offset", 0)
	if limit < 1 || limit > 100 {
		limit = 100
	}
	projects, err := h.Queries.ListProjects(ctx, db.ListProjectsParams{WorkspaceID: workspaceID})
	if err != nil {
		return nil, &execVoiceToolError{Code: "internal", Message: "failed to list projects"}
	}
	type counts struct{ total, done int64 }
	stats := map[string]counts{}
	if len(projects) > 0 {
		ids := make([]pgtype.UUID, len(projects))
		for i, p := range projects {
			ids[i] = p.ID
		}
		rows, serr := h.Queries.GetProjectIssueStats(ctx, db.GetProjectIssueStatsParams{
			WorkspaceID:        workspaceID,
			ProjectIds:         ids,
			TerminalStatusKeys: h.projectTerminalIssueStatusKeys(ctx, workspaceID),
		})
		if serr == nil {
			for _, s := range rows {
				stats[uuidToString(s.ProjectID)] = counts{total: s.TotalCount, done: s.DoneCount}
			}
		}
	}
	// The SQL list is unpaginated (workspace project counts are small);
	// apply the MCP pagination contract in memory, matching the REST
	// handler's behavior.
	projects = projects[offset : min(len(projects), offset+limit)]
	out := map[string]any{
		"total":    offset + len(projects),
		"projects": make([]map[string]any, 0, len(projects)),
	}
	for _, p := range projects {
		c := stats[uuidToString(p.ID)]
		entry := map[string]any{
			"id":          uuidToString(p.ID),
			"title":       p.Title,
			"status":      p.Status,
			"issue_count": c.total,
			"done_count":  c.done,
		}
		if p.Description.Valid {
			entry["description"] = p.Description.String
		}
		out["projects"] = append(out["projects"].([]map[string]any), entry)
	}
	return json.Marshal(out)
}

// execVoiceGetProject — Multica MCP get_project, projection without the
// write revision (the bridge is read-only).
func execVoiceGetProject(h *Handler, ctx context.Context, session db.LiveSession, workspaceID pgtype.UUID, args map[string]any) (json.RawMessage, error) {
	rawID, _ := args["project_id"].(string)
	id, err := util.ParseUUID(rawID)
	if err != nil {
		return nil, &execVoiceToolError{Code: "invalid_arguments", Message: "project_id must be a UUID"}
	}
	p, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
	if err != nil {
		return nil, &execVoiceToolError{Code: "not_found", Message: "project not found in this workspace"}
	}
	out := map[string]any{
		"id":     uuidToString(p.ID),
		"title":  p.Title,
		"status": p.Status,
	}
	if p.Description.Valid {
		out["description"] = p.Description.String
	}
	if p.Instructions.Valid {
		out["instructions"] = p.Instructions.String
	}
	if p.DueDate.Valid {
		out["due_date"] = p.DueDate.Time.Format("2006-01-02")
	}
	return json.Marshal(out)
}

// execVoiceListAgents — Multica MCP list_agents: the workspace's active
// (kind=user, unarchived) agents, same fields the MCP surface projects.
func execVoiceListAgents(h *Handler, ctx context.Context, session db.LiveSession, workspaceID pgtype.UUID, _ map[string]any) (json.RawMessage, error) {
	agents, err := h.Queries.ListAgents(ctx, workspaceID)
	if err != nil {
		return nil, &execVoiceToolError{Code: "internal", Message: "failed to list agents"}
	}
	out := map[string]any{
		"total":   len(agents),
		"agents":  make([]map[string]any, 0, len(agents)),
	}
	for _, a := range agents {
		entry := map[string]any{
			"id":         uuidToString(a.ID),
			"name":       a.Name,
			"status":     a.Status,
			"visibility": a.Visibility,
		}
		if a.Description != "" {
			entry["description"] = a.Description
		}
		out["agents"] = append(out["agents"].([]map[string]any), entry)
	}
	return json.Marshal(out)
}

// execVoiceGetIssue — Multica MCP get_issue: one issue row with its
// workspace identifier. The identifier accepts a UUID or PREFIX-NUMBER.
func execVoiceGetIssue(h *Handler, ctx context.Context, session db.LiveSession, workspaceID pgtype.UUID, args map[string]any) (json.RawMessage, error) {
	rawID, _ := args["issue_id"].(string)
	var issue db.Issue
	if id, err := util.ParseUUID(rawID); err == nil {
		issue, err = h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
		if err != nil {
			return nil, &execVoiceToolError{Code: "not_found", Message: "issue not found in this workspace"}
		}
	} else {
		issue, err = h.Queries.GetIssueByNumber(ctx, db.GetIssueByNumberParams{
			WorkspaceID: workspaceID,
			Number:      issueNumber(rawID),
		})
		if err != nil {
			return nil, &execVoiceToolError{Code: "invalid_arguments", Message: "issue_id must be a UUID or PREFIX-NUMBER"}
		}
	}
	out := map[string]any{
		"id":            uuidToString(issue.ID),
		"title":         issue.Title,
		"status":        issue.Status,
		"priority":      issue.Priority,
		"assignee_id":   uuidToString(issue.AssigneeID),
		"assignee_type": issue.AssigneeType,
		"created_at":    issue.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
	}
	if issue.Number > 0 {
		if ws, err := h.Queries.GetWorkspace(ctx, workspaceID); err == nil {
			out["identifier"] = ws.IssuePrefix + "-" + strconv.FormatInt(int64(issue.Number), 10)
		}
	}
	if issue.Description.Valid {
		out["description"] = issue.Description.String
	}
	return json.Marshal(out)
}

// issueNumber extracts the numeric part of a PREFIX-NUMBER identifier.
func issueNumber(raw string) int32 {
	for i := 0; i < len(raw); i++ {
		if raw[i] == '-' {
			if n, err := strconv.Atoi(raw[i+1:]); err == nil {
				return int32(n)
			}
			return 0
		}
	}
	return 0
}

// voiceIntArg reads a numeric argument with its default. Missing, non-number
// or out-of-contract values fall back — argument-level validation stays
// forgiving on the read-only face (the model's tool calls are not typed).
func voiceIntArg(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
		return def
	default:
		return def
	}
}

// executeVoiceMCPTool runs one provider-issued tool call on the session's
// user identity and returns the function-response payload: a real result on
// success, a structured error object on failure. Unknown tools are rejected
// by name so a drifted provider never reaches a silent no-op.
func (h *Handler) executeVoiceMCPTool(ctx context.Context, session db.LiveSession, name string, rawArgs json.RawMessage) (json.RawMessage, error) {
	tool, known := voiceMCPToolByName(name)
	if !known {
		payload, _ := json.Marshal(map[string]any{
			"error": map[string]any{"code": "unknown_tool", "message": "tool '" + name + "' is not available in the voice session"},
		})
		return payload, nil
	}
	args := map[string]any{}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			payload, _ := json.Marshal(map[string]any{
				"error": map[string]any{"code": "invalid_arguments", "message": "tool arguments are not a JSON object"},
			})
			return payload, nil
		}
	}
	if workspace, hasWs := args["workspace"]; hasWs {
		wsRaw, _ := workspace.(string)
		wsID, toolErr := h.resolveVoiceWorkspace(ctx, session, wsRaw)
		if toolErr != nil {
			payload, _ := json.Marshal(map[string]any{"error": map[string]any{"code": toolErr.Code, "message": toolErr.Message}})
			return payload, nil
		}
		result, execErr := tool.exec(h, ctx, session, wsID, args)
		if execErr != nil {
			toolErr, ok := execErr.(*execVoiceToolError)
			if !ok {
				toolErr = &execVoiceToolError{Code: "internal", Message: "tool execution failed"}
			}
			slog.Warn("voice MCP bridge tool failed",
				"tool", name, "code", toolErr.Code,
				"session_id", uuidToString(session.ID))
			payload, _ := json.Marshal(map[string]any{"error": map[string]any{"code": toolErr.Code, "message": toolErr.Message}})
			return payload, nil
		}
		return result, nil
	}
	// The only no-workspace tool is list_workspaces; every other tool
	// requires it (the MCP registration's own argument contract).
	if name == "list_workspaces" {
		return tool.exec(h, ctx, session, pgtype.UUID{}, args)
	}
	payload, _ := json.Marshal(map[string]any{
		"error": map[string]any{"code": "invalid_arguments", "message": "tool '" + name + "' requires the workspace argument"},
	})
	return payload, nil
}

func voiceMCPToolByName(name string) (voiceMCPTool, bool) {
	for _, tool := range voiceMCPBridgeTools() {
		if tool.name == name {
			return tool, true
		}
	}
	return voiceMCPTool{}, false
}
