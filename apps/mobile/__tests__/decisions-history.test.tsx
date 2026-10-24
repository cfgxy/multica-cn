// @ts-nocheck
import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { DecisionRequest } from "@multica/core/types";
import Decisions from "@/app/(app)/[workspace]/(tabs)/decisions";

/**
 * RUYI-864 rework — mobile lane of the history-entry regression
 * (three-end parity with web's decision-center-page.test.tsx):
 *   1. the entry's total is the server's `history` count (every
 *      non-pending row) — "no pending, only approved" keeps the entry
 *      visible while the top queue (status=pending) is empty;
 *   2. the expanded rows arrive ONLY through the server's terminal-state
 *      window (status=history, keyset-paged): the screen must never call
 *      the requests list a second time with a desc&limit=50 walk plus a
 *      client-side pending strip.
 *
 * Real TQ + real options + a mocked `@/data/api` — the filter arguments
 * asserted below are the REAL options' serialization, not a re-implementation
 * of them (the api fake records every listDecisionRequests call).
 */

const mockRouterPush = jest.fn();
jest.mock("expo-router", () => ({
  router: { push: (...args: unknown[]) => mockRouterPush(...args), back: jest.fn() },
}));

const mockRequestsData = { value: null as unknown };
const mockRequestsCalls: unknown[] = [];
const mockHistoryPages: unknown[] = [];
const mockInboxPage = {
  items: [],
  counts: { open: 0, answered: 0, cancelled: 0 },
  next_cursor: null,
  next_cursor_id: null,
};

jest.mock("@/data/api", () => ({
  api: {
    listWorkspaceDecisionInbox: jest.fn(() => Promise.resolve(mockInboxPage)),
    listDecisionRequests: jest.fn((_wsId: string, filters: Record<string, unknown> = {}) => {
      mockRequestsCalls.push(filters);
      if (filters.status === "pending") {
        return Promise.resolve(mockRequestsData.value);
      }
      // The terminal-state window pages through the cursor pair: the first
      // page has no after_id; a paged call carries it. A missing page is
      // the empty list, never a hang.
      const page = mockHistoryPages[filters.after_id ? 1 : 0];
      return Promise.resolve(
        page ?? {
          items: [],
          counts: { pending: 0, approved: 0, closed: 0, executed: 0, history: 0 },
          next_cursor: null,
          next_cursor_id: null,
        },
      );
    }),
    getDecisionRequest: jest.fn().mockResolvedValue({ request: {}, steps: [] }),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector) =>
    selector({ currentWorkspaceId: "ws-1", currentWorkspaceSlug: "acme" }),
}));

jest.mock("@/data/stores/decisions-view-store", () => {
  const state = { tab: "all", wsId: "ws-1", setTab: jest.fn(), syncWorkspace: jest.fn() };
  const useStore = (selector: (s: typeof state) => unknown) => selector(state);
  return { useDecisionsViewStore: Object.assign(useStore, { getState: () => state }) };
});

jest.mock("@expo/vector-icons", () => ({ Ionicons: () => null }));

jest.mock("react-native-keyboard-controller", () => {
  const R = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    KeyboardAvoidingView: ({ children }) => R.createElement(View, null, children),
  };
});

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

jest.mock("@/components/ui/button", () => {
  const R = jest.requireActual<typeof import("react")>("react");
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Button: ({ variant: _v, size: _s, ...props }) => R.createElement(Pressable, props),
  };
});

jest.mock("@/components/ui/skeleton", () => {
  const R = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Skeleton: (props: object) => R.createElement(View, { testID: "skeleton", ...props }) };
});

jest.mock("@/components/ui/header", () => {
  const R = jest.requireActual<typeof import("react")>("react");
  const { View, Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Header: ({ title }: { title: string }) =>
      R.createElement(View, { testID: "decisions-header" }, R.createElement(Text, null, title)),
  };
});

jest.mock("@/components/ui/text-field", () => {
  const R = jest.requireActual<typeof import("react")>("react");
  const { TextInput } = jest.requireActual<typeof import("react-native")>("react-native");
  return { TextField: (props: object) => R.createElement(TextInput, props) };
});

