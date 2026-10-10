/**
 * Mobile dashboard query lane (RUYI-638 stage 3).
 *
 * Mobile cannot reuse `@multica/core/dashboard/queries` directly (the core
 * queryOptions close over the core api singleton; mobile has its own api
 * instance), so this lane MIRRORS the core semantics. These tests pin the
 * mirrored contract so drift from the web dashboard fails here first:
 *
 *   - key shape `[dashboard, wsId, kind, window, projectId, tz]` — the same
 *     six-segment shape as `dashboardKeys` in core, so cache scoping behaves
 *     identically (workspace switch flips entries, window/project/tz changes
 *     never collide),
 *   - `enabled: !!wsId`, staleTime 60s, refetchInterval 5min — the web
 *     dashboard's fetch-on-enter + periodic-poll contract (no WS),
 *   - the wire carries explicit `start`/`end` (+ `project_id`/`tz`), never
 *     the legacy relative `days`,
 *   - the abort signal travels from React Query into the api call.
 */
import { describe, expect, it, vi } from "vitest";

vi.mock("@/data/api", () => ({
  api: {
    getDashboardUsageDaily: vi.fn().mockResolvedValue([]),
    getDashboardUsageByAgent: vi.fn().mockResolvedValue([]),
    getDashboardAgentRunTime: vi.fn().mockResolvedValue([]),
    getDashboardRunTimeDaily: vi.fn().mockResolvedValue([]),
    getDashboardFailuresDaily: vi.fn().mockResolvedValue([]),
    getDashboardFailuresByAgent: vi.fn().mockResolvedValue([]),
  },
}));

import { api } from "@/data/api";
import {
  dashboardKeys,
  dashboardUsageDailyOptions,
  dashboardUsageByAgentOptions,
  dashboardAgentRunTimeOptions,
  dashboardRunTimeDailyOptions,
  dashboardFailuresDailyOptions,
  dashboardFailuresByAgentOptions,
  DASHBOARD_STALE_TIME_MS,
  DASHBOARD_REFETCH_INTERVAL_MS,
} from "./dashboard";

const WS = "ws-1";
const WINDOW = { start: "2026-02-25", end: "2026-03-03" };
const PROJECT = "proj-9";
const TZ = "Asia/Shanghai";

describe("dashboardKeys", () => {
  it("keeps the core six-segment shape [dashboard, wsId, kind, window, projectId, tz]", () => {
    expect(dashboardKeys.daily(WS, WINDOW, PROJECT, TZ)).toEqual([
      "dashboard",
      WS,
      "daily",
      WINDOW,
      PROJECT,
      TZ,
    ]);
    expect(dashboardKeys.byAgent(WS, WINDOW, null, TZ)[2]).toBe("by-agent");
    expect(dashboardKeys.agentRuntime(WS, WINDOW, null, TZ)[2]).toBe(
      "agent-runtime",
    );
    expect(dashboardKeys.runTimeDaily(WS, WINDOW, null, TZ)[2]).toBe(
      "runtime-daily",
    );
    expect(dashboardKeys.failuresDaily(WS, WINDOW, null, TZ)[2]).toBe(
      "failures-daily",
    );
    expect(dashboardKeys.failuresByAgent(WS, WINDOW, null, TZ)[2]).toBe(
      "failures-by-agent",
    );
  });

  it("scopes every key under the workspace so a ws switch flips the cache", () => {
    const a = dashboardKeys.daily("ws-a", WINDOW, null, TZ);
    const b = dashboardKeys.daily("ws-b", WINDOW, null, TZ);
    expect(a[1]).toBe("ws-a");
    expect(b[1]).toBe("ws-b");
    expect(a).not.toEqual(b);
  });
});

describe("query option wiring (mirror of core dashboard/queries.ts)", () => {
  it("matches the web cadence: 60s stale, 5min poll", () => {
    expect(DASHBOARD_STALE_TIME_MS).toBe(60_000);
    expect(DASHBOARD_REFETCH_INTERVAL_MS).toBe(300_000);
    for (const options of [
      dashboardUsageDailyOptions(WS, WINDOW, null, TZ),
      dashboardUsageByAgentOptions(WS, WINDOW, null, TZ),
      dashboardAgentRunTimeOptions(WS, WINDOW, null, TZ),
      dashboardRunTimeDailyOptions(WS, WINDOW, null, TZ),
      dashboardFailuresDailyOptions(WS, WINDOW, null, TZ),
      dashboardFailuresByAgentOptions(WS, WINDOW, null, TZ),
    ]) {
      expect(options.staleTime).toBe(60_000);
      expect(options.refetchInterval).toBe(300_000);
    }
  });

  it("stays disabled without a workspace, live with one", () => {
    expect(dashboardUsageDailyOptions(null, WINDOW, null, TZ).enabled).toBe(false);
    expect(dashboardUsageDailyOptions(WS, WINDOW, null, TZ).enabled).toBe(true);
  });

  it("sends the explicit window (never legacy days) plus project and tz", async () => {
    const options = dashboardUsageDailyOptions(WS, WINDOW, PROJECT, TZ);
    await options.queryFn!({ signal: new AbortController().signal, meta: undefined } as Parameters<
      NonNullable<typeof options.queryFn>
    >[0]);
    expect(api.getDashboardUsageDaily).toHaveBeenCalledWith(
      expect.objectContaining({
        start: WINDOW.start,
        end: WINDOW.end,
        project_id: PROJECT,
        tz: TZ,
      }),
    );
  });

  it("omits the wire project when ALL is selected (null)", async () => {
    const options = dashboardFailuresDailyOptions(WS, WINDOW, null, TZ);
    await options.queryFn!({ signal: new AbortController().signal, meta: undefined } as Parameters<
      NonNullable<typeof options.queryFn>
    >[0]);
    expect(api.getDashboardFailuresDaily).toHaveBeenCalledWith(
      expect.objectContaining({
        start: WINDOW.start,
        end: WINDOW.end,
        project_id: undefined,
        tz: TZ,
      }),
    );
  });

  it("forwards the abort signal into the api call", async () => {
    const controller = new AbortController();
    const options = dashboardUsageByAgentOptions(WS, WINDOW, null, TZ);
    await options.queryFn!({ signal: controller.signal, meta: undefined } as Parameters<
      NonNullable<typeof options.queryFn>
    >[0]);
    expect(api.getDashboardUsageByAgent).toHaveBeenCalledWith(
      expect.objectContaining({ signal: controller.signal }),
    );
  });
});
