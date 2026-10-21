// @vitest-environment jsdom

import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DecisionRequestsList, WorkspaceDecisionInbox } from "@multica/core/types";
import { decisionCenterPrefsStore } from "@multica/core/issues/stores/decision-center-prefs-store";
import { renderWithI18n } from "../../test/i18n";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";

// RUYI-494 acceptance at the component level: the list's data unit is the
// CARD (one row per card, never folded by issue), rows are fixed two lines
// (identifier+title / question), status lives in the sections with 待决策
// first, the recommendation badge and the `#decision-` deep link are both on
// the row.

const inboxRef = vi.hoisted(() => ({ current: null as WorkspaceDecisionInbox | null }));
const requestsRef = vi.hoisted(() => ({ current: null as Promise<DecisionRequestsList> | null }));
const scriptDetailRef = vi.hoisted(() => ({ current: null as unknown }));
const navigationPushRef = vi.hoisted(() => ({ push: vi.fn() }));

vi.mock("@multica/core/issues/decisions", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/issues/decisions")>();
  return {
    ...actual,
    decisionRequestDetailQueryOptions: (workspaceId: string, requestId: string) => ({
      queryKey: ["test", "decision-request-detail", workspaceId, requestId],
      queryFn: async () => scriptDetailRef.current,
    }),
    workspaceDecisionInboxQueryOptions: (workspaceId: string | null | undefined) => ({
      queryKey: ["test", "decision-inbox", workspaceId],
      queryFn: async () => inboxRef.current ?? { items: [], counts: { open: 0, answered: 0, cancelled: 0 } },
    }),
  };
});

