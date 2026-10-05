/**
 * Thin typed REST client for the Multica backend API subset.
 *
 * Ground rules (RUYI-82):
 * - Auth is exclusively the caller's PAT (`mul_…`), forwarded as
 *   `Authorization: Bearer …`. No cookie, no session, no other credential.
 * - Workspace scoping uses the backend's header contract
 *   (`server/internal/middleware/workspace.go`): `X-Workspace-Slug` for
 *   slugs, `X-Workspace-ID` for UUIDs — the middleware prefers the slug
 *   header, so a UUID must not be sent there.
 * - The client never touches the database; every operation is a REST call.
 * - Logs carry method, path template, status and duration only — never
 *   query strings, headers, request bodies or response bodies.
 */

import { stderrLogger, type Logger } from "./log.js";
import type {
  ActiveTaskInfo,
  AddIssueRelationBody,
  AddIssueRelationResult,
  AgentConfigInfo,
  AgentDetailInfo,
  AgentInfo,
  AuditEventListParams,
  AuditEventListResult,
  CancelRunResult,
  CommentInfo,
  CommentListParams,
  CreateAgentBody,
  CreateCommentBody,
  CreateExecutionProfileBody,
  CreateIssueBody,
  CreateProjectBody,
  CreateSquadBody,
  ExecutionProfileActivationInfo,
  ExecutionProfileEntryInfo,
  ExecutionProfileInfo,
  IssueInfo,
  IssueListParams,
  IssueListResult,
  IssueRelationType,
  IssueRelationsInfo,
  ModelListRequestInfo,
  ProjectInfo,
  QuickCreateBody,
  QuickReplyInfo,
  RemoveIssueRelationResult,
  RunDetail,
  RunInfo,
  RuntimeInfo,
  SearchIssueInfo,
  SquadInfo,
  SquadMemberInfo,
  UpdateAgentConfigBody,
  UpdateAgentBody,
  UpdateCommentBody,
  UpdateExecutionProfileBody,
  UpdateIssueBody,
  UpdateProjectBody,
  UpdateSquadBody,
  UpsertExecutionProfileEntryBody,
  WorkspaceInfo,
  WorkspaceRunListParams,
  WorkspaceRunListResult,
} from "./types.js";

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isUuid(value: string): boolean {
  return UUID_PATTERN.test(value);
}

export class MulticaApiError extends Error {
  readonly status: number;
  /**
   * Parsed JSON object body when the server answered with one (undefined
   * otherwise). Structured error codes — e.g. the 409 `revision_conflict`
   * payload's `actual_revision` — are read from here, never from the
   * flattened message string.
   */
  readonly body: Record<string, unknown> | undefined;

  constructor(status: number, message: string, body?: Record<string, unknown>) {
    super(`Multica API ${status}: ${message}`);
    this.name = "MulticaApiError";
    this.status = status;
    this.body = body;
  }
}

export class MulticaRequestError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "MulticaRequestError";
  }
}

export interface MulticaClientOptions {
  serverUrl: string;
  token: string;
  fetchImpl?: typeof fetch;
  timeoutMs?: number;
  logger?: Logger;
}

interface RequestOptions {
  workspace?: string | undefined;
  query?: Record<string, string | number | boolean | undefined> | undefined;
  body?: unknown;
}

export class MulticaClient {
  private readonly baseUrl: string;
  private readonly token: string;
  private readonly fetchImpl: typeof fetch;
  private readonly timeoutMs: number;
  private readonly logger: Logger;

  constructor(options: MulticaClientOptions) {
    this.baseUrl = options.serverUrl.replace(/\/+$/, "");
    this.token = options.token;
    this.fetchImpl = options.fetchImpl ?? fetch;
    this.timeoutMs = options.timeoutMs ?? 30_000;
    this.logger = options.logger ?? stderrLogger;
  }

  // ---- workspace-scoped operations -------------------------------------

  async listWorkspaces(): Promise<WorkspaceInfo[]> {
    return this.request<WorkspaceInfo[]>("GET", "/api/workspaces");
  }

  async listAgents(workspace: string): Promise<AgentInfo[]> {
    return this.request<AgentInfo[]>("GET", "/api/agents", { workspace });
  }

  async listProjects(
    workspace: string,
    params: { limit?: number; offset?: number } = {},
  ): Promise<{ projects: ProjectInfo[]; total: number }> {
    return this.request("GET", "/api/projects", {
      workspace,
      query: { limit: params.limit, offset: params.offset },
    });
  }

