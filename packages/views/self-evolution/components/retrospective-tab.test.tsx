// @vitest-environment jsdom

import { it, describe, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { toast } from "sonner";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Agent, RetrospectiveConfig, RetrospectiveRun } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { RetrospectiveTab } from "./retrospective-tab";

/**
 * The retrospective tab's execution-agent wiring (RUYI-552 direction 3).
 *
 * Agent-exists / not-archived / same-workspace validation is enforced
 * server-side and covered there. What only a mount can show is asserted
 * here: the saved selection round-trips into the trigger's label, the PATCH
 * body carries the picked agent id (and "" after clearing), an archived
 * saved agent stays visible but warns, and the localized 409 blocker when
 * "run now" is rejected for a missing agent.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const { TestApiError } = vi.hoisted(() => {
  class TestApiError extends Error {
    readonly status: number;
    constructor(message: string, status: number) {
      super(message);
      this.name = "ApiError";
      this.status = status;
    }
  }
  return { TestApiError };
});

const state = vi.hoisted(() => ({
  role: "owner" as string | null,
  config: null as RetrospectiveConfig | null,
  runs: [] as RetrospectiveRun[],
  agents: [] as Agent[],
  updateCalls: [] as unknown[],
  triggerResult: null as Promise<unknown> | null,
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ role: state.role }),
}));

// RUYI-673: the per-run results entry builds its href through the shared
// path builders; RUYI-663's detail-sheet issue links do the same. The stub
// pins both shapes and keeps the test free of workspace-route context.
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    selfEvolutionProposals: () => "/acme/self-evolution/proposals",
    issueDetail: (id: string) => `/test-workspace/issues/${id}`,
  }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({
    queryKey: ["test", "agents"],
    queryFn: () => Promise.resolve(state.agents),
  }),
}));

vi.mock("@multica/core/api", () => ({
  ApiError: TestApiError,
  // Mirrors the real clientErrorMessage contract (MUL-6472): the server's
  // message is worth rendering only for 4xx — handlers write those for the
  // user. A 5xx message is internal detail and must fall through to the
  // caller's localized fallback. Mocking it as a full passthrough made the
  // non-409 assertion below pass against behavior the real client never
  // has (RUYI-561).
  clientErrorMessage: (e: unknown) =>
    e instanceof TestApiError && e.status >= 400 && e.status < 500 ? e.message : undefined,
  api: {
    getRetrospectiveConfig: () => Promise.resolve(state.config),
    updateRetrospectiveConfig: (patch: unknown) => {
      state.updateCalls.push(patch);
      return Promise.resolve(state.config);
    },
    listRetrospectiveRuns: () => Promise.resolve(state.runs),
    triggerRetrospectiveRun: () =>
      state.triggerResult ? state.triggerResult : Promise.resolve({}),
  },
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

function agent(id: string, name: string, archived = false): Agent {
  return {
    id,
    name,
    archived_at: archived ? "2026-10-01T00:00:00Z" : null,
  } as Agent;
}

function config(overrides: Partial<RetrospectiveConfig> = {}): RetrospectiveConfig {
  return {
    enabled: true,
    include_in_review: false,
    window_days: 7,
    agent_id: null,
    agent_name: null,
    ...overrides,
  };
}

function makeAdapter(
  overrides: Partial<NavigationAdapter> = {},
): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/test-workspace/self-evolution",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p) => p,
    ...overrides,
  };
}

function renderTab() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <NavigationProvider value={makeAdapter()}>
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <RetrospectiveTab wsId="ws-1" />
        </I18nProvider>
      </NavigationProvider>
    </QueryClientProvider>,
  );
}

function rejected(err: Error & { status: number }): Promise<never> {
  // Shadow-catch so the rejection is not "unhandled" while it waits for the
  // mutation to pick it up; consumers of the returned promise still see it.
  const p = Promise.reject(err);
  p.catch(() => {});
  return p;
}

beforeEach(() => {
  state.role = "owner";
  state.config = config();
  state.runs = [];
  state.agents = [agent("agent-1", "Alpha"), agent("agent-2", "Beta"), agent("agent-9", "Old", true)];
  state.updateCalls = [];
  state.triggerResult = null;
});

afterEach(cleanup);

async function pickAgent(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(await screen.findByRole("combobox", { name: "Execution agent" }));
  await user.click(await screen.findByRole("option", { name }));
}

it("round-trips the saved selection into the trigger label", async () => {
  state.config = config({ agent_id: "agent-1", agent_name: "Alpha" });
  renderTab();

  const trigger = await screen.findByRole("combobox", { name: "Execution agent" });
  await waitFor(() => expect(trigger).toHaveTextContent("Alpha"));
});

it("sends the picked agent id in the PATCH body", async () => {
  renderTab();
  const user = userEvent.setup();
  await pickAgent(user, "Beta");
  fireEvent.click(screen.getByRole("button", { name: "Save config" }));

  await waitFor(() => expect(state.updateCalls).toHaveLength(1));
  expect(state.updateCalls[0]).toEqual({
    enabled: true,
    include_in_review: false,
    window_days: 7,
    agent_id: "agent-2",
  });
});

it("sends an empty agent_id after clearing the selection", async () => {
  state.config = config({ agent_id: "agent-1", agent_name: "Alpha" });
  renderTab();
  const user = userEvent.setup();
  await pickAgent(user, "Select an agent");
  fireEvent.click(screen.getByRole("button", { name: "Save config" }));

  await waitFor(() => expect(state.updateCalls).toHaveLength(1));
  expect(state.updateCalls[0]).toEqual({
    enabled: true,
    include_in_review: false,
    window_days: 7,
    agent_id: "",
  });
});

it("keeps an archived saved agent selectable but warns about it", async () => {
  state.config = config({ agent_id: "agent-9", agent_name: "Old" });
  renderTab();

  const trigger = await screen.findByRole("combobox", { name: "Execution agent" });
  await waitFor(() => expect(trigger).toHaveTextContent(/Old/));
  expect(trigger).toHaveTextContent("(archived)");
  expect(await screen.findByTestId("retrospective-agent-archived")).toHaveTextContent(
    "This agent is archived",
  );
  // Live agents stay pickable alongside the archived saved one.
  const user = userEvent.setup();
  await user.click(trigger);
  expect(await screen.findByRole("option", { name: "Beta" })).toBeTruthy();
});

it("toasts the localized blocker, not the server sentence, on a 409 run rejection", async () => {
  state.triggerResult = rejected(new TestApiError("未配置可用的执行智能体", 409));
  renderTab();
  fireEvent.click(await screen.findByRole("button", { name: "Run now" }));

  await waitFor(() => expect(vi.mocked(toast.error)).toHaveBeenCalled());
  expect(vi.mocked(toast.error)).toHaveBeenCalledWith(
    "No execution agent configured: select an agent in the configuration above, then run again.",
  );
});

it("falls back to the localized label for non-409 run failures", async () => {
  state.triggerResult = rejected(new TestApiError("retrospective queue busy", 500));
  renderTab();
  fireEvent.click(await screen.findByRole("button", { name: "Run now" }));

  await waitFor(() => expect(vi.mocked(toast.error)).toHaveBeenCalled());
  // A 5xx message is internal detail (clientErrorMessage returns undefined
  // for it), so the toast shows the component's own localized fallback.
  expect(vi.mocked(toast.error)).toHaveBeenCalledWith("Error");
});

it("toasts the started wording on an accepted run trigger", async () => {
  renderTab();
  fireEvent.click(await screen.findByRole("button", { name: "Run now" }));

  await waitFor(() => expect(vi.mocked(toast.success)).toHaveBeenCalled());
  expect(vi.mocked(toast.success)).toHaveBeenCalledWith("Retrospective run started.");
});

function run(overrides: Partial<RetrospectiveRun> = {}): RetrospectiveRun {
  return {
    id: "run-1",
    status: "failed",
    trigger: "schedule",
    window_start: "2026-10-01T00:00:00Z",
    window_end: "2026-10-08T00:00:00Z",
    issues_scanned: 2,
    issues_analyzed: 0,
    proposals_created: 0,
    proposals_merged: 0,
    duplicates_skipped: 0,
    error: "",
    detail: null,
    created_at: "2026-10-08T00:00:00Z",
    ...overrides,
  };
}

// The run row's failure verdict (RUYI-561): the server persists a stable
// reason code on detail.reason — the UI localizes it in the viewer's
// language. What a mount proves that the parity test cannot: the rendered
// line, the whitelisted param interpolation, and the fallbacks that keep
// internal and legacy copy off the screen.
describe("run failure reason rendering", () => {
  it("localizes a known reason code from the run detail", async () => {
    state.runs = [run({ detail: { reason: { code: "agent_archived" } } })];
    renderTab();

    const line = await screen.findByTestId("retrospective-run-error");
    expect(line).toHaveTextContent(
      "Error: The configured execution agent is archived: pick another agent in the configuration above, then save.",
    );
  });

  it("interpolates the whitelisted issue id on out-of-scope rejections", async () => {
    state.runs = [
      run({ detail: { reason: { code: "report_out_of_scope", issue_id: "9f1c-abc" } } }),
    ];
    renderTab();

    const line = await screen.findByTestId("retrospective-run-error");
    expect(line).toHaveTextContent("9f1c-abc");
    expect(line).not.toHaveTextContent("{{issue_id}}");
  });

  it("falls back to generic copy for an unknown reason code", async () => {
    state.runs = [run({ detail: { reason: { code: "reason_from_a_newer_server" } } })];
    renderTab();

    const line = await screen.findByTestId("retrospective-run-error");
    expect(line).toHaveTextContent("This run failed");
    expect(line).not.toHaveTextContent("reason_from_a_newer_server");
  });

  it("never renders the legacy raw error text", async () => {
    state.runs = [run({ error: "扫描窗口失败: pq: relation does not exist" })];
    renderTab();

    const line = await screen.findByTestId("retrospective-run-error");
    expect(line).toHaveTextContent("This run failed");
    expect(line).not.toHaveTextContent("扫描窗口失败");
    expect(line).not.toHaveTextContent("relation does not exist");
  });

  it("shows no error line for a succeeded run", async () => {
    state.runs = [run({ status: "succeeded", detail: { analyzed_issue_ids: ["x"] } })];
    renderTab();

    await screen.findByTestId("retrospective-runs");
    expect(screen.queryByTestId("retrospective-run-error")).toBeNull();
  });
});

// The run-detail sheet (RUYI-663): one affordance per row opens the full
// record — status, trigger, window, timing, counters, the localized failure
// verdict, per-issue links, and the transcript entry that only exists for
// runs that actually enqueued a platform task.
describe("run detail sheet", () => {
  async function openDetail() {
    fireEvent.click(await screen.findByTestId("retrospective-run-detail"));
    return screen.findByTestId("retrospective-detail-status");
  }

  it("renders window, timing and per-issue links for a historical run", async () => {
    state.runs = [
      run({
        status: "succeeded",
        finished_at: "2026-10-08T01:00:00Z",
        detail: {
          issue_ids: ["aaaa-1", "bbbb-2"],
          analyzed_issue_ids: ["cccc-3"],
        },
      }),
    ];
    renderTab();

    await openDetail();
    // Every scanned issue links into the issue page, including ones the run
    // did not analyze; the analyzed list renders its own links below.
    expect(screen.getByTestId("retrospective-issue-link-aaaa-1")).toHaveAttribute(
      "href",
      "/test-workspace/issues/aaaa-1",
    );
    expect(screen.getByTestId("retrospective-issue-link-bbbb-2")).toHaveAttribute(
      "href",
      "/test-workspace/issues/bbbb-2",
    );
    expect(screen.getByTestId("retrospective-issue-link-cccc-3")).toHaveAttribute(
      "href",
      "/test-workspace/issues/cccc-3",
    );
    // A terminal run shows its finish time and duration, and — with no
    // enqueued task — no transcript entry.
    expect(screen.getByText("Finished")).toBeTruthy();
    expect(screen.getByText("Duration")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "View run log" })).toBeNull();
  });

  it("keeps the localized failure verdict and hides the log entry for a taskless failed run", async () => {
    state.runs = [run({ error: "扫描窗口失败: pq: relation does not exist" })];
    renderTab();

    await openDetail();
    const line = screen.getByTestId("retrospective-detail-error");
    // Same runErrorLine semantics as the row: the generic localized verdict,
    // never the legacy raw server text.
    expect(line).toHaveTextContent("This run failed");
    expect(line).not.toHaveTextContent("扫描窗口失败");
    expect(screen.queryByRole("button", { name: "View run log" })).toBeNull();
  });

  it("offers the run log for an in-flight run that has a task", async () => {
    state.runs = [
      run({
        status: "running",
        task_id: "4a2e8d1c-7f9b-4e2a-9c1d-123456789abc",
        finished_at: null,
      }),
    ];
    renderTab();

    const status = await openDetail();
    expect(status).toHaveTextContent("Running");
    // A running run has no finish time yet — the placeholders carry the
    // running copy — and the transcript entry streams live.
    expect(screen.getAllByText("Running…").length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "View run log" })).toBeTruthy();
  });

  it("shows the issue-less fallback when the detail carries no membership", async () => {
    state.runs = [run({ status: "succeeded", detail: {} })];
    renderTab();

    await openDetail();
    expect(screen.getAllByText("None").length).toBe(2);
  });
});

// RUYI-673: run rows adopt the agent-detail list's row language (readable
// title + status icon + hover actions) and each row carries the results
// entry — a link to the proposals view scoped to that run's batch, the same
// surface the agent detail page's retrospective rows land on.
describe("run rows link to the per-run results view", () => {
  it("titles each run by its window stamp and links to the scoped results", async () => {
    state.runs = [run({ id: "run-1", status: "succeeded" })];
    renderTab();

    await screen.findByTestId("retrospective-runs");
    expect(screen.getByText(/Daily retrospective 20261008_/)).toBeTruthy();
    const link = screen.getByRole("link", { name: "View results" });
    expect(link.getAttribute("href")).toBe("/acme/self-evolution/proposals?run=run-1");
  });

  it("keeps the failure line and the rerun affordance alongside the link", async () => {
    state.runs = [run({ id: "run-2", detail: { reason: { code: "agent_archived" } } })];
    renderTab();

    await screen.findByTestId("retrospective-run-error");
    expect(screen.getByText(/Daily retrospective 20261008_/)).toBeTruthy();
    expect(
      screen.getByRole("link", { name: "View results" }).getAttribute("href"),
    ).toContain("?run=run-2");
    expect(screen.getByRole("button", { name: "Run again" })).toBeTruthy();
  });
});
