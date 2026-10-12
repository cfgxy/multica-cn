import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../../test/i18n";
import { UsageResourcesCard, formatByteRate } from "./usage-resources-card";
import { UsageTrafficCard } from "./usage-traffic-card";
import {
  mergeMetricRows,
  meanOf,
  topNamedSeries,
} from "./metric-shared";

// The live metric panels own their data fetching (relative windows are
// card-local state, not page filters), so the useQuery seam is mocked the
// same way dashboard-page.test.tsx does: option builders run for real, the
// captured key decides what the query "returns".
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
      const kind = String(opts.queryKey[2]);
      const fixture = fixturesRef.current[kind];
      const failed = fixture === "error";
      return {
        // An errored query carries no data — otherwise the cards' configured
        // guard would swallow the error state this test is trying to reach.
        data: failed || fixture === undefined ? undefined : fixture,
        isPending: fixture === undefined,
        isError: failed,
        isLoading: false,
        isSuccess: fixture !== undefined && !failed,
      };
    },
  };
});

function series(
  labels: Record<string, string>,
  points: [number, number][],
): { labels: Record<string, string>; points: { t: number; v: number }[] } {
  return { labels, points: points.map(([t, v]) => ({ t, v })) };
}

const RESOURCES_FIXTURE = {
  configured: true,
  window: "1h",
  series: {
    cpu_cores: [series({ daemon: "d1" }, [[1000, 0.5], [1060, 1.5]])],
    memory_total_bytes: [series({ daemon: "d1" }, [[1000, 100], [1060, 100]])],
    memory_available_bytes: [series({ daemon: "d1" }, [[1000, 25], [1060, 50]])],
    disk_read_bytes_per_second: [
      series({ daemon: "d1" }, [[1000, 1024], [1060, 2048]]),
    ],
    disk_written_bytes_per_second: [
      series({ daemon: "d1" }, [[1000, 4096], [1060, 8192]]),
    ],
  },
};

const TRAFFIC_FIXTURE = {
  configured: true,
  window: "1h",
  by: "provider",
  series: {
    tokens_per_second: [
      series({ provider: "anthropic" }, [[1000, 10], [1060, 20]]),
      series({ provider: "openai" }, [[1000, 5], [1060, 2]]),
    ],
    cost_usd_per_second: [
      series({ provider: "anthropic" }, [[1000, 0.01], [1060, 0.02]]),
    ],
    tasks_per_second: [series({ provider: "anthropic" }, [[1000, 0.1], [1060, 0.2]])],
  },
};

beforeEach(() => {
  queryKeys.length = 0;
  fixturesRef.current = {
    "usage-resources": RESOURCES_FIXTURE,
    "usage-traffic": TRAFFIC_FIXTURE,
  };
});

afterEach(cleanup);

describe("UsageResourcesCard", () => {
  it("renders nothing when the deployment has no metric backend", () => {
    fixturesRef.current["usage-resources"] = { configured: false };
    const { container } = renderWithI18n(<UsageResourcesCard wsId="ws-1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("draws the cpu, memory and disk tiles from the relayed series", () => {
    renderWithI18n(<UsageResourcesCard wsId="ws-1" />);
    expect(screen.getByText("System resources")).toBeInTheDocument();
    expect(screen.getByText("CPU cores")).toBeInTheDocument();
    expect(screen.getByText("Memory used")).toBeInTheDocument();
    expect(screen.getByText("Disk I/O")).toBeInTheDocument();
    // One chart per tile (jsdom never sizes ResponsiveContainer, so the
    // line paths themselves don't render — the container is the observable).
    const charts = document.querySelectorAll(".recharts-responsive-container");
    expect(charts).toHaveLength(3);
  });

  it("shows the backend-error note when the proxy query fails", () => {
    fixturesRef.current["usage-resources"] = "error";
    renderWithI18n(<UsageResourcesCard wsId="ws-1" />);
    expect(screen.getByText("Metric backend unavailable.")).toBeInTheDocument();
  });

  it("re-queries under the picked window", async () => {
    const user = userEvent.setup();
    renderWithI18n(<UsageResourcesCard wsId="ws-1" />);
    await user.click(screen.getByRole("button", { name: "7d" }));
    const key = queryKeys.findLast((k) => k[2] === "usage-resources");
    expect(key).toEqual(["dashboard", "ws-1", "usage-resources", "7d"]);
  });
});

describe("UsageTrafficCard", () => {
  it("renders nothing when the deployment has no metric backend", () => {
    fixturesRef.current["usage-traffic"] = { configured: false };
    const { container } = renderWithI18n(<UsageTrafficCard wsId="ws-1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("draws tokens, cost and task rate tiles", () => {
    renderWithI18n(<UsageTrafficCard wsId="ws-1" />);
    expect(screen.getByText("Model traffic")).toBeInTheDocument();
    expect(screen.getByText("Tokens/s")).toBeInTheDocument();
    expect(screen.getByText("Cost/h")).toBeInTheDocument();
    expect(screen.getByText("Tasks/h")).toBeInTheDocument();
  });

  it("switches the breakdown dimension through the query key", async () => {
    const user = userEvent.setup();
    renderWithI18n(<UsageTrafficCard wsId="ws-1" />);
    await user.click(screen.getByRole("button", { name: "Model" }));
    const key = queryKeys.findLast((k) => k[2] === "usage-traffic");
    expect(key).toEqual(["dashboard", "ws-1", "usage-traffic", "1h", "model"]);
  });
});

describe("metric-shared helpers", () => {
  it("merges series into rows keyed by timestamp", () => {
    const rows = mergeMetricRows([
      { name: "a", points: [{ t: 2, v: 20 }, { t: 1, v: 10 }] },
      { name: "b", points: [{ t: 2, v: 30 }] },
    ]);
    expect(rows).toEqual([
      { t: 1, a: 10 },
      { t: 2, a: 20, b: 30 },
    ]);
  });

  it("ranks series by mean value and dedupes display names", () => {
    const input = [
      { id: "quiet", points: [{ t: 1, v: 1 }] },
      { id: "loud", points: [{ t: 1, v: 100 }] },
      { id: "loud-dup", points: [{ t: 1, v: 50 }] },
    ];
    const named = topNamedSeries(
      input,
      (s) => s.id.split("-")[0] ?? s.id,
      (s) => meanOf(s.points.map((p) => p.v)),
    );
    // Both "loud" candidates survive (dedupe renames the second), "quiet"
    // ranks last but still fits under the default cap.
    expect(named.map((n) => n.name)).toEqual(["loud", "loud (2)", "quiet"]);
  });

  it("respects an explicit series cap", () => {
    const input = Array.from({ length: 12 }, (_, i) => ({
      id: `s${i}`,
      points: [{ t: 1, v: i }],
    }));
    expect(topNamedSeries(input, (s) => s.id, (s) => s.points[0]!.v, 4)).toHaveLength(4);
  });

  it("formats byte rates without a 1000K step", () => {
    expect(formatByteRate(0)).toBe("0.0B/s");
    expect(formatByteRate(1024)).toBe("1.0KB/s");
    expect(formatByteRate(999_000)).toBe("999KB/s");
    expect(formatByteRate(1_500_000)).toBe("1.5MB/s");
    expect(formatByteRate(2_500_000_000)).toBe("2.5GB/s");
  });
});
