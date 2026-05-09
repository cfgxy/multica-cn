/**
 * Mobile-owned fetch wrapper. Mirrors the surface area of
 * packages/core/api/client.ts that mobile actually uses, but lives in
 * apps/mobile/ so we control retry/timeout/error handling independently.
 *
 * Types are imported via `import type` from @multica/core/types — zero
 * runtime coupling.
 */
import type {
  Agent,
  InboxItem,
  Issue,
  ListIssuesParams,
  ListIssuesResponse,
  MemberWithUser,
  User,
  Workspace,
} from "@multica/core/types";
import { getCurrentSlug } from "./workspace-store";

const API_URL = process.env.EXPO_PUBLIC_API_URL;

if (!API_URL) {
  throw new Error(
    "EXPO_PUBLIC_API_URL is not set. Add it to apps/mobile/.env.development.local " +
      "(see apps/mobile/.env.staging for an example).",
  );
}

export interface LoginResponse {
  token: string;
  user: User;
}

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

class ApiClient {
  private token: string | null = null;

  setToken(token: string | null) {
    this.token = token;
  }

  private async fetch<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      "X-Client-Platform": "mobile",
      "X-Client-OS": "ios",
      "X-Client-Version": "0.1.0",
      ...((init.headers as Record<string, string>) ?? {}),
    };
    if (this.token) {
      headers["Authorization"] = `Bearer ${this.token}`;
    }
    // Inject current workspace slug. Backend middleware
    // (server/internal/middleware/workspace.go) resolves slug → ws UUID and
    // gates membership. Mirrors packages/core/api/client.ts:220.
    const slug = getCurrentSlug();
    if (slug) {
      headers["X-Workspace-Slug"] = slug;
    }

    const res = await fetch(`${API_URL}${path}`, { ...init, headers });

    if (!res.ok) {
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
      throw new ApiError(message, res.status, body);
    }

    if (res.status === 204) return undefined as T;
    return (await res.json()) as T;
  }

  // --- Auth ---
  async sendCode(email: string): Promise<void> {
    await this.fetch<void>("/auth/send-code", {
      method: "POST",
      body: JSON.stringify({ email }),
    });
  }

  async verifyCode(email: string, code: string): Promise<LoginResponse> {
    return this.fetch<LoginResponse>("/auth/verify-code", {
      method: "POST",
      body: JSON.stringify({ email, code }),
    });
  }

  async getMe(): Promise<User> {
    return this.fetch<User>("/api/me");
  }

  // --- Workspaces ---
  async listWorkspaces(): Promise<Workspace[]> {
    return this.fetch<Workspace[]>("/api/workspaces");
  }

  // --- Inbox ---
  async listInbox(): Promise<InboxItem[]> {
    return this.fetch<InboxItem[]>("/api/inbox");
  }

  async markInboxRead(id: string): Promise<InboxItem> {
    return this.fetch<InboxItem>(`/api/inbox/${id}/read`, { method: "POST" });
  }

  // --- Members & Agents (for actor name/avatar lookup) ---
  async listMembers(workspaceId: string): Promise<MemberWithUser[]> {
    return this.fetch<MemberWithUser[]>(`/api/workspaces/${workspaceId}/members`);
  }

  async listAgents(): Promise<Agent[]> {
    return this.fetch<Agent[]>("/api/agents");
  }

  // --- Issues ---
  async listIssues(params: ListIssuesParams = {}): Promise<ListIssuesResponse> {
    const search = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) {
      if (v == null) continue;
      if (Array.isArray(v)) {
        for (const item of v) search.append(k, String(item));
      } else {
        search.set(k, String(v));
      }
    }
    const qs = search.toString();
    return this.fetch<ListIssuesResponse>(
      `/api/issues${qs ? `?${qs}` : ""}`,
    );
  }
}

export const api = new ApiClient();
