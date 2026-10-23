// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { AgentRuntime } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

// The card owns its fetch (the window is card-local state), so the useQuery
// seam is mocked the way the dashboard panel tests do it: option builders
// run for real, the captured key decides what the query "returns".
const queryKeys = vi.hoisted(() => [] as unknown[][]);
const fixturesRef = vi.hoisted(() => ({
  current: {} as Record<string, unknown>,
}));

vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: (opts: {
      queryKey: unknown[];
      data?: unknown;
      isPending?: boolean;
      isError?: boolean;
    }) => {
      queryKeys.push(opts.queryKey);
      // Key shape is ["runtimes", "resources", rid, window] — index 1 is the
      // panel discriminator (rid at index 2 is per-machine, not the fixture).
      const kind = String(opts.queryKey?.[1]);
      const fixture = fixturesRef.current[kind];
      const failed = fixture === "error";
      return {
        data: failed || fixture === undefined ? undefined : fixture,
        isPending: fixture === undefined && !failed,
        isError: failed,
        isLoading: false,
        isSuccess: fixture !== undefined && !failed,
        refetch: vi.fn(),
      };
    },
  };
});

import { RuntimeResourcesCard } from "./runtime-resources-card";

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

function series(
  labels: Record<string, string>,
  points: [number, number][],
) {
  return { labels, points: points.map(([t, v]) => ({ t, v })) };
}

// Ten keys — the nine panel series plus boot_time_seconds for uptime — all
// pinned to the same daemon so the "single host" invariant is visible in the
// fixture.
const RESOURCES_FIXTURE = {
  configured: true,
  daemon_id: "daemon-1",
  has_daemon: true,
  window: "1h",
  step_seconds: 60,
  sampled_at: "2026-10-10T00:00:00Z",
  series: {
    cpu_cores: [series({ daemon: "daemon-1" }, [[1000, 1.5], [1060, 3.2]])],
    memory_total_bytes: [series({ daemon: "daemon-1" }, [[1000, 16_000_000_000], [1060, 16_000_000_000]])],
    memory_available_bytes: [series({ daemon: "daemon-1" }, [[1000, 12_000_000_000], [1060, 8_000_000_000]])],
    swap_total_bytes: [series({ daemon: "daemon-1" }, [[1000, 4_000_000_000], [1060, 4_000_000_000]])],
    swap_free_bytes: [series({ daemon: "daemon-1" }, [[1000, 3_000_000_000], [1060, 1_000_000_000]])],
    filesystem_size_bytes: [series({ daemon: "daemon-1" }, [[1000, 500_000_000_000], [1060, 500_000_000_000]])],
    filesystem_avail_bytes: [series({ daemon: "daemon-1" }, [[1000, 100_000_000_000], [1060, 90_000_000_000]])],
    disk_read_bytes_per_second: [series({ daemon: "daemon-1" }, [[1000, 1024], [1060, 2048]])],
    disk_written_bytes_per_second: [series({ daemon: "daemon-1" }, [[1000, 4096], [1060, 8192]])],
    boot_time_seconds: [series({ daemon: "daemon-1" }, [[1000, 1_000_000], [1060, 1_000_000]])],
  },
};

beforeEach(() => {
  queryKeys.length = 0;
  fixturesRef.current = {};
});

afterEach(cleanup);

describe("RuntimeResourcesCard", () => {
  it("shows the not-configured state when the server has no Prometheus", () => {
    fixturesRef.current["resources"] = { configured: false, has_daemon: true };
    renderWithI18n(<RuntimeResourcesCard runtime={makeRuntime()} />);
    expect(screen.getByTestId("runtime-resources-card")).toBeTruthy();
    // The structured empty is VISIBLE — title + named state, never nothing.
    expect(screen.getByText("Host resources")).toBeInTheDocument();
    expect(screen.getByText("Observability not configured")).toBeInTheDocument();
    // No chart surface in the not-configured state.
    expect(screen.queryByTestId("runtime-resources-charts")).toBeNull();
  });

  it("shows the no-daemon-host state for cloud / manual runtimes", () => {
    fixturesRef.current["resources"] = {
      configured: true,
      has_daemon: false,
      series: {},
    };
    renderWithI18n(<RuntimeResourcesCard runtime={makeRuntime()} />);
    expect(screen.getByText("No daemon host")).toBeInTheDocument();
    expect(
      screen.getByText(
        "This runtime binds no local daemon host, so there are no host resources to show.",
      ),
    ).toBeInTheDocument();
  });

  it("shows the no-samples state when Prometheus is up but empty", () => {
    fixturesRef.current["resources"] = {
      configured: true,
      has_daemon: true,
      series: {},
    };
    renderWithI18n(<RuntimeResourcesCard runtime={makeRuntime()} />);
    expect(screen.getByText("No samples yet")).toBeInTheDocument();
  });

  it("shows a retry-able error state when the proxy query fails", () => {
    fixturesRef.current["resources"] = "error";
    renderWithI18n(<RuntimeResourcesCard runtime={makeRuntime()} />);
    expect(screen.getByText("Failed to read host resources.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });

  it("renders current values and trend tiles from the narrowed series", () => {
    fixturesRef.current["resources"] = RESOURCES_FIXTURE;
    renderWithI18n(<RuntimeResourcesCard runtime={makeRuntime()} />);
    // The card carries the daemon host identity in its own header — the
    // reader sees which machine these readings belong to.
    expect(screen.getByText("daemon-1")).toBeInTheDocument();
    // Freshness: the newest sample timestamp is surfaced as "how old is this
    // reading", not hidden.
    expect(screen.getAllByText(/ago/).length).toBeGreaterThan(0);
    // Trend surface present (one chart per tile).
    expect(screen.getByTestId("runtime-resources-charts")).toBeTruthy();
    const charts = document.querySelectorAll(".recharts-responsive-container");
    expect(charts.length).toBeGreaterThanOrEqual(4);
  });

  it("re-queries under the picked window", async () => {
    const user = userEvent.setup();
    fixturesRef.current["resources"] = RESOURCES_FIXTURE;
    renderWithI18n(<RuntimeResourcesCard runtime={makeRuntime()} />);
    await user.click(screen.getByRole("button", { name: "7d" }));
    const key = queryKeys.findLast((k) => k?.[1] === "resources");
    expect(key).toEqual(["runtimes", "resources", "rt-1", "7d"]);
  });
});