jest.mock("@/components/decision/decision-inbox-row", () => {
  const R = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    DecisionInboxRow: ({ row }) =>
      R.createElement(View, { testID: `decisions-inbox-row-${row.id}` }, [String(row.id)]),
  };
});

jest.mock("@/components/decision/decision-request-row", () => {
  const R = jest.requireActual<typeof import("react")>("react");
  const { View, Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    DecisionRequestRow: ({ request }: { request: DecisionRequest }) =>
      R.createElement(
        View,
        { testID: `decisions-request-row-${request.id}` },
        R.createElement(Text, null, request.title),
      ),
  };
});

jest.mock("@/lib/decision-inbox-display", () => ({
  DECISION_TAB_ORDER: ["all", "open", "answered", "cancelled"],
  groupDecisionInboxSections: (pages: { items: { id: string; status: string }[] }[]) => {
    // The scenarios under test carry an empty inbox; mirror the real helper's
    // row-status bucketing for the one row a test might add.
    const rows = pages.flatMap((page) => page.items);
    return rows.length
      ? [{ key: "open", count: 0, rows }]
      : [];
  },
}));

jest.mock("@/lib/task-toolbar", () => ({ shouldWrapTaskPills: () => false }));
jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));
jest.mock("@/lib/theme", () => ({
  THEME: {
    light: { brand: "#00f", mutedForeground: "#666", secondary: "#eee" },
    dark: {},
  },
}));
jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (_key: string, fallback?: string) => fallback ?? _key }),
}));

function makeRequest(id: string, created: string, overrides: Partial<DecisionRequest> = {}): DecisionRequest {
  return {
    id,
    request_group_id: id,
    workspace_id: "ws-1",
    role: "origin",
    operable: true,
    status: "approved",
    action_type: "project_prompt_sync",
    risk_tier: "write_low",
    title: `Request ${id}`,
    detail: "",
    origin_workspace_id: "ws-1",
    origin_agent_id: "agent-1",
    origin_agent_name: "Seed Agent",
    origin_task_id: null,
    origin_issue_id: null,
    origin_issue_title: null,
    operator_tier: "owner",
    named_approver_ids: [],
    approve_label: null,
    deny_label: null,
    expires_at: "2026-10-25T00:00:00Z",
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_source: null,
    executed_at: null,
    execution_error: null,
    created_at: created,
    updated_at: created,
    ...overrides,
  } as unknown as DecisionRequest;
}

function list(
  items: DecisionRequest[],
  counts: Partial<{ pending: number; approved: number; closed: number; executed: number; history: number }> = {},
  cursor: { next_cursor: string | null; next_cursor_id: string | null } = {
    next_cursor: null,
    next_cursor_id: null,
  },
) {
  return {
    items,
    counts: { pending: 0, approved: 0, closed: 0, executed: 0, history: 0, ...counts },
    ...cursor,
  };
}

// RTL's render is async: setRenderResult only fires after its internal
// act() resolves. Awaiting it here is what attaches `screen` before the
// findBy* polls below (a non-awaited render leaves screen detached and
// findByTestId throws `render function has not been called`).
async function renderDecisions() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  await render(
    <QueryClientProvider client={queryClient}>
      <Decisions />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockRequestsData.value = list([]);
  mockRequestsCalls.length = 0;
  mockHistoryPages.length = 0;
  jest.clearAllMocks();
});

// Row-source helpers: the REAL options' serialization, recorded by the api
// fake — the pinned contract is "only pending-walk + history-window calls,
// never the legacy desc&limit=50 walk".
const pendingCalls = () =>
  mockRequestsCalls.filter((c: Record<string, unknown>) => c.status === "pending");
const historyCalls = () =>
  mockRequestsCalls.filter((c: Record<string, unknown>) => c.status === "history");

