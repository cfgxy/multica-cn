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
  AgentInfo,
  CommentInfo,
  CommentListParams,
  CreateCommentBody,
  CreateIssueBody,
  IssueInfo,
  IssueListParams,
  IssueListResult,
  ProjectInfo,
  QuickCreateBody,
  SearchIssueInfo,
  UpdateIssueBody,
  WorkspaceInfo,
} from "./types.js";

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isUuid(value: string): boolean {
  return UUID_PATTERN.test(value);
}

export class MulticaApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(`Multica API ${status}: ${message}`);
    this.name = "MulticaApiError";
    this.status = status;
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
      throw new MulticaApiError(response.status, describeErrorBody(rawBody, response.status));
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
