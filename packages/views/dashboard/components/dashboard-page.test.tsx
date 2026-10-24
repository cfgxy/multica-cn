import { useMemo, useState } from "react";
import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../../test/i18n";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation";
import { addDaysIso } from "../../runtimes/utils";

// The viewing timezone flows: auth store `user.timezone` → useViewingTimezone()
// → every dashboard query key. This test pins that chain: when the stored
// timezone changes, the dashboard report query keys must change, which is
// what makes TanStack Query refetch under the new tz.

// Capture every queryKey passed to useQuery. queryOptions() inside the
// dashboard options builders runs for real, so the key is the production key.
const queryKeys = vi.hoisted(() => [] as unknown[][]);
const dashboardDataRef = vi.hoisted(() => ({ current: false }));
const modelCallsRef = vi.hoisted(() => ({ known: 0, unknown: 0 }));
// Swaps the per-agent fixtures for ones with enough agents to exercise the
// top-offenders and leaderboard caps. Kept off by default so the other tests
// keep their exact 4-of-10 arithmetic.
const manyAgentsRef = vi.hoisted(() => ({ current: false }));
// Appends the server's `__restricted_agents__` bucket to the per-agent rollups
// — what a plain member actually receives once the backend folds the agents
// they may not view (MUL-5409).
const restrictedBucketRef = vi.hoisted(() => ({ current: false }));
// The empty-usage suite: every calendar rollup answers successfully with zero
// rows — a workspace with agents but no completed tasks, the exact shape of
// the QA repro where the page used to swap itself for the empty state and the
// live Prometheus panels never mounted.
const emptyUsageRef = vi.hoisted(() => ({ current: false }));
// Arms the live metric panels for that suite: `false` keeps a panel pending,
// "empty" serves configured=true with no series, "data" adds one sample.
const metricPanelsRef = vi.hoisted(() => ({
  current: {
    resources: false as false | "empty" | "data",
    traffic: false as false | "empty" | "data",
  },
}));
// When set, per-agent rollup queries whose window equals this JSON are
// answered with alternate fixtures — a stand-in for a server that answers
// per-window. The leaderboard-follows-selection tests use it to prove the
// rendered rows come from the SELECTED window's query, not the full-window
// one.
const lbWindowRef = vi.hoisted(() => ({ current: null as string | null }));

// Kept out of the fixture ternary so the sentinel's shape reads at a glance.
// Unlike the deleted-agents bucket this one carries real seconds / tasks: the
// agents behind it are alive and ran.
const RESTRICTED_BUCKET_ROWS = vi.hoisted(
  () =>
    ({
      "by-agent": [
        {
          agent_id: "__restricted_agents__",
          provider: "anthropic",
          model: "claude-sonnet-4-6",
          input_tokens: 500,
          output_tokens: 500,
          cache_read_tokens: 0,
          cache_write_tokens: 0,
          task_count: 4,
        },
      ],
      "agent-runtime": [
        {
          agent_id: "__restricted_agents__",
          total_seconds: 2 * 3_600,
          task_count: 4,
          failed_count: 2,
        },
      ],
    }) as Record<string, unknown[]>,
);

function todayIso() {
  return new Date().toISOString().slice(0, 10);
}

vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    // The page reads the client only to invalidate the dashboard keys from
    // the refresh button; there is no provider in these renders.
    useQueryClient: () => ({ invalidateQueries: vi.fn() }),
    useQuery: (opts: { queryKey: unknown[] }) => {
      queryKeys.push(opts.queryKey);
      const kind = opts.queryKey[2];
      // Live Prometheus panels: relative-window queries the page must fire
      // even when every calendar rollup comes back empty. Unarmed panels
      // stay pending, exactly as an unmocked query would on mount.
      if (kind === "usage-resources" || kind === "usage-traffic") {
        const armed =
          kind === "usage-resources"
            ? metricPanelsRef.current.resources
            : metricPanelsRef.current.traffic;
        if (!armed) return { data: undefined, isLoading: true };
        return {
          data: {
            configured: true,
            series:
              armed === "data"
                ? {
                    cpu_cores: [
                      { labels: { daemon: "daemon-1" }, points: [{ t: 1, v: 2.5 }] },
                    ],
                    tokens_per_second: [
                      { labels: { provider: "anthropic" }, points: [{ t: 1, v: 12 }] },
                    ],
                  }
                : {},
          },
          isLoading: false,
          isSuccess: true,
        };
      }
      // Empty-usage suite: every calendar rollup succeeds with zero rows, the
      // way a workspace that has agents but no completed tasks answers.
      if (emptyUsageRef.current) {
        if (opts.queryKey[0] === "workspaces" && kind === "agents") {
          return {
            data: [{ id: "agent-1", name: "Agent One" }],
            isLoading: false,
            isSuccess: true,
          };
        }
        return { data: [], isLoading: false, isSuccess: true };
      }
      if (dashboardDataRef.current) {
        // ["workspaces", wsId, "agents"] — needed so the Errors breakdown can
        // resolve agent-1 to a name and render its drill-down link.
        if (opts.queryKey[0] === "workspaces" && opts.queryKey[2] === "agents") {
          return {
            data: manyAgentsRef.current
              ? [
                  ...Array.from({ length: 12 }, (_, i) => ({
                    id: `bulk-${i}`,
                    name: `Bulk Agent ${i}`,
                  })),
                  { id: "sel-agent", name: "Selected Agent" },
                ]
              : [
                  { id: "agent-1", name: "Agent One" },
                  { id: "sel-agent", name: "Selected Agent" },
                ],
            isLoading: false,
            isSuccess: true,
          };
        }
        // The selected-window answer: a distinct agent, so a test can tell
        // WHICH window's per-agent rows the leaderboard rendered.
        if (
          lbWindowRef.current !== null &&
          JSON.stringify(opts.queryKey[3]) === lbWindowRef.current &&
          (opts.queryKey[2] === "by-agent" || opts.queryKey[2] === "agent-runtime")
        ) {
          return {
            data:
              opts.queryKey[2] === "by-agent"
                ? [
                    {
                      agent_id: "sel-agent",
                      provider: "anthropic",
                      model: "claude-sonnet-4-6",
                      input_tokens: 7_000,
                      output_tokens: 0,
                      cache_read_tokens: 0,
                      cache_write_tokens: 0,
                      task_count: 3,
                    },
                  ]
                : [
                    {
                      agent_id: "sel-agent",
                      total_seconds: 60,
                      task_count: 3,
                      failed_count: 0,
                    },
                  ],
            isLoading: false,
            isSuccess: true,
          };
        }
        // Bulk fixture: 12 agents, every per-agent metric strictly descending
        // so both caps (leaderboard top 10, offenders top 8) are testable by
        // rank without the ties an equal-valued fixture would create. The
        // date-bucketed series stay on the small fixture below — the caps are
        // a property of the per-agent lists only.
        const bulkRows =
          !manyAgentsRef.current
            ? null
            : kind === "by-agent"
              ? Array.from({ length: 12 }, (_, i) => ({
                  agent_id: `bulk-${i}`,
                  provider: "anthropic",
                  model: "claude-sonnet-4-6",
                  input_tokens: (12 - i) * 1_000,
                  output_tokens: 0,
                  cache_read_tokens: 0,
                  cache_write_tokens: 0,
                  task_count: 12 - i,
                }))
              : kind === "agent-runtime"
                ? Array.from({ length: 12 }, (_, i) => ({
                    agent_id: `bulk-${i}`,
                    total_seconds: (12 - i) * 600,
                    task_count: 12 - i,
                    failed_count: 12 - i,
                  }))
                : kind === "failures-by-agent"
                  ? Array.from({ length: 12 }, (_, i) => [
                      {
                        agent_id: `bulk-${i}`,
                        failure_reason: "",
                        task_count: 100,
                      },
                      {
                        agent_id: `bulk-${i}`,
                        failure_reason: "timeout",
                        task_count: 12 - i,
                      },
                    ]).flat()
                  : null;
        if (bulkRows) {
          return { data: bulkRows, isLoading: false, isSuccess: true };
        }
        const data =
          kind === "daily"
            ? [
                {
                  date: todayIso(),
                  provider: "anthropic",
                  model: "claude-sonnet-4-6",
                  input_tokens: 1_000,
                  output_tokens: 2_000,
                  cache_read_tokens: 0,
                  cache_write_tokens: 0,
                    model_call_count: modelCallsRef.known,
                    model_call_unknown_count: modelCallsRef.unknown,
                  task_count: 2,
                },
              ]
            : kind === "agent-runtime"
              ? [
                  {
                    agent_id: "agent-1",
                    total_seconds: 3 * 3_600 + 17 * 60,
                    task_count: 12,
                    failed_count: 1,
                  },
                ]
              : kind === "runtime-daily"
                ? [
                    {
                      date: todayIso(),
                      total_seconds: 3 * 3_600 + 17 * 60,
                      task_count: 12,
                      failed_count: 1,
                    },
                  ]
                : // `failure_reason: ""` is the succeeded bucket — the
                  // denominator behind every rate the Errors surface shows.
                  kind === "failures-daily"
                  ? [
                      { date: todayIso(), failure_reason: "", task_count: 6 },
                      {
                        date: todayIso(),
                        failure_reason: "agent_error.provider_auth_or_access",
                        task_count: 3,
                      },
                      { date: todayIso(), failure_reason: "timeout", task_count: 1 },
                    ]
                  : kind === "failures-by-agent"
                    ? [
                        { agent_id: "agent-1", failure_reason: "", task_count: 6 },
                        {
                          agent_id: "agent-1",
                          failure_reason: "agent_error.provider_auth_or_access",
                          task_count: 3,
                        },
                        {
                          agent_id: "agent-1",
                          failure_reason: "timeout",
                          task_count: 1,
                        },
                        // Not in the agent list below — a private agent this
                        // member cannot see, or a deleted one. The rollup
                        // still returns it.
                        {
                          agent_id: "0f9d1c2e-private-agent-uuid",
                          failure_reason: "agent_error.provider_auth_or_access",
                          task_count: 2,
                        },
                      ]
                    : [];
        return {
          data: restrictedBucketRef.current
            ? [...data, ...(RESTRICTED_BUCKET_ROWS[kind as string] ?? [])]
            : data,
          isLoading: false,
          isSuccess: true,
        };
      }
      return { data: undefined, isLoading: true };
    },
  };
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// The dashboard reaches the charts through the shared barrel, and recharts
// paints nothing in jsdom. The barrel is stubbed with charts that expose the
// selection seam as a button: clicking emits a span derived from the data the
// chart was given. Without a handler the stubs render nothing — the same
// inertness the runtime usage section (which passes no selection props)
// relies on. `overhang` makes the emitted span cross the window edges by two
// days, what a Mon–Sun calendar week straddling the stat window looks like.
vi.mock("../../runtimes/components/charts", () => {
  const addDays = (iso: string, n: number) => {
    const d = new Date(iso + "T00:00:00");
    d.setDate(d.getDate() + n);
    return d.toISOString().slice(0, 10);
  };
  const stub =
    (testId: string, overhang: boolean) =>
    function StubChart({
      data,
      onSelectionChange,
    }: {
      data: { date?: string; weekStart?: string; weekEnd?: string }[];
      onSelectionChange?: (next: { start: string; end: string }) => void;
    }) {
      if (!onSelectionChange) return null;
      const first = data[0];
      const last = data[data.length - 1];
      const startDay = first?.date ?? first?.weekStart ?? "";
      const endDay = last?.date ?? last?.weekEnd ?? "";
      return (
        <button
          type="button"
          onClick={() =>
            onSelectionChange({
              start: overhang ? addDays(startDay, -2) : startDay,
              end: overhang ? addDays(endDay, 2) : endDay,
            })
          }
        >
          {testId}
        </button>
      );
    };
  return {
    DailyCostChart: stub("stub:daily-cost", false),
    DailyTokensChart: stub("stub:daily-tokens", false),
    DailyTimeChart: stub("stub:daily-time", false),
    DailyTasksChart: stub("stub:daily-tasks", false),
    WeeklyCostChart: stub("stub:weekly-cost", false),
    WeeklyTokensChart: stub("stub:weekly-tokens", true),
    WeeklyTimeChart: stub("stub:weekly-time", false),
    WeeklyTasksChart: stub("stub:weekly-tasks", false),
    DailyErrorsChart: stub("stub:daily-errors", false),
    WeeklyErrorsChart: stub("stub:weekly-errors", false),
    // errors-tab also pulls these two non-chart values from the barrel.
    FAILURE_CLASS_COLOR: {},
    formatRate: (failed: number, total: number) =>
      total === 0 ? "0%" : `${Math.round((failed / total) * 100)}%`,
  };
});

