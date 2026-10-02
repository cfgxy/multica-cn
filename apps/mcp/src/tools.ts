/**
 * The RUYI-82 v1 MCP tool surface.
 *
 * Read: list_workspaces, list_agents, list_projects, list_issues, get_issue,
 *       search_issues, progress_digest, list_comments, get_comment.
 * Write: create_issue (general — any workspace, any project), add_comment,
 *       update_issue_status, assign_issue (assign/reassign/unassign an
 *       existing issue — agent/squad assignment triggers a real run, the
 *       tool description must say so), edit_comment, delete_comment
 *       (RUYI-352 comment management: the product's own author-or-admin gate
 *       is enforced server-side; content-changing edits re-run the comment's
 *       trigger computation, so the edit_comment description must declare the
 *       mention side effects, and defined failures come back as structured
 *       results keyed by `code` — permission_denied, revision_conflict,
 *       not_found, invalid_mentions — never as exception strings).
 * Dispatch: dispatch_agent (issue quick-create with an agent — triggers a
 *       real agent run and consumes the token owner's quota; the tool
 *       description must say so).
 *
 * Every tool takes an explicit `workspace` (slug, or UUID). There is no
 * ambient workspace: the Owner decision makes create_issue universal, so
 * callers always name their target.
 *
 * The security envelope (Owner-confirmed) still excludes permission/member
 * management and cross-user administration. Comment edit/delete are exposed
 * with the product's own author-or-admin permission gate and audit trail
 * (revision + updated_at) — no separate MCP-side permission layer.
 */

import { DIGEST_TRACKED_STATUSES, buildDigest } from "./digest.js";
import { MulticaApiError } from "./rest.js";
import type { MulticaClient } from "./rest.js";
import {
  optionalBoolean,
  optionalEnum,
  optionalInt,
  optionalString,
  optionalStringArray,
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
    updated_at: comment.updated_at,
    // revision > 1 (or updated_at later than created_at) marks an edited
    // comment — the recognizable audit trail; no per-edit content history
    // exists beyond this.
    revision: comment.revision,
    content: comment.content,
    // roots_only reads only: orientation stats promised by the tool
    // description; absent on other bounded reads.
    reply_count: comment.reply_count,
    last_activity_at: comment.last_activity_at,
  };
}

// ---- structured comment failures (RUYI-352) ------------------------------
//
// The comment management endpoints define a small outcome matrix — permission
// denial (403), revision conflict (409), unknown/already-deleted comment (404),
// blocked mention admission (422 invalid_agent_mentions). Tools surface these
// as structured results keyed by `code` so callers branch on the code, never
// on exception strings. Anything outside the matrix (auth, transport, 5xx,
// other 422s) is rethrown to the MCP error surface unchanged.

function apiErrorMessage(error: MulticaApiError): string {
  const fromBody = error.body?.error;
  return typeof fromBody === "string" && fromBody.length > 0 ? fromBody : error.message;
}

function intField(body: Record<string, unknown>, key: string): number | undefined {
  const value = body[key];
  return typeof value === "number" && Number.isInteger(value) ? value : undefined;
}

