// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enRuntimes from "../../locales/en/runtimes.json";
import { BackpressureSettingsCard } from "./runtime-backpressure-settings-card";

const TEST_RESOURCES = { en: { runtimes: enRuntimes } };

const mockGetSettings = vi.hoisted(() => vi.fn());
const mockPutSettings = vi.hoisted(() => vi.fn());
const mockDeleteSettings = vi.hoisted(() => vi.fn());
const mockListEvents = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: {
    getRuntimeBackpressureSettings: (...a: unknown[]) => mockGetSettings(...a),
    putRuntimeBackpressureSettings: (...a: unknown[]) => mockPutSettings(...a),
    deleteRuntimeBackpressureSettings: (...a: unknown[]) =>
      mockDeleteSettings(...a),
    listRuntimeBackpressureEvents: (...a: unknown[]) => mockListEvents(...a),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));

const SAVED_SETTINGS = {
  daemon_id: "daemon-1",
  configured: true,
  config: {
    enabled: true,
    mem_high_pct: 20,
    mem_recovery_pct: 40,
    swap_high_pct: 0,
    swap_recovery_pct: 0,
    psi_high_pct: 0,
    psi_recovery_pct: 0,
    sample_interval_seconds: 10,
    window_size: 3,
    config_version: 2,
    source: "db",
  },
  updated_by: "user-me",
  updated_at: "2026-10-10T00:00:00Z",
  report: null,
};

