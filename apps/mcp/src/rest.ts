/**
 * Thin typed REST client for the Multica backend API subset.
 *
 * Ground rules:
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
  ClauseGovernanceDetail,
  ClauseGovernanceOverview,
  ClauseGovernanceSearchResult,
  ClauseReconciliation,
  CommentInfo,
  CommentListParams,
  CreateAgentBody,
  CreateCommentBody,
  CreateExecutionProfileBody,
  CreateIssueBody,
  CreateProjectBody,
  CreateProjectResourceBody,
  CreateSquadBody,
  DecisionBatchAnswerBody,
  DecisionBatchResult,
  DecisionCardInfo,
  DecisionInboxItemInfo,
  DecisionInboxResult,
  DirectClauseRevisionBody,
  DirectClauseRevisionPreview,
  DirectClauseRevisionResult,
  ExecutionProfileActivationInfo,
  ExecutionProfileEntryInfo,
  ExecutionProfileInfo,
  ExecutionProfileListResponse,
  IssueInfo,
  IssueListParams,
  IssueListResult,
  IssueRelationType,
  IssueRelationsInfo,
  InboxItemInfo,
  InboxWorkspaceUnreadInfo,
  ModelListRequestInfo,
  ProjectInfo,
  ProjectResourceInfo,
  ProjectResourceListResult,
  CreatePromptProposalBody,
  PromptProposalInfo,
  PromptProposalPreview,
  PromptVersionListResult,
  UpdatePromptProposalBody,
  QuickCreateBody,
  QuickReplyInfo,
  RemoveIssueRelationResult,
  RunDetail,
  RunActivity,
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
  UpdateProjectResourceBody,
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

/** The wire renders options as [{label}] objects; the typed surface uses
 * plain label arrays with option letter A = index 0. A hand-edited row that
 * fails to decode projects as an empty card rather than a crash — the same
 * grace the server's own renderer applies. */
function normalizeDecisionOptions(raw: unknown): string[] {
  if (!Array.isArray(raw)) return [];
  return raw
    .map((entry) =>
      typeof entry === "object" && entry !== null && typeof (entry as { label?: unknown }).label === "string"
        ? ((entry as { label: string }).label)
        : undefined,
    )
    .filter((label): label is string => label !== undefined);
}

function normalizeDecisionCard(raw: unknown): DecisionCardInfo {
  const record = raw as Record<string, unknown> & { options?: unknown };
  const { options, ...rest } = record;
  return { ...rest, options: normalizeDecisionOptions(options) } as DecisionCardInfo;
}

export interface MulticaClientOptions {
  serverUrl: string;
  token: string;
  fetchImpl?: typeof fetch;
  timeoutMs?: number;
  logger?: Logger;
  auditServiceCredential?: string | undefined;
  mcpInstanceId?: string | undefined;
  fileAccessInstanceCredential?: string | undefined;
}

interface RequestOptions {
  workspace?: string | undefined;
  query?: Record<string, string | number | boolean | undefined> | undefined;
  body?: unknown;
  headers?: Record<string, string> | undefined;
  auditService?: boolean | undefined;
  fileAccessInstance?: boolean | undefined;
}

export class MulticaClient {
  private readonly baseUrl: string;
  private readonly token: string;
  private readonly fetchImpl: typeof fetch;
  private readonly timeoutMs: number;
  private readonly logger: Logger;
  private readonly auditServiceCredential: string | undefined;
  private readonly mcpInstanceId: string | undefined;
  private readonly fileAccessInstanceCredential: string | undefined;

  constructor(options: MulticaClientOptions) {
    this.baseUrl = options.serverUrl.replace(/\/+$/, "");
    this.token = options.token;
    this.fetchImpl = options.fetchImpl ?? fetch;
    this.timeoutMs = options.timeoutMs ?? 30_000;
    this.logger = options.logger ?? stderrLogger;
    this.auditServiceCredential = options.auditServiceCredential?.trim() || undefined;
    this.mcpInstanceId = options.mcpInstanceId?.trim() || undefined;
    this.fileAccessInstanceCredential = options.fileAccessInstanceCredential?.trim() || undefined;
  }

  get mcpInstanceIdentity(): string | undefined {
    return this.mcpInstanceId;
  }

