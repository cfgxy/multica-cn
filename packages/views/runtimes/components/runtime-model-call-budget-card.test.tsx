// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enRuntimes from "../../locales/en/runtimes.json";
import { ModelCallBudgetCard } from "./runtime-model-call-budget-card";

const TEST_RESOURCES = { en: { runtimes: enRuntimes } };

const mockGetBudget = vi.hoisted(() => vi.fn());
const mockPutBudget = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: {
    getRuntimeModelCallBudget: (...args: unknown[]) => mockGetBudget(...args),
    putRuntimeModelCallBudget: (...args: unknown[]) => mockPutBudget(...args),
  },
}));

vi.mock("sonner", () => ({
  toast: { error: mockToastError, success: mockToastSuccess },
}));

const DEFAULT_BUDGET = {
  config: { enabled: false, max_calls: 1000, window_seconds: 86400 },
  report: {
    known_calls: 0,
    unknown_usage_rows: 0,
    complete: true,
    active: false,
    window_started_at: "2026-10-24T00:00:00Z",
  },
};

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "host.local",
    runtime_mode: "local",
    provider: "claude_code",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: "user-me",
    visibility: "public",
    last_seen_at: null,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    registration_source: "manual",
    ...overrides,
  } as AgentRuntime;
}

function renderCard(runtime: AgentRuntime, canEdit = true) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ModelCallBudgetCard runtime={runtime} canEdit={canEdit} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("ModelCallBudgetCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetBudget.mockResolvedValue(DEFAULT_BUDGET);
    mockPutBudget.mockResolvedValue(DEFAULT_BUDGET);
  });

  it("renders on cloud and daemon-less runtimes because the budget is server-owned", async () => {
    renderCard(makeRuntime({ runtime_mode: "cloud", daemon_id: null }));

    await waitFor(() =>
      expect(screen.getByTestId("model-call-budget-card")).toBeTruthy(),
    );
    expect(screen.getByText("Model call budget")).toBeTruthy();
    expect(mockGetBudget).toHaveBeenCalledWith("rt-1");
  });

  it("shows incomplete usage as tracking rather than a hold", async () => {
    mockGetBudget.mockResolvedValue({
      config: { enabled: true, max_calls: 100, window_seconds: 3600 },
      report: {
        known_calls: 100,
        unknown_usage_rows: 1,
        complete: false,
        active: false,
        window_started_at: "2026-10-24T00:00:00Z",
      },
    });
    renderCard(makeRuntime());

    expect(await screen.findByText("Tracking")).toBeTruthy();
    expect(screen.getByText("Incomplete")).toBeTruthy();
    expect(
      screen.getByText(
        "Unknown usage keeps this runtime eligible; it never activates the call budget hold.",
      ),
    ).toBeTruthy();
    expect(screen.queryByText("Held")).toBeNull();
  });

  it("renders the server-provided active verdict", async () => {
    mockGetBudget.mockResolvedValue({
      config: { enabled: true, max_calls: 100, window_seconds: 3600 },
      report: {
        known_calls: 100,
        unknown_usage_rows: 0,
        complete: true,
        active: true,
        window_started_at: "2026-10-24T00:00:00Z",
      },
    });
    renderCard(makeRuntime());

    expect(await screen.findByText("Held")).toBeTruthy();
    expect(
      screen.getByText(
        "New task claims are paused because known model calls reached the configured limit.",
      ),
    ).toBeTruthy();
  });

  it("renders the report read-only for members without scheduling control", async () => {
    renderCard(makeRuntime(), false);

    await waitFor(() =>
      expect(screen.getByTestId("model-call-budget-card")).toBeTruthy(),
    );
    expect(screen.queryByTestId("mb-edit")).toBeNull();
    expect(screen.getByText("1000")).toBeTruthy();
  });

  it("saves the full snake_case budget config", async () => {
    renderCard(makeRuntime());
    fireEvent.click(await screen.findByTestId("mb-edit"));
    fireEvent.click(screen.getByRole("switch", { name: "Enable model call budget" }));
    fireEvent.change(screen.getByLabelText("Maximum calls"), {
      target: { value: "250" },
    });
    fireEvent.change(screen.getByLabelText("Window duration (seconds)"), {
      target: { value: "3600" },
    });
    fireEvent.click(screen.getByTestId("mb-save"));

    await waitFor(() =>
      expect(mockPutBudget).toHaveBeenCalledWith("rt-1", {
        enabled: true,
        max_calls: 250,
        window_seconds: 3600,
      }),
    );
  });

  it("rejects a call limit outside the server-supported range before saving", async () => {
    renderCard(makeRuntime());
    fireEvent.click(await screen.findByTestId("mb-edit"));
    fireEvent.change(screen.getByLabelText("Maximum calls"), {
      target: { value: "0" },
    });
    fireEvent.click(screen.getByTestId("mb-save"));

    expect(mockPutBudget).not.toHaveBeenCalled();
    expect(mockToastError).toHaveBeenCalledWith(
      "Maximum calls must be between 1 and 1000000000.",
    );
  });
});