vi.mock("@multica/core/issues/decision-requests", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/issues/decision-requests")>();
  return {
    ...actual,
    workspaceDecisionRequestsQueryOptions: (workspaceId: string | null | undefined) => ({
      queryKey: ["test", "decision-requests", workspaceId],
      queryFn: () =>
        requestsRef.current ??
        Promise.resolve<DecisionRequestsList>({ items: [], counts: { pending: 0, closed: 0, executed: 0 } }),
    }),
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
  decisionCenterPrefsStore.setState({ viewMode: "list", hiddenStatuses: [] });
  requestsRef.current = null;
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

  it("opens the selected card details and provides its task anchor (AC12)", async () => {
    inboxRef.current = {
      items: [
        makeItem({ id: "d1", issue_id: "issue-9", recommended_indices: [0] }),
        makeItem({ id: "d2", issue_id: "issue-9", question: "second" }),
      ],
      counts: { open: 2, answered: 0, cancelled: 0 },
    };
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

// RUYI-547: the Decision Center gains the issues surface's display model —
// a persisted list/board toggle and a status filter, both rendered with the
// same control shapes as the issues header. The data unit stays the CARD.

const MIXED_INBOX = {
  items: [
    makeItem({ id: "o1", question: "still open" }),
    makeItem({ id: "a1", status: "answered" as const, selected_indices: [0], question: "done deal" }),
    makeItem({ id: "x1", status: "cancelled" as const, question: "voided" }),
  ],
  counts: { open: 5, answered: 1, cancelled: 1 },
};

async function openViewMenu() {
  fireEvent.click(screen.getByTestId("decision-center-view-trigger"));
  await waitFor(() =>
    expect(screen.getByRole("menuitemradio", { name: "Board" })).toBeInTheDocument(),
  );
}

async function switchToBoard() {
  await openViewMenu();
  fireEvent.click(screen.getByRole("menuitemradio", { name: "Board" }));
}

async function openFilterMenu() {
  fireEvent.click(screen.getByTestId("decision-center-filter-trigger"));
  await waitFor(() =>
    expect(
      screen.getByRole("menuitemcheckbox", { name: "Decided" }),
    ).toBeInTheDocument(),
  );
}

describe("DecisionCenterPage view modes (RUYI-547)", () => {
  it("renders the list view by default, then the board with one column per status after switching", async () => {
    inboxRef.current = MIXED_INBOX;
    renderPage();

    expect(await rowAnchors()).toHaveLength(3);
    expect(screen.queryByTestId("decision-board-view")).toBeNull();

    await switchToBoard();

    expect(screen.getByTestId("decision-board-view")).toBeInTheDocument();
    expect(screen.queryByTestId("decision-inbox-row")).toBeNull();
    // One column per decision status, in canonical order — empty columns
    // included, like the issues board keeps its status columns.
    const open = screen.getByTestId("decision-board-column-open");
    const answered = screen.getByTestId("decision-board-column-answered");
    const cancelled = screen.getByTestId("decision-board-column-cancelled");
    expect(open.compareDocumentPosition(answered) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(answered.compareDocumentPosition(cancelled) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // Column headers show the workspace totals, same source as the list
    // section headers.
    expect(open.textContent).toContain("5");
  });

  it("opens full details from board cards and keeps issue context", async () => {
    inboxRef.current = {
      items: [makeItem({ id: "d1", recommended_indices: [0] })],
      counts: { open: 1, answered: 0, cancelled: 0 },
    };
    renderPage();

    await switchToBoard();

    const card = await screen.findByTestId("decision-board-card");
    expect(card.textContent).toContain("RUYI-494");
    expect(card.textContent).toContain("Decision Inbox");
    expect(card.textContent).toContain("Choose an option");
    expect(card.querySelector('[data-testid="decision-inbox-recommended"]')).not.toBeNull();
    fireEvent.click(card);
    expect(await screen.findByTestId("decision-detail-open-task")).toHaveAttribute(
      "href",
      "/acme/issues/issue-1#decision-d1",
    );
  });

  it("switching back to list restores the rows", async () => {
    inboxRef.current = MIXED_INBOX;
    renderPage();
    await rowAnchors();

    await switchToBoard();
    expect(screen.getByTestId("decision-board-view")).toBeInTheDocument();

    await openViewMenu();
    fireEvent.click(screen.getByRole("menuitemradio", { name: "List" }));
    expect(await rowAnchors()).toHaveLength(3);
  });

  it("hides an unchecked status from both list and board, and marks the filter active", async () => {
    inboxRef.current = MIXED_INBOX;
    renderPage();
    await rowAnchors();

    await openFilterMenu();
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: "Decided" }));

    expect(screen.queryByTestId("decision-section-answered")).toBeNull();
    expect(screen.getByTestId("decision-section-open")).toBeInTheDocument();
    expect(screen.getByTestId("decision-section-cancelled")).toBeInTheDocument();
    // The filter trigger reads as active with one filter applied.
    expect(screen.getByTestId("decision-center-filter-trigger").textContent).toMatch(/1/);

    await switchToBoard();
    expect(screen.queryByTestId("decision-board-column-answered")).toBeNull();
    expect(screen.getByTestId("decision-board-column-open")).toBeInTheDocument();
  });

  it("shows the filtered-empty state when every status is hidden, and clear restores", async () => {
    inboxRef.current = MIXED_INBOX;
    renderPage();
    await rowAnchors();

    // The filter menu stays open across toggles (closeOnClick=false), so all
    // three statuses uncheck inside one opening — like a user would.
    await openFilterMenu();
    for (const name of ["Pending decisions", "Decided", "Voided"]) {
      fireEvent.click(screen.getByRole("menuitemcheckbox", { name }));
    }

    expect(screen.getByTestId("decision-filtered-empty")).toBeInTheDocument();
    expect(screen.queryByTestId("decision-inbox-row")).toBeNull();

    fireEvent.click(screen.getByTestId("decision-filter-clear"));
    expect(screen.getByTestId("decision-section-open")).toBeInTheDocument();
    expect(decisionCenterPrefsStore.getState().hiddenStatuses).toEqual([]);
  });
});

// RUYI-738: the authorization-requests surface (RUYI-630) is auxiliary. Its
// query may settle long after the decision inbox (slow response, retry cycle
// after a failure) — the page-level skeleton must never sit on top of the
// already-rendered list while it waits, and the requests section must arrive
// on its own schedule instead of gating the page body.

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
  it("does not hold the page skeleton over the rendered list while the requests query is still pending", async () => {
    inboxRef.current = MIXED_INBOX;
    requestsRef.current = new Promise<DecisionRequestsList>(() => undefined);
    renderPage();

    const rows = await screen.findAllByTestId("decision-inbox-row");
    expect(rows.length).toBeGreaterThan(0);
    expect(screen.queryByTestId("decision-center-loading")).toBeNull();
  });

  it("keeps the skeleton while the page would otherwise be blank: empty inbox, requests still pending", async () => {
    inboxRef.current = { items: [], counts: { open: 0, answered: 0, cancelled: 0 } };
    requestsRef.current = new Promise<DecisionRequestsList>(() => undefined);
    renderPage();

    expect(await screen.findAllByTestId("decision-center-loading")).toBeTruthy();
    expect(screen.queryByTestId("decision-inbox-row")).toBeNull();
    expect(screen.queryByTestId("decision-center-empty")).toBeNull();
  });

  it("renders the requests section on its own schedule once the query settles", async () => {
    inboxRef.current = MIXED_INBOX;
    const deferred = deferredRequests({
      items: [
        {
          id: "dr-1",
          request_group_id: "rg-1",
          workspace_id: "ws-1",
          role: "origin",
          status: "pending",
          action_type: "project_prompt_sync",
          action_params: { project_ids: ["project-1"], content_sha256: "hash" },
          execution_result: { projects: [{ project_id: "project-1", status: "updated", version: 3 }] },
          risk_tier: "write_low",
          title: "Read workspace info",
          detail: "",
          origin_workspace_id: "ws-1",
          origin_agent_id: "agent-1",
          origin_issue_title: "Seed issue",
          operable: true,
          created_at: "2026-10-12T00:00:00Z",
        } as unknown as DecisionRequestsList["items"][number],
      ],
      counts: { pending: 1, closed: 0, executed: 0 },
    });
    requestsRef.current = deferred.promise;
    renderPage();

    await screen.findAllByTestId("decision-inbox-row");
    expect(screen.queryByTestId("decision-requests-section")).toBeNull();

    deferred.settle();
    expect(await screen.findByTestId("decision-requests-section")).toBeInTheDocument();
    expect(screen.getByTestId("decision-request-project-result")).toHaveTextContent("project-1 · updated · v3");
    expect(screen.queryByText("approved prompt body")).toBeNull();
  });

  it("shows a verification warning for unknown side effects and keeps the approval toggle accessible", async () => {
    inboxRef.current = { items: [], counts: { open: 0, answered: 0, cancelled: 0 } };
    requestsRef.current = Promise.resolve({
      items: [{
        id: "dr-unknown",
        request_group_id: "rg-unknown",
        workspace_id: "ws-1",
        role: "origin",
        operable: true,
        status: "execute_failed",
        action_type: "project_prompt_sync",
        action_params: { project_ids: ["project-1"] },
        authorization_spec: {
          version: 1,
          kind: "cli",
          action_type: "project_prompt_sync",
          scope: { target_workspace_id: "ws-2", project_ids: ["project-1"] },
          parameter_digest: "a".repeat(64),
          source_workspace_id: "ws-1",
          target_workspace_id: "ws-2",
          expires_at: "2026-10-22T00:00:00Z",
          public_summary: "Sync project prompts",
          digest: "b".repeat(64),
        },
        execution_error: "PROMPT_SYNC_FAILED",
        execution_result: {
          receipt_version: 1,
          side_effect_status: "unknown",
          steps: [{ handler: "project_prompt_sync", status: "unknown", side_effect_status: "unknown" }],
        },
        risk_tier: "write_low",
        title: "Sync project prompts",
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
        expires_at: "2026-10-22T00:00:00Z",
        answered_by_type: null,
        answered_by_id: null,
        answered_at: null,
        answer_source: null,
        executed_at: null,
        created_at: "2026-10-21T00:00:00Z",
        updated_at: "2026-10-21T00:00:00Z",
      }],
      counts: { pending: 0, closed: 1, executed: 0 },
    } as unknown as DecisionRequestsList);
    renderPage();

    expect(await screen.findByTestId("decision-request-result-unknown")).toHaveTextContent("Result needs verification");
    expect(screen.queryByText("PROMPT_SYNC_FAILED")).toBeNull();
    expect(screen.getByTestId("decision-request-steps-toggle")).toHaveAttribute("aria-expanded", "false");
  });

  it("labels a single-issue request and shows its Done receipt", async () => {
    inboxRef.current = { items: [], counts: { open: 0, answered: 0, cancelled: 0 } };
    requestsRef.current = Promise.resolve({
      items: [{
        id: "dr-issue",
        request_group_id: "rg-issue",
        workspace_id: "ws-1",
        role: "origin",
        operable: false,
        status: "executed",
        action_type: "single_issue_create",
        action_params: { title: "Fix callback", description: "Make one callback idempotent" },
        authorization_spec: { version: 1, kind: "single_issue", digest: "d".repeat(64) },
        execution_result: {
          target_issue_id: "issue-12",
          target_identifier: "RUYI-12",
          target_issue_status: "in_progress",
          done_receipt: { status: "done" },
        },
        risk_tier: "write_low",
        title: "Create one exact issue",
        detail: "",
        origin_workspace_id: "ws-1",
        origin_agent_id: "agent-1",
        origin_agent_name: "Seed Agent",
        origin_issue_id: null,
        origin_issue_title: null,
        created_at: "2026-10-21T00:00:00Z",
      }],
      counts: { pending: 0, closed: 0, executed: 1 },
    } as unknown as DecisionRequestsList);
    renderPage();

    expect(await screen.findByTestId("decision-request-created-issue")).toHaveTextContent("Target issue RUYI-12 · done");
    expect(screen.getByText("Create one issue")).toBeInTheDocument();
  });

  it("loads the fixed script snapshot only through target request detail and labels review without execution", async () => {
    inboxRef.current = { items: [], counts: { open: 0, answered: 0, cancelled: 0 } };
    const script = "printf reviewed";
    scriptDetailRef.current = {
      request: {
        id: "dr-script",
        action_params: { script, interpreter: "bash", args: ["-e"], sha256: "a".repeat(64) },
      },
      steps: [],
    };
    requestsRef.current = Promise.resolve({
      items: [{
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
        detail: "",
        origin_workspace_id: "ws-origin",
        origin_agent_id: "agent-1",
        origin_agent_name: "Seed Agent",
        origin_task_id: null,
        origin_issue_id: null,
        origin_issue_title: null,
        operator_tier: "owner",
        named_approver_ids: [],
        approve_label: null,
        deny_label: null,
        expires_at: "2026-10-22T00:00:00Z",
        answered_by_type: null,
        answered_by_id: null,
        answered_at: null,
        answer_source: null,
        executed_at: "2026-10-21T00:00:00Z",
        execution_error: null,
        created_at: "2026-10-21T00:00:00Z",
        updated_at: "2026-10-21T00:00:00Z",
      }],
      counts: { pending: 0, closed: 1, executed: 1 },
    } as unknown as DecisionRequestsList);
    renderPage();

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
  const projection = {
    id: "request-origin",
    request_group_id: "group-1",
    workspace_id: "ws-origin",
    role: "origin",
    operable: false,
    status: "pending",
    action_type: "workspace_info_read",
    risk_tier: "read",
    title: "Read workspace info",
    detail: "",
    origin_workspace_id: "ws-origin",
    origin_agent_id: "agent-1",
    created_at: "2026-10-12T00:00:00Z",
  } as unknown as DecisionRequestsList["items"][number];

  it("links an accessible origin projection to the target decisions center", async () => {
    inboxRef.current = { items: [], counts: { open: 0, answered: 0, cancelled: 0 } };
    requestsRef.current = Promise.resolve({
      items: [{ ...projection, target_workspace_slug: "target-space" }],
      counts: { pending: 1, closed: 0, executed: 0 },
    });
    renderPage();

    const link = await screen.findByTestId("decision-request-target-link");
    expect(link).toHaveAttribute("href", "/target-space/decisions");
    expect(link.textContent).toContain("Read workspace info");
    expect(screen.queryByText("This row is a read-only projection — act in the target workspace")).toBeNull();
  });

  it("navigates when the read-only origin request row face is clicked", async () => {
    inboxRef.current = { items: [], counts: { open: 0, answered: 0, cancelled: 0 } };
    requestsRef.current = Promise.resolve({
      items: [{ ...projection, target_workspace_slug: "target-space" }],
      counts: { pending: 1, closed: 0, executed: 0 },
    });
    renderPage();

    const rowFace = await screen.findByTestId("decision-request-target-link");
    fireEvent.click(rowFace.querySelector(".text-body")!);

    expect(navigationPushRef.push).toHaveBeenCalledWith("/target-space/decisions");
  });

  it("keeps the read-only hint when the API omits a safe target route", async () => {
    inboxRef.current = { items: [], counts: { open: 0, answered: 0, cancelled: 0 } };
    requestsRef.current = Promise.resolve({
      items: [projection],
      counts: { pending: 1, closed: 0, executed: 0 },
    });
    renderPage();

    expect(await screen.findByText("This row is a read-only projection — act in the target workspace")).toBeInTheDocument();
    expect(screen.queryByTestId("decision-request-target-link")).toBeNull();
  });
});