describe("Decisions tab history entry (RUYI-864 rework)", () => {
  it("keeps the entry visible for approved-only history when no pending row remains", async () => {
    // No pending rows — the top queue area is gone — but a consent is in
    // flight: one approved row, and ONLY closed/executed re-derivation (0)
    // or the queue's own items would hide the entry. The server's history
    // bucket is 1.
    mockRequestsData.value = list([], { approved: 1, history: 1 });
    mockHistoryPages.push(
      list([makeRequest("r-approved", "2026-10-18T00:00:00Z")], { approved: 1, history: 1 }),
    );
    await renderDecisions();

    await screen.findByTestId("decisions-history-toggle");
    // Queue absent, entry present, total from the history bucket.
    expect(screen.queryByText("Pending authorizations")).toBeNull();
    const toggle = screen.getByTestId("decisions-history-toggle");
    expect(screen.getByText("· 1")).toBeTruthy();

    // While collapsed, the terminal-state window is not even requested
    // (the options' enabled rides the open state).
    expect(historyCalls()).toHaveLength(0);

    // Expand: the approved row arrives from the terminal-state window.
    fireEvent.press(toggle);
    await screen.findByTestId("decisions-request-row-r-approved");
    // The expanded row never re-enters the queue.
    expect(screen.queryByText("Pending authorizations")).toBeNull();

    // Every requests-list call is a real-options walk: the queue's
    // oldest-pending, the lazy history window — and NEVER the legacy
    // desc&limit=50 client walk.
    pendingCalls().forEach((c: Record<string, unknown>) =>
      expect(c).toMatchObject({ status: "pending", order: "asc", limit: 1 }),
    );
    expect(historyCalls().length).toBeGreaterThan(0);
    historyCalls().forEach((c: Record<string, unknown>) =>
      expect(c).toMatchObject({ status: "history", order: "desc", limit: 20 }),
    );
    expect(mockRequestsCalls.some((c: Record<string, unknown>) => c.limit === 50)).toBe(false);
  });

  it("badges the entry total from the history bucket, not closed+executed", async () => {
    // The distribution the old client-side strip could not survive: several
    // in-flight consents (approved), zero terminal rows.
    mockRequestsData.value = list([], { approved: 3, history: 3 });
    mockHistoryPages.push(
      list(
        [
          makeRequest("a1", "2026-10-15T00:00:00Z"),
          makeRequest("a2", "2026-10-16T00:00:00Z"),
          makeRequest("a3", "2026-10-17T00:00:00Z"),
        ],
        { approved: 3, history: 3 },
      ),
    );
    await renderDecisions();

    await waitFor(() => expect(screen.getByText("· 3")).toBeTruthy());
    fireEvent.press(screen.getByTestId("decisions-history-toggle"));
    await screen.findByTestId("decisions-request-row-a1");
    await screen.findByTestId("decisions-request-row-a2");
    await screen.findByTestId("decisions-request-row-a3");
  });

  it("keeps walking the terminal-state window with the keyset cursor pair", async () => {
    mockRequestsData.value = list([], { approved: 4, history: 4 });
    mockHistoryPages.push(
      list(
        [
          makeRequest("a1", "2026-10-17T00:00:00Z"),
          makeRequest("a2", "2026-10-16T00:00:00Z"),
        ],
        { approved: 4, history: 4 },
        { next_cursor: "2026-10-16T00:00:00Z", next_cursor_id: "a2" },
      ),
      list(
        [
          makeRequest("a3", "2026-10-15T00:00:00Z"),
          makeRequest("a4", "2026-10-14T00:00:00Z"),
        ],
        { approved: 4, history: 4 },
      ),
    );
    await renderDecisions();

    await screen.findByTestId("decisions-history-toggle");
    fireEvent.press(screen.getByTestId("decisions-history-toggle"));
    await screen.findByTestId("decisions-request-row-a1");
    const loadMore = await screen.findByTestId("decisions-history-load-more");

    fireEvent.press(loadMore);
    await screen.findByTestId("decisions-request-row-a3");
    await screen.findByTestId("decisions-request-row-a4");
    // The keyset walk actually paged: a history call carried the cursor pair.
    expect(
      historyCalls().some((c: Record<string, unknown>) => c.after_id === "a2"),
    ).toBe(true);
    // The tail page is short — the walk ends with the button gone.
    await waitFor(() => expect(screen.queryByTestId("decisions-history-load-more")).toBeNull());
  });
});