  async getProject(workspace: string, projectId: string): Promise<ProjectInfo> {
    return this.request<ProjectInfo>(
      "GET",
      `/api/projects/${encodeURIComponent(projectId)}`,
      { workspace },
    );
  }

  async createProject(workspace: string, body: CreateProjectBody): Promise<ProjectInfo> {
    return this.request<ProjectInfo>("POST", "/api/projects", { workspace, body });
  }

  async updateProject(
    workspace: string,
    projectId: string,
    body: UpdateProjectBody,
  ): Promise<ProjectInfo> {
    return this.request<ProjectInfo>(
      "PUT",
      `/api/projects/${encodeURIComponent(projectId)}`,
      { workspace, body },
    );
  }

  async listIssues(
    workspace: string,
    params: IssueListParams = {},
  ): Promise<IssueListResult> {
    return this.request("GET", "/api/issues", {
      workspace,
      query: {
        status: params.status,
        statuses: params.statuses,
        status_categories: params.status_categories,
        project_id: params.project_id,
        assignee_id: params.assignee_id,
        open_only: params.open_only === undefined ? undefined : String(params.open_only),
        sort: params.sort,
        direction: params.direction,
        limit: params.limit,
        offset: params.offset,
      },
    });
  }

  async getIssue(workspace: string, issueId: string): Promise<IssueInfo> {
    return this.request<IssueInfo>("GET", `/api/issues/${encodeURIComponent(issueId)}`, {
      workspace,
    });
  }

  async searchIssues(
    workspace: string,
    query: string,
    params: { limit?: number; offset?: number; include_closed?: boolean } = {},
  ): Promise<{ issues: SearchIssueInfo[]; total: number }> {
    return this.request("GET", "/api/issues/search", {
      workspace,
      query: {
        q: query,
        limit: params.limit,
        offset: params.offset,
        include_closed:
          params.include_closed === undefined ? undefined : String(params.include_closed),
      },
    });
  }

  async listComments(
    workspace: string,
    issueId: string,
    params: CommentListParams = {},
  ): Promise<CommentInfo[]> {
    return this.request<CommentInfo[]>("GET", `/api/issues/${encodeURIComponent(issueId)}/comments`, {
      workspace,
      query: {
        since: params.since,
        thread: params.thread,
        recent: params.recent,
        tail: params.tail,
        roots_only: params.roots_only === undefined ? undefined : String(params.roots_only),
        summary: params.summary === undefined ? undefined : String(params.summary),
        fold: params.fold === undefined ? undefined : String(params.fold),
      },
    });
  }

  async createIssue(workspace: string, body: CreateIssueBody): Promise<IssueInfo> {
    return this.request<IssueInfo>("POST", "/api/issues", { workspace, body });
  }

  async quickCreateIssue(
    workspace: string,
    body: QuickCreateBody,
  ): Promise<{ task_id: string }> {
    return this.request("POST", "/api/issues/quick-create", { workspace, body });
  }

  async addComment(
    workspace: string,
    issueId: string,
    body: CreateCommentBody,
  ): Promise<CommentInfo> {
    return this.request<CommentInfo>(
      "POST",
      `/api/issues/${encodeURIComponent(issueId)}/comments`,
      { workspace, body },
    );
  }

  async updateComment(
    workspace: string,
    commentId: string,
    body: UpdateCommentBody,
  ): Promise<CommentInfo> {
    return this.request<CommentInfo>(
      "PUT",
      `/api/comments/${encodeURIComponent(commentId)}`,
      { workspace, body },
    );
  }

  async deleteComment(workspace: string, commentId: string): Promise<void> {
    // The handler answers 204 with an empty body; request() resolves undefined.
    await this.request("DELETE", `/api/comments/${encodeURIComponent(commentId)}`, { workspace });
  }

  async updateIssue(
    workspace: string,
    issueId: string,
    body: UpdateIssueBody,
  ): Promise<IssueInfo> {
    return this.request<IssueInfo>(
      "PUT",
      `/api/issues/${encodeURIComponent(issueId)}`,
      { workspace, body },
    );
  }

  // ---- structured issue relations (RUYI-351) -----------------------------
  // Pure relationship changes: the server guarantees these never start,
  // wake, or queue an agent run.