// The leaderboard renders ActorAvatar, which resolves avatar URLs through
// the api singleton. Only the base-URL read is exercised here.
vi.mock("@multica/core/api", () => ({
  api: { getBaseUrl: () => "https://example.test" },
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/acme/agents/${id}`,
  }),
}));

const tzRef = vi.hoisted(() => ({ current: "UTC" as string | null }));

vi.mock("@multica/core/auth", () => {
  type AuthState = { user: { timezone: string | null } | null };
  const state = (): AuthState => ({ user: { timezone: tzRef.current } });
  const useAuthStore = Object.assign(
    (sel?: (s: AuthState) => unknown) => (sel ? sel(state()) : state()),
    { getState: state },
  );
  return { useAuthStore };
});

// The page anchors every quick window on `todayIso(viewTZ)`. Pin it to a ref
// so the rollover test can advance the calendar day deterministically
// instead of racing the wall clock across a timezone boundary. While the ref
// is empty the real implementation runs — every other test sees exactly the
// windows it always did.
const todayRef = vi.hoisted(() => ({ current: "" }));

vi.mock("../../runtimes/utils", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../../runtimes/utils")>();
  return {
    ...actual,
    todayIso: (tz: string) => todayRef.current || actual.todayIso(tz),
  };
});

afterEach(() => {
  todayRef.current = "";
});

vi.mock("@multica/core/runtimes/custom-pricing-store", () => {
  const state = () => ({ pricings: {} });
  const useCustomPricingStore = Object.assign(
    (sel?: (s: ReturnType<typeof state>) => unknown) =>
      sel ? sel(state()) : state(),
    { getState: state },
  );
  return { useCustomPricingStore };
});

import { DashboardPage } from "./dashboard-page";

const replaceSpy = vi.fn();

/**
 * A navigation adapter that actually holds the query string, because the tab
 * IS the URL: the page reads `?tab=` and writes it back through `replace`. A
 * spy-only adapter would swallow the write and the tab could never change,
 * which would make every Errors assertion below test a screen the user cannot
 * reach.
 *
 * The adapter also backs the offender drill-down's <AppLink>. Asserting on the
 * rendered href (rather than a push spy) keeps that test on the contract that
 * matters: the row points at the agent's Overview, whose ActivityTab lists its
 * runs and each failure's reason.
 */
function DashboardHarness({ initialSearch = "" }: { initialSearch?: string }) {
  const [search, setSearch] = useState(initialSearch);
  const adapter = useMemo<NavigationAdapter>(
    () => ({
      push: vi.fn(),
      replace: (path: string) => {
        replaceSpy(path);
        setSearch(path.split("?")[1] ?? "");
      },
      back: vi.fn(),
      pathname: "/acme/usage",
      searchParams: new URLSearchParams(search),
      hash: "",
      getShareableUrl: (path: string) => `https://example.test${path}`,
    }),
    [search],
  );
  return (
    <NavigationProvider value={adapter}>
      <DashboardPage />
    </NavigationProvider>
  );
}

function renderDashboard(initialSearch = "") {
  return renderWithI18n(<DashboardHarness initialSearch={initialSearch} />);
}

/** Everything about failures now lives behind its own tab, so a test that
 *  wants it has to go there first — exactly as a reader does. */
async function openErrorsTab(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("tab", { name: /Errors/ }));
}

// The offender ranking control. Named rather than reached through the DOM
// tree so it survives the card's internal layout changing.
function offenderSort(): HTMLElement {
  return screen.getByRole("group", { name: "Rank offenders by" });
}

// The filled part of one offender row's bar. `role="img"` is on the fill (it
// carries the class-split label), and its inline width is the metric the list
// is currently ranked by.
function offenderBar(index: number): HTMLElement {
  const rows = within(
    screen.getByRole("list", { name: "Top offenders" }),
  ).getAllByRole("listitem");
  return within(rows[index] as HTMLElement).getByRole("img");
}

describe("DashboardPage — viewing timezone drives the query key", () => {
  beforeEach(() => {
    queryKeys.length = 0;
    dashboardDataRef.current = false;
    cleanup();
  });

  // The `tz` segment is the last element of every dashboard key
  // (see dashboardKeys in @multica/core/dashboard/queries).
  function tzSegments(): unknown[] {
    return queryKeys
      .filter((k) => k[0] === "dashboard")
      .map((k) => k[k.length - 1]);
  }

  it("uses the stored timezone in every dashboard query key", () => {
    tzRef.current = "UTC";
    renderDashboard();

    const tzs = tzSegments();
    expect(tzs.length).toBeGreaterThan(0);
    expect(tzs.every((tz) => tz === "UTC")).toBe(true);
  });

  it("flips the query key when the stored timezone changes", () => {
    tzRef.current = "UTC";
    renderDashboard();
    const utcKeys = queryKeys.filter((k) => k[0] === "dashboard");

    queryKeys.length = 0;
    cleanup();

    tzRef.current = "Asia/Tokyo";
    renderDashboard();
    const tokyoKeys = queryKeys.filter((k) => k[0] === "dashboard");

    expect(utcKeys.length).toBe(tokyoKeys.length);
    expect(utcKeys.length).toBeGreaterThan(0);
    // Same number of dashboard queries, but no key is shared between the
    // two timezones — so TanStack Query treats every series as a fresh
    // fetch and refetches under the new tz.
    for (let i = 0; i < utcKeys.length; i++) {
      expect(utcKeys[i]).not.toEqual(tokyoKeys[i]);
    }
  });

  it("renders every workspace KPI as an animated number", () => {
    dashboardDataRef.current = true;
    tzRef.current = "UTC";

    const { container } = renderDashboard();
    const flows = Array.from(
      container.querySelectorAll("number-flow-react"),
    );

    expect(flows).toHaveLength(6);
    expect(flows.map((flow) => flow.getAttribute("aria-label"))).toEqual(
      expect.arrayContaining(["$0.03", "3K", "12"]),
    );
    expect(container).toHaveTextContent("3h 17m");
    expect(
      flows.every(
        (flow) =>
          (flow as HTMLElement & { respectMotionPreference?: boolean })
            .respectMotionPreference === true,
      ),
    ).toBe(true);
  });
});

