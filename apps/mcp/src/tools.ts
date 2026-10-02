/**
 * The RUYI-82 v1 MCP tool surface.
 *
 * Read: list_workspaces, list_agents, list_projects, list_issues, get_issue,
 *       search_issues, progress_digest.
 * Write: create_issue (general — any workspace, any project), add_comment,
 *       update_issue_status, update_issue (edit an existing issue's core
 *       fields in place — pure metadata, never starts a run), assign_issue
 *       (assign/reassign/unassign an existing issue — agent/squad assignment
 *       triggers a real run, the tool description must say so).
 * Dispatch: dispatch_agent (issue quick-create with an agent — triggers a
 *       real agent run and consumes the token owner's quota; the tool
 *       description must say so).
 *
 * Every tool takes an explicit `workspace` (slug, or UUID). There is no
 * ambient workspace: the Owner decision makes create_issue universal, so
 * callers always name their target.
 *
 * v1 deliberately exposes no delete, no permission/member management, and no
 * cross-user administration (Owner-confirmed security envelope).
 */

import { DIGEST_TRACKED_STATUSES, buildDigest } from "./digest.js";
import { MulticaApiError } from "./rest.js";
import type { MulticaClient } from "./rest.js";
import {
  optionalBoolean,
  optionalClearableString,
  optionalEnum,
  optionalInt,
  optionalString,
  requireString,
  ToolInputError,
} from "./schemas.js";
import type {
  CancelRunResult,
  CommentInfo,
  IssueInfo,
  SearchIssueInfo,
  UpdateIssueBody,
} from "./types.js";

export interface JsonSchemaProperty {
  // string | string[] per JSON Schema: nullable PATCH fields declare
  // ["string", "null"] so callers can pass an explicit clearing null.
  type: string | string[];
  description: string;
  enum?: string[];
  items?: { type: string };
  minimum?: number;
  maximum?: number;
  pattern?: string;
}

export interface ToolDefinition {
  name: string;
  description: string;
  inputSchema: { type: "object"; properties: Record<string, JsonSchemaProperty>; required: string[] };
  handler(args: Record<string, unknown>, client: MulticaClient): Promise<unknown>;
}

const PRIORITY_ENUM = ["urgent", "high", "medium", "low", "none"] as const;
const ASSIGNER_TYPES = ["member", "agent", "squad"] as const;
const ASSIGN_ISSUE_TYPES = [...ASSIGNER_TYPES, "unassigned"] as const;

const DATE_PATTERN = "^\\d{4}-\\d{2}-\\d{2}$";

function wsProperty(): JsonSchemaProperty {
  return {
    type: "string",
    description:
      "Target workspace: slug (preferred) or workspace UUID. Every tool names its workspace explicitly.",
  };
}

function issueProperty(): JsonSchemaProperty {
  return {
    type: "string",
    description:
      "Issue identifier (e.g. RUYI-82) or UUID, within the target workspace.",
  };
}

function issueBrief(issue: IssueInfo | SearchIssueInfo): Record<string, unknown> {
  const brief: Record<string, unknown> = {
    identifier: issue.identifier,
    title: issue.title,
    status: issue.status,
  };
  if (issue.id !== undefined) brief.id = issue.id;
  if (issue.priority !== undefined && issue.priority !== "none") brief.priority = issue.priority;
  if (issue.assignee_type !== undefined) brief.assignee_type = issue.assignee_type;
  if (issue.assignee_id !== undefined) brief.assignee_id = issue.assignee_id;
  if (issue.project_id !== undefined) brief.project_id = issue.project_id;
  if (issue.parent_issue_id !== undefined) brief.parent_issue_id = issue.parent_issue_id;
  if (issue.due_date !== undefined) brief.due_date = issue.due_date;
  if (issue.updated_at !== undefined) brief.updated_at = issue.updated_at;
  if (issue.last_activity_at !== undefined) brief.last_activity_at = issue.last_activity_at;
  const snippet = (issue as SearchIssueInfo).matched_snippet;
  if (snippet !== undefined) brief.matched_snippet = snippet;
  return brief;
}

function commentBrief(comment: CommentInfo): Record<string, unknown> {
  return {
    id: comment.id,
    author_type: comment.author_type,
    author_id: comment.author_id,
    parent_id: comment.parent_id,
    created_at: comment.created_at,
    content: comment.content,
  };
}

