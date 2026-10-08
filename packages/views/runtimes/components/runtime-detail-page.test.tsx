// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ComponentProps, ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

const mockState = vi.hoisted(() => ({
  fixtureRuntimes: [] as unknown[],
  runtimeListProps: [] as Array<Record<string, unknown>>,
}));

// Unit under test: the PAGE WIRING — which machineRuntimeCount
// RuntimeDetailPage hands to RuntimeList. RuntimeList's DOM is stubbed to
// capture props and re-derive each row label with the real runtimeRowLabel,
// so assertions cover the label the user would see without re-testing
// RuntimeList internals.
vi.mock("./runtime-list", async () => {
  const actual =
    await vi.importActual<typeof import("./runtime-list")>("./runtime-list");
  const { runtimeRowLabel } =
    await vi.importActual<typeof import("./runtime-machines")>(
      "./runtime-machines",
    );
  return {
    ...actual,
    RuntimeList: (props: ComponentProps<typeof actual.RuntimeList>): ReactNode => {
      mockState.runtimeListProps.push(props);
      return (
        <div>
          {props.runtimes.map((runtime) => (
            <div key={runtime.id} data-testid="runtime-row-label">
              {runtimeRowLabel(
                runtime,
                props.machineTitle ?? "",
                props.machineRuntimeCount,
              )}
            </div>
          ))}
        </div>
      );
    },
  };
});

vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: vi.fn((options: { queryKey?: readonly unknown[] }) => {
      const key = options?.queryKey;
      if (key?.[0] === "runtimes" && key?.[2] === "list") {
        return { data: mockState.fixtureRuntimes, isLoading: false };
      }
      return { data: [], isLoading: false };
    }),
  };
});

vi.mock("@multica/core/runtimes/queries", () => ({
  runtimeKeys: { all: (wsId: string) => ["runtimes", wsId] as const },
  runtimeListOptions: (wsId: string) => ({
    queryKey: ["runtimes", wsId, "list"] as const,
  }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: (wsId: string) => ({ queryKey: ["agents", wsId] }),
  memberListOptions: (wsId: string) => ({ queryKey: ["members", wsId] }),
}));

vi.mock("@multica/core/agents", () => ({
  agentTaskSnapshotOptions: (wsId: string) => ({
    queryKey: ["agent-tasks", wsId],
  }),
}));

vi.mock("@multica/core/runtimes", async () => ({
  ...(await vi.importActual<typeof import("@multica/core/runtimes")>(
    "@multica/core/runtimes",
  )),
  runtimeProfileListOptions: (wsId: string) => ({
    queryKey: ["runtime-profiles", wsId],
  }),
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } }) => unknown) =>
    sel({ user: { id: "user-me" } }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    runtimes: () => "/runtimes",
    runtimeDetail: (machineId: string) => `/runtimes/machines/${machineId}`,
    runtimeSettings: (machineId: string, runtimeId: string) =>
      `/runtimes/machines/${machineId}/settings/${runtimeId}`,
  }),
}));

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: () => undefined,
}));

vi.mock("./shared", () => ({
  HealthIcon: () => null,
  useHealthLabel: () => (health: string) => health,
}));

vi.mock("./rename-machine-dialog", () => ({
  RenameMachineDialog: () => null,
}));

vi.mock("./runtime-profiles-dialog", () => ({
  RuntimeProfilesDialog: () => null,
}));

vi.mock("./machine-cli-section", () => ({
  MachineCliSection: () => null,
}));

vi.mock("../../navigation", () => ({
  AppLink: ({ children }: { children: ReactNode }) => <>{children}</>,
  useNavigation: () => ({ push: vi.fn(), replace: vi.fn() }),
}));

import { RuntimeDetailPage } from "./runtime-detail-page";

function makeRuntime(overrides: Partial<AgentRuntime>): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Local Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "host.local",
    metadata: {},
    owner_id: "user-me",
    visibility: "private",
    last_seen_at: "2026-04-27T11:59:50Z",
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    ...overrides,
  };
}

function renderPage(runtimeId: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <RuntimeDetailPage runtimeId={runtimeId} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

interface CapturedRuntimeListProps {
  machineRuntimeCount?: number;
  machineTitle?: string;
}

function lastRuntimeListProps(): CapturedRuntimeListProps {
  const props = mockState.runtimeListProps.at(-1);
  if (!props) throw new Error("RuntimeList was never rendered");
  return props as CapturedRuntimeListProps;
}

describe("RuntimeDetailPage machineRuntimeCount wiring", () => {
  beforeEach(() => {
    mockState.runtimeListProps.length = 0;
    mockState.fixtureRuntimes = [];
  });

  // RUYI-564 candidate 1-A: on a single-runtime machine the alias IS the
  // instance display name, so the page must hand RuntimeList the machine's
  // real runtime count — an unwired (undefined) count silently falls back
  // to the legacy collapse and the user's name disappears.
  it("passes the machine's real runtime count so a single-runtime machine keeps its alias", () => {
    mockState.fixtureRuntimes = [
      makeRuntime({
        id: "rt-voice",
        daemon_id: "daemon-voice",
        name: "Gemini (Web-1)",
        custom_name: "My Voice",
        provider: "gemini",
      }),
    ];
    renderPage("local:daemon-voice");

    const props = lastRuntimeListProps();
    expect(props.machineTitle).toBe("My Voice");
    expect(props.machineRuntimeCount).toBe(1);

    const labels = screen.getAllByTestId("runtime-row-label");
    expect(labels).toHaveLength(1);
    expect(labels[0]).toHaveTextContent("My Voice");
  });

  // The count must be the real number, not a hardcoded 1: a multi-runtime
  // machine renamed at machine level keeps collapsing per-row aliases to
  // the provider base (MUL-4217 noise rule).
  it("passes the true count for multi-runtime machines so machine-level aliases still collapse", () => {
    mockState.fixtureRuntimes = [
      makeRuntime({
        id: "rt-a",
        daemon_id: "daemon-multi",
        name: "Pi (Studio)",
        custom_name: "Studio Mac",
        provider: "pi",
      }),
      makeRuntime({
        id: "rt-b",
        daemon_id: "daemon-multi",
        name: "Codex (Studio)",
        custom_name: "Studio Mac",
        provider: "codex",
      }),
    ];
    renderPage("local:daemon-multi");

    const props = lastRuntimeListProps();
    expect(props.machineRuntimeCount).toBe(2);

    const texts = screen
      .getAllByTestId("runtime-row-label")
      .map((el) => el.textContent)
      .sort();
    expect(texts).toEqual(["Codex", "Pi"]);
  });
});
