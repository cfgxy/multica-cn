// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Agent, AgentTask } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";
import {
  NavigationProvider,
  type NavigationAdapter,
} from "../../../navigation";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// api / paths are only reached from a rendered TaskRow, which never mounts in
// the loading and empty states under test — stub them so the module graph
// resolves without dragging in platform wiring.
vi.mock("@multica/core/api", () => ({ api: {} }));

// TaskRow renders path links only when a task row mounts (the retrospective
// cases below); the workspace-slug context they normally read from is not
// part of this harness, so hand back a fixed shape.
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/acme/issues/${id}`,
    selfEvolutionProposals: () => "/acme/self-evolution/proposals",
  }),
}));

// The tab reads three data sources. Snapshot ("Now") and the activity map
// ("Last 30 days") stay empty; the per-agent task list is the one under test,
// its queryFn swapped per test to stay pending or resolve.
const agentTasksRef = vi.hoisted(() => ({
  current: () => new Promise<unknown>(() => {}),
}));
vi.mock("@multica/core/agents", () => ({
  agentTaskSnapshotOptions: () => ({
    queryKey: ["snapshot"],
    queryFn: () => Promise.resolve([]),
  }),
  agentTasksOptions: () => ({
    queryKey: ["agent-tasks"],
    queryFn: () => agentTasksRef.current(),
  }),
  useWorkspaceActivityMap: () => ({ byAgent: new Map() }),
  summarizeActivityWindow: () => ({
    totalRuns: 0,
    totalFailed: 0,
    buckets: [],
  }),
}));

import { ActivityTab } from "./activity-tab";

const baseAgent = {
  id: "agent-1",
  name: "Agent",
} as unknown as Agent;

const EMPTY_RECENT = "This agent hasn't completed anything yet.";

function renderTab() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const navigation: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/agents/agent-1",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (path) => path,
  };
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <NavigationProvider value={navigation}>
        <QueryClientProvider client={queryClient}>
          <ActivityTab agent={baseAgent} showPerformance={false} />
        </QueryClientProvider>
      </NavigationProvider>
    </I18nProvider>,
  );
}

beforeEach(() => {
  agentTasksRef.current = () => new Promise<unknown>(() => {});
});

describe("ActivityTab Recent work loading state", () => {
  it("shows a skeleton, not the empty state, while the task list is loading", () => {
    // Never-resolving queryFn keeps the per-agent task query pending, which is
    // exactly the first-paint window the skeleton is meant to cover.
    const { container } = renderTab();
    expect(
      container.querySelectorAll('[data-slot="skeleton"]').length,
    ).toBeGreaterThan(0);
    expect(screen.queryByText(EMPTY_RECENT)).not.toBeInTheDocument();
  });

  it("shows the empty state once the task list resolves to no runs", async () => {
    agentTasksRef.current = () => Promise.resolve([]);
    renderTab();
    expect(await screen.findByText(EMPTY_RECENT)).toBeInTheDocument();
    expect(
      document.querySelectorAll('[data-slot="skeleton"]').length,
    ).toBe(0);
  });
});

// A daily-retrospective run (RUYI-673): no issue, no chat, no autopilot —
// its identity comes from kind="retrospective" plus the server-provided
// run id and window end.
const retroTask = {
  id: "task-retro-1",
  agent_id: "agent-1",
  issue_id: "",
  status: "completed",
  created_at: "2026-10-11T00:00:00Z",
  started_at: "2026-10-11T00:00:05Z",
  completed_at: "2026-10-11T00:03:00Z",
  kind: "retrospective",
  retrospective_run_id: "5b0e0000-0000-4000-8000-000000000001",
  retrospective_window_end: "2026-10-11T00:00:00Z",
} as unknown as AgentTask;

describe("ActivityTab daily-retrospective runs (RUYI-673)", () => {
  it("titles the run and shows its source badge instead of Untracked", async () => {
    agentTasksRef.current = () => Promise.resolve([retroTask]);
    renderTab();
    // Readable title: localized label + the window-end stamp, never the
    // "Untracked" fallback.
    expect(
      await screen.findByText(/Daily retrospective 20261011_/),
    ).toBeInTheDocument();
    expect(screen.queryByText("Untracked")).not.toBeInTheDocument();
    // Visible source marker in the meta line (exact text, distinct from
    // the stamped title).
    expect(screen.getByText("Daily retrospective")).toBeInTheDocument();
  });

  it("links the run to its retrospective results view", async () => {
    agentTasksRef.current = () => Promise.resolve([retroTask]);
    renderTab();
    const link = await screen.findByRole("link", {
      name: /retrospective results/i,
    });
    expect(link.getAttribute("href")).toContain(
      "/self-evolution/proposals?run=5b0e0000-0000-4000-8000-000000000001",
    );
  });

  it("keeps non-retrospective no-issue runs on their existing labels", async () => {
    agentTasksRef.current = () =>
      Promise.resolve([
        {
          id: "task-qc-1",
          agent_id: "agent-1",
          issue_id: "",
          status: "completed",
          created_at: "2026-10-11T00:00:00Z",
          completed_at: "2026-10-11T00:01:00Z",
          kind: "quick_create",
        } as unknown as AgentTask,
      ]);
    renderTab();
    expect(await screen.findByText("Quick create")).toBeInTheDocument();
    expect(screen.queryByText(/Daily retrospective/)).not.toBeInTheDocument();
  });
});