export const TOOL_DEFINITIONS: ToolDefinition[] = [
  {
    name: "list_workspaces",
    description:
      "List the Multica workspaces the authenticated user belongs to. " +
      "Returns id, name, slug and issue prefix for each; use the slug as the `workspace` argument of every other tool.",
    inputSchema: { type: "object", properties: {}, required: [] },
    async handler(_args, client) {
      const workspaces = await client.listWorkspaces();
      return {
        total: workspaces.length,
        workspaces: workspaces.map((workspace) => ({
          id: workspace.id,
          slug: workspace.slug,
          name: workspace.name,
          description: workspace.description,
          issue_prefix: workspace.issue_prefix,
        })),
      };
    },
  },
  {
    name: "list_agents",
    description:
      "List the agents available in a workspace. Use a returned agent id with dispatch_agent.",
    inputSchema: {
      type: "object",
      properties: { workspace: wsProperty() },
      required: ["workspace"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const agents = await client.listAgents(workspace);
      return {
        total: agents.length,
        agents: agents.map((agent) => ({
          id: agent.id,
          name: agent.name,
          description: agent.description,
          runtime_bound: agent.runtime_bound,
        })),
      };
    },
  },
  {
    name: "list_projects",
    description:
      "List projects in a workspace. Use a returned project id with create_issue or progress_digest.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        limit: { type: "integer", description: "Page size, 1-100 (default 100).", minimum: 1, maximum: 100 },
        offset: { type: "integer", description: "Pagination offset (default 0).", minimum: 0 },
      },
      required: ["workspace"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const result = await client.listProjects(workspace, {
        limit: optionalInt(args, "limit", { min: 1, max: 100 }) ?? 100,
        offset: optionalInt(args, "offset", { min: 0 }),
      });
      return {
        total: result.total,
        projects: result.projects.map((project) => ({
          id: project.id,
          title: project.title,
          status: project.status,
          issue_count: project.issue_count,
        })),
      };
    },
  },
  {
    name: "list_issues",
    description:
      "List issues in a workspace with optional filters. Returns brief issue refs plus the total matching count. " +
      "For keyword search use search_issues; for a status overview use progress_digest.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        status: {
          type: "string",
          description:
            "Single status key: backlog | todo | in_progress | in_review | done | blocked | cancelled, or a workspace custom status key.",
        },
        statuses: {
          type: "string",
          description: "Comma-separated status keys (e.g. 'todo,in_progress').",
        },
        project_id: { type: "string", description: "Restrict to one project UUID." },
        assignee_id: { type: "string", description: "Restrict to one assignee UUID." },
        sort: {
          type: "string",
          description:
            "One of: position | title | created_at | updated_at | start_date | due_date | last_activity | status | priority.",
        },
        direction: { type: "string", description: "asc or desc (default asc)." },
        limit: {
          type: "integer",
          description: "Page size, 1-100 (default 50).",
          minimum: 1,
          maximum: 100,
        },
        offset: { type: "integer", description: "Pagination offset (default 0).", minimum: 0 },
      },
      required: ["workspace"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const result = await client.listIssues(workspace, {
        status: optionalString(args, "status"),
        statuses: optionalString(args, "statuses"),
        project_id: optionalString(args, "project_id"),
        assignee_id: optionalString(args, "assignee_id"),
        sort: optionalString(args, "sort"),
        direction: optionalString(args, "direction"),
        limit: optionalInt(args, "limit", { min: 1, max: 100 }) ?? 50,
        offset: optionalInt(args, "offset", { min: 0 }),
      });
      return {
        total: result.total,
        count: result.issues.length,
        issues: result.issues.map(issueBrief),
      };
    },
  },
  {
    name: "get_issue",
    description:
      "Get one issue with full title, description and (by default) its comment thread. " +
      "The issue can be referenced by identifier (RUYI-82) or UUID.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        include_comments: {
          type: "boolean",
          description: "Include the comment thread (default true).",
        },
      },
      required: ["workspace", "issue"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const includeComments = optionalBoolean(args, "include_comments") ?? true;
      const issue = await client.getIssue(workspace, issueId);
      const result: Record<string, unknown> = { issue };
      if (includeComments) {
        const comments = await client.listComments(workspace, issueId);
        result.comments = comments.map(commentBrief);
      }
      return result;
    },
  },
  {
    name: "search_issues",
    description:
      "Full-text search issues in a workspace by keyword. Matches titles, descriptions and comments; " +
      "pass include_closed to search done/cancelled issues too.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        query: { type: "string", description: "Keyword text to search for." },
        limit: { type: "integer", description: "Page size, 1-50 (default 20).", minimum: 1, maximum: 50 },
        offset: { type: "integer", description: "Pagination offset (default 0).", minimum: 0 },
        include_closed: {
          type: "boolean",
          description: "Include done/cancelled issues (default false).",
        },
      },
      required: ["workspace", "query"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const query = requireString(args, "query");
      const result = await client.searchIssues(workspace, query, {
        limit: optionalInt(args, "limit", { min: 1, max: 50 }) ?? 20,
        offset: optionalInt(args, "offset", { min: 0 }),
        include_closed: optionalBoolean(args, "include_closed"),
      });
      return {
        total: result.total,
        count: result.issues.length,
        issues: result.issues.map(issueBrief),
      };
    },
  },
  {
    name: "progress_digest",
    description:
      "Progress snapshot for a workspace: open-issue counts per status, overdue and due-soon issues, " +
      "and the most recently active issues. The main entry point for 'how are we doing' conversations.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        project_id: { type: "string", description: "Restrict the digest to one project UUID." },
      },
      required: ["workspace"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const projectId = optionalString(args, "project_id");
      const activeCategories = "todo,in_progress,in_review,blocked";

      const counts = await Promise.all(
        DIGEST_TRACKED_STATUSES.map(async (status) => {
          const result = await client.listIssues(workspace, {
            status,
            project_id: projectId,
            limit: 1,
          });
          return { status, total: result.total };
        }),
      );
      const recentlyActive = await client.listIssues(workspace, {
        status_categories: activeCategories,
        project_id: projectId,
        sort: "last_activity",
        direction: "desc",
        limit: 10,
      });
      const dueQueue = await client.listIssues(workspace, {
        status_categories: activeCategories,
        project_id: projectId,
        sort: "due_date",
        direction: "asc",
        limit: 10,
      });

      return buildDigest({
        workspace,
        generatedAt: new Date(),
        counts,
        recentlyActive: recentlyActive.issues,
        dueQueue: dueQueue.issues,
      });
    },
  },
  {
    name: "create_issue",
    description:
      "Create an issue in any workspace the authenticated user belongs to, optionally inside a project. " +
      "The general capture path for ideas. Returns the server-assigned identifier (e.g. RUYI-83). " +
      "Use list_workspaces to find the workspace slug and list_projects to find a project id.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        title: { type: "string", description: "Issue title (required)." },
        description: { type: "string", description: "Issue body. Markdown is supported." },
        project_id: { type: "string", description: "Project UUID within the target workspace (optional)." },
        status: {
          type: "string",
          description: "Initial status key (default 'todo').",
        },
        priority: { type: "string", enum: [...PRIORITY_ENUM], description: "Default 'none'." },
        assignee_type: { type: "string", enum: [...ASSIGNER_TYPES], description: "Member, agent or squad." },
        assignee_id: {
          type: "string",
          description: "Assignee UUID. Required when assignee_type is set. Note: assigning an agent may trigger a run once the issue leaves backlog.",
        },
        parent_issue_id: { type: "string", description: "Parent issue id/identifier for sub-issues." },
        start_date: { type: "string", description: "Start date, YYYY-MM-DD.", pattern: DATE_PATTERN },
        due_date: { type: "string", description: "Due date, YYYY-MM-DD.", pattern: DATE_PATTERN },
      },
      required: ["workspace", "title"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const title = requireString(args, "title", { maxLength: 500 });
      const assigneeType = optionalEnum(args, "assignee_type", ASSIGNER_TYPES);
      const assigneeId = optionalString(args, "assignee_id");
      if (assigneeType !== undefined && assigneeId === undefined) {
        throw new ToolInputError("'assignee_id' is required when 'assignee_type' is set");
      }
      for (const key of ["start_date", "due_date"] as const) {
        const value = optionalString(args, key);
        if (value !== undefined && !/^\d{4}-\d{2}-\d{2}$/.test(value)) {
          throw new ToolInputError(`'${key}' must be formatted YYYY-MM-DD (got '${value}')`);
        }
      }
      const issue = await client.createIssue(workspace, {
        title,
        description: optionalString(args, "description", { maxLength: 50_000 }),
        project_id: optionalString(args, "project_id"),
        status: optionalString(args, "status"),
        priority: optionalEnum(args, "priority", PRIORITY_ENUM),
        assignee_type: assigneeType,
        assignee_id: assigneeId,
        parent_issue_id: optionalString(args, "parent_issue_id"),
        start_date: optionalString(args, "start_date"),
        due_date: optionalString(args, "due_date"),
      });
      return {
        created: true,
        id: issue.id,
        identifier: issue.identifier,
        title: issue.title,
        status: issue.status,
        project_id: issue.project_id,
        workspace_id: issue.workspace_id,
      };
    },
  },
  {
    name: "add_comment",
    description:
      "Add a comment to an issue. Markdown is supported. " +
      "WARNING: an explicit @agent mention in the content dispatches that agent — a real run that consumes the token owner's quota. " +
      "The response reports trigger_outcomes for every mentioned agent so the dispatch is visible.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        content: { type: "string", description: "Comment body (Markdown supported)." },
        parent_id: { type: "string", description: "Parent comment id to reply in-thread." },
      },
      required: ["workspace", "issue", "content"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const content = requireString(args, "content", { maxLength: 50_000 });
      const comment = await client.addComment(workspace, issueId, {
        content,
        parent_id: optionalString(args, "parent_id"),
      });
      return {
        posted: true,
        id: comment.id,
        issue_id: comment.issue_id,
        created_at: comment.created_at,
        trigger_outcomes: comment.trigger_outcomes ?? [],
      };
    },
  },
  {
    name: "update_issue_status",
    description:
      "Move an issue to another status (backlog | todo | in_progress | in_review | done | blocked | cancelled, or a workspace custom status key). " +
      "NOTE: if the issue has an agent/squad assignee and leaves the backlog category, this dispatches a run (real quota use); pass suppress_run=true to change the status only.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        status: { type: "string", description: "Target status key." },
        suppress_run: {
          type: "boolean",
          description: "Set true to change status without triggering an agent run (default false).",
        },
        expected_revision: {
          type: "integer",
          description: "Optimistic-lock revision from a previous read; the write fails if the issue changed since.",
          minimum: 0,
        },
      },
      required: ["workspace", "issue", "status"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const status = requireString(args, "status");
      const issue = await client.updateIssue(workspace, issueId, {
        status,
        suppress_run: optionalBoolean(args, "suppress_run"),
        expected_revision: optionalInt(args, "expected_revision", { min: 0 }),
      });
      return {
        updated: true,
        id: issue.id,
        identifier: issue.identifier,
        title: issue.title,
        status: issue.status,
        revision: issue.revision,
      };
    },
  },
  {
    name: "update_issue",
    description:
      "Edit an EXISTING issue's core fields in place: title, description (Markdown), priority, project, parent, start/due dates. " +
      "PATCH semantics: omitted fields stay unchanged; pass null (or '') for project_id, parent_issue_id, start_date or due_date to clear that value. " +
      "The issue keeps its identifier, UUID, comments, history and relations — this is an in-place rewrite, not a replacement. " +
      "NEVER triggers an agent run and consumes no quota: pure metadata edits start no run; assignee and status changes have dedicated tools (assign_issue, update_issue_status). " +
      "Done and cancelled issues stay editable, matching the web app's rules. " +
      "Pass expected_revision (from a previous get_issue/update_issue result) for optimistic locking: if the issue changed since your read, the tool returns code 'revision_conflict' (HTTP 409) without writing — re-read with get_issue and retry. " +
      "Returns the updated scalar fields plus the new revision; the description body is not echoed, read it back with get_issue.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        title: {
          type: "string",
          description: "New title, max 500 chars. Omit to keep the current one.",
        },
        description: {
          type: "string",
          description: "New issue body, Markdown supported, max 50000 chars. Omit to keep the current one.",
        },
        priority: {
          type: "string",
          enum: [...PRIORITY_ENUM],
          description: "New priority. Omit to keep.",
        },
        project_id: {
          type: ["string", "null"],
          description:
            "Project UUID to move the issue into, or null to remove it from its project. Omit to keep.",
        },
        parent_issue_id: {
          type: ["string", "null"],
          description:
            "Parent issue UUID to attach this issue under (sub-issue), or null to detach it. Omit to keep.",
        },
        start_date: {
          type: ["string", "null"],
          description: "Start date, YYYY-MM-DD. Pass null or '' to clear. Omit to keep.",
          pattern: DATE_PATTERN,
        },
        due_date: {
          type: ["string", "null"],
          description: "Due date, YYYY-MM-DD. Pass null or '' to clear. Omit to keep.",
          pattern: DATE_PATTERN,
        },
        expected_revision: {
          type: "integer",
          description:
            "Optimistic-lock revision from a previous read; a mismatch answers code 'revision_conflict' (HTTP 409) and writes nothing.",
          minimum: 1,
        },
      },
      required: ["workspace", "issue"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const title = optionalString(args, "title", { maxLength: 500 });
      const description = optionalString(args, "description", { maxLength: 50_000 });
      const priority = optionalEnum(args, "priority", PRIORITY_ENUM);
      const projectId = optionalClearableString(args, "project_id");
      const parentIssueId = optionalClearableString(args, "parent_issue_id");
      const startDate = optionalClearableString(args, "start_date", {
        pattern: /^\d{4}-\d{2}-\d{2}$/,
        patternMessage: "'start_date' must be formatted YYYY-MM-DD (or null to clear)",
      });
      const dueDate = optionalClearableString(args, "due_date", {
        pattern: /^\d{4}-\d{2}-\d{2}$/,
        patternMessage: "'due_date' must be formatted YYYY-MM-DD (or null to clear)",
      });
      if (
        title === undefined &&
        description === undefined &&
        priority === undefined &&
        projectId === undefined &&
        parentIssueId === undefined &&
        startDate === undefined &&
        dueDate === undefined
      ) {
        throw new ToolInputError(
          "nothing to update: pass at least one of title, description, priority, " +
            "project_id, parent_issue_id, start_date, due_date",
        );
      }
      const body: UpdateIssueBody = {
        expected_revision: optionalInt(args, "expected_revision", { min: 1 }),
      };
      if (title !== undefined) body.title = title;
      if (description !== undefined) body.description = description;
      if (priority !== undefined) body.priority = priority;
      if (projectId !== undefined) body.project_id = projectId;
      if (parentIssueId !== undefined) body.parent_issue_id = parentIssueId;
      if (startDate !== undefined) body.start_date = startDate;
      if (dueDate !== undefined) body.due_date = dueDate;

      let issue: IssueInfo;
      try {
        issue = await client.updateIssue(workspace, issueId, body);
      } catch (error) {
        // A stale expected_revision is a defined outcome, not a transport
        // failure — surface it with the same structured dialect as
        // cancel_run's 409 handling so the caller can re-read and retry.
        // (The server answers both revision and field conflicts with code
        // "revision_conflict"; this tool sends no baselines, so any 409 here
        // is a lost optimistic-lock race.)
        if (
          error instanceof MulticaApiError &&
          error.status === 409 &&
          error.message.includes("revision_conflict")
        ) {
          return {
            updated: false,
            code: "revision_conflict",
            message: "the issue changed since it was read; nothing was written",
            hint: "Re-read the issue with get_issue, then retry with the fresh " +
              "expected_revision or re-apply your change on top of the current content.",
          };
        }
        throw error;
      }
      return {
        updated: true,
        id: issue.id,
        identifier: issue.identifier,
        title: issue.title,
        status: issue.status,
        priority: issue.priority,
        project_id: issue.project_id ?? null,
        parent_issue_id: issue.parent_issue_id ?? null,
        start_date: issue.start_date ?? null,
        due_date: issue.due_date ?? null,
        revision: issue.revision,
        updated_at: issue.updated_at,
      };
    },
  },
  {
    name: "assign_issue",
    description:
      "Assign, reassign or unassign the assignee of an EXISTING issue (the create_issue-time assignee is separate). " +
      "Pass assignee_type 'member' | 'agent' | 'squad' plus assignee_id to assign or reassign; pass the literal 'unassigned' (no assignee_id) to clear the assignee. " +
      "WARNING: assigning or reassigning to an agent or squad triggers a REAL agent run — the squad's run executes on its leader agent — and consumes the token owner's quota; use only when the user explicitly asks for the handoff. " +
      "Exceptions: if the issue is currently in backlog the assignment parks silently (no run until it leaves backlog); suppress_run=true applies the assignment without starting the run (the issue is flagged run_suppressed and can be run later). " +
      "Reassignment does NOT cancel an already queued/running task — old and new runs proceed in parallel, with no duplicate dispatch to the same agent. " +
      "Assigning to a member and unassigning never trigger a run.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        assignee_type: {
          type: "string",
          enum: [...ASSIGN_ISSUE_TYPES],
          description:
            "'member', 'agent' or 'squad' to assign/reassign, or the literal 'unassigned' to clear the assignee.",
        },
        assignee_id: {
          type: "string",
          description:
            "Assignee UUID: member user id, agent id (from list_agents) or squad id. Required unless assignee_type is 'unassigned'.",
        },
        suppress_run: {
          type: "boolean",
          description:
            "Set true to apply the assignment without starting the agent run it would trigger (default false). " +
            "No effect for member assignees or unassign — those never start a run.",
        },
        handoff_note: {
          type: "string",
          description:
            "Optional instruction injected into the triggered run's opening context. " +
            "Dropped when no run starts (suppress_run=true, backlog parking, member or unassign).",
        },
        expected_revision: {
          type: "integer",
          description: "Optimistic-lock revision from a previous read; the write fails if the issue changed since.",
          minimum: 0,
        },
      },
      required: ["workspace", "issue", "assignee_type"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const assigneeType = optionalEnum(args, "assignee_type", ASSIGN_ISSUE_TYPES);
      if (assigneeType === undefined) {
        throw new ToolInputError(
          "'assignee_type' is required: 'member', 'agent', 'squad', or 'unassigned'",
        );
      }
      const assigneeId = optionalString(args, "assignee_id");
      const body: UpdateIssueBody = {
        suppress_run: optionalBoolean(args, "suppress_run"),
        handoff_note: optionalString(args, "handoff_note", { maxLength: 5_000 }),
        expected_revision: optionalInt(args, "expected_revision", { min: 0 }),
      };
      if (assigneeType === "unassigned") {
        if (assigneeId !== undefined) {
          throw new ToolInputError("'assignee_id' must be omitted when assignee_type is 'unassigned'");
        }
        // The server decides unassign by rawFields: the keys must be present
        // as JSON nulls — omitted keys and empty strings both keep the
        // current assignee.
        body.assignee_type = null;
        body.assignee_id = null;
      } else {
        if (assigneeId === undefined) {
          throw new ToolInputError(
            `'assignee_id' is required when assignee_type is '${assigneeType}'`,
          );
        }
        body.assignee_type = assigneeType;
        body.assignee_id = assigneeId;
      }
      const issue = await client.updateIssue(workspace, issueId, body);
      const note =
        assigneeType === "unassigned"
          ? "Assignee cleared. Unassigning never triggers a run."
          : assigneeType === "member"
            ? "Assigned to a member. Member assignment does not trigger agent runs."
            : "Assigning to an agent/squad normally triggers a real agent run (quota use) — unless the issue was in backlog or suppress_run was set. run_suppressed=true in this result means the run was suppressed; reassignment never cancels an in-flight run.";
      return {
        assigned: assigneeType !== "unassigned",
        id: issue.id,
        identifier: issue.identifier,
        title: issue.title,
        assignee_type: issue.assignee_type ?? null,
        assignee_id: issue.assignee_id ?? null,
        revision: issue.revision,
        run_suppressed: issue.run_suppressed ?? false,
        note,
      };
    },
  },
  {
    name: "dispatch_agent",
    description:
      "Dispatch an agent on a new issue described by `prompt` (issue quick-create): creates the issue and enqueues one agent run. " +
      "WARNING: this triggers a REAL agent run and consumes the token owner's Multica quota — use only when the user explicitly asks an agent to act. " +
      "Returns the queued task_id; completion is asynchronous (the agent reports back on the issue).",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        agent_id: { type: "string", description: "Agent UUID to dispatch (from list_agents)." },
        prompt: {
          type: "string",
          description:
            "Task prompt; its opening line becomes the issue title. Address the agent by name for clarity.",
        },
        project_id: { type: "string", description: "Project UUID to file the issue into (optional)." },
        priority: { type: "string", enum: [...PRIORITY_ENUM], description: "Issue priority (optional)." },
        parent_issue_id: { type: "string", description: "File as a sub-issue of this parent (optional)." },
        due_date: { type: "string", description: "Due date, YYYY-MM-DD (optional).", pattern: DATE_PATTERN },
      },
      required: ["workspace", "agent_id", "prompt"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const agentId = requireString(args, "agent_id");
      const prompt = requireString(args, "prompt", { maxLength: 50_000 });
      const result = await client.quickCreateIssue(workspace, {
        agent_id: agentId,
        prompt,
        project_id: optionalString(args, "project_id"),
        priority: optionalEnum(args, "priority", PRIORITY_ENUM),
        parent_issue_id: optionalString(args, "parent_issue_id"),
        due_date: optionalString(args, "due_date"),
      });
      return {
        dispatched: true,
        task_id: result.task_id,
        note: "Agent run enqueued. The agent will work asynchronously and report back on the issue.",
      };
    },
  },
  {
    name: "list_issue_runs",
    description:
      "List the execution runs of ONE issue (RUYI-292): every run — parallel ones included — with status, agent, trigger source, timing and failure summary. " +
      "Read-only. status filter takes a comma-separated list of raw statuses (queued, dispatched, deferred, waiting_local_directory, running, cancel_requested, completed, failed, cancelled) or the 'pending' alias for the queued-family display bucket (queued+dispatched+deferred+waiting_local_directory); 'cancel_requested' means a stop was accepted and is awaiting runtime confirmation. " +
      "trigger filter buckets: comment (issue-comment triggered), autopilot, rerun (manual), system_retry. " +
      "Unknown filter values match nothing and return an empty list. Use get_run for one run's detail and retry chain.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        status: {
          type: "string",
          description:
            "Comma-separated raw statuses or 'pending' (queued-family bucket). Omit for all runs.",
        },
        trigger: {
          type: "string",
          enum: ["comment", "autopilot", "rerun", "system_retry"],
          description: "Trigger-source filter (optional).",
        },
        limit: {
          type: "integer",
          description: "Max runs to return, 1–1000 (default 200, newest first).",
          minimum: 1,
          maximum: 1000,
        },
      },
      required: ["workspace", "issue"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const status = optionalString(args, "status");
      const trigger = optionalEnum(args, "trigger", ["comment", "autopilot", "rerun", "system_retry"]);
      if (args.trigger !== undefined && trigger === undefined) {
        throw new ToolInputError("'trigger' must be one of: comment, autopilot, rerun, system_retry");
      }
      const limit = optionalInt(args, "limit", { min: 1, max: 1000 });
      // listIssueRuns resolves to the server's bare array — see rest.ts.
      const tasks = await client.listIssueRuns(workspace, issueId, {
        status,
        trigger,
        limit,
      });
      return {
        total: tasks.length,
        runs: tasks.map((t) => ({
          id: t.id,
          status: t.status,
          agent_id: t.agent_id,
          created_at: t.created_at,
          started_at: t.started_at ?? null,
          completed_at: t.completed_at ?? null,
          trigger: t.autopilot_run_id
            ? "autopilot"
            : t.retry_of_task_id
              ? "system_retry"
              : t.rerun_of_task_id
                ? "rerun"
                : t.trigger_comment_id
                  ? "comment"
                  : "other",
          failure_reason: t.failure_reason ?? null,
          attempt: t.attempt,
        })),
        note: "cancel_requested = stop accepted, awaiting runtime confirmation. Use get_run for detail and retry chain.",
      };
    },
  },
  {
    name: "get_run",
    description:
      "Get ONE run of an issue in detail (RUYI-292): status (including the two-phase 'cancel_requested' stop-in-progress state), timing, failure reason and raw error, cancel attribution (who asked to stop, when), and the full retry chain (ancestors + descendants across both manual-rerun and system-retry lineage). " +
      "Read-only. Errors: 404 if the run id does not exist or belongs to a different issue; 403 if you lack workspace access.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        run_id: {
          type: "string",
          description: "Run (task) UUID, from list_issue_runs or a dispatch result.",
        },
      },
      required: ["workspace", "issue", "run_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const runId = requireString(args, "run_id");
      const detail = await client.getIssueRun(workspace, issueId, runId);
      const t = detail.task;
      return {
        id: t.id,
        status: t.status,
        agent_id: t.agent_id,
        issue_id: t.issue_id,
        created_at: t.created_at,
        dispatched_at: t.started_at ?? null,
        started_at: t.started_at ?? null,
        completed_at: t.completed_at ?? null,
        error: t.error ?? null,
        failure_reason: t.failure_reason ?? null,
        attempt: t.attempt,
        rerun_of_task_id: t.rerun_of_task_id ?? null,
        retry_of_task_id: t.retry_of_task_id ?? null,
        cancel_requested_at: t.cancel_requested_at ?? null,
        cancel_requested_by_user_id: t.cancel_requested_by_user_id ?? null,
        ancestors: detail.ancestors,
        descendants: detail.descendants,
      };
    },
  },
  {
    name: "cancel_run",
    description:
      "Stop ONE specific run of an issue by run id (RUYI-292). SIDE EFFECT: requests a real stop — for an in-flight run (running/dispatched/deferred/waiting_local_directory) the status moves to cancel_requested and the runtime interrupts the agent process tree, then confirms; the row only becomes 'cancelled' after that confirmation. A queued run (never started) is cancelled immediately. " +
      "Other parallel runs of the same issue are NOT affected. " +
      "Semantics: repeat against cancel_requested → already_cancelling (the interrupt nudge is re-sent); repeat against cancelled → already_cancelled (idempotent); completed/failed runs answer 409 not_cancellable — a finished run cannot be stopped. " +
      "Errors: 403 no workspace access, 404 unknown run, 409 finished run. A stop that stays unconfirmed keeps cancel_requested — retry the cancel after ~30s (the call is safe to repeat).",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        run_id: {
          type: "string",
          description: "Run (task) UUID to stop, from list_issue_runs.",
        },
      },
      required: ["workspace", "issue", "run_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const runId = requireString(args, "run_id");
      let result: CancelRunResult;
      try {
        result = await client.cancelIssueRun(workspace, issueId, runId);
      } catch (error) {
        // A finished run answering 409 is a defined outcome, not a transport
        // failure — surface it with the same shape as the 200 family.
        if (error instanceof MulticaApiError && error.status === 409) {
          return {
            code: "not_cancellable",
            cancelled: false,
            message:
              "run already finished (completed/failed); nothing to stop",
          };
        }
        throw error;
      }
      return {
        code: result.code,
        cancelled: result.code === "cancelled" || result.code === "already_cancelled",
        stop_pending: result.code === "cancel_requested" || result.code === "already_cancelling",
        message: result.message,
        task: { id: result.task.id, status: result.task.status },
      };
    },
  },
  {
    name: "retry_run",
    description:
      "Retry ONE finished run of an issue (RUYI-292): failed and cancelled runs can be retried; retrying creates a NEW run on the SAME agent with the agent's CURRENT configuration (not a snapshot), in a fresh session, linked to the source run so the full retry chain stays traceable. The old run is never modified. " +
      "SIDE EFFECT: enqueues a real agent run and consumes the token owner's quota — use only when the user explicitly asks to re-run the work. " +
      "Anti-storm rules: if the agent already has an unfinished run on this issue → 409 agent_already_queued; if this source already has an unfinished retry → 409 retry_descendant_active; a repeat within ~5 seconds returns the run the first call created (idempotent, HTTP 200 vs 202 for a fresh enqueue). " +
      "Errors: 403 you may no longer invoke this agent, 404 unknown run, 409 source run has not finished.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        run_id: {
          type: "string",
          description: "Finished run (task) UUID to retry, from list_issue_runs.",
        },
      },
      required: ["workspace", "issue", "run_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const runId = requireString(args, "run_id");
      const t = await client.retryIssueRun(workspace, issueId, runId);
      return {
        retried: true,
        new_run_id: t.id,
        status: t.status,
        rerun_of_task_id: t.rerun_of_task_id ?? null,
        note: "New run enqueued on the source run's agent with its current configuration, fresh session. The source run is kept unchanged for history.",
      };
    },
  },
];

export const TOOL_NAMES: Set<string> = new Set(TOOL_DEFINITIONS.map((tool) => tool.name));

export function findTool(name: string): ToolDefinition | undefined {
  return TOOL_DEFINITIONS.find((tool) => tool.name === name);
}
