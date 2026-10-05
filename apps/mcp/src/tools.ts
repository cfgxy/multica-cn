/**
 * The RUYI-82 v1 MCP tool surface.
 *
 * Read: list_workspaces, list_agents, list_projects, get_project, list_issues,
 *       get_issue, search_issues, progress_digest, list_comments, get_comment.
 * Write: create_issue (general — any workspace, any project), add_comment,
 *       update_issue_status, update_issue (edit an existing issue's core
 *       fields in place — pure metadata, never starts a run), assign_issue
 *       (assign/reassign/unassign an existing issue — agent/squad assignment
 *       triggers a real run, the tool description must say so),
 *       bulk_update_issues (per-item results, same write path and run
 *       semantics as the single-issue tools), create_project,
 *       update_project (project metadata; never spawns agent runs),
 *       edit_comment, delete_comment (RUYI-352 comment management: the
 *       product's own author-or-admin gate is enforced server-side;
 *       content-changing edits re-run the comment's trigger computation, so
 *       the edit_comment description must declare the mention side effects,
 *       and defined failures come back as structured results keyed by `code`
 *       — permission_denied, revision_conflict, not_found, invalid_mentions —
 *       never as exception strings), get_issue_relations +
 *       manage_issue_relations (RUYI-351 structured issue relations — pure
 *       relationship changes never trigger a run, and the descriptions must
 *       say so explicitly).
 * Dispatch: dispatch_agent (issue quick-create with an agent — triggers a
 *       real agent run and consumes the token owner's quota; the tool
 *       description must say so).
 *
 * Every tool takes an explicit `workspace` (slug, or UUID). There is no
 * ambient workspace: the Owner decision makes create_issue universal, so
 * callers always name their target.
 *
 * The security envelope (Owner-confirmed) still excludes permission/member
 * management and cross-user administration. Project deletion is additionally
 * a hard delete, so there is no delete_project either (RUYI-354 scope
 * decision). Comment edit/delete are exposed with the product's own
 * author-or-admin permission gate and audit trail (revision + updated_at) —
 * no separate MCP-side permission layer.
 */

import { DIGEST_TRACKED_STATUSES, buildDigest } from "./digest.js";
import { MulticaApiError, MulticaRequestError, isUuid } from "./rest.js";
import type { MulticaClient } from "./rest.js";
import {
  optionalBoolean,
  optionalClearableString,
  optionalEnum,
  optionalInt,
  optionalString,
  optionalStringArray,
  requireString,
  ToolInputError,
} from "./schemas.js";
import type {
  AgentConfigInfo,
  CancelRunResult,
  CommentInfo,
  ExecutionProfileInfo,
  IssueInfo,
  ModelEntryInfo,
  ProjectInfo,
  RuntimeInfo,
  SearchIssueInfo,
  UnavailableModelEntryInfo,
  UpdateIssueBody,
  UpdateProjectBody,
} from "./types.js";

export interface JsonSchemaProperty {
  // string | string[] per JSON Schema: nullable PATCH fields declare
  // ["string", "null"] so callers can pass an explicit clearing null.
  type: string | string[];
  description: string;
  // string | null members: a nullable enum (update_project.lead_type) must
  // admit the clearing null the type already allows.
  enum?: Array<string | null>;
  items?: {
    type: string;
    properties?: Record<string, JsonSchemaProperty>;
    required?: string[];
  };
  minimum?: number;
  maximum?: number;
  maxItems?: number;
  pattern?: string;
}

export interface ToolDefinition {
  name: string;
  description: string;
  inputSchema: { type: "object"; properties: Record<string, JsonSchemaProperty>; required: string[] };
  handler(args: Record<string, unknown>, client: MulticaClient): Promise<unknown>;
}

export interface BulkUpdateItemError {
  code: string;
  message: string;
}

export type BulkUpdateItemResult =
  | {
      index: number;
      issue: string;
      outcome: "updated";
      id: string;
      identifier: string;
      status: string;
      revision?: number;
      run_suppressed: boolean;
    }
  | { index: number; issue: string; outcome: "failed"; error: BulkUpdateItemError }
  | { index: number; issue: string; outcome: "skipped"; reason: "not_attempted" };

export interface BulkUpdateResult {
  total: number;
  updated: number;
  failed: number;
  skipped: number;
  results: BulkUpdateItemResult[];
}

const PRIORITY_ENUM = ["urgent", "high", "medium", "low", "none"] as const;
const ASSIGNER_TYPES = ["member", "agent", "squad"] as const;
const ASSIGN_ISSUE_TYPES = [...ASSIGNER_TYPES, "unassigned"] as const;
const RELATION_TYPES = ["blocks", "blocked_by", "relates_to", "supersedes", "superseded_by"] as const;
const RELATION_ACTIONS = ["set_parent", "clear_parent", "add_relation", "remove_relation"] as const;
// Mirrors the backend CHECK constraint on project.status (migration 034) and
// the handler's validProjectStatuses pre-validation.
const PROJECT_STATUS_ENUM = ["planned", "in_progress", "paused", "completed", "cancelled"] as const;
const PROJECT_LEAD_TYPES = ["member", "agent"] as const;
// The backend caps project instructions at 32,000 runes (maxProjectInstructionsLen).
const PROJECT_INSTRUCTIONS_MAX = 32_000;

const DATE_PATTERN = "^\\d{4}-\\d{2}-\\d{2}$";

const BULK_ON_ERROR = ["continue", "stop"] as const;
const MAX_BULK_UPDATE_ITEMS = 50;
// Fields a bulk item may carry. Unknown keys are rejected rather than
// ignored: a silently dropped field across 50 issues is exactly the
// "silent miss" bulk_update_issues exists to prevent.
const BULK_ITEM_KEYS: ReadonlySet<string> = new Set([
  "issue",
  "status",
  "priority",
  "assignee_type",
  "assignee_id",
  "project_id",
  "parent_issue_id",
  "start_date",
  "due_date",
  "handoff_note",
  "suppress_run",
  "expected_revision",
]);
// An item carrying only these keys would write nothing.
const BULK_META_KEYS: ReadonlySet<string> = new Set([
  "suppress_run",
  "expected_revision",
  "handoff_note",
]);

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

function buildBulkItemBody(
  item: Record<string, unknown>,
  batchSuppressRun: boolean | undefined,
  index: number,
): UpdateIssueBody {
  const body: UpdateIssueBody = {};
  const status = optionalString(item, "status");
  if (status !== undefined) body.status = status;
  const priority = optionalEnum(item, "priority", PRIORITY_ENUM);
  if (priority !== undefined) body.priority = priority;
  const projectId = optionalString(item, "project_id");
  if (projectId !== undefined) body.project_id = projectId;
  const parentIssueId = optionalString(item, "parent_issue_id");
  if (parentIssueId !== undefined) body.parent_issue_id = parentIssueId;
  for (const key of ["start_date", "due_date"] as const) {
    const value = optionalString(item, key);
    if (value !== undefined) {
      if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) {
        throw new ToolInputError(`updates[${index}].${key} must be formatted YYYY-MM-DD (got '${value}')`);
      }
      body[key] = value;
    }
  }
  const assigneeType = optionalEnum(item, "assignee_type", ASSIGN_ISSUE_TYPES);
  const assigneeId = optionalString(item, "assignee_id");
  if (assigneeType === "unassigned") {
    if (assigneeId !== undefined) {
      throw new ToolInputError(`updates[${index}].assignee_id must be omitted when assignee_type is 'unassigned'`);
    }
    // Same wire contract as assign_issue: the server decides unassign by
    // the keys being present as JSON nulls.
    body.assignee_type = null;
    body.assignee_id = null;
  } else if (assigneeType !== undefined) {
    if (assigneeId === undefined) {
      throw new ToolInputError(`updates[${index}].assignee_id is required when assignee_type is '${assigneeType}'`);
    }
    body.assignee_type = assigneeType;
    body.assignee_id = assigneeId;
  } else if (assigneeId !== undefined) {
    throw new ToolInputError(`updates[${index}].assignee_type is required when assignee_id is set`);
  }
  const handoffNote = optionalString(item, "handoff_note", { maxLength: 5_000 });
  if (handoffNote !== undefined) body.handoff_note = handoffNote;
  // Per-item value wins over the batch-level default; unset on both levels
  // keeps the key off the wire — exactly the single-issue tools' default.
  const suppressRun = optionalBoolean(item, "suppress_run") ?? batchSuppressRun;
  if (suppressRun !== undefined) body.suppress_run = suppressRun;
  // The server rejects expected_revision < 1 (positive integer), so gate at
  // the same floor instead of letting the item die as a 400 round-trip.
  const expectedRevision = optionalInt(item, "expected_revision", { min: 1 });
  if (expectedRevision !== undefined) body.expected_revision = expectedRevision;
  return body;
}

