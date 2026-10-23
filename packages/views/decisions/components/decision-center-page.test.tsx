// @vitest-environment jsdom

import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  DecisionRequest,
  DecisionRequestsList,
  WorkspaceDecisionInbox,
} from "@multica/core/types";
import type { WorkspaceDecisionInboxFilters } from "@multica/core/issues/decisions";
import type { DecisionRequestsListFilters } from "@multica/core/issues/decision-requests";
import { decisionCenterPrefsStore } from "@multica/core/issues/stores/decision-center-prefs-store";
import { renderWithI18n } from "../../test/i18n";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";

// RUYI-494 acceptance at the component level: the list's data unit is the
// CARD (one row per card, never folded by issue), rows are fixed two lines
// (identifier+title / question), status lives in the sections with 待决策
// first, the recommendation badge and the `#decision-` deep link are both on
// the row. RUYI-864: the page is the list plus a single-card pending
// authorization queue, a collapsed history entry, a debounced keyword search,
// and a keyset load-more — the board view and the multi-status filter are
// gone.

const inboxRef = vi.hoisted(() => ({
  current: null as null | ((pageParam: { after_created_at: string; after_id: string } | null) => WorkspaceDecisionInbox),
}));
const requestsRef = vi.hoisted(() => ({
  current: null as null | ((filters: DecisionRequestsListFilters) => Promise<DecisionRequestsList>),
}));
const historyRequestsRef = vi.hoisted(() => ({
  current: null as null | ((
    pageParam: { after_created_at: string; after_id: string } | null,
  ) => Promise<DecisionRequestsList>),
}));
const historyCallsRef = vi.hoisted(() => ({
  current: [] as Array<{ after_created_at: string; after_id: string } | null>,
}));
const pageFiltersRef = vi.hoisted(() => ({ current: [] as WorkspaceDecisionInboxFilters[] }));
const requestFiltersRef = vi.hoisted(() => ({ current: [] as DecisionRequestsListFilters[] }));
const scriptDetailRef = vi.hoisted(() => ({ current: null as unknown }));
const navigationPushRef = vi.hoisted(() => ({ push: vi.fn() }));

vi.mock("@multica/core/issues/decisions", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/issues/decisions")>();
  return {
    ...actual,
    workspaceDecisionInboxPageOptions: (
      workspaceId: string | null | undefined,
      filters?: WorkspaceDecisionInboxFilters,
    ) => {
      pageFiltersRef.current.push(filters ?? {});
      type PageParam = { after_created_at: string; after_id: string } | null;
      return {
        queryKey: ["test", "decision-inbox", workspaceId, filters],
        initialPageParam: null as PageParam,
        queryFn: async ({ pageParam }: { pageParam: PageParam }) =>
          inboxRef.current?.(pageParam) ?? {
            items: [],
            counts: { open: 0, answered: 0, cancelled: 0 },
            next_cursor: null,
            next_cursor_id: null,
          },
        // Same cursor contract as the real options: the pair travels
        // together; a page without both ends the walk.
        getNextPageParam: (lastPage: WorkspaceDecisionInbox) =>
          lastPage.next_cursor && lastPage.next_cursor_id
            ? {
                after_created_at: lastPage.next_cursor,
                after_id: lastPage.next_cursor_id,
              }
            : undefined,
        enabled: true,
      };
    },
  };
});

vi.mock("@multica/core/issues/decision-requests", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/issues/decision-requests")>();
  return {
    ...actual,
    decisionRequestDetailQueryOptions: (workspaceId: string, requestId: string) => ({
      queryKey: ["test", "decision-request-detail", workspaceId, requestId],
      queryFn: async () => scriptDetailRef.current,
    }),
    workspaceDecisionRequestsQueryOptions: (
      workspaceId: string | null | undefined,
      filters?: DecisionRequestsListFilters,
    ) => {
      requestFiltersRef.current.push(filters ?? {});
      return {
        queryKey: ["test", "decision-requests", workspaceId, filters],
        queryFn: () =>
          requestsRef.current?.(filters ?? {}) ??
          Promise.resolve<DecisionRequestsList>(emptyRequestsList()),
        enabled: true,
      };
    },
    // RUYI-864 rework: the history window's keyset pages — the same
    // terminal-state set the counts' `history` total badges. The mock
    // mirrors the real options' cursor contract (pair travels together,
    // a short page ends the walk).
    workspaceDecisionRequestsHistoryPageOptions: (
      workspaceId: string | null | undefined,
    ) => {
      return {
        queryKey: ["test", "decision-requests", workspaceId, "history-pages"],
        initialPageParam: null as { after_created_at: string; after_id: string } | null,
        queryFn: async ({
          pageParam,
        }: {
          pageParam: { after_created_at: string; after_id: string } | null;
        }) => {
          historyCallsRef.current.push(pageParam);
          return historyRequestsRef.current?.(pageParam) ?? emptyRequestsList();
        },
        getNextPageParam: (lastPage: DecisionRequestsList) =>
          lastPage.next_cursor && lastPage.next_cursor_id
            ? {
                after_created_at: lastPage.next_cursor,
                after_id: lastPage.next_cursor_id,
              }
            : undefined,
        enabled: true,
      };
    },
  };
});

