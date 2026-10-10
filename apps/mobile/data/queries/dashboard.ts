/**
 * Mobile mirror of `packages/core/dashboard/queries.ts` (RUYI-638).
 *
 * Mobile cannot import the core queryOptions directly — they close over the
 * core api singleton, and mobile owns its own ApiClient instance (the mobile
 * import whitelist allows core pure functions + schemas, not the core api).
 * So the SEMANTICS are mirrored one-for-one and pinned by `dashboard.test.ts`:
 *
 *   - key shape `[dashboard, wsId, kind, window, projectId, tz]` — identical
 *     to `dashboardKeys` in core, so cache scoping (workspace switch flips,
 *     window/project/tz never collide) behaves the same,
 *   - `enabled: !!wsId`, staleTime 60s, refetchInterval 5min — the web
 *     dashboard's fetch-on-enter + periodic-poll cadence (no WS). The server
 *     materializes these rollups on a 5-minute cycle, so polling faster only
 *     re-reads an unchanged rollup; the short staleTime makes re-entering the
 *     screen refetch anything older than a minute,
 *   - window changes keep the previous data mounted (scope-aware
 *     `placeholderData`); workspace/project/tz changes deliberately do NOT,
 *   - the only deliberate deltas from core: the abort signal is forwarded
 *     into the mobile api call (mobile CLAUDE.md queryFn contract), and the
 *     six methods live on the mobile `api` instance.
 */
import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

/** Inclusive calendar window in the viewer's timezone — YYYY-MM-DD strings. */
export interface DashboardWindow {
  start: string;
  end: string;
}

export const DASHBOARD_STALE_TIME_MS = 60 * 1000;
export const DASHBOARD_REFETCH_INTERVAL_MS = 5 * 60 * 1000;

export const dashboardKeys = {
  all: (wsId: string) => ["dashboard", wsId] as const,
  daily: (
    wsId: string,
    window: DashboardWindow,
    projectId: string | null,
    tz: string,
  ) => [...dashboardKeys.all(wsId), "daily", window, projectId, tz] as const,
  byAgent: (
    wsId: string,
    window: DashboardWindow,
    projectId: string | null,
    tz: string,
  ) => [...dashboardKeys.all(wsId), "by-agent", window, projectId, tz] as const,
  agentRuntime: (
    wsId: string,
    window: DashboardWindow,
    projectId: string | null,
    tz: string,
  ) =>
    [...dashboardKeys.all(wsId), "agent-runtime", window, projectId, tz] as const,
  runTimeDaily: (
    wsId: string,
    window: DashboardWindow,
    projectId: string | null,
    tz: string,
  ) =>
    [...dashboardKeys.all(wsId), "runtime-daily", window, projectId, tz] as const,
  failuresDaily: (
    wsId: string,
    window: DashboardWindow,
    projectId: string | null,
    tz: string,
  ) =>
    [...dashboardKeys.all(wsId), "failures-daily", window, projectId, tz] as const,
  failuresByAgent: (
    wsId: string,
    window: DashboardWindow,
    projectId: string | null,
    tz: string,
  ) =>
    [
      ...dashboardKeys.all(wsId),
      "failures-by-agent",
      window,
      projectId,
      tz,
    ] as const,
};

// Window changes should keep the previous result mounted so KPI cards and
// charts transition in place instead of flashing a skeleton. Scope changes
// are deliberately excluded: carrying data across workspaces, projects,
// report kinds, or timezones would briefly display the wrong data. Index 3
// is the window segment of the six-part key.
function isSameDashboardScope(
  previousKey: readonly unknown[] | undefined,
  nextKey: readonly unknown[],
): boolean {
  if (!previousKey || previousKey.length !== nextKey.length) return false;
  return previousKey.every(
    (part, index) => index === 3 || Object.is(part, nextKey[index]),
  );
}

export function dashboardUsageDailyOptions(
  wsId: string | null,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.daily(wsId ?? "", window, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: ({ signal }) =>
      api.getDashboardUsageDaily({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
        signal,
      }),
    enabled: !!wsId,
    staleTime: DASHBOARD_STALE_TIME_MS,
    refetchInterval: DASHBOARD_REFETCH_INTERVAL_MS,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardUsageByAgentOptions(
  wsId: string | null,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.byAgent(wsId ?? "", window, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: ({ signal }) =>
      api.getDashboardUsageByAgent({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
        signal,
      }),
    enabled: !!wsId,
    staleTime: DASHBOARD_STALE_TIME_MS,
    refetchInterval: DASHBOARD_REFETCH_INTERVAL_MS,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardAgentRunTimeOptions(
  wsId: string | null,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.agentRuntime(wsId ?? "", window, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: ({ signal }) =>
      api.getDashboardAgentRunTime({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
        signal,
      }),
    enabled: !!wsId,
    staleTime: DASHBOARD_STALE_TIME_MS,
    refetchInterval: DASHBOARD_REFETCH_INTERVAL_MS,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardRunTimeDailyOptions(
  wsId: string | null,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.runTimeDaily(wsId ?? "", window, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: ({ signal }) =>
      api.getDashboardRunTimeDaily({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
        signal,
      }),
    enabled: !!wsId,
    staleTime: DASHBOARD_STALE_TIME_MS,
    refetchInterval: DASHBOARD_REFETCH_INTERVAL_MS,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardFailuresDailyOptions(
  wsId: string | null,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.failuresDaily(wsId ?? "", window, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: ({ signal }) =>
      api.getDashboardFailuresDaily({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
        signal,
      }),
    enabled: !!wsId,
    staleTime: DASHBOARD_STALE_TIME_MS,
    refetchInterval: DASHBOARD_REFETCH_INTERVAL_MS,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardFailuresByAgentOptions(
  wsId: string | null,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.failuresByAgent(
    wsId ?? "",
    window,
    projectId,
    tz,
  );
  return queryOptions({
    queryKey,
    queryFn: ({ signal }) =>
      api.getDashboardFailuresByAgent({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
        signal,
      }),
    enabled: !!wsId,
    staleTime: DASHBOARD_STALE_TIME_MS,
    refetchInterval: DASHBOARD_REFETCH_INTERVAL_MS,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}