  async getIssueRelations(workspace: string, issueId: string): Promise<IssueRelationsInfo> {
    return this.request(
      "GET",
      `/api/issues/${encodeURIComponent(issueId)}/relations`,
      { workspace },
    );
  }

  async addIssueRelation(
    workspace: string,
    issueId: string,
    body: AddIssueRelationBody,
  ): Promise<AddIssueRelationResult> {
    return this.request(
      "POST",
      `/api/issues/${encodeURIComponent(issueId)}/relations`,
      { workspace, body },
    );
  }

  async removeIssueRelation(
    workspace: string,
    issueId: string,
    relationType: IssueRelationType,
    targetIssueId: string,
    expectedRevision?: number,
  ): Promise<RemoveIssueRelationResult> {
    return this.request(
      "DELETE",
      `/api/issues/${encodeURIComponent(issueId)}/relations/${encodeURIComponent(relationType)}/${encodeURIComponent(targetIssueId)}`,
      { workspace, query: { expected_revision: expectedRevision } },
    );
  }

  async getActiveTask(workspace: string, issueId: string): Promise<ActiveTaskInfo | null> {
    try {
      return await this.request<ActiveTaskInfo>(
        "GET",
        `/api/issues/${encodeURIComponent(issueId)}/active-task`,
        { workspace },
      );
    } catch (error) {
      // No task has ever been dispatched for this issue.
      if (error instanceof MulticaApiError && error.status === 404) {
        return null;
      }
      throw error;
    }
  }

  // ---- run lifecycle (RUYI-292) ------------------------------------------

  async listIssueRuns(
    workspace: string,
    issueId: string,
    params: { status?: string; trigger?: string; limit?: number } = {},
  ): Promise<RunInfo[]> {
    // The handler answers task-runs with a bare array (writeJSON of
    // []AgentTaskResponse) — no wrapper object. tools.test.ts pins this
    // shape end-to-end; keep both sides in sync.
    return this.request<RunInfo[]>("GET", `/api/issues/${encodeURIComponent(issueId)}/task-runs`, {
      workspace,
      query: {
        status: params.status,
        trigger: params.trigger,
        limit: params.limit,
      },
    });
  }

  async getIssueRun(workspace: string, issueId: string, runId: string): Promise<RunDetail> {
    return this.request(
      "GET",
      `/api/issues/${encodeURIComponent(issueId)}/tasks/${encodeURIComponent(runId)}`,
      { workspace },
    );
  }

  async cancelIssueRun(workspace: string, issueId: string, runId: string): Promise<CancelRunResult> {
    return this.request(
      "POST",
      `/api/issues/${encodeURIComponent(issueId)}/tasks/${encodeURIComponent(runId)}/cancel`,
      { workspace, body: {} },
    );
  }

  async retryIssueRun(workspace: string, issueId: string, runId: string): Promise<RunInfo> {
    return this.request(
      "POST",
      `/api/issues/${encodeURIComponent(issueId)}/tasks/${encodeURIComponent(runId)}/retry`,
      { workspace, body: {} },
    );
  }

  // ---- execution-config management (RUYI-433) ----------------------------
  // Daemon/runtime/model/thinking-effort/squad-profile discovery and writes.
  // The execution-profile paths carry the workspace UUID in the URL (the
  // middleware parses it as a UUID, slugs are header-only), so those methods
  // take the resolved UUID; the tool layer resolves slug → UUID once.

  async listRuntimes(workspace: string): Promise<RuntimeInfo[]> {
    return this.request<RuntimeInfo[]>("GET", "/api/runtimes", { workspace });
  }

  /** Full agent projection (GET /api/agents carries the config fields). */
  async listAgentConfigs(workspace: string): Promise<AgentConfigInfo[]> {
    return this.request<AgentConfigInfo[]>("GET", "/api/agents", { workspace });
  }

  async getAgentConfig(workspace: string, agentId: string): Promise<AgentConfigInfo> {
    return this.request<AgentConfigInfo>(
      "GET",
      `/api/agents/${encodeURIComponent(agentId)}`,
      { workspace },
    );
  }

  /** Router exposes UpdateAgent at PUT only (no PATCH route). */
  async updateAgentConfig(
    workspace: string,
    agentId: string,
    body: UpdateAgentConfigBody,
  ): Promise<AgentConfigInfo> {
    return this.request<AgentConfigInfo>(
      "PUT",
      `/api/agents/${encodeURIComponent(agentId)}`,
      { workspace, body },
    );
  }

