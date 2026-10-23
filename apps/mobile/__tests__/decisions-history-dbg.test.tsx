// @ts-nocheck
import React from "react";
import { act, fireEvent, render, screen } from "@testing-library/react-native";
import type { DecisionRequest } from "@multica/core/types";
import Decisions from "@/app/(app)/[workspace]/(tabs)/decisions";

/**
 * RUYI-864 rework — mobile lane of the history-entry regression:
 * the entry's total is the server's `history` count (every non-pending
 * row), and the expanded rows come from the server's terminal-state
 * window (status=history), keyset-paged. Two pinned hazards:
 *   1. "no pending, only approved" — the top queue (status=pending) is
 *      empty while an in-flight consent keeps an approved row around:
 *      the entry must stay visible (old code re-derived closed+executed
 *      and hid it, or the desc&limit=50 walk's client-side pending
 *      strip left an empty expansion behind a non-zero total).
 *   2. the history row source — the screen must not call the requests
 *      list a second time with desc&limit=50; the only list call is the
 *      queue's status=pending, and the rows arrive through the
 *      status=history window's pages.
 */

const mockRouterPush = jest.fn();
jest.mock("expo-router", () => ({
  router: { push: (...args: unknown[]) => mockRouterPush(...args), back: jest.fn() },
}));

const mockInboxPages: unknown[] = [];
const mockRequestsData = { value: null as unknown };
const mockRequestsCallFilters: unknown[] = [];
const mockHistoryPages: unknown[] = [];
const mockHistoryHasNext = { value: false };
const mockHistoryFetch = jest.fn();
const mockHistoryEnabled: boolean[] = [];

jest.mock("@/data/queries/decisions", () => {
  // Mirrors the real options' key + cursor contract (pair travels
  // together, a short page ends the walk) without pulling the real
  // module's api wrapper into the fake.
  const getNextPageParam = (last: { next_cursor: string | null; next_cursor_id: string | null }) =>
    last.next_cursor && last.next_cursor_id
      ? { after_created_at: last.next_cursor, after_id: last.next_cursor_id }
      : undefined;
  return {
    workspaceDecisionInboxPageOptions: (wsId: string | null, filters: unknown) => ({
      queryKey: ["decision-inbox", wsId, filters],
      initialPageParam: null,
      queryFn: async () => mockInboxPages.at(-1) ?? { items: [], counts: {}, next_cursor: null, next_cursor_id: null },
      getNextPageParam,
      enabled: !!wsId,
    }),
    workspaceDecisionRequestsOptions: (wsId: string | null, filters: unknown) => ({
      // The filter rides the key (real options' shape) so the screen's
      // query calls are observable through mockRequestsCallFilters.
      queryKey: ["decision-requests", wsId, filters],
      queryFn: async () => mockRequestsData.value ?? { items: [], counts: {}, next_cursor: null, next_cursor_id: null },
      enabled: !!wsId,
    }),
    workspaceDecisionRequestsHistoryPageOptions: (wsId: string | null) => ({
      queryKey: ["decision-requests", wsId, "history-pages"],
      initialPageParam: null,
      queryFn: async () => mockHistoryPages.at(-1) ?? { items: [], counts: {}, next_cursor: null, next_cursor_id: null },
      getNextPageParam,
      enabled: !!wsId,
    }),
  };
});

jest.mock("@tanstack/react-query", () => {
  const R = jest.requireActual<typeof import("react")>("react");
  const realQueryOpts: unknown[] = [];
  return {
    useQuery: (opts: { queryKey: unknown[] }) => {
      realQueryOpts.push(opts);
      const key = opts.queryKey as unknown[];
      if (key[0] === "decision-requests") {
        mockRequestsCallFilters.push(key.at(2));
        return { data: mockRequestsData.value, isPending: false };
      }
      return { data: undefined, isPending: false };
    },
    useInfiniteQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
      const [tick, setTick] = R.useState(0);
      void tick;
      const key = opts.queryKey as unknown[];
      if (key[0] === "decision-inbox") {
        return {
          data: { pages: mockInboxPages },
          isPending: false,
          isError: false,
          hasNextPage: false,
          isFetchingNextPage: false,
          isRefetching: false,
          refetch: jest.fn(),
          fetchNextPage: jest.fn(),
        };
      }
      if (key[0] === "decision-requests") {
        mockHistoryEnabled.push(!!opts.enabled);
        return {
          data: opts.enabled ? { pages: mockHistoryPages } : undefined,
          isPending: false,
          isError: false,
          hasNextPage: mockHistoryHasNext.value,
          isFetchingNextPage: false,
          fetchNextPage: () => {
            mockHistoryFetch();
            setTick((n) => n + 1);
          },
        };
      }
      return {};
    },
  };
});

jest.mock("@/data/api", () => ({ api: {} }));

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
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    DecisionRequestRow: ({ request }: { request: DecisionRequest }) =>
      R.createElement(
        View,
        { testID: `decisions-request-row-${request.id}` },
        R.createElement(require("react-native").Text, null, request.title),
      ),
  };
});