  // ---- workspace-scoped operations -------------------------------------

  async listWorkspaces(): Promise<WorkspaceInfo[]> {
    return this.request<WorkspaceInfo[]>("GET", "/api/workspaces");
  }

  async resolveWorkspaceId(workspace: string): Promise<string> {
    return isUuid(workspace) ? workspace : this.resolveWorkspaceIdBySlug(workspace);
  }

  async listInbox(workspace: string): Promise<InboxItemInfo[]> {
    return this.request("GET", "/api/inbox", { workspace });
  }

  async listArchivedInbox(workspace: string): Promise<InboxItemInfo[]> {
    return this.request("GET", "/api/inbox/archived", { workspace });
  }

  async countUnreadInbox(workspace: string): Promise<{ count: number }> {
    return this.request("GET", "/api/inbox/unread-count", { workspace });
  }

  async unreadInboxSummary(workspace: string): Promise<InboxWorkspaceUnreadInfo[]> {
    return this.request("GET", "/api/inbox/unread-summary", { workspace });
  }

  async markAllInboxRead(workspace: string): Promise<{ count: number }> {
    return this.request("POST", "/api/inbox/mark-all-read", { workspace, body: {} });
  }

  async archiveAllInbox(workspace: string): Promise<{ count: number }> {
    return this.request("POST", "/api/inbox/archive-all", { workspace, body: {} });
  }

  async archiveAllReadInbox(workspace: string): Promise<{ count: number }> {
    return this.request("POST", "/api/inbox/archive-all-read", { workspace, body: {} });
  }

  async archiveCompletedInbox(workspace: string): Promise<{ count: number }> {
    return this.request("POST", "/api/inbox/archive-completed", { workspace, body: {} });
  }

  async markInboxItemRead(workspace: string, id: string): Promise<InboxItemInfo> {
    return this.request("POST", `/api/inbox/${encodeURIComponent(id)}/read`, { workspace, body: {} });
  }

  async markInboxItemUnread(workspace: string, id: string): Promise<InboxItemInfo> {
    return this.request("POST", `/api/inbox/${encodeURIComponent(id)}/unread`, { workspace, body: {} });
  }

  async archiveInboxItem(workspace: string, id: string): Promise<InboxItemInfo> {
    return this.request("POST", `/api/inbox/${encodeURIComponent(id)}/archive`, { workspace, body: {} });
  }

  async unarchiveInboxItem(workspace: string, id: string): Promise<InboxItemInfo> {
    return this.request("POST", `/api/inbox/${encodeURIComponent(id)}/unarchive`, { workspace, body: {} });
  }

  async startMcpInvocationAudit(input: {
    invocation_id: string;
    workspace_id: string;
    tool_name: string;
    reported_client_name?: string;
    reported_client_version?: string;
    target_type?: "workspace";
    target_id?: string;
    instance_id?: string;
  }): Promise<void> {
    await this.request("POST", "/api/mcp/audit-start", {
      body: input,
      auditService: true,
      fileAccessInstance: input.instance_id !== undefined,
    });
  }

  async finishMcpInvocationAudit(input: {
    invocation_id: string;
    status: "success" | "failure" | "refused";
    duration_ms: number;
    operation_id?: string;
    instance_id?: string;
    error_code?: "invalid_input" | "not_found" | "permission_denied" | "unavailable" | "timeout" | "internal";
  }): Promise<"recorded" | "queued" | "unrecorded"> {
    const { instance_id: instanceId, ...body } = input;
    const response = await this.request<{ status: "recorded" | "queued" | "unrecorded" }>("POST", "/api/mcp/audit-finish", {
      body,
      auditService: true,
      fileAccessInstance: instanceId !== undefined,
    });
    return response.status;
  }

  async recordMcpPreAuthRejection(input: {
    invocation_id: string;
    reason_code: "unknown_tool" | "missing_workspace" | "invalid_workspace" | "unauthenticated_actor" | "entry_rejected";
    file_tool_name?: "list_files" | "get_file_metadata" | "read_file_chunk";
  }): Promise<void> {
    await this.request("POST", "/api/mcp/pre-auth-rejections", {
      body: input,
      auditService: true,
      fileAccessInstance: true,
    });
  }