  /** Cache-first: a warm catalog answers completed inline; a cold one
   * enqueues a daemon round trip the caller polls by request id. */
  async initiateModelList(workspace: string, runtimeId: string): Promise<ModelListRequestInfo> {
    return this.request<ModelListRequestInfo>(
      "POST",
      `/api/runtimes/${encodeURIComponent(runtimeId)}/models`,
      { workspace, body: {} },
    );
  }

  async getModelListRequest(
    workspace: string,
    runtimeId: string,
    requestId: string,
  ): Promise<ModelListRequestInfo> {
    return this.request<ModelListRequestInfo>(
      "GET",
      `/api/runtimes/${encodeURIComponent(runtimeId)}/models/${encodeURIComponent(requestId)}`,
      { workspace },
    );
  }

  async listExecutionProfiles(workspaceId: string): Promise<ExecutionProfileInfo[]> {
    return this.request<ExecutionProfileInfo[]>(
      "GET",
      `/api/workspaces/${encodeURIComponent(workspaceId)}/execution-profiles`,
    );
  }

  async getExecutionProfile(workspaceId: string, profileId: string): Promise<ExecutionProfileInfo> {
    return this.request<ExecutionProfileInfo>(
      "GET",
      `/api/workspaces/${encodeURIComponent(workspaceId)}/execution-profiles/${encodeURIComponent(profileId)}`,
    );
  }

  async createExecutionProfile(
    workspaceId: string,
    body: CreateExecutionProfileBody,
  ): Promise<ExecutionProfileInfo> {
    return this.request<ExecutionProfileInfo>(
      "POST",
      `/api/workspaces/${encodeURIComponent(workspaceId)}/execution-profiles`,
      { body },
    );
  }

  async updateExecutionProfile(
    workspaceId: string,
    profileId: string,
    body: UpdateExecutionProfileBody,
  ): Promise<ExecutionProfileInfo> {
    return this.request<ExecutionProfileInfo>(
      "PATCH",
      `/api/workspaces/${encodeURIComponent(workspaceId)}/execution-profiles/${encodeURIComponent(profileId)}`,
      { body },
    );
  }

  async deleteExecutionProfile(
    workspaceId: string,
    profileId: string,
    expectedRevision?: number,
  ): Promise<void> {
    await this.request(
      "DELETE",
      `/api/workspaces/${encodeURIComponent(workspaceId)}/execution-profiles/${encodeURIComponent(profileId)}`,
      { query: { expected_revision: expectedRevision } },
    );
  }

  async upsertExecutionProfileEntry(
    workspaceId: string,
    profileId: string,
    body: UpsertExecutionProfileEntryBody,
  ): Promise<ExecutionProfileEntryInfo> {
    return this.request<ExecutionProfileEntryInfo>(
      "PUT",
      `/api/workspaces/${encodeURIComponent(workspaceId)}/execution-profiles/${encodeURIComponent(profileId)}/entries`,
      { body },
    );
  }

  /** 204 on success — resolves undefined. */
  async deleteExecutionProfileEntry(
    workspaceId: string,
    profileId: string,
    agentId: string,
    expectedRevision?: number,
  ): Promise<void> {
    await this.request(
      "DELETE",
      `/api/workspaces/${encodeURIComponent(workspaceId)}/execution-profiles/${encodeURIComponent(profileId)}/entries/${encodeURIComponent(agentId)}`,
      { query: { expected_revision: expectedRevision } },
    );
  }

  async activateExecutionProfile(
    workspaceId: string,
    profileId: string,
  ): Promise<ExecutionProfileActivationInfo> {
    return this.request<ExecutionProfileActivationInfo>(
      "POST",
      `/api/workspaces/${encodeURIComponent(workspaceId)}/execution-profiles/${encodeURIComponent(profileId)}/activate`,
      { body: {} },
    );
  }

  async listSquads(workspace: string): Promise<SquadInfo[]> {
    return this.request<SquadInfo[]>("GET", "/api/squads", { workspace });
  }

  async listSquadMembers(workspace: string, squadId: string): Promise<SquadMemberInfo[]> {
    return this.request<SquadMemberInfo[]>(
      "GET",
      `/api/squads/${encodeURIComponent(squadId)}/members`,
      { workspace },
    );
  }

