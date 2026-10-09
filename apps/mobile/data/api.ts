/**
 * Mobile-owned fetch wrapper. Mirrors the surface area of
 * packages/core/api/client.ts that mobile actually uses, but lives in
 * apps/mobile/ so we control retry/timeout/error handling independently.
 *
 * Types are imported via `import type` from @multica/core/types — zero
 * runtime coupling. Zod schemas + fallbacks are imported from
 * @multica/core/api/schemas (pure data, on the mobile sharing whitelist).
 *
 * Design checklist (apps/mobile/CLAUDE.md "Lessons → ApiClient capability list"):
 *   1. Zod parseWithFallback for endpoints with schemas (drift defense)
 *   2. onUnauthorized callback on 401 (auto sign-out, avoids retry loops)
 *   3. X-Request-ID per request + structured logger (debug + tracing)
 *   4. Bearer auth + X-Workspace-Slug — NOT cookie auth (no CSRF, no credentials)
 */
import i18n from "i18next";
import type {
  Agent,
  AgentEnvResponse,
  AgentTask,
  AgentWebhook,
  Attachment,
  ChatMessage,
  ChatPendingTask,
  ChatSession,
  Comment,
  CreateAgentRequest,
  CreateAgentWebhookRequest,
  AddSquadMemberRequest,
  CreateIssueRequest,
  CreateLabelRequest,
  CreateProjectRequest,
  CreateSquadRequest,
  CreateProjectResourceRequest,
  CreateRuntimeProfileRequest,
  ExecutionProfile,
  ExecutionProfileActivationResponse,
  ExecutionProfileEntry,
  ExecutionProfileListResponse,
  CreateExecutionProfileRequest,
  UpdateExecutionProfileRequest,
  UpsertExecutionProfileEntryRequest,
  RuntimeModelListRequest,
  RuntimeLocalSkillListRequest,
  RuntimeLocalSkillImportRequest,
  CreateRuntimeLocalSkillImportRequest,
  SetAgentRuntimeSkillEnabledRequest,
  SkillCatalogEntry,
  WorkspaceMcpServer,
  GitHubPullRequest,
  InboxItem,
  InboxWorkspaceUnread,
  Issue,
  IssueDecision,
  WorkspaceDecisionInbox,
  IssueLabelsResponse,
  IssuePriority,
  Label,
  IssueReaction,
  ListIssuesParams,
  ListIssuesResponse,
  ListLabelsResponse,
  ListProjectResourcesResponse,
  ListProjectsResponse,
  MemberWithUser,
  PinnedItem,
  PinnedItemType,
  Project,
  ProjectResource,
  Reaction,
  ReorderPinsRequest,
  RemoveSquadMemberRequest,
  RuntimeDevice,
  RuntimeProfile,
  SearchIssuesResponse,
  SearchProjectsResponse,
  ListIssueStatusesResponse,
  ListQuickRepliesResponse,
  SendChatMessageResponse,
  Squad,
  SquadMember,
  SquadMemberStatusListResponse,
  SetAgentSkillsRequest,
  SkillSummary,
  UpdateSquadMemberRoleRequest,
  UpdateSquadRequest,
  NotificationPreferenceResponse,
  NotificationPreferences,
  BatchDecisionAnswerResult,
  BatchIssueDecisionAnswer,
  TaskMessagePayload,
  UpdateAgentEnvRequest,
  UpdateAgentRequest,
  UpdateAgentWebhookRequest,
  UpdateIssueRequest,
  UpdateMeRequest,
  UpdateProjectRequest,
  User,
  Workspace,
  WorkspaceSubscriptionSummary,
} from "@multica/core/types";
import {
  parseTimelineTruncatedKinds,
  type TimelineQueryData,
} from "@multica/core/issues/timeline-query";
import {
  AgentEnvResponseSchema,
  AgentWebhookListSchema,
  AgentWebhookSchema,
  AppConfigSchema,
  AttachmentResponseSchema,
  EMPTY_AGENT_ENV,
  EMPTY_AGENT_WEBHOOK,
  EMPTY_AGENT_WEBHOOK_LIST,
  EMPTY_APP_CONFIG,
  EMPTY_ATTACHMENT,
  EMPTY_LIST_QUICK_REPLIES_RESPONSE,
  EMPTY_ISSUE_PULL_REQUESTS_RESPONSE,
  EMPTY_LIST_ISSUE_STATUSES_RESPONSE,
  EMPTY_LIST_ISSUES_RESPONSE,
  EMPTY_SQUAD,
  EMPTY_SQUAD_MEMBER,
  EMPTY_SQUAD_MEMBER_LIST,
  EMPTY_SQUAD_MEMBER_STATUS_LIST,
  EMPTY_TIMELINE_ENTRIES,
  IssuePullRequestsResponseSchema,
  IssueSchema,
  ListIssuesResponseSchema,
  ListIssueStatusesResponseSchema,
  ListQuickRepliesResponseSchema,
  SquadMemberListSchema,
  SquadMemberSchema,
  SquadMemberStatusListResponseSchema,
  SquadSchema,
  TimelineEntriesSchema,
  SkillSummaryListSchema,
  WorkspaceSubscriptionSummarySchema,
  ExecutionProfileEntrySchema,
  ExecutionProfileListResponseSchema,
  ExecutionProfileActivationResponseSchema,
  ExecutionProfileSchema,
  EMPTY_EXECUTION_PROFILE,
  EMPTY_EXECUTION_PROFILE_ACTIVATION,
  EMPTY_EXECUTION_PROFILE_LIST,
  MALFORMED_RUNTIME_MODEL_LIST_REQUEST,
  RuntimeModelListRequestSchema,
  WorkspaceMcpServerListSchema,
} from "@multica/core/api/schemas";
import type { AppConfigResponse } from "@multica/core/api/schemas";
import {
  BatchDecisionAnswersSchema,
  IssueDecisionSchema,
  IssueDecisionsListSchema,
  WorkspaceDecisionInboxSchema,
} from "@multica/core/api/schemas";
import {
  ActiveTasksResponseSchema,
  AgentListSchema,
  AgentSchema,
  AgentTaskListSchema,
  AttachmentListSchema,
  AttachmentSchema,
  ChatMessageListSchema,
  CommentSchema,
  ChatPendingTaskSchema,
  ChatSessionListSchema,
  ChatSessionSchema,
  EMPTY_ACTIVE_TASKS_RESPONSE,
  EMPTY_AGENT_FALLBACK,
  EMPTY_AGENT_LIST,
  EMPTY_AGENT_TASK_LIST,
  EMPTY_ATTACHMENT_LIST,
  EMPTY_CHAT_MESSAGE_LIST,
  EMPTY_CHAT_PENDING_TASK,
  EMPTY_CHAT_SESSION_LIST,
  EMPTY_COMPOSIO_CONNECTIONS,
  EMPTY_COMMENT,
  EMPTY_INBOX_LIST,
  EMPTY_INBOX_UNREAD_SUMMARY,
  EMPTY_INTEGRATION_INSTALLATIONS,
  EMPTY_ISSUE_FALLBACK,
  EMPTY_LIST_LABELS_RESPONSE,
  EMPTY_LIST_PROJECT_RESOURCES_RESPONSE,
  EMPTY_LIST_PROJECTS_RESPONSE,
  EMPTY_MEMBER_LIST,
  EMPTY_NOTIFICATION_PREFERENCES,
  EMPTY_PIN_LIST,
  EMPTY_PROJECT,
  EMPTY_RUNTIME,
  EMPTY_RUNTIME_LIST,
  EMPTY_RUNTIME_PROFILE_LIST_RESPONSE,
  EMPTY_SEARCH_ISSUES_RESPONSE,
  EMPTY_SEARCH_PROJECTS_RESPONSE,
  EMPTY_SQUAD_LIST,
  EMPTY_USER,
  EMPTY_WORKSPACE_LIST,
  AgentCancelTasksResponseSchema,
  EMPTY_AGENT_CANCEL_TASKS_RESPONSE,
  InboxListSchema,
  InboxUnreadSummarySchema,
  ComposioConnectionsSchema,
  IntegrationInstallationsSchema,
  NotificationPreferenceResponseSchema,
  ListLabelsResponseSchema,
  ListProjectResourcesResponseSchema,
  ListProjectsResponseSchema,
  MemberListSchema,
  PinListSchema,
  PinnedItemSchema,
  ProjectSchema,
  RuntimeListSchema,
  RuntimeProfileListResponseSchema,
  RuntimeSchema,
  SearchIssuesResponseSchema,
  SearchProjectsResponseSchema,
  SendChatMessageResponseSchema,
  SquadListSchema,
  TaskMessageListSchema,
  EMPTY_TASK_MESSAGE_LIST,
  UserSchema,
  WorkspaceListSchema,
  AgentTaskSchema,
} from "./schemas";
import type { ComposioConnections, IntegrationInstallations } from "./schemas";
import type { ZodType } from "zod";
import type { RuntimeCredentialPutResult } from "./schemas";
import { getCurrentSlug } from "./workspace-store";
import { getApiUrl } from "./server-store";
import { parseWithFallback } from "@/lib/parse-response";
import { createRequestId } from "@/lib/request-id";
import { buildCommentUpdateBody } from "./revision";

// API 基地址不再是模块级常量:用户可以在应用内切换服务器,地址必须在每次
// 请求时从 server-store 现取(RUYI-4)。env 缺失的启动检查移到了
// server-store 合成内置默认项处,失败面与改造前相同。

export interface LoginResponse {
  token: string;
  user: User;
}

/** Smart-mode (agent quick-create) request body. Field-for-field mirror of
 *  the inline body type on web's `ApiClient.quickCreateIssue`
 *  (packages/core/api/client.ts). */
export interface QuickCreateIssueRequest {
  agent_id?: string;
  squad_id?: string;
  prompt: string;
  priority?: IssuePriority;
  due_date?: string;
  project_id?: string | null;
  attachment_ids?: string[];
}

/** Mobile file payload for `uploadFile`. RN doesn't have a browser `File`
 *  object; the fetch `FormData` polyfill accepts `{ uri, name, type }`
 *  directly and streams from disk. expo-image-picker / expo-document-picker
 *  return assets that map straight onto this shape. */
export interface FileAsset {
  uri: string;
  name: string;
  type: string;
}

/** Web mirrors this from `packages/core/constants/upload.ts`. Mobile keeps
 *  its own copy per the `mirror, don't import` rule in apps/mobile/CLAUDE.md. */
const MAX_FILE_SIZE = 100 * 1024 * 1024;

/** Hard ceiling for every HTTP request. Mobile-specific because iOS may
 *  suspend a backgrounded network task without ever resolving/rejecting
 *  the JS-side fetch promise (facebook/react-native#35384). Without this
 *  timeout, a refetch fired after returning to foreground can leave the
 *  query stuck in `isRefetching` state forever (visible as the
 *  pull-to-refresh spinner never going away). 30s is generous for any
 *  reasonable Multica payload size on cellular. */
const FETCH_TIMEOUT_MS = 30_000;

/** RUYI-568: login-chain deadline. Measured single-request overhead on the
 *  slow (frpc relay) entry is 0.85–2.1s (RUYI-565 scope 2), so 10s bounds
 *  the user's blind wait to ~5× the worst observed case instead of 30s.
 *  Applies only to sendCode / verifyCode / getMe; every other request keeps
 *  the 30s FETCH_TIMEOUT_MS ceiling. */
const LOGIN_TIMEOUT_MS = 10_000;

/** Budget for `/api/upload-file` (RUYI-569). Much larger than
 *  FETCH_TIMEOUT_MS because the ceiling is MAX_FILE_SIZE (100MB) over a
 *  weak uplink: at ~1MB/s the wire transfer alone is ~100s, plus TLS
 *  handshake, server-side write and the response round trip. RN's fetch
 *  exposes no upload-progress events, so a progress-aware idle timeout is
 *  not an option without native modules — this is a fixed total budget.
 *  A link slower than that fails here with a clear, distinguishable error
 *  and the composer resets for retry; the pre-RUYI-569 alternative was an
 *  unbounded hang. Tune only with QA weak-network evidence. */
const UPLOAD_TIMEOUT_MS = 120_000;

export class ApiError extends Error {
  readonly status: number;
  readonly body?: unknown;
  constructor(message: string, status: number, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }
}