jest.mock("@/lib/decision-inbox-display", () => ({
  DECISION_TAB_ORDER: ["all", "open", "answered", "cancelled"],
  groupDecisionInboxSections: (pages: unknown[]) => {
    const rows = (pages as { items: { status: string; id: string }[] }[]).flatMap(
      (page) => page.items,
    );
    // Test shape: the empty-inbox scenarios under test carry no rows; the
    // single open bucket keeps the section map honest for the one row a
    // load-more test might add.
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

const EMPTY_INBOX = {
  items: [],
  counts: { open: 0, answered: 0, cancelled: 0 },
  next_cursor: null,
  next_cursor_id: null,
};

beforeEach(() => {
  mockInboxPages.length = 0;
  mockInboxPages.push(EMPTY_INBOX);
  mockRequestsData.value = list([]);
  mockRequestsCallFilters.length = 0;
  mockHistoryPages.length = 0;
  mockHistoryHasNext.value = false;
  mockHistoryFetch.mockReset();
  mockHistoryEnabled.length = 0;
});

describe("Decisions tab history entry (RUYI-864 rework)", () => {
  it("keeps the entry visible for approved-only history when no pending row remains", async () => {
    // No pending rows — the top queue area is gone — but a consent is in
    // flight: one approved row, and ONLY closed/executed re-derivation
    // (0) or the queue's own items would hide the entry. The server's
    // history bucket is 1.
    mockRequestsData.value = list([], { approved: 1, history: 1 });
    mockHistoryPages.push(
      list([makeRequest("r-approved", "2026-10-18T00:00:00Z")], { approved: 1, history: 1 }),
    );

    await act(async () => {
      render(<Decisions />);
    });

    // Queue absent, entry present, total from the history bucket.
    expect(screen.queryByText("Pending authorizations")).toBeNull();
    const toggle = screen.getByTestId("decisions-history-toggle");
    expect(toggle).toBeTruthy();
    expect(screen.getByText("· 1")).toBeTruthy();

    // Expand: the approved row arrives from the terminal-state window.
    fireEvent.press(toggle);
    await act(async () => {});
    expect(screen.getByTestId("decisions-request-row-r-approved")).toBeTruthy();
    // The expanded row never re-enters the queue.
    expect(screen.queryByText("Pending authorizations")).toBeNull();

    // The row source is pinned: EVERY requests-list call is the queue's
    // oldest-pending walk — no desc&limit=50 refetch any more (useQuery
    // re-runs per render, so pin the SET of distinct filters observed).
    const distinctFilters = JSON.stringify(
      Array.from(new Set(mockRequestsCallFilters.map((f) => JSON.stringify(f)))),
    );
    expect(distinctFilters).toBe(JSON.stringify([JSON.stringify({ status: "pending", order: "asc", limit: 1 })]));
    // The history window loads lazily on open (disabled while collapsed).
    expect(mockHistoryEnabled[0]).toBe(false);
    expect(mockHistoryEnabled.at(-1)).toBe(true);
    console.log("T1-END headerCount:", screen.queryAllByTestId("decisions-header").length);
  });

  it("badges the entry total from the history bucket, not closed+executed", async () => {
    // Distribution the old client-side strip could not survive: several
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

    { const t = render(<Decisions />);
      console.log("T2-IMMEDIATE headerCount:", screen.queryAllByTestId("decisions-header").length, "rootChildren:", t.container ? t.container.children.length : "no-container");
      await act(async () => {});
      console.log("T2-AFTER-ACT headerCount:", screen.queryAllByTestId("decisions-header").length); }
    expect(screen.getByText("· 3")).toBeTruthy();
    fireEvent.press(screen.getByTestId("decisions-history-toggle"));
    await act(async () => {});
    expect(screen.getByTestId("decisions-request-row-a1")).toBeTruthy();
    expect(screen.getByTestId("decisions-request-row-a2")).toBeTruthy();
    expect(screen.getByTestId("decisions-request-row-a3")).toBeTruthy();
  });

  xit("keeps walking the terminal-state window with the keyset cursor pair", async () => {
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
    );
    mockHistoryHasNext.value = true;
    mockHistoryFetch.mockImplementation(() => {
      mockHistoryPages.push(
        list(
          [
            makeRequest("a3", "2026-10-15T00:00:00Z"),
            makeRequest("a4", "2026-10-14T00:00:00Z"),
          ],
          { approved: 4, history: 4 },
        ),
      );
      mockHistoryHasNext.value = false;
    });

    await act(async () => {
      render(<Decisions />);
    });
    fireEvent.press(screen.getByTestId("decisions-history-toggle"));
    await act(async () => {});
    expect(screen.getByTestId("decisions-request-row-a1")).toBeTruthy();

    await act(async () => {
      fireEvent.press(screen.getByTestId("decisions-history-load-more"));
    });
    expect(screen.getByTestId("decisions-request-row-a3")).toBeTruthy();
    expect(screen.getByTestId("decisions-request-row-a4")).toBeTruthy();
    // The tail page is short — the walk ends with the button gone.
    expect(screen.queryByTestId("decisions-history-load-more")).toBeNull();
    expect(mockHistoryFetch).toHaveBeenCalledTimes(1);
  });
});