function classifyUpdateFailure(error: unknown): BulkUpdateItemError {
  if (error instanceof MulticaApiError) {
    // 409 covers both the revision-conflict body (code "revision_conflict")
    // and the rarer archived-status race; the server's message stays attached
    // so callers can tell them apart.
    const code =
      error.status === 409
        ? "conflict"
        : error.status === 403
          ? "forbidden"
          : error.status === 404
            ? "not_found"
            : error.status === 400
              ? "invalid_input"
              : error.status === 429
                ? "rate_limited"
                : "error";
    return { code, message: error.message };
  }
  if (error instanceof MulticaRequestError) {
    return {
      code: "transport_error",
      message: `${error.message} (the write's outcome is unknown — re-read the issue before retrying)`,
    };
  }
  return { code: "error", message: error instanceof Error ? error.message : String(error) };
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

// The full base-field projection the project tools return (RUYI-354). Flat on
// purpose: get_project is the read entry, create/update echo it so the caller
// always walks away with the revision it needs for the next write.
function projectFull(project: ProjectInfo): Record<string, unknown> {
  const out: Record<string, unknown> = {
    id: project.id,
    title: project.title,
    status: project.status,
    priority: project.priority,
    revision: project.revision,
  };
  if (project.workspace_id !== undefined) out.workspace_id = project.workspace_id;
  if (project.description !== undefined) out.description = project.description;
  if (project.instructions !== undefined) out.instructions = project.instructions;
  if (project.icon !== undefined) out.icon = project.icon;
  if (project.lead_type !== undefined) out.lead_type = project.lead_type;
  if (project.lead_id !== undefined) out.lead_id = project.lead_id;
  if (project.start_date !== undefined) out.start_date = project.start_date;
  if (project.due_date !== undefined) out.due_date = project.due_date;
  if (project.created_at !== undefined) out.created_at = project.created_at;
  if (project.updated_at !== undefined) out.updated_at = project.updated_at;
  if (project.issue_count !== undefined) out.issue_count = project.issue_count;
  if (project.done_count !== undefined) out.done_count = project.done_count;
  if (project.resource_count !== undefined) out.resource_count = project.resource_count;
  return out;
}

function projectDateProperty(): JsonSchemaProperty {
  return { type: "string", description: "Calendar date, YYYY-MM-DD.", pattern: DATE_PATTERN };
}

// PATCH field helpers. The server decides "clear vs keep" by rawFields: a key
// present as JSON null clears the field, an absent key keeps the current
// value. optionalString/optionalEnum would collapse null to "absent", so the
// nullable variants below detect null first and pass it through untouched.

function validateProjectDate(args: Record<string, unknown>, key: string): string | undefined {
  const value = optionalString(args, key);
  if (value !== undefined && !/^\d{4}-\d{2}-\d{2}$/.test(value)) {
    throw new ToolInputError(`'${key}' must be formatted YYYY-MM-DD (got '${value}')`);
  }
  return value;
}

function nullableString(
  args: Record<string, unknown>,
  key: string,
  options: { maxLength?: number } = {},
): string | null | undefined {
  if (args[key] === undefined) return undefined;
  if (args[key] === null) return null;
  return optionalString(args, key, options);
}

function nullableEnum<T extends string>(
  args: Record<string, unknown>,
  key: string,
  allowed: readonly T[],
): T | null | undefined {
  if (args[key] === undefined) return undefined;
  if (args[key] === null) return null;
  return optionalEnum(args, key, allowed);
}

// Dates: absent keeps, null or "" clears (the server treats an empty string
// as an explicit clear too), a valid YYYY-MM-DD sets.
function nullableDate(args: Record<string, unknown>, key: string): string | null | undefined {
  if (args[key] === undefined) return undefined;
  if (args[key] === null) return null;
  return validateProjectDate(args, key) ?? null;
}

// ---- execution-config helpers (RUYI-433) ---------------------------------
//
// The config faces share one dialect: read revision → write with
// expected_revision → structured revision_conflict carrying actual_revision;
// model writes can be refused 400 unsupported_model when the runtime's
// discovered catalog lists the model as incompatible. All of it lands as
// structured results keyed by `code`, never exception strings.

/** Tri-state PATCH argument for the agent config faces (MUL-2339): absent →
 * keep, "" → explicit clear, value → set. optionalString would collapse ""
 * to "absent", so this reads the raw arg. */
function triStateString(args: Record<string, unknown>, key: string): string | undefined {
  if (!(key in args) || args[key] === undefined) return undefined;
  const value = args[key];
  if (typeof value !== "string") {
    throw new ToolInputError(`'${key}' must be a string ("" clears the stored value)`);
  }
  return value;
}

/** Entry-level tri-state (ExecutionProfileEntryRequest semantics): absent →
 * undefined (no opinion — activation leaves the agent's level alone),
 * null → null (explicit "runtime default" clear), "" → "" (also the clear),
 * value → the token. */
function triStateNullableString(args: Record<string, unknown>, key: string): string | null | undefined {
  if (!(key in args) || args[key] === undefined) return undefined;
  const value = args[key];
  if (value === null) return null;
  if (typeof value !== "string") {
    throw new ToolInputError(`'${key}' must be a string or null`);
  }
  return value;
}

/** Flat full-profile projection: the read entry + every write echoes it, so
 * the caller always walks away with the revision it needs for the next write. */
function profileFull(profile: ExecutionProfileInfo): Record<string, unknown> {
  return {
    id: profile.id,
    name: profile.name,
    description: profile.description ?? null,
    is_active: profile.is_active,
    entry_count: profile.entry_count,
    last_activated_at: profile.last_activated_at ?? null,
    revision: profile.revision,
    created_at: profile.created_at ?? null,
    updated_at: profile.updated_at ?? null,
    entries: profile.entries.map((entry) => ({
      agent_id: entry.agent_id,
      runtime_id: entry.runtime_id,
      model: entry.model,
      thinking_level: entry.thinking_level ?? null,
      updated_at: entry.updated_at ?? null,
    })),
  };
}

/** The execution-profile paths carry the workspace UUID in the URL (the
 * middleware parses it as a UUID; slugs are header-only), so a slug argument
 * is resolved once via list_workspaces. */
async function resolveWorkspaceId(client: MulticaClient, workspace: string): Promise<string> {
  if (isUuid(workspace)) return workspace;
  const workspaces = await client.listWorkspaces();
  const hit = workspaces.find((ws) => ws.slug === workspace);
  if (hit === undefined) {
    throw new ToolInputError(`workspace '${workspace}' is not among the token owner's workspaces`);
  }
  return hit.id;
}

/** Agent refs accept the UUID or the agent's exact (case-insensitive) name;
 * the name path resolves against the workspace's agent list. */
async function resolveAgentRef(
  client: MulticaClient,
  workspace: string,
  ref: string,
  label = "agent",
): Promise<{ id: string; name: string }> {
  if (isUuid(ref)) {
    const agent = await client.getAgentConfig(workspace, ref);
    return { id: agent.id, name: agent.name };
  }
  const agents = await client.listAgentConfigs(workspace);
  const hits = agents.filter((agent) => agent.name.toLowerCase() === ref.toLowerCase());
  if (hits.length === 0) {
    throw new ToolInputError(`no ${label} named '${ref}' in this workspace (use its UUID or exact name)`);
  }
  if (hits.length > 1) {
    throw new ToolInputError(
      `${hits.length} agents share the name '${ref}'; pass the agent UUID instead`,
    );
  }
  const hit = hits[0];
  if (hit === undefined) {
    throw new ToolInputError(`no ${label} named '${ref}' in this workspace (use its UUID or exact name)`);
  }
  return { id: hit.id, name: hit.name };
}

/** Structured answer for a refused 400: unsupported_model keeps the server's
 * code; any other 400 is invalid_input with the server's message. */
function configInvalidInputResult(error: MulticaApiError): Record<string, unknown> {
  const message = apiErrorMessage(error);
  if (error.body?.code === "unsupported_model") {
    return {
      updated: false,
      code: "unsupported_model",
      message,
      hint: "Pick a model the runtime advertises (get_runtime_models), or clear the model (\"\") to use the runtime default. A model the runtime has not reported stays accepted (catalog-miss passthrough).",
    };
  }
  return { updated: false, code: "invalid_input", message };
}

/** Shared 409 shape for the config faces (same dialect as update_project). */
function configConflictResult(
  error: MulticaApiError,
  resource: string,
): Record<string, unknown> {
  const conflictBody = error.body ?? {};
  return {
    updated: false,
    code: "revision_conflict",
    expected_revision: intField(conflictBody, "expected_revision") ?? null,
    actual_revision: intField(conflictBody, "actual_revision") ?? null,
    message: `the ${resource} changed since it was read; nothing was written`,
    hint: "Re-read it with the matching get/list tool, then retry with the fresh expected_revision.",
  };
}

interface ConfigItemError {
  code: string;
  message: string;
  actual_revision?: number | null;
}

function classifyConfigFailure(error: unknown): ConfigItemError {
  if (error instanceof MulticaApiError) {
    if (error.status === 409) {
      return {
        code: "revision_conflict",
        message: error.message,
        actual_revision: intField(error.body ?? {}, "actual_revision") ?? null,
      };
    }
    if (error.status === 400 && error.body?.code === "unsupported_model") {
      return { code: "unsupported_model", message: apiErrorMessage(error) };
    }
    const code =
      error.status === 403
        ? "forbidden"
        : error.status === 404
          ? "not_found"
          : error.status === 400
            ? "invalid_input"
            : error.status === 429
              ? "rate_limited"
              : "error";
    return { code, message: error.message };
  }
  if (error instanceof MulticaRequestError) {
    return {
      code: "transport_error",
      message: `${error.message} (the write's outcome is unknown — re-read the agent before retrying)`,
    };
  }
  return { code: "error", message: error instanceof Error ? error.message : String(error) };
}

const MAX_BULK_CONFIG_ITEMS = 50;
const BULK_CONFIG_ITEM_KEYS: ReadonlySet<string> = new Set([
  "agent",
  "runtime_id",
  "model",
  "thinking_level",
  "expected_revision",
]);

function agentConfigBrief(agent: AgentConfigInfo): Record<string, unknown> {
  return {
    id: agent.id,
    name: agent.name,
    runtime_id: agent.runtime_id ?? null,
    runtime_mode: agent.runtime_mode ?? null,
    model: agent.model ?? null,
    thinking_level: agent.thinking_level ?? null,
    service_tier: agent.service_tier ?? null,
    revision: agent.revision,
    updated_at: agent.updated_at ?? null,
  };
}

function runtimeBrief(runtime: RuntimeInfo): Record<string, unknown> {
  return {
    id: runtime.id,
    name: runtime.custom_name ?? runtime.name,
    provider: runtime.provider ?? null,
    runtime_mode: runtime.runtime_mode ?? null,
    status: runtime.status ?? null,
    daemon_id: runtime.daemon_id ?? null,
    visibility: runtime.visibility ?? null,
    owner_id: runtime.owner_id ?? null,
    last_seen_at: runtime.last_seen_at ?? null,
  };
}

/** Daemon instance grouping over the workspace's runtimes (RUYI-433
 * discovery): the backend has no dedicated daemon-list endpoint for member
 * tokens, so instances are derived from the runtimes' daemon_id + status. */
function groupDaemonInstances(runtimes: RuntimeInfo[]): Array<Record<string, unknown>> {
  const byDaemon = new Map<string, RuntimeInfo[]>();
  for (const runtime of runtimes) {
    if (runtime.daemon_id === undefined || runtime.daemon_id === null) continue;
    const list = byDaemon.get(runtime.daemon_id) ?? [];
    list.push(runtime);
    byDaemon.set(runtime.daemon_id, list);
  }
  const instances: Array<Record<string, unknown>> = [];
  for (const [daemonId, list] of byDaemon) {
    const lastSeen = list
      .map((rt) => rt.last_seen_at ?? "")
      .sort()
      .at(-1);
    instances.push({
      daemon_id: daemonId,
      status: list.some((rt) => rt.status === "online") ? "online" : "offline",
      runtime_count: list.length,
      online_runtime_count: list.filter((rt) => rt.status === "online").length,
      runtimes: list.map(runtimeBrief),
      last_seen_at: lastSeen === "" ? null : lastSeen,
    });
  }
  instances.sort((a, b) => String(a.daemon_id).localeCompare(String(b.daemon_id)));
  return instances;
}

/** Bounded polling for a model-list discovery round trip. Cache hits answer
 * completed inline; a cold catalog enqueues a daemon round trip that the
 * tool polls until the deadline, then hands back the request_id. */
const MODEL_LIST_POLL_INTERVAL_MS = 750;
const MODEL_LIST_DEFAULT_WAIT_MS = 15_000;
const MODEL_LIST_MAX_WAIT_MS = 60_000;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function modelCatalogBrief(request: {
  id: string;
  runtime_id: string;
  status: string;
  models?: ModelEntryInfo[];
  unavailable_models?: UnavailableModelEntryInfo[];
  supported?: boolean;
  error?: string;
  cached?: boolean;
  cached_at?: string;
}): Record<string, unknown> {
  return {
    request_id: request.id,
    runtime_id: request.runtime_id,
    status: request.status,
    supported: request.supported ?? null,
    cached: request.cached ?? false,
    cached_at: request.cached_at ?? null,
    models: (request.models ?? []).map((model) => ({
      id: model.id,
      label: model.label ?? null,
      default: model.default ?? false,
      thinking_levels: model.thinking?.supported_levels ?? null,
    })),
    unavailable_models: request.unavailable_models ?? [],
    error: request.error ?? null,
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
    name: "get_project",
    description:
      "Get ONE project in a workspace with its full metadata and the optimistic-lock revision. " +
      "Read-only. `instructions` is project-level prompt text injected into every task brief in the project. " +
      "Use list_projects to find project ids.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        project_id: { type: "string", description: "Project UUID, from list_projects." },
      },
      required: ["workspace", "project_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const projectId = requireString(args, "project_id");
      const project = await client.getProject(workspace, projectId);
      return projectFull(project);
    },
  },
  {
    name: "create_project",
    description:
      "Create a project in a workspace. Returns the new project id and its revision (starts at 1) — pass that revision as expected_revision on update_project. " +
      "Metadata-only: creating a project does not create issues and never triggers agent runs.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        title: { type: "string", description: "Project title (required)." },
        description: { type: "string", description: "Project description." },
        instructions: {
          type: "string",
          description:
            "Project-level prompt text injected into every task brief in the project (max 32,000 characters). Set deliberately.",
        },
        icon: { type: "string", description: "Project icon." },
        status: {
          type: "string",
          enum: [...PROJECT_STATUS_ENUM],
          description: "One of: planned (default) | in_progress | paused | completed | cancelled.",
        },
        priority: { type: "string", enum: [...PRIORITY_ENUM], description: "Default 'none'." },
        lead_type: { type: "string", enum: [...PROJECT_LEAD_TYPES], description: "Lead kind: member or agent." },
        lead_id: {
          type: "string",
          description: "Lead UUID. Required when lead_type is set.",
        },
        start_date: projectDateProperty(),
        due_date: projectDateProperty(),
      },
      required: ["workspace", "title"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const title = requireString(args, "title");
      const leadType = optionalEnum(args, "lead_type", PROJECT_LEAD_TYPES);
      const leadId = optionalString(args, "lead_id");
      if (leadType !== undefined && leadId === undefined) {
        throw new ToolInputError("'lead_id' is required when 'lead_type' is set");
      }
      if (leadId !== undefined && leadType === undefined) {
        throw new ToolInputError("'lead_type' is required when 'lead_id' is set");
      }
      const project = await client.createProject(workspace, {
        title,
        description: optionalString(args, "description"),
        instructions: optionalString(args, "instructions", { maxLength: PROJECT_INSTRUCTIONS_MAX }),
        icon: optionalString(args, "icon"),
        status: optionalEnum(args, "status", PROJECT_STATUS_ENUM),
        priority: optionalEnum(args, "priority", PRIORITY_ENUM),
        lead_type: leadType,
        lead_id: leadId,
        start_date: validateProjectDate(args, "start_date"),
        due_date: validateProjectDate(args, "due_date"),
      });
      return {
        created: true,
        ...projectFull(project),
      };
    },
  },
  {
    name: "update_project",
    description:
      "Update project metadata (PATCH): omitted fields keep their current value, an explicit null clears a nullable field (description, instructions, icon, lead_type/lead_id, start_date, due_date). " +
      "Pass expected_revision (from a previous read or write) for optimistic locking — a stale value fails with a structured revision_conflict instead of overwriting a concurrent change. " +
      "Metadata-only: updating a project never triggers agent runs or creates tasks.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        project_id: { type: "string", description: "Project UUID, from list_projects or a previous result." },
        title: { type: "string", description: "New title. Omit to keep the current one." },
        description: {
          type: ["string", "null"],
          description: "New description. Pass null to clear; omit to keep.",
        },
        instructions: {
          type: ["string", "null"],
          description:
            "Project-level prompt text injected into every task brief in the project (max 32,000 characters). Pass null to clear; omit to keep.",
        },
        icon: { type: ["string", "null"], description: "New icon. Pass null to clear; omit to keep." },
        status: {
          type: "string",
          enum: [...PROJECT_STATUS_ENUM],
          description: "One of: planned | in_progress | paused | completed | cancelled.",
        },
        priority: { type: "string", enum: [...PRIORITY_ENUM], description: "One of: urgent | high | medium | low | none." },
        lead_type: {
          type: ["string", "null"],
          enum: [...PROJECT_LEAD_TYPES, null],
          description: "Lead kind: member or agent. Pass null (with lead_id null) to clear; omit to keep.",
        },
        lead_id: {
          type: ["string", "null"],
          description: "Lead UUID. Pass null (with lead_type null) to clear; omit to keep.",
        },
        start_date: {
          type: ["string", "null"],
          description: "Start date, YYYY-MM-DD. Pass null to clear; omit to keep.",
          pattern: DATE_PATTERN,
        },
        due_date: {
          type: ["string", "null"],
          description: "Due date, YYYY-MM-DD. Pass null to clear; omit to keep.",
          pattern: DATE_PATTERN,
        },
        expected_revision: {
          type: "integer",
          description:
            "Optimistic-lock revision from a previous read/write of this project; the write fails with revision_conflict if the project changed since.",
          minimum: 1,
        },
      },
      required: ["workspace", "project_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const projectId = requireString(args, "project_id");
      // The lead is one concept with two columns: set or clear it as a pair,
      // never half — a half-updated lead reads as lead_id without a kind.
      const leadTypeGiven = args.lead_type !== undefined;
      const leadIdGiven = args.lead_id !== undefined;
      if (leadTypeGiven !== leadIdGiven) {
        throw new ToolInputError("'lead_type' and 'lead_id' must be set (or cleared) together");
      }
      const body: UpdateProjectBody = {
        expected_revision: optionalInt(args, "expected_revision", { min: 1 }),
        title: optionalString(args, "title"),
        status: optionalEnum(args, "status", PROJECT_STATUS_ENUM),
        priority: optionalEnum(args, "priority", PRIORITY_ENUM),
        description: nullableString(args, "description"),
        instructions: nullableString(args, "instructions", { maxLength: PROJECT_INSTRUCTIONS_MAX }),
        icon: nullableString(args, "icon"),
        lead_type: nullableEnum(args, "lead_type", PROJECT_LEAD_TYPES),
        lead_id: nullableString(args, "lead_id"),
        start_date: nullableDate(args, "start_date"),
        due_date: nullableDate(args, "due_date"),
      };
      let project: ProjectInfo;
      try {
        project = await client.updateProject(workspace, projectId, body);
      } catch (error) {
        // A stale expected_revision is a defined outcome, not a transport
        // failure — answer it with the same structured dialect as the issue
        // and comment faces (edit_comment's shape) so callers branch on the
        // code instead of exception strings. This tool sends no field
        // baselines, so any 409 here is a lost optimistic-lock race.
        if (
          error instanceof MulticaApiError &&
          error.status === 409 &&
          error.message.includes("revision_conflict")
        ) {
          const conflictBody = error.body ?? {};
          return {
            updated: false,
            id: projectId,
            code: "revision_conflict",
            expected_revision: intField(conflictBody, "expected_revision") ?? null,
            actual_revision: intField(conflictBody, "actual_revision") ?? null,
            message: "the project changed since it was read; nothing was written",
            hint: "Re-read the project with get_project, then retry with the fresh expected_revision or re-apply your change on top of the current content.",
          };
        }
        throw error;
      }
      return {
        updated: true,
        ...projectFull(project),
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
          minimum: 1,
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
        expected_revision: optionalInt(args, "expected_revision", { min: 1 }),
      });
      return {
        updated: true,
        id: issue.id,
        identifier: issue.identifier,
        title: issue.title,
        status: issue.status,
        revision: issue.revision,
        run_suppressed: issue.run_suppressed ?? false,
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
          minimum: 1,
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
        expected_revision: optionalInt(args, "expected_revision", { min: 1 }),
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
    name: "get_issue_relations",
    description:
      "Get one issue's structured relations (RUYI-351): parent plus the five edge views — blocks, blocked_by, relates_to, supersedes, superseded_by — each a list of issue briefs (id, identifier, title, status). Read-only. " +
      "Each edge is visible from both endpoints in each side's frame: a blocks edge on one issue reads as blocked_by on the other, a supersedes edge as superseded_by; relates_to reads the same both ways. " +
      "Use manage_issue_relations to change any of these.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
      },
      required: ["workspace", "issue"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const relations = await client.getIssueRelations(workspace, issueId);
      return {
        issue_id: relations.issue_id,
        identifier: relations.identifier,
        revision: relations.revision,
        parent: relations.parent ?? null,
        blocks: relations.blocks,
        blocked_by: relations.blocked_by,
        relates_to: relations.relates_to,
        supersedes: relations.supersedes,
        superseded_by: relations.superseded_by,
      };
    },
  },
  {
    name: "manage_issue_relations",
    description:
      "Manage one EXISTING issue's structured relations (RUYI-351). Actions: set_parent (target_issue required — re-parents the issue; the server rejects cycles), clear_parent (target_issue must be omitted), add_relation / remove_relation (relation_type plus target_issue required). " +
      "relation_type is one of blocks, blocked_by, relates_to, supersedes, superseded_by, named from THIS issue's perspective: blocked_by(A→B) stores 'B blocks A', superseded_by(A→B) stores 'B supersedes A', relates_to is symmetric — adding it from either side dedupes to one edge. " +
      "SIDE EFFECTS: NONE on agent runs — establishing, remounting or removing relations NEVER dispatches, wakes, or queues an agent run and consumes no run quota (unlike assign_issue or dispatch_agent, no suppress flag is involved). " +
      "A committed change bumps BOTH endpoints' issue revisions. Pass expected_revision (from a previous read) for optimistic concurrency: a stale value answers the structured revision_conflict error and nothing changes. " +
      "Other structured errors: relation_exists (409, duplicate edge incl. symmetric relates_to), relation_not_found (404, removing an unknown edge), 400 for self relations, unknown types, or targets outside the workspace.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        issue: issueProperty(),
        action: {
          type: "string",
          enum: [...RELATION_ACTIONS],
          description: "set_parent, clear_parent, add_relation or remove_relation.",
        },
        relation_type: {
          type: "string",
          enum: [...RELATION_TYPES],
          description: "One of the five relation types; required for add_relation / remove_relation.",
        },
        target_issue: {
          type: "string",
          description:
            "The other issue's UUID (as returned by list_issues / search_issues / get_issue); required for set_parent, add_relation and remove_relation, must be omitted for clear_parent.",
        },
        expected_revision: {
          type: "integer",
          description:
            "Optimistic-lock revision of THIS issue from a previous read; the write fails with revision_conflict if it changed since.",
          minimum: 1,
        },
      },
      required: ["workspace", "issue", "action"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const issueId = requireString(args, "issue");
      const action = optionalEnum(args, "action", RELATION_ACTIONS);
      if (action === undefined) {
        throw new ToolInputError(
          "'action' is required: set_parent, clear_parent, add_relation or remove_relation",
        );
      }
      const target = optionalString(args, "target_issue");
      const relationType = optionalEnum(args, "relation_type", RELATION_TYPES);
      const expectedRevision = optionalInt(args, "expected_revision", { min: 1 });
      const noRunNote =
        "No agent run: pure relationship changes never dispatch, wake, or queue one.";

      if (action === "set_parent" || action === "clear_parent") {
        if (action === "set_parent" && target === undefined) {
          throw new ToolInputError("'target_issue' is required for action 'set_parent'");
        }
        if (action === "clear_parent" && target !== undefined) {
          throw new ToolInputError("'target_issue' must be omitted for action 'clear_parent'");
        }
        const issue = await client.updateIssue(workspace, issueId, {
          parent_issue_id: action === "set_parent" ? target : null,
          expected_revision: expectedRevision,
        });
        return {
          updated: true,
          action,
          id: issue.id,
          identifier: issue.identifier,
          parent_issue_id: action === "set_parent" ? (issue.parent_issue_id ?? null) : null,
          revision: issue.revision,
          note: noRunNote,
        };
      }

      if (relationType === undefined) {
        throw new ToolInputError(`'relation_type' is required for action '${action}'`);
      }
      if (target === undefined) {
        throw new ToolInputError(`'target_issue' is required for action '${action}'`);
      }
      if (action === "add_relation") {
        const result = await client.addIssueRelation(workspace, issueId, {
          type: relationType,
          target_issue_id: target,
          expected_revision: expectedRevision,
        });
        return {
          updated: true,
          action,
          relation: result.relation,
          revision: result.issue.revision,
          note: noRunNote,
        };
      }
      const result = await client.removeIssueRelation(workspace, issueId, relationType, target, expectedRevision);
      return {
        updated: true,
        action,
        relation: result.relation,
        revision: result.issue.revision,
        note: noRunNote,
      };
    },
  },

  {
    name: "bulk_update_issues",
    description:
      "Apply field updates to MANY issues in one call: each `updates` entry names an issue (identifier or UUID) " +
      "and writes any of status, priority, assignee_type/assignee_id ('unassigned' clears), project_id, " +
      "parent_issue_id, start_date, due_date — the same write path and semantics as update_issue_status/assign_issue, " +
      "including per-item expected_revision optimistic locking and handoff_note. " +
      "RUN SIDE EFFECT: an item whose write would start an agent run (status leaving backlog, agent/squad assignment) " +
      "starts a REAL run and consumes the token owner's quota — pass suppress_run=true (batch-level, or per item to override) " +
      "to apply the writes without starting runs. " +
      "Results are per-item and index-aligned: outcome 'updated' (with the post-write revision and run_suppressed), " +
      "'failed' (error code conflict | forbidden | not_found | invalid_input | rate_limited | transport_error, " +
      "plus the server's message) or 'skipped' (reason not_attempted). " +
      "on_error='continue' (default) attempts every item independently; on_error='stop' stops at the first failure. " +
      "Neither mode is a transaction: items applied before a failure stay applied. " +
      "Retries: re-send only the failed items with a fresh expected_revision — replaying an already-applied item with its " +
      "old expected_revision fails as a conflict instead of writing twice or re-triggering a run. " +
      `Batch limit: ${MAX_BULK_UPDATE_ITEMS} items; larger batches are rejected before any write.`,
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        updates: {
          type: "array",
          description: "1-50 update items, applied in order; results line up by index.",
          items: {
            type: "object",
            properties: {
              issue: issueProperty(),
              status: { type: "string", description: "Target status key." },
              priority: { type: "string", enum: [...PRIORITY_ENUM], description: "Target priority." },
              assignee_type: {
                type: "string",
                enum: [...ASSIGN_ISSUE_TYPES],
                description: "'member', 'agent', 'squad', or 'unassigned' (clears the assignee).",
              },
              assignee_id: {
                type: "string",
                description: "Assignee UUID; required unless assignee_type is 'unassigned'.",
              },
              project_id: { type: "string", description: "Project UUID within the target workspace." },
              parent_issue_id: { type: "string", description: "Parent issue UUID within the target workspace (identifiers not accepted)." },
              start_date: { type: "string", description: "Start date, YYYY-MM-DD.", pattern: DATE_PATTERN },
              due_date: { type: "string", description: "Due date, YYYY-MM-DD.", pattern: DATE_PATTERN },
              handoff_note: {
                type: "string",
                description: "Injected into a triggered run's opening context; dropped when no run starts.",
              },
              suppress_run: {
                type: "boolean",
                description: "Per-item override of the batch-level suppress_run.",
              },
              expected_revision: {
                type: "integer",
                description: "Optimistic-lock revision from a previous read; the item fails if the issue changed since.",
                minimum: 1,
              },
            },
            required: ["issue"],
          },
          maxItems: MAX_BULK_UPDATE_ITEMS,
        },
        suppress_run: {
          type: "boolean",
          description:
            "Batch-level default: set true to apply every item without starting the agent runs they would trigger " +
            "(default false, matching the single-issue tools).",
        },
        on_error: {
          type: "string",
          enum: [...BULK_ON_ERROR],
          description:
            "'continue' (default) attempts every item independently; 'stop' stops at the first failure and reports the rest as skipped.",
        },
      },
      required: ["workspace", "updates"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const onError = optionalEnum(args, "on_error", BULK_ON_ERROR) ?? "continue";
      const batchSuppressRun = optionalBoolean(args, "suppress_run");
      const rawUpdates = args.updates;
      if (!Array.isArray(rawUpdates) || rawUpdates.length === 0) {
        throw new ToolInputError("'updates' must be a non-empty array of { issue, ...fields } items");
      }
      if (rawUpdates.length > MAX_BULK_UPDATE_ITEMS) {
        throw new ToolInputError(
          `'updates' exceeds the batch limit of ${MAX_BULK_UPDATE_ITEMS} items (got ${rawUpdates.length}); ` +
            "split the work into smaller batches — nothing was modified",
        );
      }
      // Validate the whole batch before touching anything: one bad item
      // means zero writes, never a partial application.
      const plan = rawUpdates.map((raw, index) => {
        if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
          throw new ToolInputError(`updates[${index}] must be an object`);
        }
        const item = raw as Record<string, unknown>;
        for (const key of Object.keys(item)) {
          if (!BULK_ITEM_KEYS.has(key)) {
            throw new ToolInputError(
              `updates[${index}].${key} is not a supported field; supported: ${[...BULK_ITEM_KEYS].join(", ")}`,
            );
          }
        }
        const rawIssue = item.issue;
        if (typeof rawIssue !== "string" || rawIssue.trim().length === 0) {
          throw new ToolInputError(`updates[${index}].issue is required and must be a non-empty string`);
        }
        let body: UpdateIssueBody;
        try {
          body = buildBulkItemBody(item, batchSuppressRun, index);
        } catch (error) {
          if (error instanceof ToolInputError && !error.message.startsWith("updates[")) {
            throw new ToolInputError(`updates[${index}]: ${error.message}`);
          }
          throw error;
        }
        if (Object.keys(body).filter((key) => !BULK_META_KEYS.has(key)).length === 0) {
          throw new ToolInputError(
            `updates[${index}] must set at least one writable field (status, priority, ` +
              "assignee_type/assignee_id, project_id, parent_issue_id, start_date, due_date)",
          );
        }
        return { issue: rawIssue.trim(), body };
      });

      const results: BulkUpdateItemResult[] = [];
      let stopped = false;
      for (const [index, entry] of plan.entries()) {
        if (stopped) {
          results.push({ index, issue: entry.issue, outcome: "skipped", reason: "not_attempted" });
          continue;
        }
        try {
          const issue = await client.updateIssue(workspace, entry.issue, entry.body);
          results.push({
            index,
            issue: entry.issue,
            outcome: "updated",
            id: issue.id,
            identifier: issue.identifier,
            status: issue.status,
            revision: issue.revision,
            run_suppressed: issue.run_suppressed ?? false,
          });
        } catch (error) {
          results.push({
            index,
            issue: entry.issue,
            outcome: "failed",
            error: classifyUpdateFailure(error),
          });
          if (onError === "stop") stopped = true;
        }
      }
      return {
        total: plan.length,
        updated: results.filter((item) => item.outcome === "updated").length,
        failed: results.filter((item) => item.outcome === "failed").length,
        skipped: results.filter((item) => item.outcome === "skipped").length,
        results,
      } satisfies BulkUpdateResult;
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

  // ---- execution-config management (RUYI-433) ----------------------------
  // Discovery + read-modify-write over daemon instances, runtimes, model
  // catalogs, per-agent execution config (runtime / model / thinking level)
  // and workspace execution profiles. The write faces follow the same
  // optimistic-lock contract as the issue/project faces: read revision,
  // write with expected_revision, lose a race → structured revision_conflict
  // carrying actual_revision. None of these tools starts an agent run.

  {
    name: "list_daemon_instances",
    description:
      "List the daemon instances (self-hosted devices) visible in a workspace, derived from the workspace's runtimes: per daemon its online status, runtime count and runtime summaries. " +
      "Cloud runtimes (no daemon) are summarized separately. Read-only.",
    inputSchema: {
      type: "object",
      properties: { workspace: wsProperty() },
      required: ["workspace"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const runtimes = await client.listRuntimes(workspace);
      const cloudCount = runtimes.filter((rt) => rt.daemon_id === undefined || rt.daemon_id === null).length;
      return {
        daemons: groupDaemonInstances(runtimes),
        cloud_runtime_count: cloudCount,
        total_runtime_count: runtimes.length,
      };
    },
  },
  {
    name: "get_daemon_instance",
    description:
      "Show one daemon instance (self-hosted device): its derived online status and every runtime it serves, with provider, mode and last-seen. " +
      "Read-only; unknown daemon ids return code not_found.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        daemon_id: { type: "string", description: "Daemon instance UUID, from list_daemon_instances." },
      },
      required: ["workspace", "daemon_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const daemonId = requireString(args, "daemon_id");
      const runtimes = await client.listRuntimes(workspace);
      const owned = runtimes.filter((rt) => rt.daemon_id === daemonId);
      if (owned.length === 0) {
        return { found: false, code: "not_found", daemon_id: daemonId };
      }
      return {
        found: true,
        daemon_id: daemonId,
        status: owned.some((rt) => rt.status === "online") ? "online" : "offline",
        runtime_count: owned.length,
        online_runtime_count: owned.filter((rt) => rt.status === "online").length,
        runtimes: owned.map(runtimeBrief),
      };
    },
  },
  {
    name: "list_runtimes",
    description:
      "List the agent runtimes (execution backends — daemon-installed CLI runtimes or cloud) in a workspace: name, provider (claude/codex/hermes/…), runtime mode, online status, visibility and owner. " +
      "Read-only; use get_runtime_models for a runtime's model catalog.",
    inputSchema: {
      type: "object",
      properties: { workspace: wsProperty() },
      required: ["workspace"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const runtimes = await client.listRuntimes(workspace);
      return { runtimes: runtimes.map(runtimeBrief), total: runtimes.length };
    },
  },
  {
    name: "get_runtime",
    description:
      "Show one agent runtime in detail plus used_by: every non-archived agent currently bound to it, with the model and thinking level each agent carries. " +
      "Read-only; unknown runtime ids return code not_found.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        runtime_id: { type: "string", description: "Runtime UUID, from list_runtimes." },
      },
      required: ["workspace", "runtime_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const runtimeId = requireString(args, "runtime_id");
      const [runtimes, agents] = await Promise.all([
        client.listRuntimes(workspace),
        client.listAgentConfigs(workspace),
      ]);
      const runtime = runtimes.find((rt) => rt.id === runtimeId);
      if (runtime === undefined) {
        return { found: false, code: "not_found", runtime_id: runtimeId };
      }
      const usedBy = agents
        .filter((agent) => agent.runtime_id === runtimeId &&
          (agent.archived_at === undefined || agent.archived_at === null))
        .map(agentConfigBrief);
      return {
        found: true,
        runtime: runtimeBrief(runtime),
        device_info: runtime.device_info ?? null,
        profile_id: runtime.profile_id ?? null,
        used_by: usedBy,
        used_by_count: usedBy.length,
      };
    },
  },
  {
    name: "get_runtime_models",
    description:
      "Discover a runtime's model catalog: the models the daemon advertises (each with its per-model thinking/effort levels), the ones marked unavailable, and which is the runtime default. " +
      "Answers from the server-side catalog cache when warm; otherwise asks the daemon and polls — pass wait_ms (default 15000, max 60000) to bound the wait, and request_id to resume polling an earlier request. " +
      "An offline runtime returns code runtime_offline. Read-only; this is also the source for valid model / thinking_level values on the config-write faces.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        runtime_id: { type: "string", description: "Runtime UUID, from list_runtimes." },
        request_id: {
          type: "string",
          description: "Resume polling this model-list request instead of initiating a new one.",
        },
        wait_ms: {
          type: "integer",
          description: "How long to wait for the daemon to answer (default 15000, max 60000).",
          minimum: 0,
          maximum: 60_000,
        },
      },
      required: ["workspace", "runtime_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const runtimeId = requireString(args, "runtime_id");
      const requestId = optionalString(args, "request_id");
      const waitMs = optionalInt(args, "wait_ms", { min: 0, max: MODEL_LIST_MAX_WAIT_MS }) ?? MODEL_LIST_DEFAULT_WAIT_MS;

      let current: Awaited<ReturnType<MulticaClient["getModelListRequest"]>>;
      try {
        current = requestId !== undefined
          ? await client.getModelListRequest(workspace, runtimeId, requestId)
          : await client.initiateModelList(workspace, runtimeId);
      } catch (error) {
        // The initiate face answers 503 when the runtime is offline — a
        // defined outcome for this tool, not a transport failure.
        if (error instanceof MulticaApiError && error.status === 503) {
          return {
            completed: false,
            code: "runtime_offline",
            runtime_id: runtimeId,
            message: apiErrorMessage(error),
            hint: "Bring the runtime online (its daemon must be running), then re-call.",
          };
        }
        throw error;
      }

      const deadline = Date.now() + waitMs;
      while ((current.status === "pending" || current.status === "running") && Date.now() < deadline) {
        await sleep(MODEL_LIST_POLL_INTERVAL_MS);
        current = await client.getModelListRequest(workspace, runtimeId, current.id);
      }
      if (current.status === "pending" || current.status === "running") {
        return {
          completed: false,
          code: "still_pending",
          ...modelCatalogBrief(current),
          hint: `The daemon has not answered within ${waitMs}ms. Re-call with request_id='${current.id}' to resume polling.`,
        };
      }
      if (current.status === "failed" || (current.error !== undefined && current.error !== "")) {
        return {
          completed: false,
          code: "discovery_failed",
          ...modelCatalogBrief(current),
        };
      }
      return { completed: true, ...modelCatalogBrief(current) };
    },
  },
  {
    name: "get_agent_runtime_config",
    description:
      "Show one agent's execution config: bound runtime, model, thinking level, service tier, concurrency and session-context gate, plus the agent's revision — the optimistic-lock token to pass back as expected_revision on update_agent_runtime_config. " +
      "Accepts the agent UUID or its exact name. Read-only.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        agent: { type: "string", description: "Agent UUID or exact name." },
      },
      required: ["workspace", "agent"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const agentRef = requireString(args, "agent");
      const agent = await resolveAgentRef(client, workspace, agentRef);
      const config = await client.getAgentConfig(workspace, agent.id);
      return { found: true, config: agentConfigBrief(config) };
    },
  },
  {
    name: "update_agent_runtime_config",
    description:
      "Update ONE agent's execution config in place: rebind its runtime, set its model, or set its thinking level (effort). Omitted fields keep their current value; model \"\" clears to the runtime default; thinking_level \"\" clears to the runtime default. " +
      "Pass expected_revision (from get_agent_runtime_config or a previous write) — a concurrent config write fails with structured revision_conflict carrying actual_revision instead of overwriting. " +
      "A model the runtime's discovered catalog lists as incompatible is refused with structured unsupported_model BEFORE anything is written; a model the catalog doesn't know (custom/proxy strings, cold catalog) stays accepted. " +
      "The change applies to the agent's NEXT runs; this tool never starts a run.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        agent: { type: "string", description: "Agent UUID or exact name." },
        runtime_id: { type: "string", description: "New runtime UUID (list_runtimes). Omit to keep." },
        model: {
          type: "string",
          description:
            "New model id. \"\" clears to the runtime default; a catalog-listed id must match the runtime's provider.",
        },
        thinking_level: {
          type: "string",
          description:
            "Runtime-native reasoning/effort token (see get_runtime_models thinking_levels). \"\" clears to the runtime default; values are never normalized across providers.",
        },
        expected_revision: {
          type: "integer",
          description: "Optimistic-lock revision from a previous read/write of this agent.",
          minimum: 1,
        },
      },
      required: ["workspace", "agent"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const agentRef = requireString(args, "agent");
      const resolved = await resolveAgentRef(client, workspace, agentRef);
      const body = {
        expected_revision: optionalInt(args, "expected_revision", { min: 1 }),
        runtime_id: optionalString(args, "runtime_id"),
        model: triStateString(args, "model"),
        thinking_level: triStateString(args, "thinking_level"),
      };
      try {
        const updated = await client.updateAgentConfig(workspace, resolved.id, body);
        return { updated: true, config: agentConfigBrief(updated) };
      } catch (error) {
        if (error instanceof MulticaApiError && error.status === 409) {
          return configConflictResult(error, "agent");
        }
        if (error instanceof MulticaApiError && error.status === 400) {
          return configInvalidInputResult(error);
        }
        throw error;
      }
    },
  },
  {
    name: "bulk_update_agent_runtime_config",
    description:
      "Apply execution-config changes (runtime_id / model / thinking_level) to up to 50 agents in one call — same semantics as update_agent_runtime_config per item. " +
      "Each item carries its own agent ref and optional expected_revision; results come back per item (updated / failed with code+message / skipped when on_error='stop' halts the batch). " +
      "on_error 'continue' (default) attempts every item; 'stop' stops at the first failure. Never starts a run.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        updates: {
          type: "array",
          maxItems: MAX_BULK_CONFIG_ITEMS,
          items: {
            type: "object",
            properties: {
              agent: { type: "string", description: "Agent UUID or exact name." },
              runtime_id: { type: "string", description: "New runtime UUID. Omit to keep." },
              model: { type: "string", description: "New model id; \"\" clears to the runtime default." },
              thinking_level: { type: "string", description: "New thinking token; \"\" clears." },
              expected_revision: { type: "integer", description: "Per-agent optimistic lock.", minimum: 1 },
            },
            required: ["agent"],
          },
          description: "1–50 per-agent config writes.",
        },
        on_error: {
          type: "string",
          enum: [...BULK_ON_ERROR],
          description: "continue (default) attempts every item; stop halts at the first failure.",
        },
      },
      required: ["workspace", "updates"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const rawUpdates = args.updates;
      if (!Array.isArray(rawUpdates) || rawUpdates.length === 0) {
        throw new ToolInputError("'updates' must be a non-empty array");
      }
      if (rawUpdates.length > MAX_BULK_CONFIG_ITEMS) {
        throw new ToolInputError(`'updates' accepts at most ${MAX_BULK_CONFIG_ITEMS} items (got ${rawUpdates.length})`);
      }
      const onError = optionalEnum(args, "on_error", BULK_ON_ERROR) ?? "continue";

      const results: Array<Record<string, unknown>> = [];
      let updated = 0;
      let failed = 0;
      let skipped = 0;
      let halted = false;

      for (let index = 0; index < rawUpdates.length; index += 1) {
        const raw = rawUpdates[index];
        if (typeof raw !== "object" || raw === null) {
          failed += 1;
          results.push({ index, agent: null, outcome: "failed", error: { code: "invalid_input", message: "update item must be an object" } });
          continue;
        }
        const item = raw as Record<string, unknown>;
        for (const key of Object.keys(item)) {
          if (!BULK_CONFIG_ITEM_KEYS.has(key)) {
            throw new ToolInputError(`updates[${index}].${key} is not a recognized field`);
          }
        }
        const itemIndex = index;
        if (halted) {
          skipped += 1;
          results.push({ index: itemIndex, agent: optionalString(item, "agent") ?? null, outcome: "skipped", reason: "not_attempted" });
          continue;
        }
        try {
          const agentRef = requireString(item, "agent");
          const resolved = await resolveAgentRef(client, workspace, agentRef);
          const body = {
            expected_revision: optionalInt(item, "expected_revision", { min: 1 }),
            runtime_id: optionalString(item, "runtime_id"),
            model: triStateString(item, "model"),
            thinking_level: triStateString(item, "thinking_level"),
          };
          const result = await client.updateAgentConfig(workspace, resolved.id, body);
          updated += 1;
          results.push({
            index: itemIndex,
            agent: agentRef,
            outcome: "updated",
            id: result.id,
            revision: result.revision,
            model: result.model ?? null,
            thinking_level: result.thinking_level ?? null,
            runtime_id: result.runtime_id ?? null,
          });
        } catch (error) {
          failed += 1;
          const itemError = error instanceof ToolInputError
            ? { code: "invalid_input", message: error.message }
            : classifyConfigFailure(error);
          results.push({
            index: itemIndex,
            agent: optionalString(item, "agent") ?? null,
            outcome: "failed",
            error: itemError,
          });
          if (onError === "stop") halted = true;
        }
      }
      return {
        total: rawUpdates.length,
        updated,
        failed,
        skipped,
        on_error: onError,
        results,
      };
    },
  },
  {
    name: "list_execution_profiles",
    description:
      "List a workspace's execution profiles: each profile's name, entry count, whether it is the workspace's currently-active profile, last activation time, and its revision — the optimistic-lock token for update/delete/entry writes and apply_execution_profile. " +
      "Read-only.",
    inputSchema: {
      type: "object",
      properties: { workspace: wsProperty() },
      required: ["workspace"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const workspaceId = await resolveWorkspaceId(client, workspace);
      const profiles = await client.listExecutionProfiles(workspaceId);
      return {
        profiles: profiles.map((profile) => ({
          id: profile.id,
          name: profile.name,
          description: profile.description ?? null,
          is_active: profile.is_active,
          entry_count: profile.entry_count,
          last_activated_at: profile.last_activated_at ?? null,
          revision: profile.revision,
          updated_at: profile.updated_at ?? null,
        })),
        total: profiles.length,
      };
    },
  },
  {
    name: "get_execution_profile",
    description:
      "Show one execution profile in full: its entries (agent → runtime, model, thinking level), whether it is active, and its revision — the optimistic-lock token to pass back as expected_revision on update/delete/entry/apply writes. " +
      "Read-only; unknown profile ids return code not_found.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        profile_id: { type: "string", description: "Execution profile UUID, from list_execution_profiles." },
      },
      required: ["workspace", "profile_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const profileId = requireString(args, "profile_id");
      const workspaceId = await resolveWorkspaceId(client, workspace);
      try {
        const profile = await client.getExecutionProfile(workspaceId, profileId);
        return { found: true, profile: profileFull(profile) };
      } catch (error) {
        if (error instanceof MulticaApiError && error.status === 404) {
          return { found: false, code: "not_found", profile_id: profileId };
        }
        throw error;
      }
    },
  },
  {
    name: "create_execution_profile",
    description:
      "Create an empty execution profile (a named, re-appliable set of per-agent runtime/model/thinking assignments for a workspace). " +
      "Add entries afterwards with update_execution_profile (entries=…) or apply them from a squad with apply_execution_profile. Never starts a run; activation is a separate, explicit step.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        name: { type: "string", description: "Profile name, unique within the workspace." },
        description: { type: "string", description: "Optional human description." },
      },
      required: ["workspace", "name"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const name = requireString(args, "name");
      const workspaceId = await resolveWorkspaceId(client, workspace);
      const profile = await client.createExecutionProfile(workspaceId, {
        name,
        description: optionalString(args, "description"),
      });
      return { created: true, profile: profileFull(profile) };
    },
  },
  {
    name: "update_execution_profile",
    description:
      "Update an execution profile: rename, edit its description, and/or replace-style upsert member entries (agent → runtime, model, thinking level). Omitted fields keep their value; description null clears it. " +
      "Pass expected_revision (from get_execution_profile / list_execution_profiles / a previous write) — a concurrent write fails with structured revision_conflict carrying actual_revision. " +
      "Entry writes move the profile's revision too; when 'entries' is given the tool chains the fresh revision after the metadata write automatically. " +
      "Editing a profile never rewrites agents — call apply_execution_profile to activate it.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        profile_id: { type: "string", description: "Execution profile UUID." },
        name: { type: "string", description: "New name. Omit to keep." },
        description: { type: ["string", "null"], description: "New description; null clears. Omit to keep." },
        expected_revision: {
          type: "integer",
          description: "Optimistic-lock revision from a previous read/write of this profile.",
          minimum: 1,
        },
        entries: {
          type: "array",
          items: {
            type: "object",
            properties: {
              agent: { type: "string", description: "Agent UUID or exact name." },
              runtime_id: { type: "string", description: "Runtime UUID for this agent." },
              model: { type: "string", description: "Model id for this agent." },
              thinking_level: {
                type: ["string", "null"],
                description: "Thinking token; null = no opinion (activation leaves the agent's level alone), \"\" = clear on activation.",
              },
            },
            required: ["agent", "runtime_id", "model"],
          },
          description: "Upsert these member entries after the metadata write.",
        },
      },
      required: ["workspace", "profile_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const profileId = requireString(args, "profile_id");
      const workspaceId = await resolveWorkspaceId(client, workspace);
      const expectedRevision = optionalInt(args, "expected_revision", { min: 1 });

      try {
        let profile = await client.updateExecutionProfile(workspaceId, profileId, {
          name: optionalString(args, "name"),
          description: nullableString(args, "description"),
          expected_revision: expectedRevision,
        });

        const rawEntries = args.entries;
        if (Array.isArray(rawEntries) && rawEntries.length > 0) {
          for (let index = 0; index < rawEntries.length; index += 1) {
            const raw = rawEntries[index];
            if (typeof raw !== "object" || raw === null) {
              throw new ToolInputError(`entries[${index}] must be an object`);
            }
            const item = raw as Record<string, unknown>;
            const agentRef = requireString(item, "agent");
            const resolvedAgent = await resolveAgentRef(client, workspace, agentRef);
            const entryBody = {
              agent_id: resolvedAgent.id,
              runtime_id: requireString(item, "runtime_id"),
              model: requireString(item, "model"),
              thinking_level: triStateNullableString(item, "thinking_level"),
              // Entry writes move the profile's revision: guard the FIRST
              // entry with the revision the metadata write just produced,
              // then let the rest of the batch land unguarded (one writer,
              // sequential).
              expected_revision: index === 0 ? profile.revision : undefined,
            };
            await client.upsertExecutionProfileEntry(workspaceId, profileId, entryBody);
            // Re-read so the next entry (and the response) carry live revisions.
            profile = await client.getExecutionProfile(workspaceId, profileId);
          }
        }
        return { updated: true, profile: profileFull(profile) };
      } catch (error) {
        if (error instanceof MulticaApiError && error.status === 409) {
          return configConflictResult(error, "execution profile");
        }
        if (error instanceof MulticaApiError && error.status === 404) {
          return { updated: false, code: "not_found", profile_id: profileId };
        }
        throw error;
      }
    },
  },
  {
    name: "delete_execution_profile",
    description:
      "Delete an execution profile. Pass expected_revision to refuse deleting a profile that changed since it was read (structured revision_conflict). " +
      "Deleting the ACTIVE profile also clears the workspace pointer; agents already configured keep their current runtime/model — nothing is rewritten. Never starts a run.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        profile_id: { type: "string", description: "Execution profile UUID." },
        expected_revision: {
          type: "integer",
          description: "Optimistic-lock revision; delete fails with revision_conflict if the profile changed since.",
          minimum: 1,
        },
      },
      required: ["workspace", "profile_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const profileId = requireString(args, "profile_id");
      const workspaceId = await resolveWorkspaceId(client, workspace);
      try {
        await client.deleteExecutionProfile(
          workspaceId,
          profileId,
          optionalInt(args, "expected_revision", { min: 1 }),
        );
        return { deleted: true, profile_id: profileId };
      } catch (error) {
        if (error instanceof MulticaApiError && error.status === 409) {
          return configConflictResult(error, "execution profile");
        }
        if (error instanceof MulticaApiError && error.status === 404) {
          return { deleted: false, code: "not_found", profile_id: profileId };
        }
        throw error;
      }
    },
  },
  {
    name: "apply_execution_profile",
    description:
      "Activate an execution profile: write every entry's runtime/model/thinking onto its agent, then point the workspace's active-profile at it. THIS REWRITES LIVE AGENT CONFIG — the named agents run with the profile's settings on their next task. " +
      "Entries come from either explicit 'entries' or 'squad': given a squad, each AGENT member becomes an entry carrying the shared runtime_id/model(/thinking_level) template; human members are skipped. MAPPING SEMANTICS: the squad roster is read ONCE at apply time and materialized as entries — later squad membership changes do NOT follow the profile (re-apply to re-sync). " +
      "replace_entries=true (default false) first deletes stored entries whose agent is absent from the new set. Pass expected_revision to guard the first write. Per-agent apply results come back applied/skipped/failed.",
    inputSchema: {
      type: "object",
      properties: {
        workspace: wsProperty(),
        profile_id: { type: "string", description: "Execution profile UUID." },
        entries: {
          type: "array",
          items: {
            type: "object",
            properties: {
              agent: { type: "string", description: "Agent UUID or exact name." },
              runtime_id: { type: "string", description: "Runtime UUID for this agent." },
              model: { type: "string", description: "Model id for this agent." },
              thinking_level: {
                type: ["string", "null"],
                description: "null = leave the agent's level alone; \"\" = clear to runtime default.",
              },
            },
            required: ["agent", "runtime_id", "model"],
          },
          description: "Explicit entry set. Mutually exclusive with 'squad'.",
        },
        squad: {
          type: "string",
          description:
            "Squad UUID or exact name: its agent members become entries carrying the shared runtime_id/model/thinking_level template. Mutually exclusive with 'entries'.",
        },
        runtime_id: { type: "string", description: "Required with 'squad': the runtime every squad agent is mapped to." },
        model: { type: "string", description: "Required with 'squad': the model every squad agent is mapped to." },
        thinking_level: {
          type: ["string", "null"],
          description: "Optional with 'squad': shared thinking token (null = leave levels alone).",
        },
        replace_entries: {
          type: "boolean",
          description: "Delete stored entries whose agent is absent from the new set before applying (default false = merge).",
        },
        expected_revision: {
          type: "integer",
          description: "Optimistic-lock revision guarding the first write of the batch.",
          minimum: 1,
        },
        activate_now: {
          type: "boolean",
          description: "Activate after writing entries (default true). false stages the entries without rewriting agents.",
        },
      },
      required: ["workspace", "profile_id"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const profileId = requireString(args, "profile_id");
      const workspaceId = await resolveWorkspaceId(client, workspace);
      if (args.entries !== undefined && args.squad !== undefined) {
        throw new ToolInputError("pass either 'entries' or 'squad', not both");
      }

      type PlannedEntry = { agent_id: string; runtime_id: string; model: string; thinking_level: string | null };
      const planned: PlannedEntry[] = [];
      let squadInfo: Record<string, unknown> | null = null;

      if (args.squad !== undefined) {
        const squadRef = requireString(args, "squad");
        const templateRuntimeId = requireString(args, "runtime_id");
        const templateModel = requireString(args, "model");
        const templateThinking = triStateNullableString(args, "thinking_level") ?? null;
        const squads = await client.listSquads(workspace);
        const squad = isUuid(squadRef)
          ? squads.find((s) => s.id === squadRef)
          : squads.find((s) => s.name.toLowerCase() === squadRef.toLowerCase());
        if (squad === undefined) {
          return { applied: false, code: "not_found", squad: squadRef };
        }
        const members = await client.listSquadMembers(workspace, squad.id);
        const agentMembers = members.filter((m) => m.member_type === "agent");
        const skippedHumans = members.length - agentMembers.length;
        for (const member of agentMembers) {
          planned.push({
            agent_id: member.member_id,
            runtime_id: templateRuntimeId,
            model: templateModel,
            thinking_level: templateThinking,
          });
        }
        squadInfo = {
          squad_id: squad.id,
          squad_name: squad.name,
          members_total: members.length,
          members_mapped: agentMembers.length,
          members_skipped_human: skippedHumans,
          mapping_semantics:
            "squad roster read once at apply time and materialized as profile entries; later roster changes do not follow the profile — re-apply to re-sync",
        };
        if (agentMembers.length === 0) {
          return {
            applied: false,
            code: "no_agent_members",
            squad: squadInfo,
            hint: "The squad has no agent members to map; add agents to the squad or pass explicit entries.",
          };
        }
      } else if (args.entries !== undefined) {
        const rawEntries = args.entries;
        if (!Array.isArray(rawEntries) || rawEntries.length === 0) {
          throw new ToolInputError("'entries' must be a non-empty array when given");
        }
        for (let index = 0; index < rawEntries.length; index += 1) {
          const raw = rawEntries[index];
          if (typeof raw !== "object" || raw === null) {
            throw new ToolInputError(`entries[${index}] must be an object`);
          }
          const item = raw as Record<string, unknown>;
          const resolvedAgent = await resolveAgentRef(client, workspace, requireString(item, "agent"));
          planned.push({
            agent_id: resolvedAgent.id,
            runtime_id: requireString(item, "runtime_id"),
            model: requireString(item, "model"),
            thinking_level: triStateNullableString(item, "thinking_level") ?? null,
          });
        }
      } else {
        // No entry source: apply the profile AS STORED.
      }

      try {
        let profile = await client.getExecutionProfile(workspaceId, profileId);
        let entriesWritten = 0;
        let entriesDeleted = 0;
        const deleted: string[] = [];

        if (planned.length > 0) {
          if (args.replace_entries === true) {
            const keep = new Set(planned.map((entry) => entry.agent_id));
            for (const existing of profile.entries) {
              if (!keep.has(existing.agent_id)) {
                await client.deleteExecutionProfileEntry(workspaceId, profileId, existing.agent_id);
                entriesDeleted += 1;
                deleted.push(existing.agent_id);
              }
            }
            if (entriesDeleted > 0) {
              profile = await client.getExecutionProfile(workspaceId, profileId);
            }
          }
          for (const [index, entry] of planned.entries()) {
            await client.upsertExecutionProfileEntry(workspaceId, profileId, {
              ...entry,
              expected_revision: index === 0 ? (optionalInt(args, "expected_revision", { min: 1 }) ?? profile.revision) : undefined,
            });
            entriesWritten += 1;
            if (index === 0) {
              profile = await client.getExecutionProfile(workspaceId, profileId);
            }
          }
        }

        const activateNow = args.activate_now === undefined ? true : args.activate_now === true;
        if (!activateNow) {
          return {
            applied: false,
            staged: true,
            code: "staged_not_activated",
            squad: squadInfo,
            entries_written: entriesWritten,
            entries_deleted: entriesDeleted,
            deleted_agent_ids: deleted,
            profile: profileFull(profile),
            hint: "Entries staged; call again with activate_now=true (or activate_execution_profile's flow) to rewrite the agents.",
          };
        }

        const activation = await client.activateExecutionProfile(workspaceId, profileId);
        return {
          applied: true,
          squad: squadInfo,
          entries_written: entriesWritten,
          entries_deleted: entriesDeleted,
          deleted_agent_ids: deleted,
          activation: {
            profile: profileFull(activation.profile),
            applied: activation.applied,
            skipped: activation.skipped,
            failed: activation.failed,
            results: activation.results,
          },
        };
      } catch (error) {
        if (error instanceof MulticaApiError && error.status === 409) {
          return configConflictResult(error, "execution profile");
        }
        if (error instanceof MulticaApiError && error.status === 404) {
          return { applied: false, code: "not_found", profile_id: profileId };
        }
        if (error instanceof MulticaApiError && error.status === 403) {
          return { applied: false, code: "permission_denied", message: apiErrorMessage(error) };
        }
        throw error;
      }
    },
  },
  {
    name: "get_execution_topology",
    description:
      "One read-only overview of a workspace's execution plane: every runtime with the agents bound to it (model, thinking level, revision), the daemon instances behind them, the active execution profile, and per-agent drift — whether a bound agent's current runtime/model still matches what the active profile last wrote. " +
      "Use it to answer 'what runs where, with what model, and what would applying the active profile change'.",
    inputSchema: {
      type: "object",
      properties: { workspace: wsProperty() },
      required: ["workspace"],
    },
    async handler(args, client) {
      const workspace = requireString(args, "workspace");
      const workspaceId = await resolveWorkspaceId(client, workspace);
      const [runtimes, agents, profiles] = await Promise.all([
        client.listRuntimes(workspace),
        client.listAgentConfigs(workspace),
        client.listExecutionProfiles(workspaceId),
      ]);
      const active = profiles.find((profile) => profile.is_active) ?? null;
      const activeEntries = new Map(active?.entries.map((entry) => [entry.agent_id, entry]) ?? []);

      const liveAgents = agents.filter((agent) => agent.archived_at === undefined || agent.archived_at === null);
      const topologyRuntimes = runtimes.map((runtime) => {
        const bound = liveAgents.filter((agent) => agent.runtime_id === runtime.id);
        return {
          ...runtimeBrief(runtime),
          used_by_count: bound.length,
          agents: bound.map((agent) => {
            const entry = activeEntries.get(agent.id);
            const drift = entry !== undefined
              ? entry.runtime_id !== agent.runtime_id || entry.model !== (agent.model ?? "")
              : null;
            return {
              ...agentConfigBrief(agent),
              active_profile_entry_matches: drift,
            };
          }),
        };
      });

      const unbound = liveAgents
        .filter((agent) => agent.runtime_id === undefined || agent.runtime_id === null || agent.runtime_id === "")
        .map(agentConfigBrief);
      const drifted = liveAgents.filter((agent) => {
        const entry = activeEntries.get(agent.id);
        return entry !== undefined
          && (entry.runtime_id !== agent.runtime_id || entry.model !== (agent.model ?? ""));
      }).length;

      return {
        runtimes: topologyRuntimes,
        daemons: groupDaemonInstances(runtimes),
        active_profile: active === null ? null : {
          id: active.id,
          name: active.name,
          revision: active.revision,
          entry_count: active.entry_count,
          last_activated_at: active.last_activated_at ?? null,
        },
        unbound_agents: unbound,
        drift_summary: active === null
          ? { active_profile: null, drifted_agent_count: 0, note: "no active profile — nothing to drift from" }
          : { active_profile_id: active.id, drifted_agent_count: drifted },
      };
    },
  },
];

export const TOOL_NAMES: Set<string> = new Set(TOOL_DEFINITIONS.map((tool) => tool.name));

export function findTool(name: string): ToolDefinition | undefined {
  return TOOL_DEFINITIONS.find((tool) => tool.name === name);
}