export interface ApiClientOptions {
  /** Called once when the server returns 401. The platform layer wires this
   *  to clear the token + navigate to /login so a stale token doesn't keep
   *  every subsequent request looping on 401. */
  onUnauthorized?: () => void;
  /** RUYI-567: Android RN pauses JS timers while the app is backgrounded
   *  (Timing module on host pause), so the 30s timer below cannot fire
   *  until — sometimes long after — the deadline. The platform layer
   *  (data/api-app-state.ts, wired in app/_layout.tsx) subscribes the real
   *  AppState and forwards foreground entries here; fetchRaw re-checks the
   *  deadline on each entry and aborts a request that is still pending.
   *  Optional: absent in node tests, where the timer path is covered. */
  subscribeAppState?: (listener: (state: string) => void) => () => void;
}

class ApiClient {
  private token: string | null = null;
  private options: ApiClientOptions = {};

  setToken(token: string | null) {
    this.token = token;
  }

  setOptions(options: ApiClientOptions) {
    this.options = { ...this.options, ...options };
  }

  private async fetchRaw(
    path: string,
    init: RequestInit & { signal?: AbortSignal; timeoutMs?: number } = {},
  ): Promise<Response> {
    const rid = createRequestId();
    const start = Date.now();
    const method = init.method ?? "GET";

    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      "X-Client-Platform": "mobile",
      "X-Client-OS": "ios",
      "X-Client-Version": "0.1.0",
      "X-Request-ID": rid,
      ...((init.headers as Record<string, string>) ?? {}),
    };
    if (this.token) {
      headers["Authorization"] = `Bearer ${this.token}`;
    }
    // Backend middleware (server/internal/middleware/workspace.go) resolves
    // slug → ws UUID and gates membership. Mirrors packages/core/api/client.ts.
    const slug = getCurrentSlug();
    if (slug && !headers["X-Workspace-Slug"]) {
      headers["X-Workspace-Slug"] = slug;
    }

    // Timeout + caller-signal forwarding.
    //
    // Hermes does NOT support AbortSignal.timeout() or AbortSignal.any() —
    // see facebook/react-native#42042 and livekit#4014. So we manually
    // compose a single controller that aborts on:
    //   (a) caller-side signal (TQ cancelling a stale/inactive query, the
    //       login screens cancelling an abandoned send-code, etc),
    //   (b) per-request deadline — FETCH_TIMEOUT_MS by default, tightened
    //       to LOGIN_TIMEOUT_MS on the login chain (RUYI-568). This defends
    //       against iOS suspending the network task silently during
    //       background — fetch() then never resolves;
    //       facebook/react-native#35384. Without this, a refetch
    //       triggered by WS reconnect can leave the FlatList pull-to-refresh
    //       spinner stuck on the screen indefinitely.
    const controller = new AbortController();
    const timeoutMs = init.timeoutMs ?? FETCH_TIMEOUT_MS;
    // 这条 message 会经 ApiError 一路冒泡到 18 处屏幕的错误行/Alert 正文
    // （见 components/**/*.tsx 的 err.message 落点），所以必须是译文而
    // 不是内部日志串。
    const timeoutError = () =>
      new Error(
        i18n.t(
          "common:mobile.common.request_timeout",
          "Request timed out after {{seconds}}s",
          { seconds: Math.round(timeoutMs / 1000) },
        ),
      );
    const timeoutId = setTimeout(() => {
      controller.abort(timeoutError());
    }, timeoutMs);
    // RUYI-567: the timer above is blind while the app is backgrounded
    // (paused JS timers), so a request that outlives its deadline in the
    // background must be re-judged on foreground entry — otherwise the
    // caller keeps waiting past the hard ceiling with no error. The
    // listener is scoped to this request and removed on settle.
    const onForeground = (state: string) => {
      if (state === "active" && Date.now() - start >= timeoutMs) {
        controller.abort(timeoutError());
      }
    };
    const unsubscribeAppState =
      this.options.subscribeAppState?.(onForeground);
    const callerSignal = init.signal;
    const onCallerAbort = () => controller.abort(callerSignal?.reason);
    if (callerSignal) {
      if (callerSignal.aborted) controller.abort(callerSignal.reason);
      else callerSignal.addEventListener("abort", onCallerAbort);
    }

    console.log(`[api] → ${method} ${path}`, { rid });

    let res: Response;
    try {
      res = await fetch(`${getApiUrl()}${path}`, {
        ...init,
        signal: controller.signal,
        headers,
      });
    } catch (err) {
      clearTimeout(timeoutId);
      unsubscribeAppState?.();
      callerSignal?.removeEventListener("abort", onCallerAbort);
      // Re-throw with a clearer message if this was our own timeout abort.
      if (
        err instanceof Error &&
        err.name === "AbortError" &&
        !callerSignal?.aborted
      ) {
        const duration = Date.now() - start;
        console.warn(`[api] ← TIMEOUT ${path}`, {
          rid,
          duration: `${duration}ms`,
        });
        throw new ApiError(
          `Request timed out after ${timeoutMs}ms`,
          0,
          undefined,
        );
      }
      throw err;
    }
    clearTimeout(timeoutId);
    unsubscribeAppState?.();
    callerSignal?.removeEventListener("abort", onCallerAbort);
    const duration = Date.now() - start;

    if (!res.ok) {
      // 401 sign-out hook: invoke once, let the platform layer (auth-store)
      // clear the token + navigate. Subsequent requests in flight will also
      // 401 and re-enter here, so the callback must be idempotent.
      if (res.status === 401) {
        this.options.onUnauthorized?.();
      }

      let body: unknown;
      try {
        body = await res.json();
      } catch {
        body = undefined;
      }
      const message =
        (body && typeof body === "object" && "message" in body
          ? String((body as { message: unknown }).message)
          : null) ?? `${res.status} ${res.statusText}`;

      const level = res.status === 404 ? "warn" : "error";
      console[level](`[api] ← ${res.status} ${path}`, {
        rid,
        duration: `${duration}ms`,
        error: message,
      });

      throw new ApiError(message, res.status, body);
    }

    console.log(`[api] ← ${res.status} ${path}`, {
      rid,
      duration: `${duration}ms`,
    });

