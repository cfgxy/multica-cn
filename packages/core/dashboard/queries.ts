import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/**
 * An explicit, inclusive calendar-day window in the viewer's timezone,
 * e.g. `{ start: "2026-02-25", end: "2026-03-03" }` covers those seven days
 * under the viewer's tz. This is the client-side mirror of the server's
 * `start`/`end` query parameters: sending both overrides the legacy relative
 * `days` window and is how historical periods are selected. The dashboard
 * always sends explicit windows — quick ranges (1D/7D/…) are just windows
 * anchored on today, so every endpoint on the page shares one time boundary.
 */
export interface DashboardWindow {
  start: string;
  end: string;
}

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

// The server materializes these rollups on a 5-minute cadence, so a mounted
// dashboard re-polls on that same cadence — polling faster would only re-read
// an unchanged rollup. The short staleTime keeps re-entering the page honest:
// anything older than a minute refetches on mount instead of waiting out the
// interval. Neither fires for unmounted queries or backgrounded windows.
const STALE_TIME = 60 * 1000;
const REFETCH_INTERVAL = 5 * 60 * 1000;

// Window changes should keep the previous result mounted so KPI cards and
// charts transition in place instead of falling back to a full-page skeleton.
// Scope changes are deliberately excluded: carrying data across workspaces,
// projects, report kinds, or timezones would briefly display the wrong data.
function isSameDashboardScope(
  previousKey: readonly unknown[] | undefined,
  nextKey: readonly unknown[],
): boolean {
  if (!previousKey || previousKey.length !== nextKey.length) return false;
  return previousKey.every(
    (part, index) => index === 3 || Object.is(part, nextKey[index]),
  );
}

// `tz` participates in every dashboard key so a Preferences change
// repoints the cache. Every series — token rollups and the
// atq.completed_at-based run-time / failure series — slices its day boundary
// in the viewer's tz, so all the dashboard tabs always agree. The explicit
// window rides the same position the old `days` number held, so switching
// between historical periods keeps the same keep-previous-data behaviour.
export function dashboardUsageDailyOptions(
  wsId: string,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.daily(wsId, window, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getDashboardUsageDaily({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardUsageByAgentOptions(
  wsId: string,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.byAgent(wsId, window, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getDashboardUsageByAgent({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardAgentRunTimeOptions(
  wsId: string,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.agentRuntime(wsId, window, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getDashboardAgentRunTime({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardRunTimeDailyOptions(
  wsId: string,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.runTimeDaily(wsId, window, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getDashboardRunTimeDaily({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardFailuresDailyOptions(
  wsId: string,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.failuresDaily(wsId, window, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getDashboardFailuresDaily({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardFailuresByAgentOptions(
  wsId: string,
  window: DashboardWindow,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.failuresByAgent(wsId, window, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getDashboardFailuresByAgent({
        start: window.start,
        end: window.end,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}
