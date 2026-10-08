// @vitest-environment jsdom

import { it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { toast } from "sonner";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Agent, RetrospectiveConfig, RetrospectiveRun } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
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

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({
    queryKey: ["test", "agents"],
    queryFn: () => Promise.resolve(state.agents),
  }),
}));

vi.mock("@multica/core/api", () => ({
  ApiError: TestApiError,
  clientErrorMessage: (e: unknown) => (e instanceof Error ? e.message : undefined),
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

function renderTab() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <RetrospectiveTab wsId="ws-1" />
      </I18nProvider>
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

it("keeps the raw server message for non-409 run failures", async () => {
  state.triggerResult = rejected(new TestApiError("retrospective queue busy", 500));
  renderTab();
  fireEvent.click(await screen.findByRole("button", { name: "Run now" }));

  await waitFor(() => expect(vi.mocked(toast.error)).toHaveBeenCalled());
  expect(vi.mocked(toast.error)).toHaveBeenCalledWith("retrospective queue busy");
});

it("toasts the started wording on an accepted run trigger", async () => {
  renderTab();
  fireEvent.click(await screen.findByRole("button", { name: "Run now" }));

  await waitFor(() => expect(vi.mocked(toast.success)).toHaveBeenCalled());
  expect(vi.mocked(toast.success)).toHaveBeenCalledWith("Retrospective run started.");
});