    return res;
  }

  private async fetch<T>(
    path: string,
    init: RequestInit & { signal?: AbortSignal; timeoutMs?: number } = {},
  ): Promise<T> {
    const response = await this.fetchRaw(path, init);
    if (response.status === 204) return undefined as T;
    return (await response.json()) as T;
  }

  /**
   * Read-side helper: GET + zod parse + fallback in one call. Collapses
   * the boilerplate that every list/detail endpoint repeats:
   *
   *   const raw = await this.fetch<unknown>(path, { signal: opts?.signal });
   *   return parseWithFallback(raw, Schema, FALLBACK, { endpoint: "name" });
   *
   * Always uses GET (no method arg) — write endpoints that need parsing
   * still go through `this.fetch` + `parseWithFallback` directly because
   * they carry a body and care about method semantics. Use
   * `fetchValidatedWith` for those (PATCH / PUT / POST).
   *
   * The `endpoint` label defaults to the request path — override only when
   * the path has dynamic segments and you want stable telemetry labels.
   */
  private async fetchValidated<T>(
    path: string,
    schema: ZodType,
    fallback: T,
    opts?: { signal?: AbortSignal; endpoint?: string; timeoutMs?: number },
  ): Promise<T> {
    const raw = await this.fetch<unknown>(path, {
      signal: opts?.signal,
      timeoutMs: opts?.timeoutMs,
    });
    return parseWithFallback(raw, schema, fallback, {
      endpoint: opts?.endpoint ?? path,
    });
  }

  /** Same as fetchValidated but supports any HTTP method + body. Used by
   *  PATCH/PUT/POST endpoints whose response we still want to validate
   *  (e.g. updateMe returns User, updateNotificationPreferences returns
   *  NotificationPreferenceResponse). */
  private async fetchValidatedWith<T>(
    path: string,
    schema: ZodType,
    fallback: T,
    init: RequestInit,
    opts?: { signal?: AbortSignal; endpoint?: string; timeoutMs?: number },
  ): Promise<T> {
    // `opts.signal` wins if both are passed, but absent opts.signal does
    // NOT clear init.signal — important because forgetting `?? init.signal`
    // would silently strip a caller's abort signal when they used the
    // RequestInit shape but no opts.
    const raw = await this.fetch<unknown>(path, {
      ...init,
      signal: opts?.signal ?? init.signal ?? undefined,
      timeoutMs: opts?.timeoutMs,
    });
    return parseWithFallback(raw, schema, fallback, {
      endpoint: opts?.endpoint ?? `${init.method ?? "GET"} ${path}`,
    });
  }

  // --- Auth ---
  // 登录三链路（RUYI-568）：10s deadline + 可选调用方取消通道。超时收紧
  // 只作用于这三处；通用 API 保持 FETCH_TIMEOUT_MS 天花板。
  async sendCode(email: string, opts?: { signal?: AbortSignal }): Promise<void> {
    await this.fetch<void>("/auth/send-code", {
      method: "POST",
      body: JSON.stringify({ email }),
      signal: opts?.signal,
      timeoutMs: LOGIN_TIMEOUT_MS,
    });
  }

  async verifyCode(
    email: string,
    code: string,
    opts?: { signal?: AbortSignal },
  ): Promise<LoginResponse> {
    return this.fetch<LoginResponse>("/auth/verify-code", {
      method: "POST",
      body: JSON.stringify({ email, code }),
      signal: opts?.signal,
      timeoutMs: LOGIN_TIMEOUT_MS,
    });
  }

  async getMe(opts?: { signal?: AbortSignal }): Promise<User> {
    return this.fetchValidated(
      "/api/me",
      UserSchema,
      EMPTY_USER,
      { ...opts, endpoint: "getMe", timeoutMs: LOGIN_TIMEOUT_MS },
    );
  }

  async getConfig(opts?: { signal?: AbortSignal }): Promise<AppConfigResponse> {
    return this.fetchValidated<AppConfigResponse>(
      "/api/config",
      AppConfigSchema,
      EMPTY_APP_CONFIG,
      { ...opts, endpoint: "getConfig" },
    );
  }

  async getWorkspaceSubscriptionSummary(opts?: {
    signal?: AbortSignal;
  }): Promise<WorkspaceSubscriptionSummary | null> {
    return this.fetchValidated<WorkspaceSubscriptionSummary | null>(
      "/api/cloud-subscriptions/summary",
      WorkspaceSubscriptionSummarySchema,
      null,
      { ...opts, endpoint: "getWorkspaceSubscriptionSummary" },
    );
  }

  // PATCH /api/me — name, avatar_url, language. Server returns the updated
  // user; we parse so a partial drift doesn't bleed into the auth store.
  async updateMe(data: UpdateMeRequest): Promise<User> {
    return this.fetchValidatedWith(
      "/api/me",
      UserSchema,
      EMPTY_USER,
      { method: "PATCH", body: JSON.stringify(data) },
      { endpoint: "updateMe" },
    );
  }

  // --- Notification preferences ---
  async getNotificationPreferences(
    opts?: { signal?: AbortSignal },
  ): Promise<NotificationPreferenceResponse> {
    return this.fetchValidated(
      "/api/notification-preferences",
      NotificationPreferenceResponseSchema,
      EMPTY_NOTIFICATION_PREFERENCES,
      { ...opts, endpoint: "getNotificationPreferences" },
    );
  }

  async updateNotificationPreferences(
    preferences: NotificationPreferences,
    workspaceSlug?: string,
  ): Promise<NotificationPreferenceResponse> {
    return this.fetchValidatedWith(
      "/api/notification-preferences",
      NotificationPreferenceResponseSchema,
      EMPTY_NOTIFICATION_PREFERENCES,
      {
        method: "PATCH",
        headers: workspaceSlug
          ? { "X-Workspace-Slug": workspaceSlug }
          : undefined,
        body: JSON.stringify({ preferences }),
      },
      { endpoint: "updateNotificationPreferences" },
    );
  }

  // --- Workspaces ---
  async listWorkspaces(opts?: {
    signal?: AbortSignal;
  }): Promise<Workspace[]> {
    const raw = await this.fetch<unknown>("/api/workspaces", {
      signal: opts?.signal,
    });
    return parseWithFallback(raw, WorkspaceListSchema, EMPTY_WORKSPACE_LIST, {
      endpoint: "listWorkspaces",
    });
  }

  // --- Inbox ---
  async listInbox(opts?: { signal?: AbortSignal }): Promise<InboxItem[]> {
    const raw = await this.fetch<unknown>("/api/inbox", {
      signal: opts?.signal,
    });
    return parseWithFallback(raw, InboxListSchema, EMPTY_INBOX_LIST, {
      endpoint: "listInbox",
    });
  }

  // Archived notifications, backing the inbox's "Archived" sub-view (RUYI-532,
  // same capped endpoint web/desktop use — packages/core/api/client.ts
  // listArchivedInbox). Schema-guarded like listInbox so a contract drift
  // renders an empty archive instead of taking the screen down with it.
  async listArchivedInbox(opts?: { signal?: AbortSignal }): Promise<InboxItem[]> {
    const raw = await this.fetch<unknown>("/api/inbox/archived", {
      signal: opts?.signal,
    });
    return parseWithFallback(raw, InboxListSchema, EMPTY_INBOX_LIST, {
      endpoint: "listArchivedInbox",
    });
  }

  // Cross-workspace unread summary: one entry per workspace the user belongs
  // to that has unread inbox items. Backs the switch-workspace sheet's
  // per-workspace blue dot (RUYI-44) — the same endpoint web's sidebar dot
  // consumes. Schema-guarded so a contract drift hides the dot rather than
  // crashing the sheet (mirrors packages/core/api/client.ts getInboxUnreadSummary).
  async getInboxUnreadSummary(opts?: {
    signal?: AbortSignal;
  }): Promise<InboxWorkspaceUnread[]> {
    const raw = await this.fetch<unknown>("/api/inbox/unread-summary", {
      signal: opts?.signal,
    });
    return parseWithFallback(
      raw,
      InboxUnreadSummarySchema,
      EMPTY_INBOX_UNREAD_SUMMARY,
      { endpoint: "getInboxUnreadSummary" },
    );
  }

  async markInboxRead(id: string): Promise<InboxItem> {
    return this.fetch<InboxItem>(`/api/inbox/${id}/read`, { method: "POST" });
  }

  // Archive endpoints — write surface. Match web's surface in
  // packages/core/api/client.ts:981-1003. No parseWithFallback (mirrors
  // markInboxRead above and the project write endpoints): a malformed
  // archive response should surface naturally so the optimistic patch
  // rolls back.
  async archiveInbox(id: string): Promise<InboxItem> {
    return this.fetch<InboxItem>(`/api/inbox/${id}/archive`, { method: "POST" });
  }

  async unarchiveInbox(id: string): Promise<InboxItem> {
    return this.fetch<InboxItem>(`/api/inbox/${id}/unarchive`, {
      method: "POST",
    });
  }

  async markAllInboxRead(): Promise<{ count: number }> {
    return this.fetch<{ count: number }>("/api/inbox/mark-all-read", {
      method: "POST",
    });
  }

  async archiveAllInbox(): Promise<{ count: number }> {
    return this.fetch<{ count: number }>("/api/inbox/archive-all", {
      method: "POST",
    });
  }

  async archiveAllReadInbox(): Promise<{ count: number }> {
    return this.fetch<{ count: number }>("/api/inbox/archive-all-read", {
      method: "POST",
    });
  }

  async archiveCompletedInbox(): Promise<{ count: number }> {
    return this.fetch<{ count: number }>("/api/inbox/archive-completed", {
      method: "POST",
    });
  }

  // --- Members & Agents (for actor name/avatar lookup) ---
  async listMembers(
    workspaceId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<MemberWithUser[]> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/members`,
      { signal: opts?.signal },
    );
    return parseWithFallback(raw, MemberListSchema, EMPTY_MEMBER_LIST, {
      endpoint: "listMembers",
    });
  }

  // `include_archived` is on unconditionally, mirroring web's
  // agentListOptions (packages/core/workspace/queries.ts:75): the archived
  // scope and its count are filtered client-side from this one list, so
  // without the flag the archived segment renders permanently empty
  // (RUYI-346 defect #1).
  //
  // `workspaceSlug` pins the request to a workspace that may differ from the
  // active mirror (or when no mirror exists yet): share-target lists agents
  // for the PICKED workspace before any setCurrentWorkspace transition
  // (RUYI-463 P1 — the mirror used to be the only context carrier, leaving
  // fresh users with a 400 and a permanently empty list).
  async listAgents(opts?: {
    signal?: AbortSignal;
    workspaceSlug?: string;
  }): Promise<Agent[]> {
    const raw = await this.fetch<unknown>("/api/agents?include_archived=true", {
      signal: opts?.signal,
      headers: opts?.workspaceSlug
        ? { "X-Workspace-Slug": opts.workspaceSlug }
        : undefined,
    });
    return parseWithFallback(raw, AgentListSchema, EMPTY_AGENT_LIST, {
      endpoint: "listAgents",
    });
  }

  async listSkills(opts?: { signal?: AbortSignal }): Promise<SkillSummary[]> {
    return this.fetchValidated("/api/skills", SkillSummaryListSchema, [], { signal: opts?.signal });
  }

  // Workspace runtimes — feeds the presence dot's availability dimension
  // (runtime.status + last_seen_at). Backend route registered in
  // server/cmd/server/router.go:514 (GET /api/runtimes).
  async listRuntimes(opts?: { signal?: AbortSignal }): Promise<RuntimeDevice[]> {
    const raw = await this.fetch<unknown>("/api/runtimes", {
      signal: opts?.signal,
    });
    return parseWithFallback(raw, RuntimeListSchema, EMPTY_RUNTIME_LIST, {
      endpoint: "listRuntimes",
    });
  }

  // --- Voice runtime instances & profiles (RUYI-425 §4.3, mobile) ---
  // Endpoint paths + wire shapes mirror packages/core/api/client.ts
  // one-for-one (web parity); mobile keeps its own client so the
  // X-Workspace-Slug header follows the workspace store. The plaintext API
  // key travels once in the PUT body and is never returned — responses
  // carry only the badge plus the connectivity-probe outcome.

  // GET /api/workspaces/:id/runtime-profiles — the Type layer; the voice
  // create form filters these by capabilities.realtime_voice.
  async listRuntimeProfiles(
    workspaceId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<RuntimeProfile[]> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/runtime-profiles`,
      { signal: opts?.signal },
    );
    const parsed = parseWithFallback(
      raw,
      RuntimeProfileListResponseSchema,
      EMPTY_RUNTIME_PROFILE_LIST_RESPONSE,
      { endpoint: "listRuntimeProfiles" },
    );
    return parsed.runtime_profiles ?? [];
  }

  // POST /api/workspaces/:id/runtime-profiles — the §4.3 no-profile escape
  // hatch creates the workspace's default "Gemini Live" profile (voice
  // families are API-enforced command-less: command_name: "" is exactly
  // what the server requires for gemini_live).
  async createRuntimeProfile(
    workspaceId: string,
    body: CreateRuntimeProfileRequest,
  ): Promise<RuntimeProfile> {
    return this.fetch<RuntimeProfile>(
      `/api/workspaces/${workspaceId}/runtime-profiles`,
      {
        method: "POST",
        body: JSON.stringify(body),
      },
    );
  }

  // POST /api/runtimes — manual registration of a voice instance (§4.3).
  // Names may duplicate; the instance is born online/public with
  // registration_source "manual"; the API key is NOT part of this call —
  // store it right after via putRuntimeCredential, which also triggers the
  // server-side connectivity probe.
  async createManualRuntime(body: {
    name: string;
    profile_id: string;
    model?: string;
    advanced?: Record<string, unknown>;
  }): Promise<RuntimeDevice> {
    return this.fetchValidatedWith(
      "/api/runtimes",
      RuntimeSchema,
      EMPTY_RUNTIME,
      { method: "POST", body: JSON.stringify(body) },
      { endpoint: "createManualRuntime" },
    );
  }

  // PATCH /api/runtimes/:id — voice instance settings edits (§4.3):
  // custom_name / model / advanced / disabled merge into instance metadata;
  // visibility stays server-fixed to public for voice instances.
  async updateRuntime(
    runtimeId: string,
    patch: {
      visibility?: "private" | "public";
      custom_name?: string;
      apply_to_machine?: boolean;
      model?: string;
      advanced?: Record<string, unknown>;
      disabled?: boolean;
    },
  ): Promise<RuntimeDevice> {
    return this.fetchValidatedWith(
      `/api/runtimes/${runtimeId}`,
      RuntimeSchema,
      EMPTY_RUNTIME,
      { method: "PATCH", body: JSON.stringify(patch) },
      { endpoint: "updateRuntime" },
    );
  }

  // PUT /api/runtimes/:id/credentials/:key — stores/rotates the §4.5
  // credential and runs the §4.3 probe. Deliberately NOT routed through
  // parseWithFallback: the tri-state feedback branches on `probe.status`,
  // and a schema drift must not silently mask the probe outcome.
  async putRuntimeCredential(
    runtimeId: string,
    credentialKey: string,
    value: string,
  ): Promise<RuntimeCredentialPutResult> {
    return this.fetch<RuntimeCredentialPutResult>(
      `/api/runtimes/${runtimeId}/credentials/${credentialKey}`,
      {
        method: "PUT",
        body: JSON.stringify({ value }),
      },
    );
  }

  // DELETE /api/runtimes/:id/credentials/:key — idempotent server-side
  // (204 even when nothing was stored); badge falls back to not_configured.
  async deleteRuntimeCredential(
    runtimeId: string,
    credentialKey: string,
  ): Promise<void> {
    await this.fetch<void>(
      `/api/runtimes/${runtimeId}/credentials/${credentialKey}`,
      { method: "DELETE" },
    );
  }

  // DELETE /api/runtimes/:id — direct instance delete (RUYI-566). The
  // server refuses with a structured 409
  // (`runtime_profile_instance_delete_unsupported`) while a live runtime
  // profile backs the instance; that channel is deleteRuntimeProfile below,
  // whose cascade removes the instance and its credentials in one
  // transaction.
  async deleteRuntime(runtimeId: string): Promise<void> {
    await this.fetch<void>(`/api/runtimes/${runtimeId}`, {
      method: "DELETE",
    });
  }

  // DELETE /api/workspaces/:id/runtime-profiles/:profileId — the supported
  // delete channel for profile-backed (manual voice) instances: the server
  // tears down bound instances, deletes their credential rows and the
  // profile in one transaction (RUYI-540 QA-verified cascade, RUYI-566).
  async deleteRuntimeProfile(
    workspaceId: string,
    profileId: string,
  ): Promise<void> {
    await this.fetch<void>(
      `/api/workspaces/${workspaceId}/runtime-profiles/${profileId}`,
      { method: "DELETE" },
    );
  }

  // Workspace-wide active agent tasks + each agent's most recent terminal —
  // feeds the workload dimension of presence (currently unused in the mobile
  // dot; reserved for the P1 long-press peek sheet). Listed here now so the
  // realtime invalidation path can be wired in one PR. Backend route at
  // server/cmd/server/router.go:539 (GET /api/agent-task-snapshot).
  async listAgentTaskSnapshot(
    opts?: { signal?: AbortSignal },
  ): Promise<AgentTask[]> {
    const raw = await this.fetch<unknown>("/api/agent-task-snapshot", {
      signal: opts?.signal,
    });
    return parseWithFallback(raw, AgentTaskListSchema, EMPTY_AGENT_TASK_LIST, {
      endpoint: "listAgentTaskSnapshot",
    });
  }

  async listSquads(opts?: { signal?: AbortSignal }): Promise<Squad[]> {
    const raw = await this.fetch<unknown>("/api/squads", {
      signal: opts?.signal,
    });
    return parseWithFallback(raw, SquadListSchema, EMPTY_SQUAD_LIST, {
      endpoint: "listSquads",
    });
  }

  // --- Agents & Squads management (RUYI-346) ---
  // Endpoint paths mirror packages/core/api/client.ts (web) one-for-one —
  // behavioral parity starts with the same wire contract. Reads go through
  // fetchValidated with core-whitelisted schemas; writes that return an
  // entity we feed back into the cache go through fetchValidatedWith. Void
  // writes use bare fetch — nothing to parse.

  // GET /api/agents/:id — full Agent payload incl. the attached-skills list
  // the settings screens edit. Same shape as list rows, so the mobile
  // AgentSchema parses both.
  async getAgent(id: string, opts?: { signal?: AbortSignal }): Promise<Agent> {
    return this.fetchValidated(
      `/api/agents/${id}`,
      AgentSchema,
      EMPTY_AGENT_FALLBACK,
      { ...opts, endpoint: "getAgent" },
    );
  }

  async createAgent(data: CreateAgentRequest): Promise<Agent> {
    return this.fetchValidatedWith(
      "/api/agents",
      AgentSchema,
      EMPTY_AGENT_FALLBACK,
      { method: "POST", body: JSON.stringify(data) },
      { endpoint: "createAgent" },
    );
  }

  async updateAgent(id: string, data: UpdateAgentRequest): Promise<Agent> {
    return this.fetchValidatedWith(
      `/api/agents/${id}`,
      AgentSchema,
      EMPTY_AGENT_FALLBACK,
      { method: "PUT", body: JSON.stringify(data) },
      { endpoint: "updateAgent" },
    );
  }

  async archiveAgent(id: string): Promise<Agent> {
    return this.fetchValidatedWith(
      `/api/agents/${id}/archive`,
      AgentSchema,
      EMPTY_AGENT_FALLBACK,
      { method: "POST" },
      { endpoint: "archiveAgent" },
    );
  }

  async restoreAgent(id: string): Promise<Agent> {
    return this.fetchValidatedWith(
      `/api/agents/${id}/restore`,
      AgentSchema,
      EMPTY_AGENT_FALLBACK,
      { method: "POST" },
      { endpoint: "restoreAgent" },
    );
  }

  // Bulk-cancel every active task (queued/dispatched/running) for the agent.
  // Server broadcasts task:cancelled per row, so realtime clears the run
  // lists; the response count only feeds the confirmation toast.
  async cancelAgentTasks(id: string): Promise<{ cancelled: number }> {
    return this.fetchValidatedWith(
      `/api/agents/${id}/cancel-tasks`,
      AgentCancelTasksResponseSchema,
      EMPTY_AGENT_CANCEL_TASKS_RESPONSE,
      { method: "POST" },
      { endpoint: "cancelAgentTasks" },
    );
  }

  // GET /api/agents/:id/env — PLAINTEXT env map. Admits the agent's owner or
  // a workspace owner/admin; every successful call writes an
  // `agent_env_revealed` audit row server-side (MUL-2600). Mobile therefore
  // never prefetches this into a query cache — only an explicit user
  // confirmation may trigger it. See components/agents/env-editor.tsx.
  async getAgentEnv(id: string, opts?: { signal?: AbortSignal }): Promise<AgentEnvResponse> {
    return this.fetchValidated(
      `/api/agents/${id}/env`,
      AgentEnvResponseSchema,
      EMPTY_AGENT_ENV,
      { ...opts, endpoint: "getAgentEnv" },
    );
  }

  // PUT /api/agents/:id/env — replaces custom_env wholesale. Values equal to
  // "****" are preserved server-side (the **** guard), so the editor must
  // keep masked values verbatim in its payload. Writes an
  // `agent_env_updated` audit row.
  async updateAgentEnv(id: string, data: UpdateAgentEnvRequest): Promise<AgentEnvResponse> {
    return this.fetchValidatedWith(
      `/api/agents/${id}/env`,
      AgentEnvResponseSchema,
      EMPTY_AGENT_ENV,
      { method: "PUT", body: JSON.stringify(data) },
      { endpoint: "updateAgentEnv" },
    );
  }

  // Agent webhooks (RUYI-52). webhook_token/path/url come back only for
  // managers — the server strips them for anyone else; the UI keys its
  // manage affordances off their presence.
  async listAgentWebhooks(agentId: string, opts?: { signal?: AbortSignal }): Promise<AgentWebhook[]> {
    return this.fetchValidated(
      `/api/agents/${agentId}/webhooks`,
      AgentWebhookListSchema,
      EMPTY_AGENT_WEBHOOK_LIST,
      { ...opts, endpoint: "listAgentWebhooks" },
    );
  }

  async createAgentWebhook(agentId: string, data: CreateAgentWebhookRequest): Promise<AgentWebhook> {
    return this.fetchValidatedWith(
      `/api/agents/${agentId}/webhooks`,
      AgentWebhookSchema,
      EMPTY_AGENT_WEBHOOK,
      { method: "POST", body: JSON.stringify(data) },
      { endpoint: "createAgentWebhook" },
    );
  }

  async updateAgentWebhook(agentId: string, webhookId: string, data: UpdateAgentWebhookRequest): Promise<AgentWebhook> {
    return this.fetchValidatedWith(
      `/api/agents/${agentId}/webhooks/${webhookId}`,
      AgentWebhookSchema,
      EMPTY_AGENT_WEBHOOK,
      { method: "PUT", body: JSON.stringify(data) },
      { endpoint: "updateAgentWebhook" },
    );
  }

  async setAgentWebhookEnabled(agentId: string, webhookId: string, enabled: boolean): Promise<AgentWebhook> {
    return this.fetchValidatedWith(
      `/api/agents/${agentId}/webhooks/${webhookId}/enabled`,
      AgentWebhookSchema,
      EMPTY_AGENT_WEBHOOK,
      { method: "PUT", body: JSON.stringify({ enabled }) },
      { endpoint: "setAgentWebhookEnabled" },
    );
  }

  // POST rotate — mints a new token; the old URL stops working immediately.
  async rotateAgentWebhook(agentId: string, webhookId: string): Promise<AgentWebhook> {
    return this.fetchValidatedWith(
      `/api/agents/${agentId}/webhooks/${webhookId}/rotate`,
      AgentWebhookSchema,
      EMPTY_AGENT_WEBHOOK,
      { method: "POST", body: JSON.stringify({}) },
      { endpoint: "rotateAgentWebhook" },
    );
  }

  async deleteAgentWebhook(agentId: string, webhookId: string): Promise<void> {
    await this.fetch<void>(`/api/agents/${agentId}/webhooks/${webhookId}`, {
      method: "DELETE",
    });
  }

  // Incremental attach — POST /skills/add only inserts the given ids (server
  // upserts with ON CONFLICT DO NOTHING), mirroring web's addAgentSkills.
  async addAgentSkills(agentId: string, data: SetAgentSkillsRequest): Promise<void> {
    await this.fetch<void>(`/api/agents/${agentId}/skills/add`, {
      method: "POST",
      body: JSON.stringify(data),
    });
  }

  // Workspace-registry skill toggle (vs the runtime-local endpoint below the
  // web skills-tab also has — mobile P0 surfaces attached skills only).
  async setAgentSkillEnabled(agentId: string, skillId: string, enabled: boolean): Promise<void> {
    await this.fetch<void>(
      `/api/agents/${agentId}/skills/${skillId}/enabled`,
      { method: "PUT", body: JSON.stringify({ enabled }) },
    );
  }

  async removeAgentSkill(agentId: string, skillId: string): Promise<void> {
    await this.fetch<void>(`/api/agents/${agentId}/skills/${skillId}`, {
      method: "DELETE",
    });
  }

  // Runtime-inherited skill toggle (RUYI-418 阶段B / A13): the web skills-tab
  // twin of the workspace toggle above. Writes into the agent's
  // `disabled_runtime_skills` overrides.
  async setAgentRuntimeSkillEnabled(
    agentId: string,
    data: SetAgentRuntimeSkillEnabledRequest,
  ): Promise<void> {
    await this.fetch<void>(`/api/agents/${agentId}/runtime-skills/enabled`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  // --- Execution profiles (RUYI-57 / RUYI-418 Q10) -------------------------
  // Endpoint paths mirror packages/core/api/client.ts one-for-one. All
  // responses are schema-parsed like the web client: the activation response
  // in particular must never be read optimistically.

  async listExecutionProfiles(
    workspaceId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<ExecutionProfileListResponse> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/execution-profiles`,
      { signal: opts?.signal },
    );
    return parseWithFallback(
      raw,
      ExecutionProfileListResponseSchema,
      EMPTY_EXECUTION_PROFILE_LIST,
      { endpoint: "listExecutionProfiles" },
    );
  }

  async getExecutionProfile(
    workspaceId: string,
    profileId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<ExecutionProfile> {
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/execution-profiles/${profileId}`,
      { signal: opts?.signal },
    );
    return parseWithFallback(raw, ExecutionProfileSchema, EMPTY_EXECUTION_PROFILE, {
      endpoint: "getExecutionProfile",
    });
  }

  async createExecutionProfile(
    workspaceId: string,
    body: CreateExecutionProfileRequest,
  ): Promise<ExecutionProfile> {
    return this.fetchValidatedWith(
      `/api/workspaces/${workspaceId}/execution-profiles`,
      ExecutionProfileSchema,
      EMPTY_EXECUTION_PROFILE,
      { method: "POST", body: JSON.stringify(body) },
      { endpoint: "createExecutionProfile" },
    );
  }

  async updateExecutionProfile(
    workspaceId: string,
    profileId: string,
    patch: UpdateExecutionProfileRequest,
  ): Promise<ExecutionProfile> {
    return this.fetchValidatedWith(
      `/api/workspaces/${workspaceId}/execution-profiles/${profileId}`,
      ExecutionProfileSchema,
      EMPTY_EXECUTION_PROFILE,
      { method: "PATCH", body: JSON.stringify(patch) },
      { endpoint: "updateExecutionProfile" },
    );
  }

  async deleteExecutionProfile(
    workspaceId: string,
    profileId: string,
  ): Promise<void> {
    await this.fetch<void>(
      `/api/workspaces/${workspaceId}/execution-profiles/${profileId}`,
      { method: "DELETE" },
    );
  }

  async upsertExecutionProfileEntry(
    workspaceId: string,
    profileId: string,
    body: UpsertExecutionProfileEntryRequest,
  ): Promise<ExecutionProfileEntry> {
    return this.fetchValidatedWith(
      `/api/workspaces/${workspaceId}/execution-profiles/${profileId}/entries`,
      ExecutionProfileEntrySchema,
      {
        agent_id: body.agent_id,
        runtime_id: "",
        model: "",
        thinking_level: null,
        updated_at: "",
      },
      { method: "PUT", body: JSON.stringify(body) },
      { endpoint: "upsertExecutionProfileEntry" },
    );
  }

  async deleteExecutionProfileEntry(
    workspaceId: string,
    profileId: string,
    agentId: string,
  ): Promise<void> {
    await this.fetch<void>(
      `/api/workspaces/${workspaceId}/execution-profiles/${profileId}/entries/${agentId}`,
      { method: "DELETE" },
    );
  }

  async activateExecutionProfile(
    workspaceId: string,
    profileId: string,
  ): Promise<ExecutionProfileActivationResponse> {
    return this.fetchValidatedWith(
      `/api/workspaces/${workspaceId}/execution-profiles/${profileId}/activate`,
      ExecutionProfileActivationResponseSchema,
      EMPTY_EXECUTION_PROFILE_ACTIVATION,
      { method: "POST" },
      { endpoint: "activateExecutionProfile" },
    );
  }

  // --- Runtime model discovery (S4) ----------------------------------------
  // Poll-while-pending/running state machine lives in
  // apps/mobile/lib/runtime-discovery.ts; these are the two wire calls.

  async initiateListModels(runtimeId: string): Promise<RuntimeModelListRequest> {
    const raw = await this.fetch<unknown>(`/api/runtimes/${runtimeId}/models`, {
      method: "POST",
    });
    return parseWithFallback(
      raw,
      RuntimeModelListRequestSchema,
      { ...MALFORMED_RUNTIME_MODEL_LIST_REQUEST, runtime_id: runtimeId },
      { endpoint: "initiateListModels" },
    );
  }

  async getListModelsResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeModelListRequest> {
    const raw = await this.fetch<unknown>(
      `/api/runtimes/${runtimeId}/models/${requestId}`,
    );
    return parseWithFallback(
      raw,
      RuntimeModelListRequestSchema,
      {
        ...MALFORMED_RUNTIME_MODEL_LIST_REQUEST,
        id: requestId,
        runtime_id: runtimeId,
      },
      { endpoint: "getListModelsResult" },
    );
  }

  // --- Runtime local skills / capabilities (B3) -----------------------------
  // One daemon round trip returns both the skill inventory and a redacted MCP
  // listing; the agent surfaces treat it as the runtime capability snapshot.
  // Plain typed fetch like the web client — no schema exists for these.

  async initiateListLocalSkills(
    runtimeId: string,
  ): Promise<RuntimeLocalSkillListRequest> {
    return this.fetch<RuntimeLocalSkillListRequest>(
      `/api/runtimes/${runtimeId}/local-skills`,
      { method: "POST" },
    );
  }

  async getListLocalSkillsResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeLocalSkillListRequest> {
    return this.fetch<RuntimeLocalSkillListRequest>(
      `/api/runtimes/${runtimeId}/local-skills/${requestId}`,
    );
  }

  async initiateImportLocalSkill(
    runtimeId: string,
    data: CreateRuntimeLocalSkillImportRequest,
  ): Promise<RuntimeLocalSkillImportRequest> {
    return this.fetch<RuntimeLocalSkillImportRequest>(
      `/api/runtimes/${runtimeId}/local-skills/import`,
      { method: "POST", body: JSON.stringify(data) },
    );
  }

  async getImportLocalSkillResult(
    runtimeId: string,
    requestId: string,
  ): Promise<RuntimeLocalSkillImportRequest> {
    return this.fetch<RuntimeLocalSkillImportRequest>(
      `/api/runtimes/${runtimeId}/local-skills/import/${requestId}`,
    );
  }

  // Workspace skill catalog (RUYI-288): authored skills unioned with runtime
  // discovery sightings. Workspace resolved server-side from the slug header.
  async listSkillCatalog(opts?: { signal?: AbortSignal }): Promise<SkillCatalogEntry[]> {
    return this.fetch<SkillCatalogEntry[]>("/api/skills/catalog", {
      signal: opts?.signal,
    });
  }

  // --- Agent MCP servers (B3) ------------------------------------------------
  // Three sources, three groups: the agent's own mcp_config JSON (edited via
  // updateAgent), workspace library servers assigned to this agent, and the
  // runtime's read-only inventory. Assignment writes return the resulting
  // list so the cache never guesses.

  async listAgentMcpServers(
    agentId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<WorkspaceMcpServer[]> {
    return this.fetchValidated(
      `/api/agents/${agentId}/mcp-servers`,
      WorkspaceMcpServerListSchema,
      [],
      { ...opts, endpoint: "listAgentMcpServers" },
    );
  }

  async addAgentMcpServer(
    agentId: string,
    serverId: string,
  ): Promise<WorkspaceMcpServer[]> {
    return this.fetchValidatedWith(
      `/api/agents/${agentId}/mcp-servers`,
      WorkspaceMcpServerListSchema,
      [],
      { method: "POST", body: JSON.stringify({ server_id: serverId }) },
      { endpoint: "addAgentMcpServer" },
    );
  }

  async setAgentMcpServerEnabled(
    agentId: string,
    serverId: string,
    enabled: boolean,
  ): Promise<WorkspaceMcpServer[]> {
    return this.fetchValidatedWith(
      `/api/agents/${agentId}/mcp-servers/${encodeURIComponent(serverId)}/enabled`,
      WorkspaceMcpServerListSchema,
      [],
      { method: "PUT", body: JSON.stringify({ enabled }) },
      { endpoint: "setAgentMcpServerEnabled" },
    );
  }

  async removeAgentMcpServer(
    agentId: string,
    serverId: string,
  ): Promise<WorkspaceMcpServer[]> {
    return this.fetchValidatedWith(
      `/api/agents/${agentId}/mcp-servers/${encodeURIComponent(serverId)}`,
      WorkspaceMcpServerListSchema,
      [],
      { method: "DELETE" },
      { endpoint: "removeAgentMcpServer" },
    );
  }

  async listWorkspaceMcpServers(
    workspaceId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<WorkspaceMcpServer[]> {
    return this.fetchValidated(
      `/api/workspaces/${workspaceId}/mcp-servers`,
      WorkspaceMcpServerListSchema,
      [],
      { ...opts, endpoint: "listWorkspaceMcpServers" },
    );
  }

  // --- IM integration installations (RUYI-418 B3 entry condition) ---
  // Only `configured` is consumed on mobile (integrations row visibility on
  // the agent detail screen, mirroring web agent-overview-pane), so all five
  // listings share the minimal schema.

  async listLarkInstallations(
    workspaceId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<IntegrationInstallations> {
    return this.fetchValidated(
      `/api/workspaces/${workspaceId}/lark/installations`,
      IntegrationInstallationsSchema,
      EMPTY_INTEGRATION_INSTALLATIONS,
      { ...opts, endpoint: "listLarkInstallations" },
    );
  }

  async listSlackInstallations(
    workspaceId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<IntegrationInstallations> {
    return this.fetchValidated(
      `/api/workspaces/${workspaceId}/slack/installations`,
      IntegrationInstallationsSchema,
      EMPTY_INTEGRATION_INSTALLATIONS,
      { ...opts, endpoint: "listSlackInstallations" },
    );
  }

  async listDingTalkInstallations(
    workspaceId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<IntegrationInstallations> {
    return this.fetchValidated(
      `/api/workspaces/${workspaceId}/dingtalk/installations`,
      IntegrationInstallationsSchema,
      EMPTY_INTEGRATION_INSTALLATIONS,
      { ...opts, endpoint: "listDingTalkInstallations" },
    );
  }

  async listWecomInstallations(
    workspaceId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<IntegrationInstallations> {
    return this.fetchValidated(
      `/api/workspaces/${workspaceId}/wecom/installations`,
      IntegrationInstallationsSchema,
      EMPTY_INTEGRATION_INSTALLATIONS,
      { ...opts, endpoint: "listWecomInstallations" },
    );
  }

  async listTelegramInstallations(
    workspaceId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<IntegrationInstallations> {
    return this.fetchValidated(
      `/api/workspaces/${workspaceId}/telegram/installations`,
      IntegrationInstallationsSchema,
      EMPTY_INTEGRATION_INSTALLATIONS,
      { ...opts, endpoint: "listTelegramInstallations" },
    );
  }

  // RUYI-418 B3: the viewer's own Composio connections, consumed by the
  // owner-gated MCP-apps screen (same endpoint web's composioConnectionsOptions
  // reads; mobile keeps a reduced schema).
  async listComposioConnections(
    opts?: { signal?: AbortSignal },
  ): Promise<ComposioConnections> {
    return this.fetchValidated(
      "/api/integrations/composio/connections",
      ComposioConnectionsSchema,
      EMPTY_COMPOSIO_CONNECTIONS,
      { ...opts, endpoint: "listComposioConnections" },
    );
  }

  // --- Squads management ---

  async getSquad(id: string, opts?: { signal?: AbortSignal }): Promise<Squad> {
    return this.fetchValidated(
      `/api/squads/${id}`,
      SquadSchema,
      EMPTY_SQUAD,
      { ...opts, endpoint: "getSquad" },
    );
  }

  async createSquad(data: CreateSquadRequest): Promise<Squad> {
    return this.fetchValidatedWith(
      "/api/squads",
      SquadSchema,
      EMPTY_SQUAD,
      { method: "POST", body: JSON.stringify(data) },
      { endpoint: "createSquad" },
    );
  }

  async updateSquad(id: string, data: UpdateSquadRequest): Promise<Squad> {
    return this.fetchValidatedWith(
      `/api/squads/${id}`,
      SquadSchema,
      EMPTY_SQUAD,
      { method: "PUT", body: JSON.stringify(data) },
      { endpoint: "updateSquad" },
    );
  }

  // DELETE /api/squads/:id — archive = one-way delete. There is no restore
  // endpoint; the UI confirmation copy states this explicitly.
  async deleteSquad(id: string): Promise<void> {
    await this.fetch<void>(`/api/squads/${id}`, { method: "DELETE" });
  }

  async listSquadMembers(squadId: string, opts?: { signal?: AbortSignal }): Promise<SquadMember[]> {
    return this.fetchValidated(
      `/api/squads/${squadId}/members`,
      SquadMemberListSchema,
      EMPTY_SQUAD_MEMBER_LIST,
      { ...opts, endpoint: "listSquadMembers" },
    );
  }

  async addSquadMember(squadId: string, data: AddSquadMemberRequest): Promise<SquadMember> {
    return this.fetchValidatedWith(
      `/api/squads/${squadId}/members`,
      SquadMemberSchema,
      EMPTY_SQUAD_MEMBER,
      { method: "POST", body: JSON.stringify(data) },
      { endpoint: "addSquadMember" },
    );
  }

  // Members are addressed by the (member_type, member_id) pair — the row id
  // is never used on the wire (server contract, types/squad.ts requests).
  async removeSquadMember(squadId: string, data: RemoveSquadMemberRequest): Promise<void> {
    await this.fetch<void>(`/api/squads/${squadId}/members`, {
      method: "DELETE",
      body: JSON.stringify(data),
    });
  }

  async updateSquadMemberRole(squadId: string, data: UpdateSquadMemberRoleRequest): Promise<SquadMember> {
    return this.fetchValidatedWith(
      `/api/squads/${squadId}/members/role`,
      SquadMemberSchema,
      EMPTY_SQUAD_MEMBER,
      { method: "PATCH", body: JSON.stringify(data) },
      { endpoint: "updateSquadMemberRole" },
    );
  }

  // Per-squad derived member status (working/idle/offline/unstable/archived,
  // server-derived). Parsed with the lenient core schema so a new status
  // value degrades to a neutral pill instead of failing the screen (#2143).
  async getSquadMemberStatus(squadId: string, opts?: { signal?: AbortSignal }): Promise<SquadMemberStatusListResponse> {
    return this.fetchValidated<SquadMemberStatusListResponse>(
      `/api/squads/${squadId}/members/status`,
      SquadMemberStatusListResponseSchema,
      EMPTY_SQUAD_MEMBER_STATUS_LIST,
      { ...opts, endpoint: "getSquadMemberStatus" },
    );
  }

  // --- Issues ---
  async listIssues(
    params: ListIssuesParams = {},
    opts?: { signal?: AbortSignal },
  ): Promise<ListIssuesResponse> {
    const search = new URLSearchParams();
    // Params whose wire name/shape differs from the TS field name get the
    // same explicit mapping web applies (packages/core/api/client.ts:1124,
    // :1097): sort_by→sort + sort_direction→direction, actor-ref lists as
    // `type:id` CSV, include_no_assignee as the literal "true". Everything
    // else keeps the generic pass-through (arrays comma-joined — the server
    // parses a single comma-separated query value per key).
    const ACTOR_REF_KEYS = new Set(["assignee_filters", "creator_filters"]);
    for (const [k, v] of Object.entries(params)) {
      if (v == null) continue;
      if (k === "sort_by" || k === "sort_direction") continue;
      if (ACTOR_REF_KEYS.has(k)) {
        const refs = v as { type: string; id: string }[];
        if (refs.length > 0)
          search.set(k, refs.map((f) => `${f.type}:${f.id}`).join(","));
        continue;
      }
      if (k === "include_no_assignee") {
        if (v) search.set(k, "true");
        continue;
      }
      if (Array.isArray(v)) {
        // Backend parses comma-separated lists (server/internal/handler/issue.go
        // uses strings.Split on a single query value). Match web's serialization
        // in packages/core/api/client.ts:407 — repeated keys would silently
        // collapse to the first value only.
        if (v.length > 0) search.set(k, v.map(String).join(","));
      } else {
        search.set(k, String(v));
      }
    }
    if (params.sort_by) search.set("sort", params.sort_by);
    if (params.sort_direction) search.set("direction", params.sort_direction);
    const qs = search.toString();
    const raw = await this.fetch<unknown>(
      `/api/issues${qs ? `?${qs}` : ""}`,
      { signal: opts?.signal },
    );
    return parseWithFallback(raw, ListIssuesResponseSchema, EMPTY_LIST_ISSUES_RESPONSE, {
      endpoint: "GET /api/issues",
    });
  }

  /** Workspace-wide issue search. Backend `GET /api/issues/search` with
   *  workspace resolved by the `X-Workspace-Slug` middleware (same as
   *  `listIssues`). Caller passes its own `AbortController.signal` so the
   *  search modal can cancel an in-flight request when the user types
   *  again — see app/(app)/[workspace]/search.tsx. */
  async searchIssues(
    params: { q: string; limit?: number; include_closed?: boolean; offset?: number },
    opts?: { signal?: AbortSignal },
  ): Promise<SearchIssuesResponse> {
    const search = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) {
      if (v == null) continue;
      search.set(k, String(v));
    }
    const raw = await this.fetch<unknown>(
      `/api/issues/search?${search.toString()}`,
      { signal: opts?.signal },
    );
    return parseWithFallback(raw, SearchIssuesResponseSchema, EMPTY_SEARCH_ISSUES_RESPONSE, {
      endpoint: "GET /api/issues/search",
    });
  }

  async getIssue(
    id: string,
    opts?: { signal?: AbortSignal },
  ): Promise<Issue> {
    return this.fetchValidated(
      `/api/issues/${id}`,
      IssueSchema,
      EMPTY_ISSUE_FALLBACK,
      { ...opts, endpoint: "getIssue" },
    );
  }

  // Write endpoint — mirrors POST /api/issues
  // (server/cmd/server/router.go:320, server/internal/handler/issue.go
  // CreateIssue). Mobile sends only the fields the form fills in; backend
  // applies its own defaults for anything omitted.
  async createIssue(body: CreateIssueRequest): Promise<Issue> {
    return this.fetch<Issue>("/api/issues", {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  // Smart-mode (agent quick-create) write endpoint — mirrors web
  // packages/core/api/client.ts quickCreateIssue → POST /api/issues/quick-create
  // (server/internal/handler QuickCreateIssue). The server enqueues an agent
  // run that generates the title/description; the response `{ task_id }` is
  // never consumed by UI logic, so a raw fetch is acceptable here (same rule
  // as createIssue above).
  async quickCreateIssue(
    body: QuickCreateIssueRequest,
  ): Promise<{ task_id: string }> {
    return this.fetch("/api/issues/quick-create", {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  // Manual retry for a failed issue-less quick-create — mirrors web
  // packages/core/api/client.ts retrySourceContextQuickCreate →
  // POST /api/tasks/:id/retry-source-context (server/internal/handler/
  // task_lifecycle.go RetrySourceContextQuickCreate). The server re-enqueues
  // the creation and transfers the pending source context (original input)
  // to the new task, so the client only names the failed task. The 202 body
  // is the new AgentTask — validated, not degraded: a shape-mismatched reply
  // here must fail loudly rather than hand the UI a fake task. 409 carries
  // {code: "source_context_retry_unavailable"} through the ApiError body.
  async retrySourceContextQuickCreate(taskId: string): Promise<AgentTask> {
    const raw = await this.fetch<unknown>(
      `/api/tasks/${taskId}/retry-source-context`,
      { method: "POST" },
    );
    const task = parseWithFallback<AgentTask | null>(
      raw,
      AgentTaskSchema,
      null,
      { endpoint: "POST /api/tasks/:id/retry-source-context" },
    );
    if (!task) {
      throw new ApiError("Invalid source-context retry response", 0, raw);
    }
    return task;
  }

  // Timeline returns the full ASC entry list in one shot — server-side
  // pagination was dropped in #2322 (p99 ~30 entries per issue, cursors
  // were pure overhead and split reply threads at page boundaries).
  // Call WITHOUT pagination params: the legacy `limit/before/after/around`
  // path returns the old wrapped shape for back-compat, which mobile must
  // NOT trigger. See server/internal/handler/activity.go:60-69.
  async listTimeline(
    issueId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<TimelineQueryData> {
    const response = await this.fetchRaw(`/api/issues/${issueId}/timeline`, {
      signal: opts?.signal,
    });
    const raw = await response.json() as unknown;
    return {
      entries: parseWithFallback(raw, TimelineEntriesSchema, EMPTY_TIMELINE_ENTRIES, {
        endpoint: "GET /api/issues/:id/timeline",
      }),
      truncatedKinds: parseTimelineTruncatedKinds(
        response.headers.get("X-Timeline-Truncated"),
      ),
    };
  }

  // GET /api/issues/:id/attachments — list of file attachments hooked to
  // the issue (or its comments). Mobile uses this to resolve `mc://file/<id>`
  // markdown image URIs to their `download_url` HTTPS endpoint; without it,
  // iOS image loader doesn't understand the mc: scheme and renders broken.
  async listAttachments(
    issueId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<Attachment[]> {
    return this.fetchValidated(
      `/api/issues/${issueId}/attachments`,
      AttachmentListSchema,
      EMPTY_ATTACHMENT_LIST,
      { ...opts, endpoint: "GET /api/issues/:id/attachments" },
    );
  }

  // GET /api/attachments/:id — single-attachment metadata whose response
  // carries freshly minted, credential-free download URLs for THIS reply
  // (signed proxy capability / S3 presign / CloudFront, per storage mode —
  // see GetAttachmentByID). This is the click-time re-sign endpoint web's
  // use-download-attachment uses; mobile needs it for the same reason: list
  // `download_url`s hold the auth-gated stable path, which a system-browser
  // hand-off can neither authenticate (no Bearer header) nor survive (401
  // JSON page). Same parse-with-fallback shape as the core client's
  // getAttachment: a shape-mismatched reply degrades to the empty record,
  // whose empty URLs the open helper (lib/attachment-open) treats as "no
  // URL" and reports as a typed failure instead of opening a stale link.
  async getAttachment(
    attachmentId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<Attachment> {
    const raw = await this.fetch<unknown>(`/api/attachments/${attachmentId}`, {
      signal: opts?.signal,
    });
    return parseWithFallback(raw, AttachmentResponseSchema, EMPTY_ATTACHMENT, {
      endpoint: "GET /api/attachments/{id}",
    });
  }

  // Active tasks for an issue (status in queued/dispatched/running). Returns
  // the inner `tasks` array directly — handler wraps it in `{ tasks: [] }`
  // (server/internal/handler/daemon.go:1866) so the response object survives
  // future field additions without breaking the cache shape.
  async listActiveTasksForIssue(
    issueId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<AgentTask[]> {
    const parsed = await this.fetchValidated(
      `/api/issues/${issueId}/active-task`,
      ActiveTasksResponseSchema,
      EMPTY_ACTIVE_TASKS_RESPONSE,
      { ...opts, endpoint: "GET /api/issues/:id/active-task" },
    );
    return parsed.tasks;
  }

  // All tasks (any status) for an issue — drives the "Runs" history section.
  // Path is `/task-runs` (server/cmd/server/router.go:353), NOT `/tasks` —
  // the latter doesn't exist on this scope.
  async listTasksByIssue(
    issueId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<AgentTask[]> {
    return this.fetchValidated(
      `/api/issues/${issueId}/task-runs`,
      AgentTaskListSchema,
      EMPTY_AGENT_TASK_LIST,
      { ...opts, endpoint: "GET /api/issues/:id/task-runs" },
    );
  }

  async createComment(
    issueId: string,
    content: string,
    opts?: { parentId?: string; type?: string; attachmentIds?: string[] },
  ): Promise<Comment> {
    // Body shape mirrors backend `CreateCommentRequest`
    // (server/internal/handler/comment.go:165). `parent_id` is sent only
    // when present so top-level comments don't carry an explicit null.
    // `type` defaults to "comment" matching web client.ts:686.
    return this.fetchValidatedWith(
      `/api/issues/${issueId}/comments`,
      CommentSchema,
      EMPTY_COMMENT,
      {
        method: "POST",
        body: JSON.stringify({
          content,
          type: opts?.type ?? "comment",
          ...(opts?.parentId ? { parent_id: opts.parentId } : {}),
          ...(opts?.attachmentIds ? { attachment_ids: opts.attachmentIds } : {}),
        }),
      },
      { endpoint: "createComment" },
    );
  }

  // PUT /api/comments/:id — content edit (+ optional attachment swap).
  async updateComment(
    commentId: string,
    content: string,
    attachmentIds?: string[],
    contentBase?: string,
  ): Promise<Comment> {
    return this.fetchValidatedWith(
      `/api/comments/${commentId}`,
      CommentSchema,
      EMPTY_COMMENT,
      {
        method: "PUT",
        body: JSON.stringify(
          buildCommentUpdateBody(content, attachmentIds, contentBase),
        ),
      },
      { endpoint: "updateComment" },
    );
  }

  // DELETE /api/comments/:id — 204 No Content on success; this.fetch
  // already short-circuits 204 → undefined.
  async deleteComment(commentId: string): Promise<void> {
    await this.fetch<void>(`/api/comments/${commentId}`, { method: "DELETE" });
  }

  // POST /api/comments/:id/resolve — marks the thread root resolved; only
  // meaningful for root comments. Backend mirrors web semantics.
  async resolveComment(commentId: string): Promise<Comment> {
    return this.fetchValidatedWith(
      `/api/comments/${commentId}/resolve`,
      CommentSchema,
      EMPTY_COMMENT,
      { method: "POST" },
      { endpoint: "resolveComment" },
    );
  }

  // DELETE /api/comments/:id/resolve — un-resolves the thread.
  async unresolveComment(commentId: string): Promise<Comment> {
    return this.fetchValidatedWith(
      `/api/comments/${commentId}/resolve`,
      CommentSchema,
      EMPTY_COMMENT,
      { method: "DELETE" },
      { endpoint: "unresolveComment" },
    );
  }

  // --- Reactions ---
  // Comment reactions: POST/DELETE /api/comments/{id}/reactions
  // Issue reactions:   POST/DELETE /api/issues/{id}/reactions
  // Mirror surface from packages/core/api/client.ts:541-573.
  async addReaction(commentId: string, emoji: string): Promise<Reaction> {
    return this.fetch<Reaction>(`/api/comments/${commentId}/reactions`, {
      method: "POST",
      body: JSON.stringify({ emoji }),
    });
  }

  async removeReaction(commentId: string, emoji: string): Promise<void> {
    await this.fetch<void>(`/api/comments/${commentId}/reactions`, {
      method: "DELETE",
      body: JSON.stringify({ emoji }),
    });
  }

  async addIssueReaction(
    issueId: string,
    emoji: string,
  ): Promise<IssueReaction> {
    return this.fetch<IssueReaction>(`/api/issues/${issueId}/reactions`, {
      method: "POST",
      body: JSON.stringify({ emoji }),
    });
  }

  async removeIssueReaction(issueId: string, emoji: string): Promise<void> {
    await this.fetch<void>(`/api/issues/${issueId}/reactions`, {
      method: "DELETE",
      body: JSON.stringify({ emoji }),
    });
  }

  // --- Issue update ---
  // Write endpoint — the mutation surface handles errors via rollback, so
  // we let bad responses surface naturally (no parseWithFallback).
  // Method is PUT to match backend router (server/cmd/server/router.go:327)
  // and web client (packages/core/api/client.ts:465).
  async updateIssue(id: string, body: UpdateIssueRequest): Promise<Issue> {
    return this.fetch<Issue>(`/api/issues/${id}`, {
      method: "PUT",
      body: JSON.stringify(body),
    });
  }

  // Backend returns 204 No Content on success
  // (server/internal/handler/issue.go DeleteIssue). this.fetch already
  // short-circuits 204 → undefined (api.ts:270), so no body parsing needed.
  async deleteIssue(id: string): Promise<void> {
    await this.fetch<void>(`/api/issues/${id}`, { method: "DELETE" });
  }

  // --- Decision cards (RUYI-345) ---
  // Mirrors packages/core/api/client.ts listIssueDecisions /
  // answerIssueDecision / cancelIssueDecision. Schemas are shared from
  // @multica/core/api/schemas; answer/cancel carry a body so they go
  // through this.fetch + parseWithFallback directly (fetchValidated is
  // GET-only, see its docstring).
  async listIssueDecisions(issueId: string): Promise<IssueDecision[]> {
    const raw = await this.fetch<unknown>(`/api/issues/${issueId}/decisions`);
    return parseWithFallback(raw, IssueDecisionsListSchema, [], {
      endpoint: "GET /api/issues/:id/decisions",
    });
  }

  async answerIssueDecision(
    issueId: string,
    decisionId: string,
    selectedIndices: number[],
  ): Promise<IssueDecision> {
    const raw = await this.fetch<unknown>(
      `/api/issues/${issueId}/decisions/${decisionId}/answer`,
      {
        method: "POST",
        body: JSON.stringify({ selected_indices: selectedIndices }),
      },
    );
    const decision = parseWithFallback(raw, IssueDecisionSchema, null, {
      endpoint: "POST /api/issues/:id/decisions/:decisionId/answer",
    });
    if (!decision) throw new Error("Invalid decision answer response");
    return decision;
  }

  // Batch answer (RUYI-471): mirrors core's answerIssueDecisionsBatch.
  // Per-card outcomes never roll the batch back — callers inspect `results`.
  async answerIssueDecisionsBatch(
    issueId: string,
    answers: BatchIssueDecisionAnswer[],
  ): Promise<BatchDecisionAnswerResult> {
    const raw = await this.fetch<unknown>(
      `/api/issues/${issueId}/decisions/answer-batch`,
      { method: "POST", body: JSON.stringify({ answers }) },
    );
    return parseWithFallback(raw, BatchDecisionAnswersSchema, { results: [] }, {
      endpoint: "POST /api/issues/:id/decisions/answer-batch",
    });
  }

  async cancelIssueDecision(
    issueId: string,
    decisionId: string,
  ): Promise<IssueDecision> {
    const raw = await this.fetch<unknown>(
      `/api/issues/${issueId}/decisions/${decisionId}/cancel`,
      { method: "POST", body: JSON.stringify({}) },
    );
    const decision = parseWithFallback(raw, IssueDecisionSchema, null, {
      endpoint: "POST /api/issues/:id/decisions/:decisionId/cancel",
    });
    if (!decision) throw new Error("Invalid decision cancel response");
    return decision;
  }

  // Workspace-level decision inbox aggregation (RUYI-494). Mirrors
  // packages/core/api/client.ts listWorkspaceDecisionInbox — mobile-owned
  // fetch wrapper, shared zod schema, same query-string contract.
  async listWorkspaceDecisionInbox(
    workspaceId: string,
    params?: { status?: IssueDecision["status"]; limit?: number },
  ): Promise<WorkspaceDecisionInbox> {
    const qs = new URLSearchParams();
    if (params?.status) qs.set("status", params.status);
    if (params?.limit != null) qs.set("limit", String(params.limit));
    const query = qs.size > 0 ? `?${qs.toString()}` : "";
    const raw = await this.fetch<unknown>(
      `/api/workspaces/${workspaceId}/decision-inbox${query}`,
    );
    const inbox = parseWithFallback(raw, WorkspaceDecisionInboxSchema, null, {
      endpoint: "GET /api/workspaces/:id/decision-inbox",
    });
    if (!inbox) throw new Error("Invalid decision inbox response");
    return inbox;
  }

  // --- Labels ---
  async listLabels(opts?: {
    signal?: AbortSignal;
  }): Promise<ListLabelsResponse> {
    const raw = await this.fetch<unknown>("/api/labels", {
      signal: opts?.signal,
    });
    return parseWithFallback(
      raw,
      ListLabelsResponseSchema,
      EMPTY_LIST_LABELS_RESPONSE,
      { endpoint: "GET /api/labels" },
    );
  }

  // Create a new label and return it. Response is consumed by the
  // create-and-attach flow in label picker, so raw `this.fetch<Label>` is
  // used — same convention as createProject (cache rollback on failure is
  // preferable to a parseWithFallback fallback that would mask server errors).
  async createLabel(body: CreateLabelRequest): Promise<Label> {
    return this.fetch<Label>("/api/labels", {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  async attachLabel(
    issueId: string,
    labelId: string,
  ): Promise<IssueLabelsResponse> {
    return this.fetch<IssueLabelsResponse>(
      `/api/issues/${issueId}/labels`,
      {
        method: "POST",
        body: JSON.stringify({ label_id: labelId }),
      },
    );
  }

  async detachLabel(
    issueId: string,
    labelId: string,
  ): Promise<IssueLabelsResponse> {
    return this.fetch<IssueLabelsResponse>(
      `/api/issues/${issueId}/labels/${labelId}`,
      { method: "DELETE" },
    );
  }

  // --- Issue status catalog (MUL-6243) ---
  /**
   * The workspace's issue statuses — the 7 built-ins plus any custom ones an
   * admin defined. Reads are open to every workspace member; the catalog
   * mutations are owner/admin only and live on web's settings screen, which is
   * why mobile ships the read alone.
   *
   * `include_archived` is on by design. Archiving retires a status from FUTURE
   * assignment but leaves the issues already on it, and those issues must keep
   * their real name, colour and category — dropping archived rows here would
   * degrade them to a raw key with a guessed category. Pickers filter them out
   * via `IssueStatusCatalog.activeStatuses` instead.
   */
  async listIssueStatuses(
    includeArchived = false,
    opts?: { signal?: AbortSignal },
  ): Promise<ListIssueStatusesResponse> {
    const query = includeArchived ? "?include_archived=true" : "";
    return this.fetchValidated(
      `/api/issue-statuses${query}`,
      ListIssueStatusesResponseSchema,
      EMPTY_LIST_ISSUE_STATUSES_RESPONSE,
      { ...opts, endpoint: "GET /api/issue-statuses" },
    );
  }

  // --- Workspace quick replies (RUYI-435) ---
  /**
   * The workspace's quick-reply catalog — the templates the comment composer
   * offers behind its quick-reply button. Read-open to every member; the
   * mutations are owner/admin only and live on web's settings screen (plus
   * MCP), which is why mobile ships the read alone. An empty fallback renders
   * an empty menu, never a broken composer.
   */
  async listQuickReplies(opts?: { signal?: AbortSignal }): Promise<ListQuickRepliesResponse> {
    return this.fetchValidated(
      "/api/quick-replies",
      ListQuickRepliesResponseSchema,
      EMPTY_LIST_QUICK_REPLIES_RESPONSE,
      { ...opts, endpoint: "GET /api/quick-replies" },
    );
  }

  // --- Related pull requests (RUYI-43) ---
  /**
   * PRs linked to an issue (branch name / title / body referenced its
   * identifier) — the data behind the web sidebar's "Pull requests"
   * section (`issuePullRequestsOptions` in packages/core/github/queries.ts,
   * endpoint mirrors packages/core/api/client.ts listIssuePullRequests).
   * Schema + empty fallback come from @multica/core/api/schemas (pure
   * Zod, mobile sharing whitelist), like the issue-list endpoints above.
   */
  async listIssuePullRequests(
    issueId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<{ pull_requests: GitHubPullRequest[] }> {
    return this.fetchValidated(
      `/api/issues/${issueId}/pull-requests`,
      IssuePullRequestsResponseSchema,
      EMPTY_ISSUE_PULL_REQUESTS_RESPONSE,
      { ...opts, endpoint: "GET /api/issues/:id/pull-requests" },
    );
  }

  // --- Projects ---
  async listProjects(opts?: {
    signal?: AbortSignal;
  }): Promise<ListProjectsResponse> {
    const raw = await this.fetch<unknown>("/api/projects", {
      signal: opts?.signal,
    });
    return parseWithFallback(
      raw,
      ListProjectsResponseSchema,
      EMPTY_LIST_PROJECTS_RESPONSE,
      { endpoint: "GET /api/projects" },
    );
  }

  /** Workspace-wide project search. See `searchIssues` for the signal
   *  contract. */
  async searchProjects(
    params: { q: string; limit?: number; include_closed?: boolean; offset?: number },
    opts?: { signal?: AbortSignal },
  ): Promise<SearchProjectsResponse> {
    const search = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) {
      if (v == null) continue;
      search.set(k, String(v));
    }
    const raw = await this.fetch<unknown>(
      `/api/projects/search?${search.toString()}`,
      { signal: opts?.signal },
    );
    return parseWithFallback(
      raw,
      SearchProjectsResponseSchema,
      EMPTY_SEARCH_PROJECTS_RESPONSE,
      { endpoint: "GET /api/projects/search" },
    );
  }

  async getProject(
    id: string,
    opts?: { signal?: AbortSignal },
  ): Promise<Project> {
    const raw = await this.fetch<unknown>(`/api/projects/${id}`, {
      signal: opts?.signal,
    });
    // Drift-safe parse — UI checks `data.id === ""` to render the
    // "project not found / shape drifted" error state instead of a
    // half-populated detail page.
    return parseWithFallback(raw, ProjectSchema, EMPTY_PROJECT, {
      endpoint: "GET /api/projects/:id",
    });
  }

  // Write endpoints — no parseWithFallback (mirrors updateIssue:430). A
  // malformed write response surfaces as an error so the optimistic
  // patch rolls back; pretending the write succeeded with empty data
  // would silently desync caches.
  async createProject(body: CreateProjectRequest): Promise<Project> {
    return this.fetch<Project>("/api/projects", {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  async updateProject(
    id: string,
    body: UpdateProjectRequest,
  ): Promise<Project> {
    return this.fetch<Project>(`/api/projects/${id}`, {
      method: "PUT",
      body: JSON.stringify(body),
    });
  }

  async deleteProject(id: string): Promise<void> {
    await this.fetch<void>(`/api/projects/${id}`, { method: "DELETE" });
  }

  // --- Project resources ---
  async listProjectResources(
    projectId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<ListProjectResourcesResponse> {
    const raw = await this.fetch<unknown>(
      `/api/projects/${projectId}/resources`,
      { signal: opts?.signal },
    );
    return parseWithFallback(
      raw,
      ListProjectResourcesResponseSchema,
      EMPTY_LIST_PROJECT_RESOURCES_RESPONSE,
      { endpoint: "GET /api/projects/:id/resources" },
    );
  }

  async createProjectResource(
    projectId: string,
    body: CreateProjectResourceRequest,
  ): Promise<ProjectResource> {
    return this.fetch<ProjectResource>(
      `/api/projects/${projectId}/resources`,
      {
        method: "POST",
        body: JSON.stringify(body),
      },
    );
  }

  async deleteProjectResource(
    projectId: string,
    resourceId: string,
  ): Promise<void> {
    await this.fetch<void>(
      `/api/projects/${projectId}/resources/${resourceId}`,
      { method: "DELETE" },
    );
  }

  // --- Chat ---
  // Mirrors the surface area of packages/core/api/client.ts chat methods.
  // v1 omitted getChatSession + updateChatSession (rename) — the session
  // management writes (update / pin / archive) landed in RUYI-51; the
  // single-row getChatSession read remains omitted (mobile reads the list
  // cache instead).

  async listChatSessions(
    opts?: { status?: string; signal?: AbortSignal },
  ): Promise<ChatSession[]> {
    const search = new URLSearchParams();
    if (opts?.status) search.set("status", opts.status);
    const suffix = search.size > 0 ? `?${search.toString()}` : "";
    const raw = await this.fetch<unknown>(`/api/chat/sessions${suffix}`, {
      signal: opts?.signal,
    });
    return parseWithFallback(
      raw,
      ChatSessionListSchema,
      EMPTY_CHAT_SESSION_LIST,
      { endpoint: "GET /api/chat/sessions" },
    );
  }

  async createChatSession(
    data: { agent_id: string; title?: string },
  ): Promise<ChatSession> {
    // Strict parse — a malformed create response derails the optimistic
    // burst (we need the new session id to seed caches). Fallback would
    // be worse than the throw.
    const raw = await this.fetch<unknown>("/api/chat/sessions", {
      method: "POST",
      body: JSON.stringify(data),
    });
    const parsed = ChatSessionSchema.safeParse(raw);
    if (!parsed.success) {
      console.error("[api] ← shape mismatch POST /api/chat/sessions", {
        issues: parsed.error.issues,
      });
      throw new ApiError("Create chat session response invalid", 0, raw);
    }
    return parsed.data;
  }

  async deleteChatSession(id: string): Promise<void> {
    await this.fetch<void>(`/api/chat/sessions/${id}`, { method: "DELETE" });
  }

  async listChatMessages(
    sessionId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<ChatMessage[]> {
    const raw = await this.fetch<unknown>(
      `/api/chat/sessions/${sessionId}/messages`,
      { signal: opts?.signal },
    );
    return parseWithFallback(
      raw,
      ChatMessageListSchema,
      EMPTY_CHAT_MESSAGE_LIST,
      { endpoint: "GET /api/chat/sessions/:id/messages" },
    );
  }

  async sendChatMessage(
    sessionId: string,
    content: string,
    opts?: { attachmentIds?: string[] },
  ): Promise<SendChatMessageResponse> {
    // Strict parse — we need task_id + created_at to anchor the optimistic
    // StatusPill. Fallback would silently break the elapsed-time timer.
    //
    // `attachment_ids` mirrors the comment / issue create payloads —
    // server-side `chat.go` back-fills `chat_message_id` on the listed
    // attachments after the message row is inserted (see
    // server/internal/handler/chat.go:410-456).
    const body: { content: string; attachment_ids?: string[] } = { content };
    if (opts?.attachmentIds && opts.attachmentIds.length > 0) {
      body.attachment_ids = opts.attachmentIds;
    }
    const raw = await this.fetch<unknown>(
      `/api/chat/sessions/${sessionId}/messages`,
      {
        method: "POST",
        body: JSON.stringify(body),
      },
    );
    const parsed = SendChatMessageResponseSchema.safeParse(raw);
    if (!parsed.success) {
      console.error("[api] ← shape mismatch POST /api/chat/sessions/:id/messages", {
        issues: parsed.error.issues,
      });
      throw new ApiError("Send message response invalid", 0, raw);
    }
    return parsed.data;
  }

  async getPendingChatTask(
    sessionId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<ChatPendingTask> {
    const raw = await this.fetch<unknown>(
      `/api/chat/sessions/${sessionId}/pending-task`,
      { signal: opts?.signal },
    );
    return parseWithFallback(
      raw,
      ChatPendingTaskSchema,
      EMPTY_CHAT_PENDING_TASK,
      { endpoint: "GET /api/chat/sessions/:id/pending-task" },
    );
  }

  async markChatSessionRead(sessionId: string): Promise<void> {
    await this.fetch<void>(
      `/api/chat/sessions/${sessionId}/read`,
      { method: "POST" },
    );
  }

  // Session-management writes (rename / pin / archive) — RUYI-51. Mirror
  // web's surface in packages/core/api/client.ts; responses are not consumed
  // by UI logic (mutations patch the cache optimistically and re-invalidates
  // on settle), so a raw `as void` fetch is the accepted form here per the
  // helper table in apps/mobile/CLAUDE.md.
  async updateChatSession(
    id: string,
    data: { title: string },
  ): Promise<void> {
    await this.fetch<void>(`/api/chat/sessions/${id}`, {
      method: "PATCH",
      body: JSON.stringify(data),
    });
  }

  async setChatSessionPinned(id: string, pinned: boolean): Promise<void> {
    await this.fetch<void>(`/api/chat/sessions/${id}/pin`, {
      method: "PATCH",
      body: JSON.stringify({ pinned }),
    });
  }

  async setChatSessionArchived(id: string, archived: boolean): Promise<void> {
    await this.fetch<void>(`/api/chat/sessions/${id}/archive`, {
      method: "PATCH",
      body: JSON.stringify({ archived }),
    });
  }

  async cancelTaskById(taskId: string): Promise<void> {
    await this.fetch<void>(`/api/tasks/${taskId}/cancel`, { method: "POST" });
  }

  // Full per-agent task list (active + terminal) — RUYI-538 ② run history.
  // Mirrors packages/core/api/client.ts listAgentTasks: GET
  // /api/agents/{id}/tasks behind the server's private-agent access gate.
  // The server resolves the workspace from the agent row, so no workspace
  // header is involved.
  async listAgentTasks(
    agentId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<AgentTask[]> {
    const raw = await this.fetch<unknown>(`/api/agents/${agentId}/tasks`, {
      signal: opts?.signal,
    });
    return parseWithFallback(raw, AgentTaskListSchema, EMPTY_AGENT_TASK_LIST, {
      endpoint: "listAgentTasks",
    });
  }

  // Task retry entries (RUYI-343). Mirrors packages/core/api/client.ts —
  // retryIssueRun is the RUYI-292 run-level endpoint whose anti-storm gates
  // answer structured 409s ({code, message, task}); rerunIssue is the
  // legacy issue-level rerun, which MUST carry task_id or the server falls
  // back to the issue's current assignee and can wake the wrong agent.
  async retryIssueRun(issueId: string, runId: string): Promise<AgentTask> {
    return this.fetch<AgentTask>(
      `/api/issues/${issueId}/tasks/${runId}/retry`,
      { method: "POST" },
    );
  }

  async rerunIssue(issueId: string, taskId?: string): Promise<AgentTask> {
    return this.fetch<AgentTask>(`/api/issues/${issueId}/rerun`, {
      method: "POST",
      body: JSON.stringify(taskId ? { task_id: taskId } : {}),
    });
  }

  /** Live execution timeline for a task — used by the chat screen to
   *  render the "thinking → tool_use → tool_result → final text" trace
   *  beneath an in-flight assistant bubble. `task:message` WS events
   *  append to the same cache key in real time (see
   *  use-chat-session-realtime.ts). */
  async listTaskMessages(
    taskId: string,
    opts?: { signal?: AbortSignal },
  ): Promise<TaskMessagePayload[]> {
    return this.fetchValidated(
      `/api/tasks/${taskId}/messages`,
      TaskMessageListSchema,
      EMPTY_TASK_MESSAGE_LIST,
      { ...opts, endpoint: "GET /api/tasks/:id/messages" },
    );
  }

  // --- Pins ---
  //
  // Pin metadata only — title / status / icon for each row come from
  // `issueDetailOptions` / `projectDetailOptions` on the consumer side.
  // Endpoints mirror packages/core/api/client.ts:1551-1572.

  async listPins(opts?: { signal?: AbortSignal }): Promise<PinnedItem[]> {
    return this.fetchValidated(
      "/api/pins",
      PinListSchema,
      EMPTY_PIN_LIST,
      { ...opts, endpoint: "listPins" },
    );
  }

  async createPin(data: {
    item_type: PinnedItemType;
    item_id: string;
  }): Promise<PinnedItem> {
    return this.fetchValidatedWith(
      "/api/pins",
      PinnedItemSchema,
      // Mirror EMPTY_PIN_LIST element shape — onSuccess uses the returned
      // pin's id/position so a stub with empty id is detectable downstream.
      {
        id: "",
        workspace_id: "",
        user_id: "",
        item_type: data.item_type,
        item_id: data.item_id,
        position: 0,
        created_at: "",
      },
      { method: "POST", body: JSON.stringify(data) },
      { endpoint: "createPin" },
    );
  }

  async deletePin(itemType: PinnedItemType, itemId: string): Promise<void> {
    await this.fetch<void>(`/api/pins/${itemType}/${itemId}`, {
      method: "DELETE",
    });
  }

  async reorderPins(data: ReorderPinsRequest): Promise<void> {
    await this.fetch<void>("/api/pins/reorder", {
      method: "PUT",
      body: JSON.stringify(data),
    });
  }

  // --- File Upload ---

  /**
   * Multipart-stream a file to `/api/upload-file`. Mirrors the web
   * implementation in `packages/core/api/client.ts:uploadFile` but with the
   * RN-shaped `FileAsset` instead of a browser `File`. The fetch FormData
   * polyfill recognises `{ uri, name, type }` and reads the file off disk.
   *
   * `opts.issueId` / `opts.commentId` link the attachment record. Pass
   * `issueId` when uploading from a comment composer / reply input; leave
   * both empty when uploading from a not-yet-created issue (the attachment
   * is hooked to the issue once it's created — same flow as web).
   *
   * Does NOT use `this.fetch` because:
   *   - FormData must not have a `Content-Type` header preset (the browser /
   *     RN fetch needs to set the multipart boundary itself).
   *   - `this.fetch` hard-codes `application/json`.
   *
   * So we re-implement the auth + slug + logging shell inline.
   *
   * Budgeted at UPLOAD_TIMEOUT_MS (120s — the 100MB ceiling over a weak
   * uplink) and honouring an optional caller `signal`, using the same
   * manual AbortController composition as fetchRaw: Hermes has neither
   * AbortSignal.timeout() nor AbortSignal.any(). Our own timeout abort
   * surfaces as a status-0 timeout ApiError; a caller abort propagates
   * as-is so callers can tell "gave up" from "failed" (same contract as
   * fetchRaw, and as the web coordinator in packages/core).
   */
  async uploadFile(
    asset: FileAsset,
    opts?: { issueId?: string; commentId?: string; signal?: AbortSignal },
  ): Promise<Attachment> {
    const rid = createRequestId();
    const start = Date.now();
    const path = "/api/upload-file";

    const headers: Record<string, string> = {
      // No Content-Type — let fetch set the multipart boundary.
      "X-Client-Platform": "mobile",
      "X-Client-OS": "ios",
      "X-Client-Version": "0.1.0",
      "X-Request-ID": rid,
    };
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    const slug = getCurrentSlug();
    if (slug) headers["X-Workspace-Slug"] = slug;

    const formData = new FormData();
    // RN's FormData accepts `{ uri, name, type }` as the file value.
    // `as never` quiets TS (the global FormData type expects `Blob | string`).
    formData.append(
      "file",
      { uri: asset.uri, name: asset.name, type: asset.type } as never,
    );
    if (opts?.issueId) formData.append("issue_id", opts.issueId);
    if (opts?.commentId) formData.append("comment_id", opts.commentId);

    // Timeout + caller-signal forwarding — same manual composition as
    // fetchRaw above (Hermes lacks AbortSignal.timeout()/any()).
    const controller = new AbortController();
    const timeoutId = setTimeout(() => {
      // 超时后 RN fetch 以 AbortError reject；当调用方未取消时下面的
      // catch 会把它改写为可区分的超时 ApiError。abort reason 带译文，
      // 供按 reason 透传的运行时直接展示。
      controller.abort(
        new Error(
          i18n.t(
            "common:mobile.common.upload_timeout",
            "Upload timed out after {{seconds}}s",
            { seconds: Math.round(UPLOAD_TIMEOUT_MS / 1000) },
          ),
        ),
      );
    }, UPLOAD_TIMEOUT_MS);
    const callerSignal = opts?.signal;
    const onCallerAbort = () => controller.abort(callerSignal?.reason);
    if (callerSignal) {
      if (callerSignal.aborted) controller.abort(callerSignal.reason);
      else callerSignal.addEventListener("abort", onCallerAbort);
    }

    console.log(`[api] → POST ${path}`, { rid, filename: asset.name });

    let res: Response;
    try {
      res = await fetch(`${getApiUrl()}${path}`, {
        method: "POST",
        headers,
        body: formData,
        signal: controller.signal,
      });
    } catch (err) {
      clearTimeout(timeoutId);
      callerSignal?.removeEventListener("abort", onCallerAbort);
      // 与 fetchRaw 相同的分类：我们自己的超时中止 → 状态 0 的超时
      // ApiError；调用方主动取消 → 原样透传，不误标为超时。
      if (
        err instanceof Error &&
        err.name === "AbortError" &&
        !callerSignal?.aborted
      ) {
        const duration = Date.now() - start;
        console.warn(`[api] ← UPLOAD TIMEOUT ${path}`, {
          rid,
          duration: `${duration}ms`,
        });
        throw new ApiError(
          `Upload timed out after ${UPLOAD_TIMEOUT_MS}ms`,
          0,
          undefined,
        );
      }
      throw err;
    }
    clearTimeout(timeoutId);
    callerSignal?.removeEventListener("abort", onCallerAbort);
    const duration = Date.now() - start;

    if (!res.ok) {
      if (res.status === 401) this.options.onUnauthorized?.();
      let body: unknown;
      try {
        body = await res.json();
      } catch {
        body = undefined;
      }
      const message =
        (body && typeof body === "object" && "message" in body
          ? String((body as { message: unknown }).message)
          : null) ?? `Upload failed: ${res.status}`;
      console.error(`[api] ← ${res.status} ${path}`, {
        rid,
        duration: `${duration}ms`,
        error: message,
      });
      throw new ApiError(message, res.status, body);
    }

    console.log(`[api] ← ${res.status} ${path}`, {
      rid,
      duration: `${duration}ms`,
    });

    // Strict validation: parseWithFallback's silent-fallback pattern doesn't
    // fit here — an attachment without a `url` would be inserted into the
    // user's text as `![](undefined)`. Throw on shape mismatch so the
    // caller's Alert path fires instead of letting a broken link land in
    // the editor.
    const json: unknown = await res.json();
    const parsed = AttachmentSchema.safeParse(json);
    if (!parsed.success) {
      console.error(`[api] ← shape mismatch ${path}`, {
        rid,
        error: parsed.error.message,
      });
      throw new ApiError("Upload response invalid", res.status, json);
    }
    return parsed.data;
  }
}

export { MAX_FILE_SIZE };

export const api = new ApiClient();
