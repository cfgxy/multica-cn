// @vitest-environment jsdom

import { cleanup, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceDecisionInbox } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";

// RUYI-494 acceptance at the component level: the list's data unit is the
// CARD (one row per card, never folded by issue), rows are fixed two lines
// (identifier+title / question), status lives in the sections with 待决策
// first, the recommendation badge and the `#decision-` deep link are both on
// the row.

const inboxRef = vi.hoisted(() => ({ current: null as WorkspaceDecisionInbox | null }));

vi.mock("@multica/core/issues/decisions", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/issues/decisions")>();
  return {
    ...actual,
    workspaceDecisionInboxQueryOptions: (workspaceId: string | null | undefined) => ({
      queryKey: ["test", "decision-inbox", workspaceId],
      queryFn: async () => inboxRef.current ?? { items: [], counts: { open: 0, answered: 0, cancelled: 0 } },
    }),
  };
});

vi.mock("@multica/core/paths", () => ({
  DECISION_ANCHOR_PREFIX: "decision-",
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

function Harness({ children }: { children: ReactNode }) {
  const adapter = {
    push: vi.fn(),
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
});

afterEach(cleanup);

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
    inboxRef.current = {
      items: [
        makeItem({ id: "d1", question: "card one" }),
        makeItem({ id: "d2", question: "card two" }),
        makeItem({ id: "d3", question: "card three", status: "answered", selected_indices: [0] }),
      ],
      counts: { open: 2, answered: 1, cancelled: 0 },
    };
    renderPage();

    expect(await rowAnchors()).toHaveLength(3);
    expect(screen.getByText("card one")).toBeInTheDocument();
    expect(screen.getByText("card two")).toBeInTheDocument();
  });

  it("renders the fixed two lines: identifier+title, then the card question (AC6/AC8)", async () => {
    inboxRef.current = {
      items: [makeItem({ id: "d1" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
    };
    renderPage();

    const row = (await rowAnchors())[0];
    expect(row?.querySelector('[data-testid="decision-inbox-identifier"]')?.textContent).toBe("RUYI-494");
    expect(row?.textContent).toContain("Decision Inbox");
    expect(row?.textContent).toContain("Choose an option");
  });

  it("groups rows into status sections with 待决策 first, showing workspace counts (AC7)", async () => {
    inboxRef.current = {
      items: [
        makeItem({ id: "a1", status: "answered", selected_indices: [0], question: "done deal" }),
        makeItem({ id: "o1", question: "still open" }),
        makeItem({ id: "x1", status: "cancelled", question: "voided" }),
      ],
      counts: { open: 5, answered: 1, cancelled: 1 },
    };
    renderPage();
    await rowAnchors();

    const open = screen.getByTestId("decision-section-open");
    const answered = screen.getByTestId("decision-section-answered");
    const cancelled = screen.getByTestId("decision-section-cancelled");
    // Section order in the DOM: open before answered before cancelled.
    expect(open.compareDocumentPosition(answered) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(answered.compareDocumentPosition(cancelled) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // Header counts are the workspace totals from the server, not row counts.
    expect(open.textContent).toContain("5");
  });

  it("hides sections whose status has no cards", async () => {
    inboxRef.current = {
      items: [makeItem({ id: "o2", question: "only open" })],
      counts: { open: 1, answered: 0, cancelled: 0 },
    };
    renderPage();
    await rowAnchors();
    expect(screen.getByTestId("decision-section-open")).toBeInTheDocument();
    expect(screen.queryByTestId("decision-section-answered")).toBeNull();
    expect(screen.queryByTestId("decision-section-cancelled")).toBeNull();
  });

  it("badges cards that carry a recommendation (AC10)", async () => {
    inboxRef.current = {
      items: [
        makeItem({ id: "d1", recommended_indices: [0] }),
        makeItem({ id: "d2" }),
      ],
      counts: { open: 2, answered: 0, cancelled: 0 },
    };
    renderPage();
    expect(await rowAnchors()).toHaveLength(2);
    expect(screen.getAllByTestId("decision-inbox-recommended")).toHaveLength(1);
  });

  it("deep-links each row to its own card via #decision-<id> (AC12)", async () => {
    inboxRef.current = {
      items: [
        makeItem({ id: "d1", issue_id: "issue-9" }),
        makeItem({ id: "d2", issue_id: "issue-9", question: "second" }),
      ],
      counts: { open: 2, answered: 0, cancelled: 0 },
    };
    renderPage();

    const [first, second] = await rowAnchors();
    expect(first?.getAttribute("href")).toBe("/acme/issues/issue-9#decision-d1");
    expect(second?.getAttribute("href")).toBe("/acme/issues/issue-9#decision-d2");
  });

  it("shows the empty state when the workspace has no cards", async () => {
    inboxRef.current = {
      items: [],
      counts: { open: 0, answered: 0, cancelled: 0 },
    };
    renderPage();

    await waitFor(() =>
      expect(screen.getByTestId("decision-center-empty")).toBeInTheDocument(),
    );
    expect(screen.queryByTestId("decision-inbox-row")).toBeNull();
  });
});
