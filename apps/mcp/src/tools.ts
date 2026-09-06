/**
 * The RUYI-82 v1 MCP tool surface.
 *
 * Read: list_workspaces, list_agents, list_projects, list_issues, get_issue,
 *       search_issues, progress_digest.
 * Write: create_issue (general — any workspace, any project), add_comment,
 *       update_issue_status.
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
import type { MulticaClient } from "./rest.js";
import {
  optionalBoolean,
  optionalEnum,
  optionalInt,
  optionalString,
  requireString,
  ToolInputError,
} from "./schemas.js";
import type {
  CommentInfo,
  IssueInfo,
  SearchIssueInfo,
} from "./types.js";

export interface JsonSchemaProperty {
  type: string;
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
];

export const TOOL_NAMES: Set<string> = new Set(TOOL_DEFINITIONS.map((tool) => tool.name));

export function findTool(name: string): ToolDefinition | undefined {
  return TOOL_DEFINITIONS.find((tool) => tool.name === name);
}