const DEFAULT_SETTINGS = {
  daemon_id: "daemon-1",
  configured: false,
  config: null,
  report: null,
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

function renderCard(
  runtime: AgentRuntime,
  canEdit = true,
): ReturnType<typeof render> {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <BackpressureSettingsCard runtime={runtime} canEdit={canEdit} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("BackpressureSettingsCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetSettings.mockResolvedValue(DEFAULT_SETTINGS);
    mockListEvents.mockResolvedValue([]);
    mockPutSettings.mockResolvedValue(SAVED_SETTINGS);
    mockDeleteSettings.mockResolvedValue(DEFAULT_SETTINGS);
  });

  it("renders the built-in defaults with a defaults marker until saved", async () => {
    renderCard(makeRuntime());
    await waitFor(() =>
      expect(screen.getByTestId("backpressure-settings-card")).toBeTruthy(),
    );
    expect(screen.getByText("Using built-in default thresholds")).toBeTruthy();
    expect(screen.getByText("15% → 25%")).toBeTruthy();
    expect(screen.getByText("80% → 60%")).toBeTruthy();
    expect(screen.getByText("5s × 6")).toBeTruthy();
    // Nothing saved → no reset path, but the entry is editable.
    expect(screen.queryByTestId("bp-reset")).toBeNull();
    expect(screen.getByTestId("bp-edit")).toBeTruthy();
  });

  it("marks a saved card as customized and offers reset", async () => {
    mockGetSettings.mockResolvedValue(SAVED_SETTINGS);
    renderCard(makeRuntime());
    await waitFor(() =>
      expect(screen.getByText("Custom thresholds saved for this machine")).toBeTruthy(),
    );
    expect(screen.getByText("20% → 40%")).toBeTruthy();
    // Zero-valued conditions read as intentionally off, never as "0% → 0%".
    expect(screen.getAllByText("Off").length).toBe(2);
    expect(screen.getByTestId("bp-reset")).toBeTruthy();
  });

  it("stays hidden for cloud runtimes and daemon-less rows", () => {
    const cloud = renderCard(makeRuntime({ runtime_mode: "cloud" }));
    expect(cloud.container.innerHTML).toBe("");
    cloud.unmount();
    const noDaemon = renderCard(makeRuntime({ daemon_id: null }));
    expect(noDaemon.container.innerHTML).toBe("");
  });

  it("renders facts read-only for viewers without the admin bar", async () => {
    renderCard(makeRuntime(), false);
    await waitFor(() =>
      expect(screen.getByTestId("backpressure-settings-card")).toBeTruthy(),
    );
    expect(screen.queryByTestId("bp-edit")).toBeNull();
    expect(screen.queryByTestId("bp-reset")).toBeNull();
    expect(screen.getByText("15% → 25%")).toBeTruthy();
  });

  it("saves an edited card as the full snake_case body", async () => {
    renderCard(makeRuntime());
    fireEvent.click(screen.getByTestId("bp-edit"));
    const memHigh = await screen.findByLabelText("Memory trigger (%)");
    fireEvent.change(memHigh, { target: { value: "20" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(mockPutSettings).toHaveBeenCalledWith("rt-1", {
        enabled: true,
        mem_high_pct: 20,
        mem_recovery_pct: 25,
        swap_high_pct: 80,
        swap_recovery_pct: 60,
        psi_high_pct: 50,
        psi_recovery_pct: 20,
        sample_interval_seconds: 5,
        window_size: 6,
      }),
    );
    const { toast } = await import("sonner");
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith(
        "Backpressure gate saved — the daemon picks it up on its next heartbeat",
      ),
    );
  });

  it("blocks a hysteresis-violating card client-side before any request", async () => {
    renderCard(makeRuntime());
    fireEvent.click(screen.getByTestId("bp-edit"));
    const memHigh = await screen.findByLabelText("Memory trigger (%)");
    fireEvent.change(memHigh, { target: { value: "50" } });
    fireEvent.change(screen.getByLabelText("Memory recovery (%)"), {
      target: { value: "40" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    const { toast } = await import("sonner");
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        "Memory recovery must be above the trigger and at most 100%.",
      ),
    );
    expect(mockPutSettings).not.toHaveBeenCalled();
  });

  it("resets a saved card back to the built-in defaults", async () => {
    mockGetSettings.mockResolvedValue(SAVED_SETTINGS);
    renderCard(makeRuntime());
    fireEvent.click(await screen.findByTestId("bp-reset"));

    await waitFor(() =>
      expect(mockDeleteSettings).toHaveBeenCalledWith("rt-1"),
    );
    const { toast } = await import("sonner");
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith(
        "Backpressure gate reset to defaults",
      ),
    );
  });

  it("pairs live readings with the thresholds that judged them", async () => {
    mockGetSettings.mockResolvedValue({
      ...SAVED_SETTINGS,
      report: {
        active: true,
        reason: "mem",
        mem_available_pct: 62,
        swap_used_pct: 0,
        psi_memory_some_avg10: 3,
        psi_read_ok: true,
        deferred_claims: 4,
        config: SAVED_SETTINGS.config,
        conditions: [
          { condition: "mem", value: 62, threshold: 20, triggered: true },
          { condition: "swap", value: 0, threshold: 0, triggered: false },
          { condition: "psi", value: 3, threshold: 0, triggered: false },
        ],
      },
    });
    renderCard(makeRuntime());
    const readings = await screen.findByTestId("bp-live-readings");
    expect(readings.textContent).toContain("Memory: 62% / 20%");
    expect(readings.textContent).toContain("Swap: 0% / 0%");
    expect(readings.textContent).toContain("PSI: 3% / 0%");
  });

  it("renders the trigger and recovery timeline with hit details", async () => {
    mockListEvents.mockResolvedValue([
      {
        id: "e2",
        event_type: "cleared",
        reason: "",
        mem_available_pct: 30,
        swap_used_pct: 10,
        psi_some_avg10: 0,
        psi_read_ok: true,
        deferred_claims: 9,
        effective_config: {},
        hits: [],
        occurred_at: "2026-10-10T12:10:00Z",
      },
      {
        id: "e1",
        event_type: "entered",
        reason: "mem",
        mem_available_pct: 5,
        swap_used_pct: 0,
        psi_some_avg10: 0,
        psi_read_ok: true,
        deferred_claims: 0,
        effective_config: {},
        hits: [{ condition: "mem", value: 5, threshold: 15, triggered: true }],
        occurred_at: "2026-10-10T12:00:00Z",
      },
    ]);
    renderCard(makeRuntime());
    const list = await screen.findByTestId("bp-event-list");
    expect(list.textContent).toContain("Triggered");
    expect(list.textContent).toContain("Memory 5% / 15%");
    expect(list.textContent).toContain("Recovered");
    expect(list.textContent).toContain("9 claim polls deferred in total");
  });
});