  // The workspace audit search (RUYI-355): the same endpoint the web app
  // reads, so the MCP tool and the UI always see the same trail. Issue-level
  // filtering is just issue_id here — the server pins it on the issue route.
  // Unlike every header-scoped route, this one keys the workspace by UUID in
  // its path and has no slug resolution, so a slug input resolves to its UUID
  // first via the caller's own workspace list (the PAT user is necessarily a
  // member of any workspace it may read here).
  async listAuditEvents(
    workspace: string,
    params: AuditEventListParams = {},
  ): Promise<AuditEventListResult> {
    const workspaceId = isUuid(workspace)
      ? workspace
      : await this.resolveWorkspaceIdBySlug(workspace);
    return this.request("GET", `/api/workspaces/${encodeURIComponent(workspaceId)}/audit-events`, {
      workspace,
      query: {
        domain: params.domain,
        event_type: params.event_type,
        actor_type: params.actor_type,
        actor_id: params.actor_id,
        issue_id: params.issue_id,
        task_id: params.task_id,
        agent_id: params.agent_id,
        runtime_id: params.runtime_id,
        reason: params.reason,
        since: params.since,
        until: params.until,
        limit: params.limit,
        cursor: params.cursor,
        cursor_id: params.cursor_id,
      },
    });
  }

  private async resolveWorkspaceIdBySlug(slug: string): Promise<string> {
    const workspaces = await this.listWorkspaces();
    const match = workspaces.find((w) => w.slug === slug);
    if (!match) {
      throw new MulticaRequestError(`workspace not found for slug: ${slug}`);
    }
    return match.id;
  }

  // ---- workspace management surface (RUYI-419) ----------------------------
  // Same read/write contract as the run lifecycle: every operation is a REST
  // call scoped by the workspace headers; run side effects are declared in
  // the tool descriptions, never inferred by the client.

  async listWorkspaceRuns(
    workspace: string,
    params: WorkspaceRunListParams = {},
  ): Promise<WorkspaceRunListResult> {
    return this.request("GET", "/api/task-runs", {
      workspace,
      query: {
        status: params.status,
        agent_id: params.agent_id,
        project_id: params.project_id,
        issue: params.issue,
        trigger: params.trigger,
        created_after: params.created_after,
        created_before: params.created_before,
        limit: params.limit,
        offset: params.offset,
      },
    });
  }

  async getAgent(workspace: string, agentId: string): Promise<AgentDetailInfo> {
    return this.request("GET", `/api/agents/${encodeURIComponent(agentId)}`, { workspace });
  }

  async createAgent(workspace: string, body: CreateAgentBody): Promise<AgentDetailInfo> {
    return this.request("POST", "/api/agents", { workspace, body });
  }

  async updateAgent(
    workspace: string,
    agentId: string,
    body: UpdateAgentBody,
  ): Promise<AgentDetailInfo> {
    return this.request(
      "PUT",
      `/api/agents/${encodeURIComponent(agentId)}`,
      { workspace, body },
    );
  }

  async archiveAgent(workspace: string, agentId: string): Promise<AgentDetailInfo> {
    return this.request(
      "POST",
      `/api/agents/${encodeURIComponent(agentId)}/archive`,
      { workspace, body: {} },
    );
  }

  async restoreAgent(workspace: string, agentId: string): Promise<AgentDetailInfo> {
    return this.request(
      "POST",
      `/api/agents/${encodeURIComponent(agentId)}/restore`,
      { workspace, body: {} },
    );
  }

  async getSquad(workspace: string, squadId: string): Promise<SquadInfo> {
    return this.request("GET", `/api/squads/${encodeURIComponent(squadId)}`, { workspace });
  }

  async createSquad(workspace: string, body: CreateSquadBody): Promise<SquadInfo> {
    return this.request("POST", "/api/squads", { workspace, body });
  }

  async updateSquad(
    workspace: string,
    squadId: string,
    body: UpdateSquadBody,
  ): Promise<SquadInfo> {
    return this.request(
      "PUT",
      `/api/squads/${encodeURIComponent(squadId)}`,
      { workspace, body },
    );
  }

  async archiveSquad(workspace: string, squadId: string): Promise<void> {
    // The handler answers 204 with an empty body; request() resolves undefined.
    await this.request("DELETE", `/api/squads/${encodeURIComponent(squadId)}`, { workspace });
  }