function structuredCommentFailure(error: unknown): Record<string, unknown> | undefined {
  if (!(error instanceof MulticaApiError)) return undefined;
  const body = error.body ?? {};
  const message = apiErrorMessage(error);
  switch (error.status) {
    case 403:
      return {
        code: "permission_denied",
        message,
      };
    case 404:
      return {
        code: "not_found",
        message,
      };
    case 409:
      return {
        code: "revision_conflict",
        expected_revision: intField(body, "expected_revision") ?? null,
        actual_revision: intField(body, "actual_revision") ?? null,
        message,
      };
    case 422:
      if (body.code === "invalid_agent_mentions") {
        return {
          code: "invalid_mentions",
          invalid_mentions:
            Array.isArray(body.invalid_mentions) ? body.invalid_mentions : [],
          message,
        };
      }
      return undefined;
    default:
      return undefined;
  }
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
    name: "list_comments",
    description:
      "List an issue's comments with bounded-read modes for agents (RUYI-352). Read-only. " +
      "Modes: thread=<comment id> returns that thread's root plus every descendant (the server resolves the root from any anchor; add tail=<N> to keep only the N newest replies — the root always comes back); " +
      "recent=<N> returns the N most recently active threads, each as root + all descendants; " +
      "roots_only=true returns top-level comments with reply_count / last_activity_at orientation stats; " +
      "since=<RFC3339> keeps only newer comments; summary=true clips every body to ~200 characters (content_truncated marks the cuts); " +
      "fold=true collapses each resolved thread to root + conclusion so settled discussion costs no tokens. " +
      "Exclusivity (mirrored client-side): roots_only excludes thread/recent/tail; tail requires thread; fold excludes roots_only/since/tail. " +
      "Every comment carries revision — revision > 1 (or updated_at later than created_at) marks an edited comment. " +
      "The default full list returns the newest 2000 comments; use the modes above on long issues.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        thread: { type: "string", description: "Comment UUID anchor; returns its whole thread." },
        tail: { type: "integer", description: "With thread: keep only the N newest replies (0 = root only).", minimum: 0 },
        recent: { type: "integer", description: "Return the N most recently active threads.", minimum: 1 },
        roots_only: { type: "boolean", description: "Top-level comments only, with orientation stats." },
        since: { type: "string", description: "Only comments created after this RFC3339 timestamp." },
        summary: { type: "boolean", description: "Clip each body to ~200 characters (default false)." },
        fold: { type: "boolean", description: "Collapse resolved threads to root + conclusion (default false)." },
      },
      required: ["workspace", "issue"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const thread = optionalString(args, "thread");
      const tail = optionalInt(args, "tail", { min: 0 });
      const recent = optionalInt(args, "recent", { min: 1 });
      const rootsOnly = optionalBoolean(args, "roots_only") ?? false;
      const since = optionalString(args, "since");
      const summary = optionalBoolean(args, "summary") ?? false;
      const fold = optionalBoolean(args, "fold") ?? false;
      if (rootsOnly && (thread !== undefined || recent !== undefined || tail !== undefined)) {
        throw new ToolInputError("'roots_only' cannot be combined with 'thread', 'recent' or 'tail'");
      }
      if (tail !== undefined && thread === undefined) {
        throw new ToolInputError("'tail' requires 'thread' (it caps replies within one thread)");
      }
      if (fold && (rootsOnly || since !== undefined || tail !== undefined)) {
        throw new ToolInputError("'fold' cannot be combined with 'roots_only', 'since' or 'tail'");
      }
      const comments = await client.listComments(workspace, issueId, {
        thread,
        tail,
        recent,
        roots_only: rootsOnly || undefined,
        since,
        summary: summary || undefined,
        fold: fold || undefined,
      });
      return {
        total: comments.length,
        comments: comments.map(commentBrief),
      };
    },
  },
  {
    name: "get_comment",
    description:
      "Fetch ONE comment in full: exact body, author, parent, created_at/updated_at and revision (RUYI-352). Read-only. " +
      "revision > 1 (or updated_at later than created_at) marks an edited comment; there is no deeper per-edit history. " +
      "Returns found=false with code not_found when the id is unknown, the comment was deleted, or it lives outside the given issue/workspace. " +
      "Also reports thread_root_id so the caller can pull the surrounding thread with list_comments. " +
      "Use get_issue for issue context; use list_comments for bounded thread reads.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        comment_id: { type: "string", description: "Comment UUID, from add_comment / list_comments / get_issue results." },
      },
      required: ["workspace", "issue", "comment_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const commentId = requireString(args, "comment_id");
      // No dedicated single-comment GET exists server-side; thread=<id> makes
      // the server resolve the anchor's whole thread via recursive CTE and the
      // anchor is always part of that answer — one bounded read locates it.
      let thread: CommentInfo[];
      try {
        thread = await client.listComments(workspace, issueId, { thread: commentId });
      } catch (error) {
        if (error instanceof MulticaApiError && error.status === 404) {
          return {
            found: false,
            code: "not_found",
            comment: null,
            message: apiErrorMessage(error),
          };
        }
        throw error;
      }
      const comment = thread.find((candidate) => candidate.id === commentId);
      if (comment === undefined) {
        return {
          found: false,
          code: "not_found",
          comment: null,
          message: "comment not found in this issue (unknown id, deleted, or outside the workspace)",
        };
      }
      const root = thread.find((candidate) => !candidate.parent_id);
      return {
        found: true,
        comment: {
          id: comment.id,
          issue_id: comment.issue_id,
          author_type: comment.author_type,
          author_id: comment.author_id,
          parent_id: comment.parent_id ?? null,
          thread_root_id: (root ?? comment).id,
          created_at: comment.created_at,
          updated_at: comment.updated_at,
          revision: comment.revision,
          content: comment.content,
        },
      };
    },
  },
  {
    name: "edit_comment",
    description:
      "Edit ONE existing comment: replace its content (Markdown supported) (RUYI-352). " +
      "Permissions: only the comment's author or a workspace admin can edit; anyone else gets the structured result code permission_denied. " +
      "Concurrency: pass expected_revision (from a previous read of the comment) for optimistic locking — if the comment changed meanwhile, the edit is refused with code revision_conflict carrying actual_revision; re-read and retry. " +
      "WARNING (run side effects): a content-CHANGING edit re-runs the comment's whole trigger computation on the new body, exactly as if it were posted fresh — an explicit @agent/@squad mention kept or added in the new content dispatches that agent (a REAL run consuming the token owner's quota), and a member-edited comment on an agent/squad-assigned issue can also re-reach the assignee. Every dispatch outcome is reported in trigger_outcomes. " +
      "An edit that does not change the stored content triggers nothing. Removing a mention cancels the pending run the original comment triggered without re-dispatching it. Pass suppress_agent_ids to exclude specific agents from the re-trigger. " +
      "Audit: every content edit bumps revision and updated_at so edited comments stay recognizable (no per-edit content history is kept). " +
      "Defined failure codes: permission_denied, revision_conflict (with actual_revision), not_found (unknown id or already deleted), invalid_mentions (a mentioned agent cannot be invoked; invalid_mentions carries the spans).",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        comment_id: { type: "string", description: "Comment UUID to edit, from a previous read or create result." },
        content: { type: "string", description: "New comment body — replaces the stored content entirely (Markdown supported)." },
        expected_revision: {
          type: "integer",
          description: "Optimistic-lock revision from a previous read; the edit fails with revision_conflict if the comment changed since.",
          minimum: 1,
        },
        suppress_agent_ids: {
          type: "array",
          items: { type: "string" },
          description: "Agent/squad UUIDs excluded from the edit's re-trigger dispatch (structured as trigger_outcomes without them).",
        },
      },
      required: ["workspace", "comment_id", "content"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const commentId = requireString(args, "comment_id");
      const content = requireString(args, "content", { maxLength: 50_000 });
      const expectedRevision = optionalInt(args, "expected_revision", { min: 1 });
      const suppressAgentIds = optionalStringArray(args, "suppress_agent_ids");
      let comment: CommentInfo;
      try {
        comment = await client.updateComment(workspace, commentId, {
          content,
          expected_revision: expectedRevision,
          suppress_agent_ids: suppressAgentIds,
        });
      } catch (error) {
        const failure = structuredCommentFailure(error);
        if (failure === undefined) throw error;
        return { edited: false, id: commentId, ...failure };
      }
      return {
        edited: true,
        id: comment.id,
        issue_id: comment.issue_id,
        revision: comment.revision,
        created_at: comment.created_at,
        updated_at: comment.updated_at,
        trigger_outcomes: comment.trigger_outcomes ?? [],
      };
    },
  },
  {
    name: "delete_comment",
    description:
      "Delete ONE comment permanently (RUYI-352). There is no tombstone: the body becomes unrecoverable, and deleting a parent comment deletes its whole reply subtree with it. " +
      "Permissions: only the comment's author or a workspace admin can delete; anyone else gets the structured result code permission_denied. " +
      "SIDE EFFECTS: agent runs still queued from this comment's mentions are cancelled so no run executes the deleted content; deleting itself starts no run and consumes no quota. " +
      "Audit: the deletion bumps the issue revision and broadcasts a comment_deleted event; the comment body itself is gone. " +
      "Defined failure codes: permission_denied, not_found (unknown id or already deleted).",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        comment_id: { type: "string", description: "Comment UUID to delete, from a previous read or create result." },
      },
      required: ["workspace", "comment_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const commentId = requireString(args, "comment_id");
      try {
        await client.deleteComment(workspace, commentId);
      } catch (error) {
        const failure = structuredCommentFailure(error);
        if (failure === undefined) throw error;
        return { deleted: false, id: commentId, ...failure };
      }
      return { deleted: true, id: commentId };
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