describe("DashboardPage — known and unknown model calls", () => {
  beforeEach(() => {
    queryKeys.length = 0;
    dashboardDataRef.current = true;
    emptyUsageRef.current = false;
    modelCallsRef.known = 0;
    modelCallsRef.unknown = 0;
    tzRef.current = "UTC";
    cleanup();
  });

  it("shows zero known calls and the unreported count without folding them together", () => {
    modelCallsRef.unknown = 2;
    renderDashboard();

    const label = screen.getByText("Model calls · 30D");
    const card = label.parentElement as HTMLElement;
    expect(card.querySelector("number-flow-react")).toHaveAttribute(
      "aria-label",
      "0",
    );
    expect(card).toHaveTextContent(
      "2 usage rows have an unknown model call count; excluded from the known total.",
    );
  });

  it("shows known calls separately when both known and unknown counts are nonzero", () => {
    modelCallsRef.known = 4;
    modelCallsRef.unknown = 3;
    renderDashboard();

    const label = screen.getByText("Model calls · 30D");
    const card = label.parentElement as HTMLElement;
    expect(card.querySelector("number-flow-react")).toHaveAttribute(
      "aria-label",
      "4",
    );
    expect(card).toHaveTextContent(
      "3 usage rows have an unknown model call count; excluded from the known total.",
    );
  });

  it("omits the unknown hint when no calls are unreported", () => {
    modelCallsRef.known = 4;
    renderDashboard();

    const card = screen.getByText("Model calls · 30D").parentElement as HTMLElement;
    expect(card).toHaveTextContent("4");
    expect(card).not.toHaveTextContent("unreported");
  });
});