  async runFileAccessTool(input: {
    tool: "list_files" | "get_file_metadata" | "read_file_chunk";
    workspace: string;
    invocation_id: string;
    path: string;
    host?: string;
    offset?: number;
    length?: number;
    limit?: number;
    cursor?: string;
  }): Promise<MCPFileAccessResult> {
    const route = {
      list_files: "/api/file-access/list",
      get_file_metadata: "/api/file-access/metadata",
      read_file_chunk: "/api/file-access/read",
    }[input.tool];
    const { operation_id: operationId } = await this.request<{ operation_id: string; status: string }>("POST", route, {
      workspace: input.workspace,
      body: {
        invocation_id: input.invocation_id,
        ...(input.host !== undefined ? { host: input.host } : {}),
        path: input.path,
        ...(input.offset !== undefined ? { offset: input.offset } : {}),
        ...(input.length !== undefined ? { length: input.length } : {}),
        ...(input.limit !== undefined ? { limit: input.limit } : {}),
        ...(input.cursor !== undefined ? { cursor: input.cursor } : {}),
      },
      fileAccessInstance: true,
    });
    const deadline = Date.now() + 110_000;
    while (Date.now() < deadline) {
      const operation = await this.request<{
        operation_id: string;
        kind: string;
        status: "pending" | "claimed" | "succeeded" | "rejected" | "failed" | "timeout" | "expired";
        result: unknown;
        error: string;
      }>("GET", `/api/file-access/operations/${encodeURIComponent(operationId)}`, {
        workspace: input.workspace,
        query: { invocation_id: input.invocation_id },
        fileAccessInstance: true,
      });
      if (operation.status === "succeeded") return new MCPFileAccessResult(operation.result, operationId);
      if (operation.status === "failed" || operation.status === "rejected" || operation.status === "expired" || operation.status === "timeout") {
        throw new MCPFileAccessError(normalizeFileAccessErrorCode(operation.error || "internal"), operation.status, operationId);
      }
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    throw new MCPFileAccessError("timeout", "timeout", operationId);
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

  async getAutopilot(workspace: string, autopilotId: string): Promise<Record<string, unknown>> {
    return this.request("GET", `/api/autopilots/${encodeURIComponent(autopilotId)}`, { workspace });
  }

  async listAutopilotRuns(
    workspace: string,
    autopilotId: string,
    params: { limit?: number; offset?: number; status?: string; source?: string; triggered_after?: string; triggered_before?: string } = {},
  ): Promise<Record<string, unknown>> {
    return this.request("GET", `/api/autopilots/${encodeURIComponent(autopilotId)}/runs`, {
      workspace,
      query: params,
    });
  }

  async getAutopilotRun(workspace: string, autopilotId: string, runId: string): Promise<Record<string, unknown>> {
    return this.request("GET", `/api/autopilots/${encodeURIComponent(autopilotId)}/runs/${encodeURIComponent(runId)}`, { workspace });
  }

  async triggerAutopilot(workspace: string, autopilotId: string, body: Record<string, unknown>, idempotencyKey: string): Promise<Record<string, unknown>> {
    return this.request("POST", `/api/autopilots/${encodeURIComponent(autopilotId)}/trigger`, { workspace, body, headers: { "Idempotency-Key": idempotencyKey } });
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

  // Project resource bindings. Same surface the web project page
  // drives; create/update/delete answer 409/404/422 bodies the tool layer
  // maps to structured results.

  async listProjectResources(
    workspace: string,
    projectId: string,
  ): Promise<ProjectResourceListResult> {
    return this.request(
      "GET",
      `/api/projects/${encodeURIComponent(projectId)}/resources`,
      { workspace },
    );
  }

  async createProjectResource(
    workspace: string,
    projectId: string,
    body: CreateProjectResourceBody,
  ): Promise<ProjectResourceInfo> {
    return this.request<ProjectResourceInfo>(
      "POST",
      `/api/projects/${encodeURIComponent(projectId)}/resources`,
      { workspace, body },
    );
  }

  async updateProjectResource(
    workspace: string,
    projectId: string,
    resourceId: string,
    body: UpdateProjectResourceBody,
  ): Promise<ProjectResourceInfo> {
    return this.request<ProjectResourceInfo>(
      "PUT",
      `/api/projects/${encodeURIComponent(projectId)}/resources/${encodeURIComponent(resourceId)}`,
      { workspace, body },
    );
  }

  /** 204 on success — resolves undefined. */
  async deleteProjectResource(
    workspace: string,
    projectId: string,
    resourceId: string,
  ): Promise<void> {
    // The handler answers 204 with an empty body; request() resolves undefined.
    await this.request(
      "DELETE",
      `/api/projects/${encodeURIComponent(projectId)}/resources/${encodeURIComponent(resourceId)}`,
      { workspace },
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

  // ---- structured issue relations -----------------------------
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

  // ---- run lifecycle ------------------------------------------

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

  async getIssueRunActivity(
    workspace: string,
    issueId: string,
    runId: string,
    params: {
      after_seq?: number;
      semantic_limit?: number;
      stream?: "stdout" | "stderr" | "both";
      tail_bytes?: number;
      worker_cursor?: string;
    } = {},
  ): Promise<RunActivity> {
    return this.request(
      "GET",
      `/api/issues/${encodeURIComponent(issueId)}/tasks/${encodeURIComponent(runId)}/activity`,
      { workspace, query: params },
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

  // ---- execution-config management ----------------------------
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
    // The list endpoint answers enveloped (unlike the single-profile reads);
    // unwrap so callers always see the array.
    const page = await this.request<ExecutionProfileListResponse>(
      "GET",
      `/api/workspaces/${encodeURIComponent(workspaceId)}/execution-profiles`,
    );
    return page.execution_profiles;
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

  // The workspace audit search: the same endpoint the web app
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

  // ---- decision cards (RUYI-688) ----------------------------------
  // The same decision-card surface the mobile decision center reads and
  // writes, one source of truth: the inbox lists every card in the
  // workspace, the issue-scoped list feeds detail reads and answer
  // numbering, and the batch endpoint is the only write. Like the audit
  // search, the inbox keys the workspace by UUID in its path, so a slug
  // input resolves via resolveWorkspaceIdBySlug first. The wire carries
  // options as [{label}] objects; these methods normalize to label arrays
  // so every consumer below speaks one shape.

  async listDecisionInbox(
    workspace: string,
    params: { status?: string; limit?: number } = {},
  ): Promise<DecisionInboxResult> {
    const workspaceId = isUuid(workspace)
      ? workspace
      : await this.resolveWorkspaceIdBySlug(workspace);
    const raw = await this.request<{
      items: Array<Record<string, unknown>>;
      counts: { open: number; answered: number; cancelled: number };
    }>(
      "GET",
      `/api/workspaces/${encodeURIComponent(workspaceId)}/decision-inbox`,
      {
        workspace,
        query: { status: params.status, limit: params.limit },
      },
    );
    return {
      items: raw.items.map((item) => {
        const { options, ...rest } = item as Record<string, unknown> & {
          options?: unknown;
        };
        return {
          ...rest,
          options: normalizeDecisionOptions(options),
        } as DecisionInboxItemInfo;
      }),
      counts: raw.counts,
    };
  }

  async listIssueDecisions(workspace: string, issueId: string): Promise<DecisionCardInfo[]> {
    const raw = await this.request<Array<Record<string, unknown>>>(
      "GET",
      `/api/issues/${encodeURIComponent(issueId)}/decisions`,
      { workspace },
    );
    return raw.map(normalizeDecisionCard);
  }

  async answerDecisionsBatch(
    workspace: string,
    issueId: string,
    answers: DecisionBatchAnswerBody[],
  ): Promise<DecisionBatchResult> {
    const raw = await this.request<Record<string, unknown>>(
      "POST",
      `/api/issues/${encodeURIComponent(issueId)}/decisions/answer-batch`,
      { workspace, body: { answers } },
    );
    const results = Array.isArray(raw.results) ? raw.results : [];
    return {
      ...raw,
      results: results.map((outcome) => {
        const record = outcome as Record<string, unknown>;
        const decision = record.decision as Record<string, unknown> | undefined;
        return {
          ...record,
          decision:
            decision === undefined || decision === null
              ? undefined
              : normalizeDecisionCard(decision),
        };
      }),
    } as DecisionBatchResult;
  }

  // ---- workspace management surface ----------------------------
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

  // Workspace quick replies. Same REST surface the web settings
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

  // ---- clause governance & prompt legislation (RUYI-868) ----------------
  //
  // The MCP read/write surface over the clause asset library and the
  // proposal pool. Every path below is pinned in the backend's OAuth scope
  // table (server/internal/oauth/scope_request.go) so an OAuth token can
  // only reach the tiers these methods declare. Proposal decide-and-execute
  // endpoints never appear here; the direct-revision pair is the narrow Owner
  // exception, protected by preview-bound version and content hashes.

  /** GET /api/prompt-legislation/governance/clauses?days=N */
  async listClauseGovernance(workspace: string, params?: { days?: number }): Promise<ClauseGovernanceOverview> {
    return this.request("GET", "/api/prompt-legislation/governance/clauses", {
      workspace,
      query: params?.days ? { days: params.days } : undefined,
    });
  }

  /** GET /api/prompt-legislation/governance/clauses/search?q=&status= */
  async searchClauseGovernance(
    workspace: string,
    params: { q?: string; status?: string },
  ): Promise<ClauseGovernanceSearchResult> {
    const query: Record<string, string | number | boolean | undefined> = {};
    if (params.q !== undefined) query["q"] = params.q;
    if (params.status !== undefined) query["status"] = params.status;
    return this.request("GET", "/api/prompt-legislation/governance/clauses/search", {
      workspace,
      query,
    });
  }

  /** GET /api/prompt-legislation/governance/clauses/{clauseId}?days=N */
  async getClauseGovernanceDetail(
    workspace: string,
    clauseId: string,
    params?: { days?: number },
  ): Promise<ClauseGovernanceDetail> {
    return this.request("GET", `/api/prompt-legislation/governance/clauses/${encodeURIComponent(clauseId)}`, {
      workspace,
      query: params?.days ? { days: params.days } : undefined,
    });
  }

  /**
   * POST /api/prompt-legislation/governance/clauses/{clauseId}/direct-revision-preview
   *
   * Owner-only, read-only simulation of a direct Clause revision. The caller
   * must use the current Clause and carrier tokens it read before this call.
   */
  async previewDirectClauseRevision(
    workspace: string,
    clauseId: string,
    body: DirectClauseRevisionBody,
  ): Promise<DirectClauseRevisionPreview> {
    return this.request(
      "POST",
      `/api/prompt-legislation/governance/clauses/${encodeURIComponent(clauseId)}/direct-revision-preview`,
      { workspace, body },
    );
  }

  /**
   * POST /api/prompt-legislation/governance/clauses/{clauseId}/direct-revision
   *
   * Owner-only confirmed write. The backend creates a new Clause version,
   * audits the binding and re-renders its carrier; it never creates a proposal.
   */
  async directReviseClause(
    workspace: string,
    clauseId: string,
    body: DirectClauseRevisionBody,
  ): Promise<DirectClauseRevisionResult> {
    return this.request(
      "POST",
      `/api/prompt-legislation/governance/clauses/${encodeURIComponent(clauseId)}/direct-revision`,
      { workspace, body },
    );
  }

  /** GET /api/prompt-legislation/governance/reconciliation?days=N */
  async getClauseReconciliation(workspace: string, params?: { days?: number }): Promise<ClauseReconciliation> {
    return this.request("GET", "/api/prompt-legislation/governance/reconciliation", {
      workspace,
      query: params?.days ? { days: params.days } : undefined,
    });
  }

  /**
   * GET /api/prompt-governance/{scope}/{scopeId}/versions
   *
   * The version ledger a clause-versus-prompt comparison reads the current
   * carrier text from. scope is one of the four prompt tiers.
   */
  async listPromptGovernanceVersions(
    workspace: string,
    scope: "workspace" | "project" | "squad" | "agent",
    scopeId: string,
  ): Promise<PromptVersionListResult> {
    return this.request("GET", `/api/prompt-governance/${scope}/${encodeURIComponent(scopeId)}/versions`, {
      workspace,
    });
  }

  /** GET /api/prompt-legislation/proposals?status=&run_id= */
  async listPromptProposals(
    workspace: string,
    params?: { status?: string; run_id?: string },
  ): Promise<PromptProposalInfo[]> {
    return this.request("GET", "/api/prompt-legislation/proposals", {
      workspace,
      query: params
        ? { status: params.status, run_id: params.run_id }
        : undefined,
    });
  }

  /**
   * POST /api/prompt-legislation/proposals
   *
   * The member-side entry into the proposal pool: a draft that an owner
   * decides on. Nothing about a created draft changes any prompt or carrier
   * until the owner approves; the audit log records the whole chain.
   */
  async createPromptProposal(
    workspace: string,
    body: CreatePromptProposalBody,
  ): Promise<PromptProposalInfo> {
    return this.request("POST", "/api/prompt-legislation/proposals", { workspace, body });
  }

  /**
   * PATCH /api/prompt-legislation/proposals/{id}
   *
   * Draft edit: overwrite the draft's fields. The backend answers 409 when
   * the proposal has left draft.
   */
  async updatePromptProposal(
    workspace: string,
    id: string,
    body: UpdatePromptProposalBody,
  ): Promise<PromptProposalInfo> {
    return this.request("PATCH", `/api/prompt-legislation/proposals/${encodeURIComponent(id)}`, {
      workspace,
      body,
    });
  }

  /**
   * POST /api/prompt-legislation/proposals/{id}/submit
   *
   * Move a draft to pending_owner for the owner's decision. Answers 409
   * when the proposal is no longer a draft.
   */
  async submitPromptProposal(workspace: string, id: string): Promise<PromptProposalInfo> {
    return this.request(
      "POST",
      `/api/prompt-legislation/proposals/${encodeURIComponent(id)}/submit`,
      { workspace },
    );
  }

  /**
   * POST /api/prompt-legislation/proposals/{id}/preview
   *
   * Read-only against the carrier: synthesize the sandbox text and return
   * the full-carrier line diff the owner's approval decision is based on.
   */
  async previewPromptProposal(workspace: string, id: string): Promise<PromptProposalPreview> {
    return this.request(
      "POST",
      `/api/prompt-legislation/proposals/${encodeURIComponent(id)}/preview`,
      { workspace },
    );
  }

  /**
   * POST /api/prompt-legislation/proposals/{id}/rework
   *
   * Send a gate_failed proposal back to draft. Answers 409 when the
   * proposal is not in gate_failed.
   */
  async reworkPromptProposal(workspace: string, id: string): Promise<PromptProposalInfo> {
    return this.request(
      "POST",
      `/api/prompt-legislation/proposals/${encodeURIComponent(id)}/rework`,
      { workspace },
    );
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
    Object.assign(headers, options.headers ?? {});
    if (options.auditService) {
      if (!this.auditServiceCredential) {
        throw new MulticaRequestError("MCP audit service credential is not configured");
      }
      headers["X-Multica-MCP-Service-Credential"] = this.auditServiceCredential;
    }
    if (options.fileAccessInstance) {
      if (!this.mcpInstanceId || !this.fileAccessInstanceCredential) {
        throw new MulticaRequestError("MCP file access instance credential is not configured");
      }
      headers["X-Multica-MCP-Instance-ID"] = this.mcpInstanceId;
      headers["X-Multica-MCP-Instance-Credential"] = this.fileAccessInstanceCredential;
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

export class MCPFileAccessError extends Error {
  readonly code: MCPFileAccessErrorCode;
  readonly status: MCPFileAccessStatus;
  readonly operationId: string | undefined;

  constructor(code: MCPFileAccessErrorCode, status: MCPFileAccessStatus, operationId?: string) {
    super(`MCP file access ${status}: ${code}`);
    this.name = "MCPFileAccessError";
    this.code = code;
    this.status = status;
    this.operationId = operationId;
  }
}

type MCPFileAccessErrorCode = "invalid_input" | "not_found" | "permission_denied" | "unavailable" | "timeout" | "internal";
type MCPFileAccessStatus = "rejected" | "failed" | "expired" | "timeout";

function normalizeFileAccessErrorCode(code: string): MCPFileAccessErrorCode {
  switch (code) {
    case "invalid_input":
    case "not_found":
    case "permission_denied":
    case "unavailable":
    case "timeout":
    case "internal":
      return code;
    default:
      return "internal";
  }
}

export class MCPFileAccessResult {
  constructor(readonly result: unknown, readonly operationId: string) {}
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