  // Workspace quick replies (RUYI-435). Same REST surface the web settings
  // tab drives, so the two management views always see one data source; the
  // backend answers 403 to non-admin PATs on the writes.
  async listQuickReplies(
    workspace: string,
  ): Promise<{ quick_replies: QuickReplyInfo[]; total: number }> {
    return this.request("GET", "/api/quick-replies", { workspace });
  }

  async createQuickReply(
    workspace: string,
    body: { name: string; content: string },
  ): Promise<QuickReplyInfo> {
    return this.request("POST", "/api/quick-replies", { workspace, body });
  }

  async updateQuickReply(
    workspace: string,
    id: string,
    body: { name?: string; content?: string },
  ): Promise<QuickReplyInfo> {
    return this.request("PATCH", `/api/quick-replies/${encodeURIComponent(id)}`, {
      workspace,
      body,
    });
  }

  async deleteQuickReply(workspace: string, id: string): Promise<void> {
    // The handler answers 204 with an empty body; request() resolves undefined.
    await this.request("DELETE", `/api/quick-replies/${encodeURIComponent(id)}`, { workspace });
  }

  // ---- transport --------------------------------------------------------

  private workspaceHeaders(workspace: string): Record<string, string> {
    return isUuid(workspace)
      ? { "X-Workspace-ID": workspace }
      : { "X-Workspace-Slug": workspace };
  }

  private async request<T>(
    method: string,
    path: string,
    options: RequestOptions = {},
  ): Promise<T> {
    const url = new URL(`${this.baseUrl}${path}`);
    for (const [key, value] of Object.entries(options.query ?? {})) {
      if (value !== undefined) {
        url.searchParams.set(key, String(value));
      }
    }

    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.token}`,
      Accept: "application/json",
    };
    if (options.workspace !== undefined) {
      Object.assign(headers, this.workspaceHeaders(options.workspace));
    }
    let requestBody: string | undefined;
    if (options.body !== undefined) {
      headers["Content-Type"] = "application/json";
      requestBody = JSON.stringify(options.body);
    }

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);
    const startedAt = Date.now();
    let response: Response;
    try {
      response = await this.fetchImpl(url, {
        method,
        headers,
        body: requestBody,
        signal: controller.signal,
      });
    } catch (error) {
      const reason = error instanceof Error && error.name === "AbortError"
        ? `timed out after ${this.timeoutMs}ms`
        : `network error: ${error instanceof Error ? error.message : String(error)}`;
      this.logger.info(`api ${method} ${path} -> failed (${reason})`);
      throw new MulticaRequestError(`Multica API request ${method} ${path} ${reason}`);
    } finally {
      clearTimeout(timer);
    }

    const durationMs = Date.now() - startedAt;
    // Path template only — the query string can carry user content (search).
    this.logger.info(`api ${method} ${path} -> ${response.status} ${durationMs}ms`);

    const rawBody = await response.text();
    if (!response.ok) {
      throw new MulticaApiError(
        response.status,
        describeErrorBody(rawBody, response.status),
        parseErrorObject(rawBody),
      );
    }
    if (rawBody.length === 0) {
      return undefined as T;
    }
    try {
      return JSON.parse(rawBody) as T;
    } catch {
      throw new MulticaRequestError(
        `Multica API ${method} ${path} returned non-JSON body (status ${response.status})`,
      );
    }
  }
}

function describeErrorBody(rawBody: string, status: number): string {
  try {
    const parsed: unknown = JSON.parse(rawBody);
    if (typeof parsed === "object" && parsed !== null) {
      const record = parsed as Record<string, unknown>;
      const message = record.error ?? record.message;
      const code = record.code;
      const parts = [typeof code === "string" ? code : undefined, typeof message === "string" ? message : undefined]
        .filter((part): part is string => part !== undefined);
      if (parts.length > 0) {
        return parts.join(": ");
      }
    }
  } catch {
    // Fall through to the generic description.
  }
  return `HTTP ${status}`;
}

/** Keeps the parsed JSON object body on MulticaApiError.body for structured outcomes. */
function parseErrorObject(rawBody: string): Record<string, unknown> | undefined {
  try {
    const parsed: unknown = JSON.parse(rawBody);
    if (typeof parsed === "object" && parsed !== null && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
  } catch {
    // Non-JSON error bodies carry no structured payload.
  }
  return undefined;
}