describe("DashboardPage — failure visibility", () => {
  beforeEach(() => {
    queryKeys.length = 0;
    dashboardDataRef.current = true;
    tzRef.current = "UTC";
    replaceSpy.mockClear();
    cleanup();
  });

  it("states the error rate with its denominator spelled out", async () => {
    const user = userEvent.setup();
    renderDashboard();

    // The Tasks tile on the Usage tab keeps its own started-tasks-only figure.
    expect(screen.getByText("1 failed")).toBeInTheDocument();

    await openErrorsTab(user);

    // The run-time rollup sees 1 failure out of 12 tasks — it only counts
    // tasks that actually started. The failure rollup also sees tasks that
    // never started, so it reports 4 out of 10. The rate tile quotes the
    // latter *with* its denominator, which is what keeps it from reading as a
    // contradiction of the Tasks tile on the other tab.
    expect(screen.getByText("4 of 10 runs failed · 40%")).toBeInTheDocument();
  });

  it("breaks failures down by class and links the offending agent to its runs", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await openErrorsTab(user);

    // Auth (3) outranks Timeout (1), and both are named by class rather than
    // by the raw failure_reason enum.
    const byClass = within(screen.getByRole("list", { name: "Failure mix" }));
    expect(byClass.getAllByRole("listitem").map((li) => li.textContent)).toEqual([
      "Auth3",
      "Timeout1",
    ]);
    // The section names its own denominator: the rate tile above quotes a
    // rate over all 10 runs, this one splits the 4 failures.
    expect(screen.getByText("Failure mix · 4")).toBeInTheDocument();

    const byAgent = within(screen.getByRole("list", { name: "Top offenders" }));
    const link = byAgent.getByRole("link", { name: /Agent One/ });
    // Overview, NOT ?view=work: Work lists the issues assigned to the agent,
    // while its recent runs (and each failure's reason) live in the Overview
    // pane's ActivityTab.
    expect(link).toHaveAttribute("href", "/acme/agents/agent-1?view=overview");
    // One labelled column per number instead of the old unlabelled
    // `4 / 10 · 40%` blob.
    const row = byAgent.getAllByRole("listitem")[0] as HTMLElement;
    expect(within(row).getByText("4")).toBeInTheDocument();
    expect(within(row).getByText("10")).toBeInTheDocument();
    expect(within(row).getByText("40%")).toBeInTheDocument();
    // The bar is the only place the class split lives now that the dominant-
    // class badge is gone, so it has to name that split for screen readers.
    expect(within(row).getByRole("img")).toHaveAccessibleName("Auth 3 · Timeout 1");
  });

  it("moves the bar onto whichever metric the list is ranked by", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await openErrorsTab(user);

    // Agent One failed 4 of 10; the anonymous bucket failed 2 of 2. Under
    // Failures the bucket is half the leader's bar. Under Rate it is 100% —
    // a bar that stayed on the failure count while the row shouted "100%" is
    // exactly the mismatch this control exists to remove.
    expect(offenderBar(1).style.width).toBe("50%");

    await user.click(within(offenderSort()).getByRole("button", { name: "Rate" }));

    expect(offenderBar(1).style.width).toBe("100%");
  });

  it("says which metric the list is ranked by without relying on colour", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await openErrorsTab(user);

    // The active option used to be a colour swap and nothing else, which is
    // invisible to a screen reader. The group is named too — "Rate, pressed"
    // means nothing until you know the group ranks the offender list.
    const group = offenderSort();
    expect(
      within(group).getByRole("button", { name: "Failures" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(within(group).getByRole("button", { name: "Rate" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );

    await user.click(within(group).getByRole("button", { name: "Rate" }));

    expect(within(group).getByRole("button", { name: "Rate" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("keeps a two-run agent from hijacking the rate ranking", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await openErrorsTab(user);

    await user.click(within(offenderSort()).getByRole("button", { name: "Rate" }));

    // 2/2 is a 100% rate and 4/10 is 40%, but two runs is not evidence. The
    // small-sample row is demoted, not dropped — the list still has to
    // reconcile with the workspace failure count above it.
    const rows = within(screen.getByRole("list", { name: "Top offenders" }))
      .getAllByRole("listitem")
      .map((li) => li.textContent);
    expect(rows[0]).toMatch(/Agent One/);
    expect(rows[1]).toMatch(/Other agents/);
  });

  it("reveals the raw failure_reason values behind the class summary", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await openErrorsTab(user);

    expect(
      screen.queryByText("agent_error.provider_auth_or_access"),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Show error codes" }));

    // Raw and unlocalised: an operator pastes this exact string into a log
    // search, so it must not be translated or prettified.
    expect(
      screen.getByText("agent_error.provider_auth_or_access"),
    ).toBeInTheDocument();
  });

  it("gives failures their own chart instead of a slot on the spend toggle", async () => {
    const user = userEvent.setup();
    const { container } = renderDashboard();

    // Charting failures used to mean hiding spend: Errors was the fifth option
    // of the single trend toggle, so "what did it cost" and "what broke" could
    // not be on screen in the same breath.
    const metrics = within(screen.getByRole("group", { name: "Metric" }));
    expect(
      metrics.queryByRole("button", { name: "Errors" }),
    ).not.toBeInTheDocument();
    expect(metrics.getByRole("button", { name: "Tokens" })).toBeInTheDocument();

    await openErrorsTab(user);

    expect(container).toHaveTextContent("Daily errors");
  });
});

describe("DashboardPage — the Errors list never exposes an agent the viewer can't see", () => {
  beforeEach(() => {
    queryKeys.length = 0;
    dashboardDataRef.current = true;
    tzRef.current = "UTC";
    replaceSpy.mockClear();
    cleanup();
  });

  it("folds an unresolvable agent into an anonymous row instead of printing its UUID", async () => {
    // The failure rollups are workspace-scoped and deliberately skip
    // per-agent visibility, but the agent list does not: a private agent
    // this member can't see never appears there. Rendering `row.agentId`
    // would leak its existence, failure count and failure rate.
    const user = userEvent.setup();
    const { container } = renderDashboard();
    await openErrorsTab(user);

    expect(container).not.toHaveTextContent("0f9d1c2e-private-agent-uuid");

    const byAgent = within(screen.getByRole("list", { name: "Top offenders" }));
    expect(byAgent.getByText("Other agents")).toBeInTheDocument();
    // Anonymous, and therefore not a link — there is no page to open.
    expect(
      byAgent.queryByRole("link", { name: /Other agents/ }),
    ).not.toBeInTheDocument();
  });
});

// The page answers two questions — "what did this cost" and "what broke" —
// and used to answer both on one scroll, where the failure breakdown sat
// below a leaderboard that can itself run to thirty rows (MUL-5759).
describe("DashboardPage — the two questions are separate tabs", () => {
  beforeEach(() => {
    queryKeys.length = 0;
    dashboardDataRef.current = true;
    manyAgentsRef.current = false;
    tzRef.current = "UTC";
    replaceSpy.mockClear();
    cleanup();
  });

  it("keeps failures off the Usage tab and reaches them in one click", async () => {
    const user = userEvent.setup();
    renderDashboard();

    expect(
      screen.queryByRole("list", { name: "Top offenders" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Leaderboard" })).toBeInTheDocument();

    await openErrorsTab(user);

    expect(screen.getByRole("list", { name: "Top offenders" })).toBeInTheDocument();
    // The two per-agent rankings had near-identical shapes and used to stack
    // one above the other; each now belongs to the question it answers.
    expect(
      screen.queryByRole("list", { name: "Leaderboard" }),
    ).not.toBeInTheDocument();
  });

  it("puts the tab in the URL so an Errors view can be linked", async () => {
    const user = userEvent.setup();
    renderDashboard();

    await openErrorsTab(user);
    expect(replaceSpy).toHaveBeenLastCalledWith("/acme/usage?tab=errors");

    // Returning to the default view drops the param instead of pinning
    // ?tab=usage onto every link out of this page.
    await user.click(screen.getByRole("tab", { name: "Usage" }));
    expect(replaceSpy).toHaveBeenLastCalledWith("/acme/usage");
  });

  it("opens straight onto Errors when the URL asks for it", () => {
    renderDashboard("tab=errors");

    expect(screen.getByRole("list", { name: "Top offenders" })).toBeInTheDocument();
  });

  it("falls back to Usage for a tab value it does not recognise", () => {
    renderDashboard("tab=nonsense");

    expect(screen.getByRole("list", { name: "Leaderboard" })).toBeInTheDocument();
  });

  it("caps the offender list and expands it on demand", async () => {
    manyAgentsRef.current = true;
    const user = userEvent.setup();
    renderDashboard();
    await openErrorsTab(user);

    const list = () => screen.getByRole("list", { name: "Top offenders" });
    // 12 agents have failures, but an unbounded list is what made this card
    // taller than the rest of the page put together.
    expect(within(list()).getAllByRole("listitem")).toHaveLength(8);
    // The toggle is the truncation signal — its label carries the full count,
    // so the cap is never silent.
    await user.click(screen.getByRole("button", { name: "Show all 12" }));
    expect(within(list()).getAllByRole("listitem")).toHaveLength(12);

    await user.click(screen.getByRole("button", { name: "Show top 8" }));
    expect(within(list()).getAllByRole("listitem")).toHaveLength(8);
  });

  it("shows no expand affordance when every offender already fits", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await openErrorsTab(user);

    expect(
      screen.queryByRole("button", { name: /Show all/ }),
    ).not.toBeInTheDocument();
  });
});

// MUL-5409. The server folds every agent it won't name — those the viewer may
// not see, plus the hidden system carriers behind agent-builder sessions — onto
// one sentinel row. The leaderboard used to have a single synthetic row,
// labelled "Deleted agents" with a bin icon and dashed-out Time/Tasks, so the
// user was told "N agents were deleted" about agents that are alive and running.
describe("DashboardPage — the leaderboard tells the truth about the server's bucket", () => {
  beforeEach(() => {
    queryKeys.length = 0;
    dashboardDataRef.current = true;
    manyAgentsRef.current = false;
    restrictedBucketRef.current = true;
    tzRef.current = "UTC";
    cleanup();
  });

  afterEach(() => {
    restrictedBucketRef.current = false;
  });

  it("labels the bucket neutrally, never as deleted", () => {
    const { container } = renderDashboard();

    const list = within(screen.getByRole("list", { name: "Leaderboard" }));
    expect(list.getByText("Other agents")).toBeInTheDocument();
    expect(list.queryByText("Deleted agents")).not.toBeInTheDocument();
    // The sentinel is a placeholder, not an id to render.
    expect(container).not.toHaveTextContent("__restricted_agents__");
  });

  it("counts it as neither an agent nor a deletion in the caption", () => {
    const { container } = renderDashboard();

    // One real agent in the fixture. The bucket is a row, not an agent, and
    // nothing here was deleted — so no "· N deleted" suffix.
    expect(container).toHaveTextContent("1 agents");
    expect(container).not.toHaveTextContent("deleted");
  });

  it("keeps the bucket's run time and task count instead of dashing them out", () => {
    // Those agents really ran; the server merely merged them. Blanking these
    // columns (which is right for a hard-deleted agent) would under-report the
    // workspace's run time against the Time / Tasks KPIs above.
    renderDashboard();

    const rows = within(
      screen.getByRole("list", { name: "Leaderboard" }),
    ).getAllByRole("listitem");
    const bucket = rows.find((r) => r.textContent?.includes("Other agents"));
    expect(bucket).toBeDefined();
    expect(bucket).toHaveTextContent("2h");
    expect(bucket).toHaveTextContent("4");
  });
});

describe("DashboardPage — leaderboard density", () => {
  beforeEach(() => {
    queryKeys.length = 0;
    dashboardDataRef.current = true;
    manyAgentsRef.current = false;
    tzRef.current = "UTC";
    cleanup();
  });

  it("ranks the top 10 agents and keeps the tail behind a toggle", async () => {
    manyAgentsRef.current = true;
    const user = userEvent.setup();
    renderDashboard();

    const list = () => within(screen.getByRole("list", { name: "Leaderboard" }));
    // 12 agents have usage. Flattening all of them is what pushed the Errors
    // card a full screen below the fold (MUL-5388).
    expect(list().getAllByRole("listitem")).toHaveLength(10);
    // Ranked by tokens desc, so the two smallest spenders are the ones cut.
    expect(list().getByText("Bulk Agent 0")).toBeInTheDocument();
    expect(list().queryByText("Bulk Agent 10")).not.toBeInTheDocument();
    expect(list().queryByText("Bulk Agent 11")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Show all" }));
    expect(list().getAllByRole("listitem")).toHaveLength(12);
    expect(list().getByText("Bulk Agent 11")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Show top 10" }));
    expect(list().getAllByRole("listitem")).toHaveLength(10);
  });

  it("keeps the cap honest when the ranking metric changes", async () => {
    manyAgentsRef.current = true;
    const user = userEvent.setup();
    renderDashboard();

    // Re-ranking must not quietly reveal the tail: the cap belongs to the
    // list, not to one metric.
    const sortControls = within(
      screen.getByRole("group", { name: "Rank agents by" }),
    );
    await user.click(sortControls.getByRole("button", { name: "Time" }));

    const list = within(screen.getByRole("list", { name: "Leaderboard" }));
    expect(list.getAllByRole("listitem")).toHaveLength(10);
  });

  it("shows no expand affordance when every agent already fits", () => {
    renderDashboard();

    const list = within(screen.getByRole("list", { name: "Leaderboard" }));
    expect(list.getAllByRole("listitem")).toHaveLength(1);
    expect(
      screen.queryByRole("button", { name: "Show all" }),
    ).not.toBeInTheDocument();
  });

  it("exposes wide columns through a named keyboard-focusable local scroller", () => {
    renderDashboard();

    const scroller = screen.getByRole("region", { name: "Leaderboard" });
    const list = within(scroller).getByRole("list", { name: "Leaderboard" });
    const row = within(list).getByRole("listitem");

    expect(scroller).toHaveAttribute("tabindex", "0");
    scroller.focus();
    expect(scroller).toHaveFocus();
    expect(scroller).toHaveClass(
      "overflow-x-auto",
      "overscroll-x-contain",
      "[-webkit-overflow-scrolling:touch]",
    );
    expect(row).toHaveStyle({
      minWidth: "fit-content",
      gridTemplateColumns:
        "minmax(10rem, 1.6fr) minmax(6rem, 1fr) 5rem 5rem 5rem 4rem",
    });
  });
});

// The live Prometheus panels answer "how is the fleet doing right now", a
// question the calendar-day task rollups don't. When those rollups come back
// empty the page used to replace its whole body with the empty state — panels
// unmounted, hooks unfired, so a healthy deployment looked dead (QA P1,
// RUYI-618). Empty usage must decide the usage sections' presentation only.
describe("DashboardPage — live metric panels survive an empty usage workspace", () => {
  beforeEach(() => {
    queryKeys.length = 0;
    dashboardDataRef.current = false;
    emptyUsageRef.current = true;
    metricPanelsRef.current = { resources: "data", traffic: "data" };
    tzRef.current = "UTC";
    cleanup();
  });

  afterEach(() => {
    emptyUsageRef.current = false;
    metricPanelsRef.current = { resources: false, traffic: false };
  });

  it("mounts both panels and fires their queries when usage is empty", () => {
    renderDashboard();

    // The usage sections still speak the truth — no spend yet — but the page
    // must not stop there: the panels have their own data source.
    expect(screen.getByText("No usage yet")).toBeInTheDocument();
    expect(screen.getByTestId("usage-resources-card")).toBeInTheDocument();
    expect(screen.getByTestId("usage-traffic-card")).toBeInTheDocument();
    // Mounting is the contract the QA repro tests: with the panels inside the
    // empty branch, these two queries were never issued at all.
    expect(
      queryKeys.some((k) => k[0] === "dashboard" && k[2] === "usage-resources"),
    ).toBe(true);
    expect(
      queryKeys.some((k) => k[0] === "dashboard" && k[2] === "usage-traffic"),
    ).toBe(true);
  });

  it("keeps the traffic panel on its own empty state, not the page's", () => {
    metricPanelsRef.current = { resources: "data", traffic: "empty" };
    renderDashboard();

    const traffic = screen.getByTestId("usage-traffic-card");
    expect(
      within(traffic).getByText("No samples in this window."),
    ).toBeInTheDocument();
    // The page-level empty state renders above the panels, not instead of them.
    expect(screen.getByText("No usage yet")).toBeInTheDocument();
  });

  it("keeps the panels below the usage sections when usage has data", () => {
    dashboardDataRef.current = true;
    emptyUsageRef.current = false;
    renderDashboard();

    // Baseline order for a workspace with spend: leaderboard, then the two
    // live panels. The empty-state fix must not reshuffle this page.
    const leaderboard = screen.getByRole("list", { name: "Leaderboard" });
    const resources = screen.getByTestId("usage-resources-card");
    const traffic = screen.getByTestId("usage-traffic-card");
    expect(
      leaderboard.compareDocumentPosition(resources) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      resources.compareDocumentPosition(traffic) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });
});

// The period navigation is page-owned state: the filter only reports intent,
// the page derives the next window. That seam had zero coverage while the
// shift handler's sign flipped — ‹ clamped itself back onto the current
// window and no quick range could ever leave "now" (QA BUG-1).
describe("DashboardPage — quick-range period navigation", () => {
  beforeEach(() => {
    queryKeys.length = 0;
    dashboardDataRef.current = true;
    tzRef.current = "UTC";
    replaceSpy.mockClear();
    cleanup();
  });

  // The StatWindow rides inside every rollup query key (dashboardKeys.*),
  // so reading windows off the captured keys asserts what the six endpoints
  // are actually asked for rather than what the trigger label claims. The
  // live metric panels (usage-resources / usage-traffic) are excluded: they
  // deliberately run on their own relative windows, independent of the
  // page's calendar window, so the one-window invariant below is about the
  // rollups only.
  function askedWindows(): string[] {
    return queryKeys
      .filter((k) => k[0] === "dashboard")
      .filter((k) => !String(k[2]).startsWith("usage-"))
      .map((k) => JSON.stringify(k[3]));
  }

  it("pages ‹ into the past and › back through whole periods to now", async () => {
    const user = userEvent.setup();
    renderDashboard();

    const current = askedWindows();
    expect(current.length).toBeGreaterThan(0);
    // Eight useQuery sites, one shared window — the boundary this page exists
    // to keep, still true the moment the window starts moving. The
    // leaderboard's per-agent pair repeats the statWindow when nothing is
    // selected, so the duplicate keys are a React Query dedupe, not extra
    // fetches.
    expect(new Set(current).size).toBe(1);
    const currentWindow = JSON.parse(current[0]!);

    queryKeys.length = 0;
    await user.click(screen.getByRole("button", { name: "Previous period" }));

    const past = askedWindows();
    expect(past.length).toBeGreaterThan(0);
    expect(new Set(past).size).toBe(1);
    // Exactly one 30-day period back, not a clamped no-op.
    expect(JSON.parse(past[0]!)).toEqual({
      start: addDaysIso(currentWindow.start, -30),
      end: addDaysIso(currentWindow.end, -30),
    });

    queryKeys.length = 0;
    await user.click(screen.getByRole("button", { name: "Next period" }));

    // The round trip closes on the window it started from, and "now" is
    // where › dies again — the forward direction has no future to show.
    expect(new Set(askedWindows())).toEqual(new Set(current));
    expect(screen.getByRole("button", { name: "Next period" })).toBeDisabled();
  });
});

// The leaderboard follows the bar selection: clicking or dragging bars
// narrows the per-agent rollups to the selected span — the rollups carry no
// dates, so narrowing means re-querying, not filtering rows client-side.
// Every other card (KPIs, trend charts, failures) keeps the full statWindow.
// With no selection the leaderboard window IS the statWindow: same query
// key, zero extra requests.
describe("DashboardPage — the leaderboard follows the bar selection", () => {
  beforeEach(() => {
    queryKeys.length = 0;
    dashboardDataRef.current = true;
    manyAgentsRef.current = false;
    lbWindowRef.current = null;
    tzRef.current = "UTC";
    replaceSpy.mockClear();
    cleanup();
  });

  afterEach(() => {
    lbWindowRef.current = null;
  });

  function askedWindows(): string[] {
    return queryKeys
      .filter((k) => k[0] === "dashboard")
      // Live metric panels (usage-resources / usage-traffic) deliberately run
      // on their own relative windows, independent of the calendar window —
      // same exclusion as the earlier suite's helper; the one-window
      // invariant here is about the calendar rollups only.
      .filter((k) => !String(k[2]).startsWith("usage-"))
      .map((k) => JSON.stringify(k[3]));
  }

  function windowsFor(kind: string): string[] {
    return queryKeys
      .filter((k) => k[0] === "dashboard" && k[2] === kind)
      .map((k) => JSON.stringify(k[3]));
  }

  it("keeps one shared window everywhere and inert charts when nothing is selected", () => {
    renderDashboard();

    const current = askedWindows();
    expect(current.length).toBeGreaterThan(0);
    expect(new Set(current).size).toBe(1);
    // Nothing selected, nothing scoping the leaderboard: no range chip.
    expect(
      screen.queryByRole("button", { name: "Clear selection" }),
    ).not.toBeInTheDocument();
  });

  it("re-queries only the per-agent rollups on the selected span", async () => {
    const user = userEvent.setup();
    renderDashboard();

    const full = JSON.parse(askedWindows()[0]!);
    // The usage fixture carries one row at `today` (the window's last day),
    // so the stub emits that single day as the selection.
    const day = JSON.stringify({ start: full.end, end: full.end });
    lbWindowRef.current = day;
    queryKeys.length = 0;

    await user.click(screen.getByRole("button", { name: "stub:daily-tokens" }));

    // The per-agent rollups were asked for both the full window and the
    // selected single day — the leaderboard's two queries moved, nothing
    // else did.
    expect(
      new Set([...windowsFor("by-agent"), ...windowsFor("agent-runtime")]),
    ).toEqual(new Set([JSON.stringify(full), day]));
    // Date-bucketed series stay on the full window only.
    expect(new Set(windowsFor("daily")).size).toBe(1);
    expect(new Set(windowsFor("runtime-daily")).size).toBe(1);
    expect(new Set(windowsFor("failures-daily")).size).toBe(1);
    expect(new Set(windowsFor("failures-by-agent")).size).toBe(1);
    expect(windowsFor("daily")).not.toContain(day);

    // And the rows rendered come from the SELECTED window's answer, not the
    // full-window one.
    const board = within(screen.getByRole("list", { name: "Leaderboard" }));
    expect(board.getByText("Selected Agent")).toBeInTheDocument();
    expect(board.queryByText("Agent One")).not.toBeInTheDocument();
  });

  it("restores the full-window leaderboard when the selection is cleared", async () => {
    const user = userEvent.setup();
    renderDashboard();

    const full = JSON.parse(askedWindows()[0]!);
    lbWindowRef.current = JSON.stringify({ start: full.end, end: full.end });
    await user.click(screen.getByRole("button", { name: "stub:daily-tokens" }));
    expect(screen.getByText("Selected Agent")).toBeInTheDocument();

    queryKeys.length = 0;
    await user.click(screen.getByRole("button", { name: "Clear selection" }));

    expect(new Set(askedWindows()).size).toBe(1);
    const board = within(screen.getByRole("list", { name: "Leaderboard" }));
    expect(board.getByText("Agent One")).toBeInTheDocument();
    expect(board.queryByText("Selected Agent")).not.toBeInTheDocument();
  });

  it("clamps an out-of-window span to the stat window", async () => {
    const user = userEvent.setup();
    renderDashboard();

    const full = JSON.parse(askedWindows()[0]!);

    // The weekly stub only renders on the Weekly cut.
    const dims = within(screen.getByRole("group", { name: "Dimension" }));
    await user.click(dims.getByRole("button", { name: "Weekly" }));

    queryKeys.length = 0;
    // It emits two days beyond each window edge — a Mon–Sun calendar week
    // straddling the window start or end.
    await user.click(screen.getByRole("button", { name: "stub:weekly-tokens" }));

    // No query escapes the window the KPIs quote: the clamped selection
    // collapsed onto the full window instead of trading past it.
    for (const w of askedWindows().map((w) => JSON.parse(w))) {
      expect(w.start >= full.start).toBe(true);
      expect(w.end <= full.end).toBe(true);
    }
    expect(new Set(askedWindows()).size).toBe(1);
  });

  it("clears the selection when the stat window changes", async () => {
    const user = userEvent.setup();
    renderDashboard();

    const full = JSON.parse(askedWindows()[0]!);
    lbWindowRef.current = JSON.stringify({ start: full.end, end: full.end });
    await user.click(screen.getByRole("button", { name: "stub:daily-tokens" }));
    expect(screen.getByText("Selected Agent")).toBeInTheDocument();

    queryKeys.length = 0;
    await user.click(screen.getByRole("button", { name: "Previous period" }));

    // The shifted period's queries are all full-window again — a stale
    // selection must not ride along to windows its bars no longer exist in.
    expect(new Set(askedWindows()).size).toBe(1);
  });

  it("drops a stored selection once the day rolls over past its window", async () => {
    const user = userEvent.setup();
    renderDashboard();

    const full = JSON.parse(askedWindows()[0]!);

    // The weekly stub only renders on the Weekly cut, and its overhanging
    // Mon–Sun span clamps to exactly the stat window: the stored selection
    // covers the whole window.
    const dims = within(screen.getByRole("group", { name: "Dimension" }));
    await user.click(dims.getByRole("button", { name: "Weekly" }));
    await user.click(screen.getByRole("button", { name: "stub:weekly-tokens" }));
    expect(
      screen.getByRole("button", { name: "Clear selection" }),
    ).toBeInTheDocument();

    // Midnight: the quick window re-anchors on the new day with no handler
    // firing — the explicit-change clear sites never run. The stored
    // whole-window span starts before the shifted window's first day, so it
    // no longer overlaps and must read as no selection: no chip, every query
    // back on the one visible window, no off-axis leaderboard.
    todayRef.current = addDaysIso(todayIso(), 1);
    lbWindowRef.current = JSON.stringify(full);
    queryKeys.length = 0;
    await user.click(screen.getByRole("tab", { name: /Errors/ }));
    await user.click(screen.getByRole("tab", { name: "Usage" }));

    expect(
      screen.queryByRole("button", { name: "Clear selection" }),
    ).not.toBeInTheDocument();
    expect(new Set(askedWindows()).size).toBe(1);
    expect(askedWindows()).not.toContain(JSON.stringify(full));
    expect(
      within(screen.getByRole("list", { name: "Leaderboard" })).getByText(
        "Agent One",
      ),
    ).toBeInTheDocument();
  });

  it("shares one selection across the Usage and Errors tabs", async () => {
    const user = userEvent.setup();
    renderDashboard();

    await openErrorsTab(user);
    const full = JSON.parse(askedWindows()[0]!);
    const day = JSON.stringify({ start: full.end, end: full.end });
    lbWindowRef.current = day;
    queryKeys.length = 0;

    await user.click(screen.getByRole("button", { name: "stub:daily-errors" }));

    // The selection is page state, not chart state: the Errors-tab chart
    // drives the same per-agent re-query the Usage-tab charts do.
    expect(windowsFor("by-agent")).toContain(day);
    expect(windowsFor("daily")).not.toContain(day);

    // Back on Usage the leaderboard is already on the selected span.
    await user.click(screen.getByRole("tab", { name: "Usage" }));
    expect(
      within(screen.getByRole("list", { name: "Leaderboard" })).getByText(
        "Selected Agent",
      ),
    ).toBeInTheDocument();
  });
});
