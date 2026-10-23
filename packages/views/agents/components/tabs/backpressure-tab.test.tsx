// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import type { Agent } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enAgents from "../../../locales/en/agents.json";
import { BackpressureTab } from "./backpressure-tab";

const TEST_RESOURCES = { en: { agents: enAgents } };

const mockGetPolicy = vi.hoisted(() => vi.fn());
const mockListEvents = vi.hoisted(() => vi.fn());
const mockPutPolicy = vi.hoisted(() => vi.fn());
const mockDeletePolicy = vi.hoisted(() => vi.fn());
const mockMembers = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: {
    getAgentBackpressurePolicy: (...a: unknown[]) => mockGetPolicy(...a),
    putAgentBackpressurePolicy: (...a: unknown[]) => mockPutPolicy(...a),
    deleteAgentBackpressurePolicy: (...a: unknown[]) => mockDeletePolicy(...a),
    listAgentBackpressureEvents: (...a: unknown[]) => mockListEvents(...a),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: unknown }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({
    queryKey: ["workspaces", "ws-1", "members"],
    queryFn: mockMembers,
  }),
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

const DEFAULT_POLICY = {
  agent_id: "agent-1",
  configured: false,
  config: null,
  window: null,
  scheduling: null,
  events: [],
};

const WINDOWED_POLICY = {
  agent_id: "agent-1",
  configured: true,
  config: {
    window_minutes: 60,
    error_rate_threshold_pct: 30,
    min_samples: 5,
    recovery_window_minutes: 30,
    suppress_on_error_rate: true,
    count_model_api_failures: true,
    count_execution_failures: false,
  },
  window: {
    window_minutes: 60,
    total_terminal: 5,
    selected_failures: 2,
    recovery_threshold_pct: 15,
    error_rate_pct: 40,
    by_class: { runtime: 1, rate_limit: 1 },
  },
  scheduling: { paused: true, scope: "agent", queued_count: 3 },
  events: [
    {
      id: "evt-1",
      event_type: "entered",
      sample_count: 5,
      error_rate_pct: 40,
      effective_policy: {},
      occurred_at: "2026-10-24T00:00:00Z",
    },
  ],
};

function makeAgent(): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    name: "Reviewer",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    visibility: "workspace",
    permission_mode: "public_to",
    invocation_targets: [],
    status: "idle",
    max_concurrent_tasks: 1,
    model: "",
    owner_id: "user-1",
    skills: [],
    created_at: "2026-08-24T00:00:00Z",
    updated_at: "2026-08-24T00:00:00Z",
    archived_at: null,
    archived_by: null,
  } as Agent;
}

function renderTab(role: "owner" | "member" = "owner") {
  mockMembers.mockResolvedValue([
    { user_id: "user-1", role },
  ] as never[]);
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <BackpressureTab agent={makeAgent()} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("BackpressureTab (RUYI-859 stage A)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetPolicy.mockResolvedValue(DEFAULT_POLICY);
    mockListEvents.mockResolvedValue([]);
    mockPutPolicy.mockResolvedValue(DEFAULT_POLICY);
    mockDeletePolicy.mockResolvedValue(DEFAULT_POLICY);
  });

  it("renders the unconfigured defaults without an editor for plain members", async () => {
    renderTab("member");
    await waitFor(() =>
      expect(screen.getByText("Entry window")).toBeTruthy(),
    );
    // Built-in defaults surface: both windows and the fixed 0.5x recovery
    // threshold (30 * 0.5 = 15).
    expect(screen.getByText(/0 terminal samples/)).toBeTruthy();
    expect(
      screen.getByText(/Recovery needs the rate under 15% \(fixed 0.5×/),
    ).toBeTruthy();
    // No samples yet → the "no data" marker, not a 0% figure.
    expect(screen.getByText("No terminal samples yet")).toBeTruthy();
    // Plain members get read-only visibility: no editor, no reset.
    expect(screen.queryByTestId("bp-edit")).toBeNull();
    expect(screen.queryByTestId("bp-reset")).toBeNull();
  });

  it("gives workspace admins the editor, saves the card, and resets it", async () => {
    renderTab("owner");
    await waitFor(() => expect(screen.getByTestId("bp-edit")).toBeTruthy());

    fireEvent.click(screen.getByTestId("bp-edit"));
    expect(await screen.findByText("Window (minutes)")).toBeTruthy();
    const inputs = screen.getAllByRole("spinbutton");
    expect(inputs.length).toBeGreaterThanOrEqual(1);
    fireEvent.change(inputs[0]!, { target: { value: "45" } });
    fireEvent.click(screen.getByText("Save"));
    await waitFor(() =>
      expect(mockPutPolicy).toHaveBeenCalledWith(
        "agent-1",
        expect.objectContaining({
          window_minutes: 45,
          error_rate_threshold_pct: 30,
          min_samples: 5,
          recovery_window_minutes: 30,
          suppress_on_error_rate: false,
        }),
      ),
    );

    // Reset path only exists once a row is configured.
    mockGetPolicy.mockResolvedValue(WINDOWED_POLICY);
    const qc = new QueryClient();
    qc.setQueryData(
      ["workspaces", "ws-1", "agent-backpressure", "agent-1"],
      WINDOWED_POLICY,
    );
    const resetRender = render(
      <QueryClientProvider client={qc}>
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <BackpressureTab agent={makeAgent()} />
        </I18nProvider>
      </QueryClientProvider>,
    );
    const resetButton = await resetRender.findByTestId("bp-reset");
    fireEvent.click(resetButton);
    await waitFor(() =>
      expect(mockDeletePolicy).toHaveBeenCalledWith("agent-1"),
    );
    resetRender.unmount();
  });

  it("shows the window reading, class split, pause source and events when configured", async () => {
    mockGetPolicy.mockResolvedValue(WINDOWED_POLICY);
    mockListEvents.mockResolvedValue(WINDOWED_POLICY.events);
    renderTab("member");
    await waitFor(() =>
      expect(screen.getByText("40.0%")).toBeTruthy(),
    );
    // Class split uses the localized display-class labels.
    expect(screen.getByText("Runtime: 1")).toBeTruthy();
    expect(screen.getByText("Rate limit: 1")).toBeTruthy();
    // Pause source: the human freeze with its live queue depth.
    expect(screen.getByText(/Paused \(agent scope\)/)).toBeTruthy();
    expect(screen.getByText("Queue depth: 3")).toBeTruthy();
    // Recent events, newest first.
    expect(screen.getByText("Entered")).toBeTruthy();
    expect(screen.getByText(/5 samples at 40\.0%/)).toBeTruthy();
  });
});