// The requests row resolves author names through the workspace directory
// (member/agent/squad list queries); the row-shape test only needs a stable
// name, not the directory.
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (type: string) => (type === "agent" ? "Seed Agent" : "Unknown"),
  }),
}));

vi.mock("@multica/core/paths", () => ({
  DECISION_ANCHOR_PREFIX: "decision-",
  paths: { workspace: (slug: string) => ({ decisions: () => `/${slug}/decisions` }) },
  useCurrentWorkspace: () => ({ id: "ws-1", slug: "acme" }),
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/acme/issues/${id}`,
  }),
}));

import { DecisionCenterPage } from "./decision-center-page";

function makeItem(over: Partial<WorkspaceDecisionInbox["items"][number]> & { id: string }) {
  return {
    workspace_id: "ws-1",
    issue_id: "issue-1",
    source_comment_id: null,
    issue_number: 494,
    issue_identifier: "RUYI-494",
    issue_title: "Decision Inbox",
    question: "Choose an option",
    context: {
      why_now: "The release window closes today.",
      facts: [{ statement: "The build passed.", evidence: ["CI run 123"] }],
      impact: "A delay moves launch one week.",
      recommendation_reason: "Monday leaves rollback time.",
      unknowns: ["Review timing"],
    },
    options: [{ label: "A" }, { label: "B" }],
    multi_select: false,
    recommended_indices: [],
    status: "open" as const,
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "member",
    created_by_id: "u1",
    created_at: "2026-10-07T00:00:00Z",
    updated_at: "2026-10-07T00:00:00Z",
    ...over,
  };
}

// RUYI-864 rework: a wire-shaped requests list — counts carry the new
// `approved` / `history` buckets and the keyset cursor pair, exactly what
// handler.ListDecisionRequests now returns.
function requestsList(
  items: DecisionRequest[],
  counts: Partial<DecisionRequestsList["counts"]> = {},
  cursor: { next_cursor: string | null; next_cursor_id: string | null } = {
    next_cursor: null,
    next_cursor_id: null,
  },
): DecisionRequestsList {
  return {
    items,
    counts: { pending: 0, approved: 0, closed: 0, executed: 0, history: 0, ...counts },
    ...cursor,
  };
}

function emptyRequestsList() {
  return requestsList([]);
}

function makeRequest(over: Partial<DecisionRequest> & { id: string }): DecisionRequest {
  return {
    request_group_id: over.id,
    workspace_id: "ws-1",
    role: "origin",
    operable: true,
    status: "pending",
    action_type: "workspace_info_read",
    risk_tier: "read",
    title: "Read workspace info",
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
    created_at: "2026-10-20T00:00:00Z",
    updated_at: "2026-10-20T00:00:00Z",
    ...over,
  };
}

function Harness({ children }: { children: ReactNode }) {
  const adapter = {
    push: navigationPushRef.push,
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/decisions",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (path: string) => `https://example.test${path}`,
  } satisfies NavigationAdapter;
  return (
    <NavigationProvider value={adapter}>
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </NavigationProvider>
  );
}

let queryClient: QueryClient;

beforeEach(() => {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  // The display-prefs store is a module singleton with persistence; every
  // test starts from the shipped defaults.
  decisionCenterPrefsStore.setState({ statusFilter: "all" });
  inboxRef.current = null;
  requestsRef.current = null;
  historyRequestsRef.current = null;
  historyCallsRef.current = [];
  pageFiltersRef.current = [];
  requestFiltersRef.current = [];
  scriptDetailRef.current = null;
  navigationPushRef.push.mockClear();
});

afterEach(() => {
  cleanup();
  // Base UI portals menus onto document.body; leftovers would duplicate
  // labels across tests.
  document.body.innerHTML = "";
});

function renderPage() {
  return renderWithI18n(
    <Harness>
      <DecisionCenterPage />
    </Harness>,
  );
}

async function rowAnchors() {
  await waitFor(() => expect(screen.queryByTestId("decision-center-loading")).toBeNull());
  return screen.getAllByTestId("decision-inbox-row");
}

describe("DecisionCenterPage", () => {
  it("renders one independent row per card, even on the same issue (AC2)", async () => {
    inboxRef.current = () => ({
      items: [
        makeItem({ id: "d1", question: "card one" }),
        makeItem({ id: "d2", question: "card two" }),
        makeItem({ id: "d3", question: "card three", status: "answered", selected_indices: [0] }),
      ],
      counts: { open: 2, answered: 1, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();

    expect(await rowAnchors()).toHaveLength(3);
    expect(screen.getByText("card one")).toBeInTheDocument();
    expect(screen.getByText("card two")).toBeInTheDocument();
  });

  it("renders the fixed two lines: identifier+title, then the card question (AC6/AC8)", async () => {
    inboxRef.current = () => ({
      items: [makeItem({ id: "d1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();

    const row = (await rowAnchors())[0];
    expect(row?.querySelector('[data-testid="decision-inbox-identifier"]')?.textContent).toBe("RUYI-494");
    expect(row?.textContent).toContain("Decision Inbox");
    expect(row?.textContent).toContain("Choose an option");
  });

  it("groups rows into status sections with 待决策 first, showing window counts (AC7)", async () => {
    inboxRef.current = () => ({
      items: [
        makeItem({ id: "a1", status: "answered", selected_indices: [0], question: "done deal" }),
        makeItem({ id: "o1", question: "still open" }),
        makeItem({ id: "x1", status: "cancelled", question: "voided" }),
      ],
      counts: { open: 5, answered: 1, cancelled: 1 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();
    await rowAnchors();

    const open = screen.getByTestId("decision-section-open");
    const answered = screen.getByTestId("decision-section-answered");
    const cancelled = screen.getByTestId("decision-section-cancelled");
    // Section order in the DOM: open before answered before cancelled.
    expect(open.compareDocumentPosition(answered) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(answered.compareDocumentPosition(cancelled) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // Header counts are the server's filter-window totals, not row counts.
    expect(open.textContent).toContain("5");
  });

  it("hides sections whose status has no cards", async () => {
    inboxRef.current = () => ({
      items: [makeItem({ id: "o2", question: "only open" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();
    await rowAnchors();
    expect(screen.getByTestId("decision-section-open")).toBeInTheDocument();
    expect(screen.queryByTestId("decision-section-answered")).toBeNull();
    expect(screen.queryByTestId("decision-section-cancelled")).toBeNull();
  });

  it("badges cards that carry a recommendation (AC10)", async () => {
    inboxRef.current = () => ({
      items: [
        makeItem({ id: "d1", recommended_indices: [0] }),
        makeItem({ id: "d2" }),
      ],
      counts: { open: 2, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();
    expect(await rowAnchors()).toHaveLength(2);
    expect(screen.getAllByTestId("decision-inbox-recommended")).toHaveLength(1);
  });

  it("opens the selected card details and provides its task anchor (AC12)", async () => {
    inboxRef.current = () => ({
      items: [
        makeItem({ id: "d1", issue_id: "issue-9", recommended_indices: [0] }),
        makeItem({ id: "d2", issue_id: "issue-9", question: "second" }),
      ],
      counts: { open: 2, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();

    const [first, second] = await rowAnchors();
    fireEvent.click(first!);
    expect(await screen.findByTestId("decision-detail-sheet")).toBeInTheDocument();
    expect(screen.getByText("The release window closes today.")).toBeInTheDocument();
    expect(screen.getByTestId("decision-detail-option-0").textContent).toContain("A");
    expect(screen.getByTestId("decision-detail-option-0").textContent).toContain("Recommended");
    expect(screen.getByTestId("decision-detail-open-task")).toHaveAttribute(
      "href",
      "/acme/issues/issue-9#decision-d1",
    );
    fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
    await waitFor(() => expect(screen.queryByTestId("decision-detail-sheet")).toBeNull());
    fireEvent.click(second!);
    expect(await screen.findByTestId("decision-detail-open-task")).toHaveAttribute(
      "href",
      "/acme/issues/issue-9#decision-d2",
    );
  });

  it("shows the empty state when the workspace has no cards", async () => {
    renderPage();

    await waitFor(() =>
      expect(screen.getByTestId("decision-center-empty")).toBeInTheDocument(),
    );
    expect(screen.queryByTestId("decision-inbox-row")).toBeNull();
  });
});

// RUYI-864: the single-value status filter and the keyword search both ride
// the server's filter window, so the list, the section counts, and the empty
// state all follow the same window. The tests assert on the filter object
// the page hands to the query options — the wire shape — not on DOM shuffling.

function lastPageFilters(): WorkspaceDecisionInboxFilters {
  return pageFiltersRef.current[pageFiltersRef.current.length - 1] ?? {};
}

describe("DecisionCenterPage status filter window (RUYI-864)", () => {
  it("queries with no status by default and with one real status per pill", async () => {
    inboxRef.current = () => ({
      items: [makeItem({ id: "d1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();
    await rowAnchors();
    expect(lastPageFilters()).toEqual({});

    fireEvent.click(screen.getByTestId("decision-filter-open"));
    await vi.waitFor(() => expect(lastPageFilters().status).toBe("open"));
    expect(decisionCenterPrefsStore.getState().statusFilter).toBe("open");

    fireEvent.click(screen.getByTestId("decision-filter-cancelled"));
    await vi.waitFor(() => expect(lastPageFilters().status).toBe("cancelled"));

    // "All" lifts the window back to the unfiltered call.
    fireEvent.click(screen.getByTestId("decision-filter-all"));
    await vi.waitFor(() => expect(lastPageFilters().status).toBeUndefined());
    expect(decisionCenterPrefsStore.getState().statusFilter).toBe("all");
  });

  it("shows the filtered-empty state with a clear action when the window has no cards", async () => {
    inboxRef.current = () => ({
      items: [],
      counts: { open: 0, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();
    fireEvent.click(screen.getByTestId("decision-filter-answered"));
    await vi.waitFor(() =>
      expect(screen.getByTestId("decision-filtered-empty")).toBeInTheDocument(),
    );
    expect(screen.queryByTestId("decision-inbox-row")).toBeNull();

    fireEvent.click(screen.getByTestId("decision-window-clear"));
    expect(decisionCenterPrefsStore.getState().statusFilter).toBe("all");
    await vi.waitFor(() => expect(lastPageFilters().status).toBeUndefined());
    expect(screen.queryByTestId("decision-filtered-empty")).toBeNull();
  });
});

describe("DecisionCenterPage keyword search (RUYI-864)", () => {
  it("debounces the keyword and rides it in the page filter", async () => {
    inboxRef.current = () => ({
      items: [makeItem({ id: "d1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();
    await rowAnchors();
    expect(pageFiltersRef.current.every((filters) => filters.q === undefined)).toBe(true);

    fireEvent.change(screen.getByTestId("decision-search-input"), {
      target: { value: "RUYI-494" },
    });
    await vi.waitFor(() => expect(lastPageFilters().q).toBe("RUYI-494"));
  });

  it("clears the search from the toolbar and out of the window", async () => {
    inboxRef.current = () => ({
      items: [],
      counts: { open: 0, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();
    fireEvent.change(screen.getByTestId("decision-search-input"), {
      target: { value: "no-such-card" },
    });
    await vi.waitFor(() =>
      expect(screen.getByTestId("decision-filtered-empty")).toBeInTheDocument(),
    );

    // The clear action lifts both the status window and the keyword, so the
    // list and the counts return to the unfiltered call together.
    fireEvent.click(screen.getByTestId("decision-window-clear"));
    await vi.waitFor(() => {
      expect(lastPageFilters().q).toBeUndefined();
      expect(lastPageFilters().status).toBeUndefined();
    });
    expect(screen.queryByTestId("decision-filtered-empty")).toBeNull();
  });
});

// RUYI-864: the pending authorization queue is a single card the server
// pushes — the oldest pending row (order=asc, limit=1). Processing one
// auto-advances the next; when the queue empties the area vanishes.

describe("DecisionCenterPage pending queue (RUYI-864)", () => {
  function queueOf(rows: DecisionRequest[]) {
    return (filters: DecisionRequestsListFilters) => {
      if (filters.status === "pending") {
        // Simulate the server's oldest-first walk (order=asc, limit=1):
        // at most the oldest pending row, nothing else. The counts are
        // workspace-wide (never scoped to the page window), so `history`
        // badges the terminal-state set on the same call.
        const oldest = [...rows].sort(
          (a, b) => Date.parse(a.created_at) - Date.parse(b.created_at),
        )[0];
        return Promise.resolve(
          requestsList(oldest ? [oldest] : [], {
            pending: rows.length,
            history: rows.filter((row) => row.status !== "pending").length,
          }),
        );
      }
      return Promise.resolve(requestsList(rows));
    };
  }

  it("shows only the oldest pending row in a dedicated area above the list", async () => {
    inboxRef.current = () => ({
      items: [makeItem({ id: "d1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    requestsRef.current = queueOf([
      makeRequest({ id: "r-new", created_at: "2026-10-22T00:00:00Z", title: "New request" }),
      makeRequest({ id: "r-old", created_at: "2026-10-20T00:00:00Z", title: "Old request" }),
    ]);
    renderPage();

    const queue = await screen.findByTestId("decision-queue");
    // The server's order=asc + limit=1 already picked the oldest row; the
    // page just renders the single card it was handed.
    expect(queue.textContent).toContain("Old request");
    expect(queue.textContent).not.toContain("New request");
    // The count badge is the server's window total, not the visible rows.
    expect(queue.textContent).toContain("2");
    // The queue row is not also folded into the decision card list.
    expect(queue.querySelector('[data-testid="decision-inbox-row"]')).toBeNull();
  });

  it("vanishes the queue area when no pending row remains", async () => {
    inboxRef.current = () => ({
      items: [makeItem({ id: "d1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    requestsRef.current = queueOf([]);
    renderPage();

    await rowAnchors();
    expect(screen.queryByTestId("decision-queue")).toBeNull();
  });

  it("exposes history only through the collapsed entry, never with pending rows", async () => {
    requestsRef.current = (filters) => {
      if (filters.status === "pending") {
        // The queue call badges BOTH sets on one call: the queue total and
        // the entry's `history` total — same sets, one response.
        return Promise.resolve(
          requestsList([makeRequest({ id: "r-pending" })], {
            pending: 1,
            closed: 1,
            executed: 1,
            history: 2,
          }),
        );
      }
      return Promise.resolve(requestsList([]));
    };
    // The history window is the server's terminal-state set
    // (status=history): pending rows never arrive in it, no client-side
    // strip is needed any more.
    historyRequestsRef.current = () =>
      Promise.resolve(
        requestsList(
          [
            makeRequest({ id: "r-approved", status: "approved", created_at: "2026-10-18T00:00:00Z" }),
            makeRequest({ id: "r-executed", status: "executed", created_at: "2026-10-17T00:00:00Z" }),
          ],
          { closed: 1, executed: 1, history: 2 },
        ),
      );
    inboxRef.current = () => ({
      items: [],
      counts: { open: 0, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();

    const toggle = await screen.findByTestId("decision-history-toggle");
    // Collapsed by default: the only visible request row is the queue's
    // pending card; the processed rows are not rendered yet.
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.getAllByTestId("decision-request-row")).toHaveLength(1);

    fireEvent.click(toggle);
    // The queue row plus the two terminal rows. The pending row never
    // enters the history — the server window excludes it at the source.
    // The queue row is already visible, so findByTestId would resolve on
    // it — poll until the history's own rows arrive.
    await vi.waitFor(() =>
      expect(screen.getAllByTestId("decision-request-row")).toHaveLength(3),
    );
    expect(screen.getByTestId("decision-history-toggle")).toHaveAttribute("aria-expanded", "true");
  });

  // P1 regression (review thread 2): an `approved` row has LEFT the top
  // queue (the single-card query is status=pending) while the consent is
  // still in flight in another space. The entry must stay visible — driven
  // by the server's `history` count, never by queue rows — and expand to
  // the agreed rows without re-entering the queue.
  it("keeps the entry visible for approved-only history when no pending row remains", async () => {
    requestsRef.current = () =>
      Promise.resolve(
        requestsList([], { approved: 1, history: 1 }),
      );
    historyRequestsRef.current = () =>
      Promise.resolve(
        requestsList(
          [makeRequest({ id: "r-approved", status: "approved", created_at: "2026-10-18T00:00:00Z" })],
          { approved: 1, history: 1 },
        ),
      );
    inboxRef.current = () => ({
      items: [],
      counts: { open: 0, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    renderPage();

    // No pending row → the queue area is gone, but the entry's total is
    // 1, so the collapsed entry must still be present.
    expect(await screen.findByTestId("decision-history-toggle")).toBeInTheDocument();
    expect(screen.queryByTestId("decision-queue")).toBeNull();
    const toggle = screen.getByTestId("decision-history-toggle");
    expect(toggle.textContent).toContain("1");

    // Expanding renders the approved row — and it does NOT re-enter the
    // queue: the queue query is still status=pending.
    fireEvent.click(toggle);
    await vi.waitFor(() =>
      expect(screen.getAllByTestId("decision-request-row")).toHaveLength(1),
    );
    expect(screen.queryByTestId("decision-queue")).toBeNull();
    // The history window asked the server for status=history, never pending.
    expect(requestFiltersRef.current.filter((f) => f.status === "pending")).toHaveLength(1);
    expect(historyCallsRef.current).toEqual([null]);
  });
});

// RUYI-864: keyset load-more — the server page comes back full with a cursor
// pair, "Load more" fetches the next page, and the button retires when the
// cursor pair is absent.

describe("DecisionCenterPage load more (RUYI-864)", () => {
  it("merges the next keyset page and retires the button at the tail", async () => {
    const pageOne: WorkspaceDecisionInbox = {
      items: [makeItem({ id: "d1", created_at: "2026-10-08T00:00:00Z" })],
      counts: { open: 2, answered: 0, cancelled: 0 },
      next_cursor: "2026-10-08T00:00:00Z",
      next_cursor_id: "d1",
    };
    const pageTwo: WorkspaceDecisionInbox = {
      items: [makeItem({ id: "d2", created_at: "2026-10-07T00:00:00Z" })],
      counts: { open: 2, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    };
    inboxRef.current = (pageParam) => (pageParam ? pageTwo : pageOne);
    renderPage();

    expect((await rowAnchors())).toHaveLength(1);
    const loadMore = screen.getByTestId("decision-load-more");
    fireEvent.click(loadMore);

    await vi.waitFor(() =>
      expect(screen.getAllByTestId("decision-inbox-row")).toHaveLength(2),
    );
    // The tail page has no cursor, so the walk ends with the button gone.
    expect(screen.queryByTestId("decision-load-more")).toBeNull();
  });

  it("keeps section counts scoped to the window while paging", async () => {
    inboxRef.current = (pageParam) =>
      pageParam
        ? {
            items: [makeItem({ id: "d2" })],
            counts: { open: 5, answered: 1, cancelled: 0 },
            next_cursor: null,
            next_cursor_id: null,
          }
        : {
            items: [makeItem({ id: "d1" })],
            counts: { open: 5, answered: 1, cancelled: 0 },
            next_cursor: "2026-10-07T00:00:00Z",
            next_cursor_id: "d1",
          };
    renderPage();
    await rowAnchors();
    expect(screen.getByTestId("decision-section-open").textContent).toContain("5");
    fireEvent.click(screen.getByTestId("decision-load-more"));
    await vi.waitFor(() =>
      expect(screen.getAllByTestId("decision-inbox-row")).toHaveLength(2),
    );
    expect(screen.getByTestId("decision-section-open").textContent).toContain("5");
  });
});

// RUYI-738: the auxiliary requests queries must settle on their own schedule
// — the page-level skeleton may never sit on top of an already-rendered
// list while they wait.

function deferredRequests(value: DecisionRequestsList) {
  let resolve!: (value: DecisionRequestsList) => void;
  const promise = new Promise<DecisionRequestsList>((r) => {
    resolve = r;
  });
  void promise.then((v) => void v, () => undefined);
  return {
    promise,
    settle: () => resolve(value),
  };
}

describe("DecisionCenterPage loading skeleton (RUYI-738)", () => {
  it("does not hold the page skeleton over the rendered list while the queue query is still pending", async () => {
    inboxRef.current = () => ({
      items: [makeItem({ id: "d1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    requestsRef.current = () => new Promise<DecisionRequestsList>(() => undefined);
    renderPage();

    const rows = await screen.findAllByTestId("decision-inbox-row");
    expect(rows.length).toBeGreaterThan(0);
    expect(screen.queryByTestId("decision-center-loading")).toBeNull();
  });

  it("keeps the skeleton while the page would otherwise be blank: empty inbox, queue still pending", async () => {
    requestsRef.current = () => new Promise<DecisionRequestsList>(() => undefined);
    renderPage();

    expect(await screen.findAllByTestId("decision-center-loading")).toBeTruthy();
    expect(screen.queryByTestId("decision-inbox-row")).toBeNull();
    expect(screen.queryByTestId("decision-center-empty")).toBeNull();
  });

  it("renders the pending queue on its own schedule once the query settles", async () => {
    inboxRef.current = () => ({
      items: [makeItem({ id: "d1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    const deferred = deferredRequests(
      requestsList(
        [
          makeRequest({
            id: "dr-1",
            request_group_id: "rg-1",
            role: "origin",
            status: "pending",
            action_type: "project_prompt_sync",
            action_params: { project_ids: ["project-1"], content_sha256: "hash" },
            execution_result: { projects: [{ project_id: "project-1", status: "updated", version: 3 }] },
            risk_tier: "write_low",
            title: "Read workspace info",
          }),
        ],
        { pending: 1 },
      ),
    );
    requestsRef.current = () => deferred.promise;
    renderPage();

    await screen.findAllByTestId("decision-inbox-row");
    expect(screen.queryByTestId("decision-queue")).toBeNull();

    deferred.settle();
    expect(await screen.findByTestId("decision-queue")).toBeInTheDocument();
    expect(screen.getByTestId("decision-request-project-result")).toHaveTextContent("project-1 · updated · v3");
    expect(screen.queryByText("approved prompt body")).toBeNull();
  });

  it("surfaces processed requests through the collapsed history entry", async () => {
    inboxRef.current = () => ({
      items: [makeItem({ id: "d1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    // The queue call (status=pending) badges the entry with `history`;
    // the processed row itself comes from the terminal-state window.
    requestsRef.current = () =>
      Promise.resolve(requestsList([], { closed: 1, history: 1 }));
    historyRequestsRef.current = () =>
      Promise.resolve(
        requestsList(
          [
            makeRequest({
              id: "dr-unknown",
              request_group_id: "rg-unknown",
              operable: true,
              status: "execute_failed",
              action_type: "project_prompt_sync",
              action_params: { project_ids: ["project-1"] },
              execution_result: {
                receipt_version: 1,
                side_effect_status: "unknown",
                steps: [{ handler: "project_prompt_sync", status: "unknown", side_effect_status: "unknown" }],
              },
              risk_tier: "write_low",
              title: "Sync project prompts",
              origin_agent_name: "Seed Agent",
            }),
          ],
          { closed: 1, history: 1 },
        ),
      );
    renderPage();

    const toggle = await screen.findByTestId("decision-history-toggle");
    fireEvent.click(toggle);
    expect(await screen.findByTestId("decision-request-result-unknown")).toHaveTextContent("Result needs verification");
    expect(screen.queryByText("PROMPT_SYNC_FAILED")).toBeNull();
    expect(screen.getByTestId("decision-request-steps-toggle")).toHaveAttribute("aria-expanded", "false");
  });

  it("labels a single-issue request and shows its Done receipt", async () => {
    inboxRef.current = () => ({
      items: [makeItem({ id: "d1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    // The queue call badges the entry with `history`; the executed row
    // itself comes from the terminal-state window.
    requestsRef.current = () =>
      Promise.resolve(requestsList([], { executed: 1, history: 1 }));
    historyRequestsRef.current = () =>
      Promise.resolve(
        requestsList(
          [
            makeRequest({
              id: "dr-issue",
              request_group_id: "rg-issue",
              operable: false,
              status: "executed",
              action_type: "single_issue_create",
              action_params: { title: "Fix callback", description: "Make one callback idempotent" },
              execution_result: {
                target_issue_id: "issue-12",
                target_identifier: "RUYI-12",
                target_issue_status: "in_progress",
                done_receipt: { status: "done" },
              },
              risk_tier: "write_low",
              title: "Create one issue",
              origin_agent_name: "Seed Agent",
            }),
          ],
          { executed: 1, history: 1 },
        ),
      );
    renderPage();

    fireEvent.click(await screen.findByTestId("decision-history-toggle"));
    expect(await screen.findByTestId("decision-request-created-issue")).toHaveTextContent("Target issue RUYI-12 · done");
    // "Create one issue" appears both as the action label and the row title.
    expect(screen.getAllByText("Create one issue").length).toBeGreaterThanOrEqual(1);
  });

  it("loads the fixed script snapshot only through target request detail and labels review without execution", async () => {
    inboxRef.current = () => ({
      items: [makeItem({ id: "d1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
      next_cursor: null,
      next_cursor_id: null,
    });
    const script = "printf reviewed";
    scriptDetailRef.current = {
      request: {
        id: "dr-script",
        action_params: { script, interpreter: "bash", args: ["-e"], sha256: "a".repeat(64) },
      },
      steps: [],
    };
    // The script row is a processed (executed) row: the queue call badges
    // the entry with `history` and the row itself arrives through the
    // terminal-state window.
    requestsRef.current = () =>
      Promise.resolve(requestsList([], { closed: 1, executed: 1, history: 2 }));
    historyRequestsRef.current = () =>
      Promise.resolve(
        requestsList(
          [
            makeRequest({
              id: "dr-script",
              request_group_id: "rg-script",
              workspace_id: "ws-target",
              role: "target",
              operable: true,
              status: "executed",
              action_type: "script_review",
              action_params: { sha256: "a".repeat(64) },
              authorization_spec: {
                version: 1,
                kind: "script_review",
                action_type: "script_review",
                scope: { target_workspace_id: "ws-target" },
                parameter_digest: "b".repeat(64),
                source_workspace_id: "ws-origin",
                target_workspace_id: "ws-target",
                expires_at: "2026-10-22T00:00:00Z",
                public_summary: "Review script snapshot",
                digest: "c".repeat(64),
              },
              execution_result: { receipt_type: "script_review", status: "reviewed", script_sha256: "a".repeat(64) },
              risk_tier: "write_low",
              title: "Review script snapshot",
              origin_workspace_id: "ws-origin",
              origin_agent_id: "agent-1",
              origin_agent_name: "Seed Agent",
              executed_at: "2026-10-21T00:00:00Z",
            }),
          ],
          { closed: 1, executed: 1, history: 2 },
        ),
      );
    renderPage();

    fireEvent.click(await screen.findByTestId("decision-history-toggle"));
    expect(await screen.findByText("Reviewed")).toBeInTheDocument();
    expect(screen.queryByText("Executed")).toBeNull();
    expect(screen.queryByText(script)).toBeNull();
    fireEvent.click(screen.getByTestId("script-review-toggle"));
    expect(await screen.findByText(script)).toBeInTheDocument();
    expect(screen.getByText("bash")).toBeInTheDocument();
    expect(screen.getByText('["-e"]')).toBeInTheDocument();
    expect(screen.queryByText(/execution complete/i)).toBeNull();
  });
});

describe("DecisionCenterPage cross-space authorization navigation (RUYI-842)", () => {
  const projection = makeRequest({
    id: "request-origin",
    request_group_id: "group-1",
    workspace_id: "ws-origin",
    role: "origin",
    operable: false,
    status: "pending",
    action_type: "workspace_info_read",
    risk_tier: "read",
    title: "Read workspace info",
    origin_workspace_id: "ws-origin",
    origin_agent_id: "agent-1",
  });

  it("links an accessible origin projection to the target decisions center", async () => {
    requestsRef.current = () =>
      Promise.resolve(
        requestsList([{ ...projection, target_workspace_slug: "target-space" }], { pending: 1 }),
      );
    renderPage();

    const link = await screen.findByTestId("decision-request-target-link");
    expect(link).toHaveAttribute("href", "/target-space/decisions");
    expect(link.textContent).toContain("Read workspace info");
    expect(screen.queryByText("This row is a read-only projection — act in the target workspace")).toBeNull();
  });

  it("navigates when the read-only origin request row face is clicked", async () => {
    requestsRef.current = () =>
      Promise.resolve(
        requestsList([{ ...projection, target_workspace_slug: "target-space" }], { pending: 1 }),
      );
    renderPage();

    const rowFace = await screen.findByTestId("decision-request-target-link");
    fireEvent.click(rowFace.querySelector(".text-body")!);

    expect(navigationPushRef.push).toHaveBeenCalledWith("/target-space/decisions");
  });

  it("keeps the read-only hint when the API omits a safe target route", async () => {
    requestsRef.current = () =>
      Promise.resolve(requestsList([projection], { pending: 1 }));
    renderPage();

    expect(await screen.findByText("This row is a read-only projection — act in the target workspace")).toBeInTheDocument();
    expect(screen.queryByTestId("decision-request-target-link")).toBeNull();
  });
});
