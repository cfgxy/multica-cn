/**
 * @vitest-environment jsdom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { WSClient } from "../api/ws-client";
import { issueKeys } from "../issues/queries";
import type { IssueDecision } from "../types";
import { useRealtimeSync, type RealtimeSyncStores } from "./use-realtime-sync";

vi.mock("../platform/workspace-storage", () => ({
  getCurrentWsId: () => "ws-1",
  getCurrentSlug: () => "test-ws",
  createWorkspaceAwareStorage: (adapter: unknown) => adapter,
  registerForWorkspaceRehydration: () => {},
}));

vi.mock("../paths", () => ({
  useHasOnboarded: () => true,
  resolvePostAuthDestination: () => "/",
}));

// Same recording-ws harness as use-realtime-sync-comment.test.tsx: capture the
// registered handler per event so a test can fire one directly.
function createRecordingWs(): {
  ws: WSClient;
  handlers: Record<string, (p: unknown) => void>;
} {
  const handlers: Record<string, (p: unknown) => void> = {};
  const ws = {
    on: vi.fn((event: string, handler: (p: unknown) => void) => {
      handlers[event] = handler;
      return () => {};
    }),
    onAny: vi.fn(() => () => {}),
    onReconnect: vi.fn(() => () => {}),
  } as unknown as WSClient;
  return { ws, handlers };
}

function createStores(): RealtimeSyncStores {
  return {
    authStore: Object.assign(() => ({}), {
      getState: () => ({ user: { id: "u1" } }),
      subscribe: () => () => {},
      setState: () => {},
      destroy: () => {},
    }),
  } as unknown as RealtimeSyncStores;
}

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function decisionFixture(id: string, createdAt: string): IssueDecision {
  return {
    id,
    issue_id: "issue-1",
    source_comment_id: null,
    question: `q-${id}`,
    options: [{ label: "A" }],
    multi_select: false,
    recommended_indices: [],
    status: "open",
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "agent",
    created_by_id: "agent-1",
    created_at: createdAt,
    updated_at: createdAt,
  };
}

const decisionsKey = issueKeys.decisions("issue-1");

describe("useRealtimeSync — decision:updated cache coherence", () => {
  let qc: QueryClient;
  let rec: ReturnType<typeof createRecordingWs>;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    rec = createRecordingWs();
    renderHook(() => useRealtimeSync(rec.ws, createStores()), {
      wrapper: createWrapper(qc),
    });
  });

  afterEach(() => {
    qc.clear();
    vi.clearAllMocks();
  });

  it("inserts a decision:updated for a card the cache has never seen (created while the issue was open)", () => {
    const known = decisionFixture("d-1", "2026-10-03T01:00:00Z");
    qc.setQueryData<IssueDecision[]>(decisionsKey, [known]);

    expect(rec.handlers["decision:updated"]).toBeTypeOf("function");
    const created = decisionFixture("d-2", "2026-10-03T02:00:00Z");
    rec.handlers["decision:updated"]?.({
      decision: created,
      issue_id: "issue-1",
    });

    const cached = qc.getQueryData<IssueDecision[]>(decisionsKey);
    expect(cached?.map((d) => d.id)).toEqual(["d-1", "d-2"]);
  });

  it("replaces an existing card in place (answered/cancelled transitions)", () => {
    const open = decisionFixture("d-1", "2026-10-03T01:00:00Z");
    qc.setQueryData<IssueDecision[]>(decisionsKey, [open]);

    const answered = {
      ...decisionFixture("d-1", "2026-10-03T01:00:00Z"),
      status: "answered" as const,
      selected_indices: [1],
      answered_by_type: "member",
      answered_by_id: "u1",
    };
    rec.handlers["decision:updated"]?.({
      decision: answered,
      issue_id: "issue-1",
    });

    const cached = qc.getQueryData<IssueDecision[]>(decisionsKey);
    expect(cached).toHaveLength(1);
    expect(cached?.[0]?.status).toBe("answered");
    expect(cached?.[0]?.selected_indices).toEqual([1]);
  });

  it("ignores malformed payloads", () => {
    const known = decisionFixture("d-1", "2026-10-03T01:00:00Z");
    qc.setQueryData<IssueDecision[]>(decisionsKey, [known]);

    expect(rec.handlers["decision:updated"]).toBeTypeOf("function");
    expect(() => rec.handlers["decision:updated"]?.({})).not.toThrow();
    expect(() =>
      rec.handlers["decision:updated"]?.({ decision: known }),
    ).not.toThrow();
    const cached = qc.getQueryData<IssueDecision[]>(decisionsKey);
    expect(cached?.map((d) => d.id)).toEqual(["d-1"]);
  });
});
