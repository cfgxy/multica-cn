/**
 * Workspace usage stats (`more/stats`, RUYI-638 阶段3) — the mobile mirror
 * of the web dashboard (`packages/views/dashboard/`, web `/usage`).
 *
 * Parity architecture (验收 B4/C3/D1/E3 静态判据的锚点):
 *   - Window math, granularity binding and every aggregation run on the SAME
 *     core functions web uses — `@multica/core/dashboard` (window.ts +
 *     aggregate.ts, moved out of views in this stage) and
 *     `@multica/core/runtimes/usage` (cost/pricing + weekly folds). No
 *     mobile-local reimplementation of any number this screen shows.
 *   - Data layer mirrors `packages/core/dashboard/queries.ts` semantics
 *     (enter-fetch + 5-min polling, 60s staleTime, scope-aware
 *     keep-previous-data) — see `@/data/queries/dashboard`. No WS, same as
 *     web (F2).
 *   - Copy comes from the shared locales package (`usage` namespace, same
 *     JSON web renders), so language follows the app and wording stays
 *     web-sourced (G1).
 *
 * Deliberately RN-shaped where web's DOM cannot carry over: charts are
 * drawn with react-native-svg, the leaderboard/offender rows are compact
 * stacked layouts, and the failure-class color ramp expresses the web
 * destructive-mix as opacity steps (see `@/lib/stats-format`).
 */
import { useMemo, useState } from "react";
import {
  Pressable,
  ScrollView,
  Text as RNText,
  View,
  useWindowDimensions,
} from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Line, Rect, Svg, Text as SvgText } from "react-native-svg";
import {
  DELETED_AGENTS_ROW_ID,
  FAILURE_CLASSES,
  MIN_RATE_SAMPLE,
  OFFENDER_METRIC,
  RESTRICTED_AGENTS_ROW_ID,
  TIME_RANGES,
  aggregateAgentFailures,
  aggregateAgentTokens,
  aggregateDailyCost,
  aggregateDailyErrors,
  aggregateDailyTasks,
  aggregateDailyTime,
  aggregateDailyTokens,
  aggregateFailureClasses,
  aggregateWeeklyErrors,
  aggregateWeeklyTasks,
  aggregateWeeklyTime,
  anonymizeUnresolvedAgentRows,
  bucketUnknownAgentRows,
  computeDailyTotals,
  computeFailureTotals,
  canShiftNext,
  dimsForWindowLength,
  formatDuration,
  hasRateSample,
  isSyntheticAgentRow,
  mergeAgentDashboardRows,
  shiftedWindow,
  sortAgentFailures,
  windowLength,
  type AgentDashboardRow,
  type Dim,
} from "@multica/core/dashboard";
import { aggregateByWeek, formatTokens } from "@multica/core/runtimes/usage";
import { formatShortDate, todayIso } from "@multica/core/runtimes/date-utils";
import type { TFunction } from "i18next";
import type {
  Agent,
  DashboardAgentRunTime,
  DashboardFailureByAgent,
  DashboardFailureDaily,
  DashboardRunTimeDaily,
  DashboardUsageByAgent,
  DashboardUsageDaily,
} from "@multica/core/types";
import { Skeleton } from "@/components/ui/skeleton";
import { agentListOptions } from "@/data/queries/agents";
import { projectListOptions } from "@/data/queries/projects";
import {
  dashboardKeys,
  dashboardAgentRunTimeOptions,
  dashboardFailuresByAgentOptions,
  dashboardFailuresDailyOptions,
  dashboardRunTimeDailyOptions,
  dashboardUsageByAgentOptions,
  dashboardUsageDailyOptions,
} from "@/data/queries/dashboard";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { cn } from "@/lib/utils";
import {
  FAILURE_CLASS_OPACITY,
  formatCompactNumber,
  formatRatePercent,
  formatUsd,
} from "@/lib/stats-format";

// Web's `ALL_PROJECTS` sentinel (dashboard-shared.tsx) — empty string means
// no project filter; the effective id is validated against the workspace's
// project list before it reaches any query.
const ALL_PROJECTS = "";

// Stable empty arrays — `?? []` per render would break useMemo deps.
const EMPTY_USAGE_DAILY: DashboardUsageDaily[] = [];
const EMPTY_BY_AGENT: DashboardUsageByAgent[] = [];
const EMPTY_RUNTIME: DashboardAgentRunTime[] = [];
const EMPTY_RUNTIME_DAILY: DashboardRunTimeDaily[] = [];
const EMPTY_FAILURE_DAILY: DashboardFailureDaily[] = [];
const EMPTY_FAILURE_BY_AGENT: DashboardFailureByAgent[] = [];
const EMPTY_AGENTS: Agent[] = [];

type StatsTab = "usage" | "errors";
type LeaderboardSort = "tokens" | "cost" | "time" | "tasks";
type TrendMetric = "cost" | "tokens" | "time" | "tasks";

// Web's leaderboard cap (leaderboard.tsx LEADERBOARD_LIMIT): ten answers
// "who is spending the most", the tail sits behind the show-all toggle.
const LEADERBOARD_LIMIT = 10;
// Web's offender cap (errors-tab.tsx TOP_OFFENDER_LIMIT).
const TOP_OFFENDER_LIMIT = 8;

// Viewer's IANA tz — stored user preference, else device-detected, else
// UTC. Mirrors web's useViewingTimezone (the mobile auth store is a local
// mirror of the same `User` shape, so `user.timezone` reads identically).
function useViewingTimezone(): string {
  const stored = useAuthStore((s) => s.user?.timezone ?? null);
  if (stored && stored.trim() !== "") return stored;
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

export default function StatsScreen() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const viewTZ = useViewingTimezone();
  // Two bindings, not one destructuring: the i18n key scanner's useT regex
  // only recognises `const { t } = useT(ns)` and would resolve every t()
  // below to the common namespace otherwise (same shape the other panes use).
  const { t } = useT("usage");
  const { i18n } = useT("usage");
  const locales = i18n.resolvedLanguage ?? i18n.language;
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const queryClient = useQueryClient();

  // ── Scope state ────────────────────────────────────────────────────────
  // Web models the window as two axes (quick range + calendar offset); the
  // mobile surface exposes the same five quick ranges and the same period
  // paging (step = window length, no paging into the future, 1d = "today"
  // in the viewer's tz — all inside core's window.ts). Custom date picking
  // stays web-only; every number below derives from this one window.
  const [tab, setTab] = useState<StatsTab>("usage");
  const [days, setDays] = useState<number>(30);
  const [offset, setOffset] = useState(0);
  const [dim, setDim] = useState<Dim>("daily");
  const [projectValue, setProjectValue] = useState(ALL_PROJECTS);

  const today = todayIso(viewTZ);
  // `days` is typed as number here so the TIME_RANGES mapping stays simple;
  // shiftedWindow treats it as whole days ending `offset` periods back.
  const statWindow = useMemo(
    () => shiftedWindow(days as 1 | 7 | 30 | 90 | 180, offset, today),
    [days, offset, today],
  );
  const windowLen = windowLength(statWindow);
  // Granularity binding (D1/E3): 1d/7d → daily only, 30d/90d → both,
  // 180d → weekly only. A stale card-scoped dim clamps to what's allowed
  // instead of resetting the page-scoped range (web MUL-5759 semantics).
  const allowedDims = dimsForWindowLength(windowLen);
  const effectiveDim: Dim = allowedDims.includes(dim) ? dim : allowedDims[0]!;
  const weekCount = Math.max(1, Math.ceil(windowLen / 7));
  const canGoNext = canShiftNext(statWindow, today);

  // ── Reference data ─────────────────────────────────────────────────────
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const agentsQuery = useQuery(agentListOptions(wsId));
  const agents = agentsQuery.data ?? EMPTY_AGENTS;

  // Stale project id (deleted project, workspace switch) would silently
  // filter every query to empty — derive the effective filter like web does.
  const projectId = useMemo(() => {
    if (projectValue === ALL_PROJECTS) return null;
    return projects.some((p) => p.id === projectValue) ? projectValue : null;
  }, [projectValue, projects]);

  // ── The six rollups (same queries, same keys semantics as web) ─────────
  const dailyQuery = useQuery(
    dashboardUsageDailyOptions(wsId, statWindow, projectId, viewTZ),
  );
  const byAgentQuery = useQuery(
    dashboardUsageByAgentOptions(wsId, statWindow, projectId, viewTZ),
  );
  const runTimeQuery = useQuery(
    dashboardAgentRunTimeOptions(wsId, statWindow, projectId, viewTZ),
  );
  const runTimeDailyQuery = useQuery(
    dashboardRunTimeDailyOptions(wsId, statWindow, projectId, viewTZ),
  );
  const failuresDailyQuery = useQuery(
    dashboardFailuresDailyOptions(wsId, statWindow, projectId, viewTZ),
  );
  const failuresByAgentQuery = useQuery(
    dashboardFailuresByAgentOptions(wsId, statWindow, projectId, viewTZ),
  );

  const dailyUsage = dailyQuery.data ?? EMPTY_USAGE_DAILY;
  const byAgentUsage = byAgentQuery.data ?? EMPTY_BY_AGENT;
  const runTimeRows = runTimeQuery.data ?? EMPTY_RUNTIME;
  const runTimeDailyRows = runTimeDailyQuery.data ?? EMPTY_RUNTIME_DAILY;
  const failureDailyRows = failuresDailyQuery.data ?? EMPTY_FAILURE_DAILY;
  const failureByAgentRows = failuresByAgentQuery.data ?? EMPTY_FAILURE_BY_AGENT;

  const isRefreshing =
    dailyQuery.isFetching ||
    byAgentQuery.isFetching ||
    runTimeQuery.isFetching ||
    runTimeDailyQuery.isFetching ||
    failuresDailyQuery.isFetching ||
    failuresByAgentQuery.isFetching;
  const handleRefresh = () => {
    if (!wsId) return;
    void queryClient.invalidateQueries({ queryKey: dashboardKeys.all(wsId) });
  };

  // Data freshness label — the most recent successful fetch among the six
  // rollups, rendered in the viewer's tz (web header's "tz · updated" line).
  const updatedLabel = useMemo(() => {
    const stamps = [
      dailyQuery.dataUpdatedAt,
      byAgentQuery.dataUpdatedAt,
      runTimeQuery.dataUpdatedAt,
      runTimeDailyQuery.dataUpdatedAt,
      failuresDailyQuery.dataUpdatedAt,
      failuresByAgentQuery.dataUpdatedAt,
    ].filter((n) => typeof n === "number" && n > 0);
    if (stamps.length === 0) return null;
    try {
      return new Intl.DateTimeFormat(locales, {
        timeZone: viewTZ,
        hour: "2-digit",
        minute: "2-digit",
      }).format(new Date(Math.max(...stamps)));
    } catch {
      return null;
    }
  }, [
    dailyQuery.dataUpdatedAt,
    byAgentQuery.dataUpdatedAt,
    runTimeQuery.dataUpdatedAt,
    runTimeDailyQuery.dataUpdatedAt,
    failuresDailyQuery.dataUpdatedAt,
    failuresByAgentQuery.dataUpdatedAt,
    locales,
    viewTZ,
  ]);

  // ── Window trims + aggregations (identical core calls as web) ──────────
  // The server returns exactly the requested window; the trim guards the
  // keep-previous-data transition render when a window switch is in flight.
  const dailyUsageInWindow = useMemo(
    () =>
      dailyUsage.filter(
        (u) => u.date >= statWindow.start && u.date <= statWindow.end,
      ),
    [dailyUsage, statWindow.start, statWindow.end],
  );
  const runTimeDailyInWindow = useMemo(
    () =>
      runTimeDailyRows.filter(
        (r) => r.date >= statWindow.start && r.date <= statWindow.end,
      ),
    [runTimeDailyRows, statWindow.start, statWindow.end],
  );
  const failureDailyInWindow = useMemo(
    () =>
      failureDailyRows.filter(
        (r) => r.date >= statWindow.start && r.date <= statWindow.end,
      ),
    [failureDailyRows, statWindow.start, statWindow.end],
  );

  // Per-tab loading and emptiness (F1/F4): the Usage tab never waits on the
  // failure rollups, and spend without failures is not an empty dashboard.
  const usageLoading =
    dailyQuery.isLoading ||
    byAgentQuery.isLoading ||
    runTimeQuery.isLoading ||
    runTimeDailyQuery.isLoading;
  const errorsLoading =
    failuresDailyQuery.isLoading || failuresByAgentQuery.isLoading;
  // F3 error states are per tab too; retry re-pulls the whole dashboard
  // scope (same invalidation the refresh button uses).
  const usageErrored =
    dailyQuery.isError ||
    byAgentQuery.isError ||
    runTimeQuery.isError ||
    runTimeDailyQuery.isError;
  const errorsErrored =
    failuresDailyQuery.isError || failuresByAgentQuery.isError;

  const usageHasNoData =
    !usageLoading &&
    dailyUsage.length === 0 &&
    byAgentUsage.length === 0 &&
    runTimeRows.length === 0 &&
    runTimeDailyRows.length === 0;

  const totals = useMemo(
    () => computeDailyTotals(dailyUsageInWindow),
    [dailyUsageInWindow],
  );
  // KPI run time / tasks tiles read off the per-agent run-time rollup (a
  // true per-agent distinct task count), not the token rollup — same
  // denominator rule as web.
  const runTimeTotals = useMemo(() => {
    let totalSeconds = 0;
    let taskCount = 0;
    let failedCount = 0;
    for (const r of runTimeRows) {
      totalSeconds += r.total_seconds;
      taskCount += r.task_count;
      failedCount += r.failed_count;
    }
    return { totalSeconds, taskCount, failedCount };
  }, [runTimeRows]);

  const dailyCost = useMemo(
    () => aggregateDailyCost(dailyUsageInWindow),
    [dailyUsageInWindow],
  );
  const dailyTokens = useMemo(
    () => aggregateDailyTokens(dailyUsageInWindow),
    [dailyUsageInWindow],
  );
  const dailyTime = useMemo(
    () => aggregateDailyTime(runTimeDailyInWindow),
    [runTimeDailyInWindow],
  );
  const dailyTasks = useMemo(
    () => aggregateDailyTasks(runTimeDailyInWindow),
    [runTimeDailyInWindow],
  );
  const weeklyFold = useMemo(
    () =>
      aggregateByWeek(
        dailyUsageInWindow,
        viewTZ,
        weekCount,
        statWindow.end,
        statWindow.start,
      ),
    [dailyUsageInWindow, viewTZ, weekCount, statWindow],
  );
  const weeklyTime = useMemo(
    () =>
      aggregateWeeklyTime(
        runTimeDailyInWindow,
        viewTZ,
        weekCount,
        statWindow.end,
        statWindow.start,
      ),
    [runTimeDailyInWindow, viewTZ, weekCount, statWindow],
  );
  const weeklyTasks = useMemo(
    () =>
      aggregateWeeklyTasks(
        runTimeDailyInWindow,
        viewTZ,
        weekCount,
        statWindow.end,
        statWindow.start,
      ),
    [runTimeDailyInWindow, viewTZ, weekCount, statWindow],
  );

  // Failure summaries come from the DATE-BUCKETED rollup after the same
  // window trim (web's denominator discipline), the per-agent split feeds
  // the offender list after anonymization.
  const failureTotals = useMemo(
    () => computeFailureTotals(failureDailyInWindow),
    [failureDailyInWindow],
  );
  const failureClassRows = useMemo(
    () => aggregateFailureClasses(failureDailyInWindow),
    [failureDailyInWindow],
  );
  const dailyErrors = useMemo(
    () => aggregateDailyErrors(failureDailyInWindow),
    [failureDailyInWindow],
  );
  const weeklyErrors = useMemo(
    () =>
      aggregateWeeklyErrors(
        failureDailyInWindow,
        viewTZ,
        weekCount,
        statWindow.end,
        statWindow.start,
      ),
    [failureDailyInWindow, viewTZ, weekCount, statWindow],
  );
  const knownAgentIds = useMemo(
    () => (agentsQuery.isSuccess ? new Set(agents.map((a) => a.id)) : null),
    [agentsQuery.isSuccess, agents],
  );
  const agentFailureRows = useMemo(
    () =>
      aggregateAgentFailures(
        anonymizeUnresolvedAgentRows(failureByAgentRows, knownAgentIds),
      ),
    [failureByAgentRows, knownAgentIds],
  );

  const agentTokenRows = useMemo(
    () => aggregateAgentTokens(byAgentUsage),
    [byAgentUsage],
  );
  const agentRows = useMemo(
    () => mergeAgentDashboardRows(agentTokenRows, runTimeRows),
    [agentTokenRows, runTimeRows],
  );
  // Hard-deleted agents fold into one "Deleted agents" bucket so the
  // per-agent breakdown keeps reconciling with the top-line KPIs; archived
  // agents stay as themselves (the list fetches with archived included).
  const visibleAgentRows = useMemo(
    () => bucketUnknownAgentRows(agentRows, knownAgentIds),
    [agentRows, knownAgentIds],
  );
  const deletedAgentCount = useMemo(
    () =>
      knownAgentIds
        ? agentRows.filter(
            (r) => !knownAgentIds.has(r.agentId) && !isSyntheticAgentRow(r.agentId),
          ).length
        : 0,
    [agentRows, knownAgentIds],
  );

  const lessThanMinuteLabel = t("duration.less_than_minute", "<1m");

  // ── Render ─────────────────────────────────────────────────────────────
  return (
    <View className="flex-1 bg-background">
      {/* Tabs + freshness + refresh — the web toolbar grammar, compacted */}
      <View className="flex-row items-center justify-between gap-2 pl-4 pr-2 pt-2 pb-1">
        <View className="flex-row gap-2">
          {(["usage", "errors"] as const).map((v) => {
            const active = tab === v;
            const label =
              v === "usage"
                ? t("tab_usage", "Usage")
                : t("errors.title", "Errors");
            return (
              <Pressable
                key={v}
                onPress={() => setTab(v)}
                accessibilityRole="button"
                accessibilityState={{ selected: active }}
                className={cn(
                  "rounded-full px-3 py-1.5",
                  active ? "bg-primary" : "bg-muted",
                )}
              >
                <RNText
                  className={cn(
                    "text-xs font-medium",
                    active ? "text-primary-foreground" : "text-foreground",
                  )}
                >
                  {label}
                </RNText>
              </Pressable>
            );
          })}
        </View>
        <View className="flex-row items-center gap-2">
          {updatedLabel ? (
            <RNText className="text-[11px] text-muted-foreground">
              {t("header.timezone_and_updated", "{{tz}} · updated {{time}}", {
                tz: viewTZ,
                time: updatedLabel,
              })}
            </RNText>
          ) : null}
          <Pressable
            onPress={handleRefresh}
            disabled={isRefreshing}
            accessibilityLabel={t("header.refresh", "Refresh")}
            accessibilityRole="button"
            className="size-9 items-center justify-center rounded-full active:bg-secondary"
          >
            <Ionicons
              name="refresh"
              size={18}
              color={
                isRefreshing ? theme.mutedForeground : theme.foreground
              }
            />
          </Pressable>
        </View>
      </View>

      {/* Five quick ranges — the same TIME_RANGES ladder web renders */}
      <View className="flex-row flex-wrap gap-2 px-4 pb-2">
        {TIME_RANGES.map((r) => {
          const active = days === r.days;
          return (
            <Pressable
              key={r.days}
              onPress={() => {
                // Web resets the period offset when the range changes —
                // every pill is "ending today" by definition.
                setDays(r.days);
                setOffset(0);
              }}
              accessibilityRole="button"
              accessibilityState={{ selected: active }}
              accessibilityLabel={r.label}
              className={cn(
                "rounded-full px-3 py-1.5",
                active ? "bg-primary" : "bg-muted",
              )}
            >
              <RNText
                className={cn(
                  "text-xs font-medium",
                  active ? "text-primary-foreground" : "text-foreground",
                )}
              >
                {r.label}
              </RNText>
            </Pressable>
          );
        })}
      </View>

      {/* Period navigation: step = window length, no paging into the
          future (next dies at the present), one-tap way home (B3/B4). */}
      <View className="flex-row items-center gap-2 px-4 pb-2">
        <Pressable
          onPress={() => setOffset((o) => o + 1)}
          accessibilityLabel={t("filter.period_prev", "Previous period")}
          accessibilityRole="button"
          className="size-8 items-center justify-center rounded-full bg-muted active:bg-secondary"
        >
          <Ionicons name="chevron-back" size={16} color={theme.foreground} />
        </Pressable>
        <RNText className="flex-1 text-center text-xs text-muted-foreground" numberOfLines={1}>
          {statWindow.start === statWindow.end
            ? formatShortDate(statWindow.start)
            : `${formatShortDate(statWindow.start)} – ${formatShortDate(statWindow.end)}`}
        </RNText>
        <Pressable
          onPress={() => setOffset((o) => Math.max(0, o - 1))}
          disabled={!canGoNext}
          accessibilityLabel={t("filter.period_next", "Next period")}
          accessibilityRole="button"
          className="size-8 items-center justify-center rounded-full bg-muted active:bg-secondary"
        >
          <Ionicons
            name="chevron-forward"
            size={16}
            color={canGoNext ? theme.foreground : theme.mutedForeground}
          />
        </Pressable>
      </View>
      {offset > 0 ? (
        <View className="px-4 pb-2">
          <Pressable
            onPress={() => setOffset(0)}
            accessibilityRole="button"
            accessibilityLabel={t(
              "filter.period_back_to_current",
              "Back to current period",
            )}
            className="self-start rounded-full bg-muted px-3 py-1 active:bg-secondary"
          >
            <RNText className="text-xs text-foreground">
              {t("filter.period_back_to_current", "Back to current period")}
            </RNText>
          </Pressable>
        </View>
      ) : null}

      {/* Project filter — one row, every surface below reads `projectId` */}
      <View className="border-b border-border pb-2">
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerClassName="gap-2 px-4">
          {[ALL_PROJECTS, ...projects.map((p) => p.id)].map((id) => {
            const active = projectValue === id;
            const label =
              id === ALL_PROJECTS
                ? t("filter.all_projects", "All projects")
                : (projects.find((p) => p.id === id)?.title ?? id);
            return (
              <Pressable
                key={id || "__all__"}
                onPress={() => setProjectValue(id)}
                accessibilityRole="button"
                accessibilityState={{ selected: active }}
                className={cn(
                  "rounded-full px-3 py-1.5",
                  active ? "bg-primary" : "bg-muted",
                )}
              >
                <RNText
                  className={cn(
                    "text-xs font-medium",
                    active ? "text-primary-foreground" : "text-foreground",
                  )}
                  numberOfLines={1}
                >
                  {label}
                </RNText>
              </Pressable>
            );
          })}
        </ScrollView>
      </View>

      <ScrollView className="flex-1" contentContainerClassName="gap-4 px-4 pb-8 pt-4">
        {tab === "usage" ? (
          <UsagePane
            loading={usageLoading}
            errored={usageErrored}
            hasNoData={usageHasNoData}
            onRetry={handleRefresh}
            errorTitle={t("error.title", "Couldn't load stats")}
            retryLabel={t("common:mobile.common.retry", "Retry")}
            emptyDesktopHint={t(
              "empty.desktop_hint",
              "For the full analytics experience, open the desktop app.",
            )}
            totals={totals}
            runTimeTotals={runTimeTotals}
            windowLen={windowLen}
            allowedDims={allowedDims}
            dim={effectiveDim}
            onDimChange={setDim}
            dailyCost={dailyCost}
            dailyTokens={dailyTokens}
            dailyTime={dailyTime}
            dailyTasks={dailyTasks}
            weeklyCost={weeklyFold.weeklyCostStack}
            weeklyTokens={weeklyFold.weeklyTokens}
            weeklyTime={weeklyTime}
            weeklyTasks={weeklyTasks}
            lessThanMinuteLabel={lessThanMinuteLabel}
            leaderboardRows={visibleAgentRows}
            agents={agents}
            deletedAgentCount={deletedAgentCount}
          />
        ) : (
          <ErrorsPane
            loading={errorsLoading}
            errored={errorsErrored}
            onRetry={handleRefresh}
            errorTitle={t("error.title", "Couldn't load stats")}
            retryLabel={t("common:mobile.common.retry", "Retry")}
            windowLen={windowLen}
            allowedDims={allowedDims}
            dim={effectiveDim}
            onDimChange={setDim}
            totals={failureTotals}
            classRows={failureClassRows}
            agentRows={agentFailureRows}
            dailyErrors={dailyErrors}
            weeklyErrors={weeklyErrors}
            agents={agents}
            lessThanMinuteLabel={lessThanMinuteLabel}
          />
        )}
      </ScrollView>
    </View>
  );
}

// ─── Usage tab ─────────────────────────────────────────────────────────────

interface UsagePaneProps {
  loading: boolean;
  errored: boolean;
  hasNoData: boolean;
  onRetry: () => void;
  errorTitle: string;
  retryLabel: string;
  emptyDesktopHint: string;
  totals: ReturnType<typeof computeDailyTotals>;
  runTimeTotals: { totalSeconds: number; taskCount: number; failedCount: number };
  windowLen: number;
  allowedDims: readonly Dim[];
  dim: Dim;
  onDimChange: (d: Dim) => void;
  dailyCost: ReturnType<typeof aggregateDailyCost>;
  dailyTokens: ReturnType<typeof aggregateDailyTokens>;
  dailyTime: ReturnType<typeof aggregateDailyTime>;
  dailyTasks: ReturnType<typeof aggregateDailyTasks>;
  weeklyCost: ReturnType<typeof aggregateByWeek>["weeklyCostStack"];
  weeklyTokens: ReturnType<typeof aggregateByWeek>["weeklyTokens"];
  weeklyTime: ReturnType<typeof aggregateWeeklyTime>;
  weeklyTasks: ReturnType<typeof aggregateWeeklyTasks>;
  lessThanMinuteLabel: string;
  leaderboardRows: AgentDashboardRow[];
  agents: Agent[];
  deletedAgentCount: number;
}

function UsagePane(props: UsagePaneProps) {
  const { t } = useT("usage");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];

  if (props.loading) return <StatsSkeleton />;
  if (props.errored) {
    return (
      <ErrorPane
        title={props.errorTitle}
        retryLabel={props.retryLabel}
        onRetry={props.onRetry}
      />
    );
  }
  if (props.hasNoData) {
    // Cold-start empty state, with the desktop hand-off hint (PM 建议 3 /
    // F1): zero-value nothing instead of an error, plus where the full
    // surface lives.
    return (
      <View className="items-center rounded-lg border border-dashed border-border px-6 py-12">
        <Ionicons
          name="bar-chart-outline"
          size={36}
          color={theme.mutedForeground}
        />
        <RNText className="mt-3 text-center text-sm font-medium text-foreground">
          {t("empty.title", "No usage yet")}
        </RNText>
        <RNText className="mt-1 text-center text-xs text-muted-foreground">
          {t(
            "empty.body",
            "Once agents start running tasks here, their token spend and run time will appear in this view.",
          )}
        </RNText>
        <RNText className="mt-3 text-center text-xs text-muted-foreground">
          {props.emptyDesktopHint}
        </RNText>
      </View>
    );
  }

  const totalTokens =
    props.totals.input +
    props.totals.output +
    props.totals.cacheRead +
    props.totals.cacheWrite;

  return (
    <>
      {/* KPI 2×2 — cost / tokens / run time / tasks, every label carries the
          window length ("· 30D") so a paged-back screenshot stays honest
          (C1). Same sources as web: cost+tokens from the daily usage rollup,
          run time + tasks from the per-agent run-time rollup. */}
      <View className="flex-row gap-3">
        <KpiCard
          label={t("kpi.cost_label", "Cost · {{days}}D", { days: props.windowLen })}
          value={formatUsd(props.totals.cost)}
        />
        <KpiCard
          label={t("kpi.tokens_label", "Tokens · {{days}}D", { days: props.windowLen })}
          value={formatCompactNumber(totalTokens)}
          hint={t("kpi.tokens_hint", "Input {{input}} · Output {{output}}", {
            input: formatTokens(props.totals.input),
            output: formatTokens(props.totals.output),
          })}
        />
      </View>
      <View className="flex-row gap-3">
        <KpiCard
          label={t("kpi.run_time_label", "Run time · {{days}}D", { days: props.windowLen })}
          value={formatDuration(
            props.runTimeTotals.totalSeconds,
            props.lessThanMinuteLabel,
          )}
          hint={t("kpi.run_time_hint", "Across {{tasks}} tasks", {
            tasks: props.runTimeTotals.taskCount,
          })}
        />
        <KpiCard
          label={t("kpi.tasks_label", "Tasks · {{days}}D", { days: props.windowLen })}
          value={String(props.runTimeTotals.taskCount)}
          hint={t("kpi.tasks_hint", "{{failed}} failed", {
            failed: props.runTimeTotals.failedCount,
          })}
        />
      </View>

      <TrendCard
        allowedDims={props.allowedDims}
        dim={props.dim}
        onDimChange={props.onDimChange}
        dailyCost={props.dailyCost}
        dailyTokens={props.dailyTokens}
        dailyTime={props.dailyTime}
        dailyTasks={props.dailyTasks}
        weeklyCost={props.weeklyCost}
        weeklyTokens={props.weeklyTokens}
        weeklyTime={props.weeklyTime}
        weeklyTasks={props.weeklyTasks}
      />

      <LeaderboardCard
        rows={props.leaderboardRows}
        agents={props.agents}
        deletedAgentCount={props.deletedAgentCount}
        lessThanMinuteLabel={props.lessThanMinuteLabel}
      />
    </>
  );
}

function KpiCard({
  label,
  value,
  hint,
}: {
  label: string;
  value: string;
  hint?: string;
}) {
  return (
    <View className="flex-1 rounded-lg border border-border bg-card p-3">
      <RNText className="text-[11px] text-muted-foreground" numberOfLines={1}>
        {label}
      </RNText>
      <RNText className="mt-1 text-lg font-semibold text-foreground">
        {value}
      </RNText>
      {hint ? (
        <RNText className="mt-0.5 text-[11px] text-muted-foreground" numberOfLines={1}>
          {hint}
        </RNText>
      ) : null}
    </View>
  );
}

function StatsSkeleton() {
  return (
    <>
      <View className="flex-row gap-3">
        <Skeleton className="h-20 flex-1 rounded-lg" />
        <Skeleton className="h-20 flex-1 rounded-lg" />
      </View>
      <Skeleton className="h-48 rounded-lg" />
      <Skeleton className="h-64 rounded-lg" />
    </>
  );
}

function ErrorPane({
  title,
  retryLabel,
  onRetry,
}: {
  title: string;
  retryLabel: string;
  onRetry: () => void;
}) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  return (
    <View className="items-center rounded-lg border border-dashed border-border px-6 py-12">
      <Ionicons name="cloud-offline-outline" size={36} color={theme.mutedForeground} />
      <RNText className="mt-3 text-center text-sm font-medium text-foreground">
        {title}
      </RNText>
      <Pressable
        onPress={onRetry}
        accessibilityRole="button"
        accessibilityLabel={retryLabel}
        className="mt-4 rounded-full bg-primary px-4 py-2 active:bg-secondary"
      >
        <RNText className="text-xs font-medium text-primary-foreground">
          {retryLabel}
        </RNText>
      </Pressable>
    </View>
  );
}

// ─── Trend card ────────────────────────────────────────────────────────────

function TrendCard({
  allowedDims,
  dim,
  onDimChange,
  dailyCost,
  dailyTokens,
  dailyTime,
  dailyTasks,
  weeklyCost,
  weeklyTokens,
  weeklyTime,
  weeklyTasks,
}: {
  allowedDims: readonly Dim[];
  dim: Dim;
  onDimChange: (d: Dim) => void;
  dailyCost: ReturnType<typeof aggregateDailyCost>;
  dailyTokens: ReturnType<typeof aggregateDailyTokens>;
  dailyTime: ReturnType<typeof aggregateDailyTime>;
  dailyTasks: ReturnType<typeof aggregateDailyTasks>;
  weeklyCost: ReturnType<typeof aggregateByWeek>["weeklyCostStack"];
  weeklyTokens: ReturnType<typeof aggregateByWeek>["weeklyTokens"];
  weeklyTime: ReturnType<typeof aggregateWeeklyTime>;
  weeklyTasks: ReturnType<typeof aggregateWeeklyTasks>;
}) {
  const { t } = useT("usage");
  const { colorScheme } = useColorScheme();
  const [metric, setMetric] = useState<TrendMetric>("cost");
  const weekly = dim === "weekly";

  // Literal-key switch so every title stays auditable by the i18n key
  // scanner (dynamic key construction would bypass it silently).
  const title = weekly
    ? metric === "cost"
      ? t("weekly.title_cost", "Weekly cost")
      : metric === "tokens"
        ? t("weekly.title_tokens", "Weekly tokens")
        : metric === "time"
          ? t("weekly.title_time", "Weekly run time")
          : t("weekly.title_tasks", "Weekly tasks")
    : metric === "cost"
      ? t("daily.title_cost", "Daily cost")
      : metric === "tokens"
        ? t("daily.title_tokens", "Daily tokens")
        : metric === "time"
          ? t("daily.title_time", "Daily run time")
          : t("daily.title_tasks", "Daily tasks");

  // Single-series totals of the same core aggregates web's stacked charts
  // consume — the sum equals the web stack by construction.
  const data = useMemo(() => {
    if (weekly) {
      switch (metric) {
        case "cost":
          return weeklyCost.map((d) => ({ label: d.label, value: d.total, partial: d.partial }));
        case "tokens":
          return weeklyTokens.map((d) => ({
            label: d.label,
            value: d.input + d.output + d.cacheRead + d.cacheWrite,
            partial: d.partial,
          }));
        case "time":
          return weeklyTime.map((d) => ({ label: d.label, value: d.totalSeconds, partial: d.partial }));
        case "tasks":
          return weeklyTasks.map((d) => ({
            label: d.label,
            value: d.completed + d.failed + d.cancelled,
            partial: d.partial,
          }));
      }
    }
    switch (metric) {
      case "cost":
        return dailyCost.map((d) => ({ label: d.label, value: d.total, partial: false }));
      case "tokens":
        return dailyTokens.map((d) => ({
          label: d.label,
          value: d.input + d.output + d.cacheRead + d.cacheWrite,
          partial: false,
        }));
      case "time":
        return dailyTime.map((d) => ({ label: d.label, value: d.totalSeconds, partial: false }));
      case "tasks":
        return dailyTasks.map((d) => ({
          label: d.label,
          value: d.completed + d.failed + d.cancelled,
          partial: false,
        }));
    }
  }, [weekly, metric, dailyCost, dailyTokens, dailyTime, dailyTasks, weeklyCost, weeklyTokens, weeklyTime, weeklyTasks]);

  return (
    <View className="rounded-lg border border-border bg-card p-4">
      <View className="mb-3 flex-row items-center justify-between gap-2">
        <RNText className="text-sm font-semibold text-foreground">{title}</RNText>
        {allowedDims.length > 1 ? (
          <View className="flex-row gap-1.5">
            {allowedDims.map((d) => {
              const active = dim === d;
              const label =
                d === "daily"
                  ? t("dim.daily", "Daily")
                  : t("dim.weekly", "Weekly");
              return (
                <Pressable
                  key={d}
                  onPress={() => onDimChange(d)}
                  accessibilityRole="button"
                  accessibilityState={{ selected: active }}
                  className={cn(
                    "rounded-full px-2.5 py-1",
                    active ? "bg-primary" : "bg-muted",
                  )}
                >
                  <RNText
                    className={cn(
                      "text-[11px] font-medium",
                      active ? "text-primary-foreground" : "text-foreground",
                    )}
                  >
                    {label}
                  </RNText>
                </Pressable>
              );
            })}
          </View>
        ) : null}
      </View>
      <View className="mb-2 flex-row flex-wrap gap-1.5">
        {(["cost", "tokens", "time", "tasks"] as const).map((m) => {
          const active = metric === m;
          const label =
            m === "cost"
              ? t("daily.metric_cost", "Cost")
              : m === "tokens"
                ? t("daily.metric_tokens", "Tokens")
                : m === "time"
                  ? t("daily.metric_time", "Time")
                  : t("daily.metric_tasks", "Tasks");
          return (
            <Pressable
              key={m}
              onPress={() => setMetric(m)}
              accessibilityRole="button"
              accessibilityState={{ selected: active }}
              className={cn(
                "rounded-full px-2.5 py-1",
                active ? "bg-primary" : "bg-muted",
              )}
            >
              <RNText
                className={cn(
                  "text-[11px] font-medium",
                  active ? "text-primary-foreground" : "text-foreground",
                )}
              >
                {label}
              </RNText>
            </Pressable>
          );
        })}
      </View>
      {data.length === 0 ? (
        <RNText className="py-8 text-center text-xs text-muted-foreground">
          {t("daily.no_data", "No usage in this window.")}
        </RNText>
      ) : (
        <BarChart data={data} color={THEME[colorScheme].brand} />
      )}
    </View>
  );
}

// react-native-svg bar chart: one bar per bucket, sparse x labels, a max-y
// tick and a baseline. Partial weekly buckets (week still in progress) draw
// at half opacity so they don't read as finished weeks — the same signal
// web's tooltip carries.
function BarChart({
  data,
  color,
  height = 160,
}: {
  data: { label: string; value: number; partial: boolean }[];
  color: string;
  height?: number;
}) {
  const { width: screenW } = useWindowDimensions();
  const width = screenW - 32 - 32; // screen padding + card padding
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const max = data.reduce((m, d) => Math.max(m, d.value), 0);
  const n = data.length;
  const slot = width / n;
  const barWidth = Math.max(2, Math.min(slot * 0.7, 24));
  const baselineY = height - 18;
  const chartH = baselineY - 14;
  const labelStep = Math.max(1, Math.ceil(n / 6));

  return (
    <Svg width={width} height={height}>
      {max > 0
        ? data.map((d, i) => {
            const h = Math.max(1, (d.value / max) * chartH);
            return (
              <Rect
                key={i}
                x={i * slot + (slot - barWidth) / 2}
                y={baselineY - h}
                width={barWidth}
                height={h}
                rx={2}
                fill={color}
                opacity={d.partial ? 0.5 : 1}
              />
            );
          })
        : null}
      <Line
        x1={0}
        y1={baselineY}
        x2={width}
        y2={baselineY}
        stroke={theme.border}
        strokeWidth={1}
      />
      {max > 0 ? (
        <SvgText x={0} y={10} fontSize={9} fill={theme.mutedForeground}>
          {formatCompactNumber(max)}
        </SvgText>
      ) : null}
      {data.map((d, i) =>
        i % labelStep === 0 || i === n - 1 ? (
          <SvgText
            key={i}
            x={i * slot + slot / 2}
            y={height - 4}
            fontSize={9}
            fill={theme.mutedForeground}
            textAnchor="middle"
          >
            {d.label}
          </SvgText>
        ) : null,
      )}
    </Svg>
  );
}

// ─── Leaderboard ───────────────────────────────────────────────────────────

// Same four metrics as web's leaderboard.tsx SORT_METRIC — which column is
// emphasised, the bar width, and the row order stay in lockstep.
const SORT_METRIC: Record<LeaderboardSort, (r: AgentDashboardRow) => number> = {
  tokens: (r) => r.tokens,
  cost: (r) => r.cost,
  time: (r) => r.seconds,
  tasks: (r) => r.taskCount,
};

function LeaderboardCard({
  rows,
  agents,
  deletedAgentCount,
  lessThanMinuteLabel,
}: {
  rows: AgentDashboardRow[];
  agents: Agent[];
  deletedAgentCount: number;
  lessThanMinuteLabel: string;
}) {
  const { t } = useT("usage");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const [sortBy, setSortBy] = useState<LeaderboardSort>("tokens");
  const [showAll, setShowAll] = useState(false);

  const sortedRows = useMemo(() => {
    const metric = SORT_METRIC[sortBy];
    return rows.toSorted((a, b) => metric(b) - metric(a));
  }, [rows, sortBy]);

  // Bar scale measured across every row so collapsed and expanded states
  // mean the same thing (web parity).
  const maxValue = useMemo(() => {
    const metric = SORT_METRIC[sortBy];
    return sortedRows.reduce((m, r) => Math.max(m, metric(r)), 0);
  }, [sortedRows, sortBy]);

  const visibleRows = showAll
    ? sortedRows
    : sortedRows.slice(0, LEADERBOARD_LIMIT);

  const namedAgentCount = useMemo(
    () => rows.filter((r) => !isSyntheticAgentRow(r.agentId)).length,
    [rows],
  );

  const sortLabel = (s: LeaderboardSort) =>
    s === "tokens"
      ? t("leaderboard.header_tokens", "Tokens")
      : s === "cost"
        ? t("leaderboard.header_cost", "Cost")
        : s === "time"
          ? t("leaderboard.header_time", "Time")
          : t("leaderboard.header_tasks", "Tasks");

  return (
    <View className="rounded-lg border border-border bg-card">
      <View className="flex-row items-center justify-between gap-2 border-b border-border px-4 pt-3 pb-2">
        <RNText className="text-sm font-semibold text-foreground">
          {t("leaderboard.title", "Leaderboard")}
        </RNText>
        <RNText className="text-[11px] text-muted-foreground">
          {deletedAgentCount > 0
            ? t("leaderboard.caption_with_deleted", "{{count}} agents · {{deleted}} deleted", {
                count: namedAgentCount,
                deleted: deletedAgentCount,
              })
            : t("leaderboard.caption", "{{count}} agents", { count: namedAgentCount })}
        </RNText>
      </View>
      <View className="flex-row flex-wrap items-center gap-1.5 px-4 pt-2">
        {(["tokens", "cost", "time", "tasks"] as const).map((s) => {
          const active = sortBy === s;
          return (
            <Pressable
              key={s}
              onPress={() => setSortBy(s)}
              accessibilityRole="button"
              accessibilityState={{ selected: active }}
              className={cn(
                "rounded-full px-2.5 py-1",
                active ? "bg-primary" : "bg-muted",
              )}
            >
              <RNText
                className={cn(
                  "text-[11px] font-medium",
                  active ? "text-primary-foreground" : "text-foreground",
                )}
              >
                {sortLabel(s)}
              </RNText>
            </Pressable>
          );
        })}
        {sortedRows.length > LEADERBOARD_LIMIT ? (
          <Pressable
            onPress={() => setShowAll((v) => !v)}
            accessibilityRole="button"
            className="ml-auto"
          >
            <RNText className="text-[11px] text-muted-foreground underline">
              {showAll
                ? t("leaderboard.show_less", "Show top {{count}}", { count: LEADERBOARD_LIMIT })
                : t("leaderboard.show_all", "Show all")}
            </RNText>
          </Pressable>
        ) : null}
      </View>
      {sortedRows.length === 0 ? (
        <RNText className="px-4 py-8 text-center text-xs text-muted-foreground">
          {t("leaderboard.no_data", "No agent activity in this window.")}
        </RNText>
      ) : (
        <View className="px-4 pb-2 pt-1">
          {visibleRows.map((row) => {
            const isDeletedBucket = row.agentId === DELETED_AGENTS_ROW_ID;
            const isRestrictedBucket = row.agentId === RESTRICTED_AGENTS_ROW_ID;
            const isBucket = isDeletedBucket || isRestrictedBucket;
            const agent = agents.find((a) => a.id === row.agentId);
            const value = SORT_METRIC[sortBy](row);
            const pct = maxValue > 0 ? (value / maxValue) * 100 : 0;
            return (
              <View key={row.agentId} className="py-2">
                <View className="flex-row items-center gap-2">
                  {isBucket ? (
                    <View className="size-6 items-center justify-center rounded-full bg-muted">
                      <Ionicons
                        name={isDeletedBucket ? "trash-outline" : "eye-off-outline"}
                        size={12}
                        color={theme.mutedForeground}
                      />
                    </View>
                  ) : (
                    <View className="size-6 items-center justify-center rounded-full bg-secondary">
                      <RNText className="text-[10px] font-medium text-foreground">
                        {(agent?.name ?? row.agentId).charAt(0).toUpperCase()}
                      </RNText>
                    </View>
                  )}
                  <RNText
                    className={cn(
                      "flex-1 text-xs font-medium",
                      isBucket && "italic text-muted-foreground",
                      !isBucket && "text-foreground",
                    )}
                    numberOfLines={1}
                  >
                    {isDeletedBucket
                      ? t("leaderboard.deleted_agents", "Deleted agents")
                      : isRestrictedBucket
                        ? t("leaderboard.other_agents", "Other agents")
                        : (agent?.name ?? row.agentId)}
                  </RNText>
                  <RNText className="text-xs font-semibold tabular-nums text-foreground">
                    {sortBy === "tokens"
                      ? formatTokens(row.tokens)
                      : sortBy === "cost"
                        ? `$${row.cost.toFixed(2)}`
                        : sortBy === "time"
                          ? isDeletedBucket
                            ? "—"
                            : formatDuration(row.seconds, lessThanMinuteLabel)
                          : isDeletedBucket
                            ? "—"
                            : String(row.taskCount)}
                  </RNText>
                </View>
                {/* Bar always measures the active sort metric (web lockstep
                    between order, bar and emphasis). */}
                <View className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted">
                  <View
                    className="h-full rounded-full bg-brand"
                    style={{ width: `${pct}%` }}
                  />
                </View>
                <View className="mt-1 flex-row justify-between">
                  <RNText className="text-[11px] tabular-nums text-muted-foreground">
                    {formatTokens(row.tokens)}
                  </RNText>
                  <RNText className="text-[11px] tabular-nums text-muted-foreground">
                    ${row.cost.toFixed(2)}
                  </RNText>
                  <RNText className="text-[11px] tabular-nums text-muted-foreground">
                    {isDeletedBucket
                      ? "—"
                      : formatDuration(row.seconds, lessThanMinuteLabel)}
                  </RNText>
                  <RNText className="text-[11px] tabular-nums text-muted-foreground">
                    {isDeletedBucket ? "—" : row.taskCount}
                  </RNText>
                </View>
              </View>
            );
          })}
        </View>
      )}
    </View>
  );
}

// ─── Errors tab ────────────────────────────────────────────────────────────

// Translated failure-class label — switch, not lookup, so the type checker
// flags any class added to FAILURE_CLASSES without copy (web's rule).
function failureClassLabel(c: (typeof FAILURE_CLASSES)[number], t: TFunction): string {
  switch (c) {
    case "auth":
      return t("errors.class.auth", "Auth");
    case "rate_limit":
      return t("errors.class.rate_limit", "Rate limit");
    case "timeout":
      return t("errors.class.timeout", "Timeout");
    case "provider":
      return t("errors.class.provider", "Provider");
    case "runtime":
      return t("errors.class.runtime", "Runtime");
    case "agent":
      return t("errors.class.agent", "Agent");
    case "other":
      return t("errors.class.other", "Other");
  }
}

interface ErrorsPaneProps {
  loading: boolean;
  errored: boolean;
  onRetry: () => void;
  errorTitle: string;
  retryLabel: string;
  windowLen: number;
  allowedDims: readonly Dim[];
  dim: Dim;
  onDimChange: (d: Dim) => void;
  totals: ReturnType<typeof computeFailureTotals>;
  classRows: ReturnType<typeof aggregateFailureClasses>;
  agentRows: ReturnType<typeof aggregateAgentFailures>;
  dailyErrors: ReturnType<typeof aggregateDailyErrors>;
  weeklyErrors: ReturnType<typeof aggregateWeeklyErrors>;
  agents: Agent[];
  lessThanMinuteLabel: string;
}

function ErrorsPane(props: ErrorsPaneProps) {
  const { t } = useT("usage");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];

  if (props.loading) return <StatsSkeleton />;
  if (props.errored) {
    return (
      <ErrorPane
        title={props.errorTitle}
        retryLabel={props.retryLabel}
        onRetry={props.onRetry}
      />
    );
  }

  // "Agents affected" counts the offender list rows, so the tile and the
  // list cannot disagree (web rule). The list has already anonymized
  // unresolvable agents upstream, keeping the count privacy-safe.
  const affectedAgents = props.agentRows.length;
  const worst = props.agentRows[0];
  const worstName = worst
    ? (props.agents.find((a) => a.id === worst.agentId)?.name ??
      t("errors.other_agents", "Other agents"))
    : null;

  return (
    <>
      <View className="flex-row gap-3">
        <KpiCard
          label={t("errors.kpi_failed_label", "Failed tasks · {{days}}D", { days: props.windowLen })}
          value={String(props.totals.failed)}
          hint={t("errors.kpi_failed_hint", "Of {{total}} runs", { total: props.totals.total })}
        />
        <KpiCard
          label={t("errors.kpi_rate_label", "Failure rate · {{days}}D", { days: props.windowLen })}
          value={formatRatePercent(props.totals.failed, props.totals.total)}
          hint={t("errors.summary", "{{failed}} of {{total}} runs failed · {{rate}}", {
            failed: props.totals.failed,
            total: props.totals.total,
            rate: formatRatePercent(props.totals.failed, props.totals.total),
          })}
        />
      </View>
      <View className="flex-row gap-3">
        <KpiCard
          label={t("errors.kpi_agents_label", "Agents affected · {{days}}D", { days: props.windowLen })}
          value={String(affectedAgents)}
          hint={
            worst && worstName
              ? t("errors.kpi_agents_hint", "Worst {{name}} · {{count}}", {
                  name: worstName,
                  count: worst.failed,
                })
              : undefined
          }
        />
      </View>

      {props.totals.failed === 0 ? (
        <View className="items-center rounded-lg border border-dashed border-border py-10">
          <Ionicons name="checkmark-circle-outline" size={32} color={theme.mutedForeground} />
          <RNText className="mt-2 text-xs text-muted-foreground">
            {t("errors.no_data", "No failed runs in this window.")}
          </RNText>
        </View>
      ) : (
        <>
          {/* Daily/weekly failure trend — granularity binding identical to
              the spend charts (30d+ windows offer weekly, E3). */}
          <View className="rounded-lg border border-border bg-card p-4">
            <View className="mb-3 flex-row items-center justify-between gap-2">
              <RNText className="text-sm font-semibold text-foreground">
                {props.dim === "weekly"
                  ? t("weekly.title_errors", "Weekly errors")
                  : t("daily.title_errors", "Daily errors")}
              </RNText>
              {props.allowedDims.length > 1 ? (
                <View className="flex-row gap-1.5">
                  {props.allowedDims.map((d) => {
                    const active = props.dim === d;
                    const label =
                      d === "daily"
                        ? t("dim.daily", "Daily")
                        : t("dim.weekly", "Weekly");
                    return (
                      <Pressable
                        key={d}
                        onPress={() => props.onDimChange(d)}
                        accessibilityRole="button"
                        accessibilityState={{ selected: active }}
                        className={cn(
                          "rounded-full px-2.5 py-1",
                          active ? "bg-primary" : "bg-muted",
                        )}
                      >
                        <RNText
                          className={cn(
                            "text-[11px] font-medium",
                            active ? "text-primary-foreground" : "text-foreground",
                          )}
                        >
                          {label}
                        </RNText>
                      </Pressable>
                    );
                  })}
                </View>
              ) : null}
            </View>
            <BarChart
              data={(props.dim === "weekly"
                ? props.weeklyErrors.map((d) => ({
                    label: d.label,
                    value: d.failed,
                    partial: d.partial,
                  }))
                : props.dailyErrors.map((d) => ({
                    label: d.label,
                    value: d.failed,
                    partial: false,
                  })))}
              color={theme.destructive}
            />
          </View>

          {/* Failure mix — one 100%-stacked bar plus a legend, reading
              heaviest-first left to right (web ClassComposition). */}
          <View className="rounded-lg border border-border bg-card p-4">
            <RNText className="mb-3 text-sm font-semibold text-foreground">
              {t("errors.mix_title", "Failure mix · {{failed}}", { failed: props.totals.failed })}
            </RNText>
            <View className="h-2 flex-row overflow-hidden rounded-full bg-muted">
              {props.classRows.map((row) => (
                <View
                  key={row.failureClass}
                  className="h-full"
                  style={{
                    width: `${(row.count / props.totals.failed) * 100}%`,
                    backgroundColor: theme.destructive,
                    opacity: FAILURE_CLASS_OPACITY[row.failureClass],
                  }}
                />
              ))}
            </View>
            <View className="mt-3 gap-1.5">
              {props.classRows.map((row) => (
                <View key={row.failureClass} className="flex-row items-center gap-2">
                  <View
                    className="size-2 rounded-[2px]"
                    style={{
                      backgroundColor: theme.destructive,
                      opacity: FAILURE_CLASS_OPACITY[row.failureClass],
                    }}
                  />
                  <RNText className="flex-1 text-xs text-foreground">
                    {failureClassLabel(row.failureClass, t)}
                  </RNText>
                  <RNText className="text-xs tabular-nums text-muted-foreground">
                    {row.count}
                  </RNText>
                </View>
              ))}
            </View>
          </View>

          <OffendersCard
            agentRows={props.agentRows}
            agents={props.agents}
          />
        </>
      )}
    </>
  );
}

function OffendersCard({
  agentRows,
  agents,
}: {
  agentRows: ReturnType<typeof aggregateAgentFailures>;
  agents: Agent[];
}) {
  const { t } = useT("usage");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const [sortBy, setSortBy] = useState<"failed" | "rate">("failed");
  const [showAll, setShowAll] = useState(false);

  const sortedAgents = useMemo(
    () => sortAgentFailures(agentRows, sortBy),
    [agentRows, sortBy],
  );
  // Scale off the leader so a demoted small-sample row can't squash every
  // meaningful bar (web rule).
  const leader = sortedAgents[0];
  const maxValue = leader ? OFFENDER_METRIC[sortBy](leader) : 0;
  const visibleAgents = showAll
    ? sortedAgents
    : sortedAgents.slice(0, TOP_OFFENDER_LIMIT);

  return (
    <View className="rounded-lg border border-border bg-card">
      <View className="flex-row items-center justify-between gap-2 border-b border-border px-4 pt-3 pb-2">
        <RNText className="text-sm font-semibold text-foreground">
          {t("errors.by_agent", "Top offenders")}
        </RNText>
        <View className="flex-row gap-1.5">
          {(["failed", "rate"] as const).map((s) => {
            const active = sortBy === s;
            const label =
              s === "failed"
                ? t("errors.sort_failed", "Failures")
                : t("errors.sort_rate", "Rate");
            return (
              <Pressable
                key={s}
                onPress={() => setSortBy(s)}
                accessibilityRole="button"
                accessibilityState={{ selected: active }}
                className={cn(
                  "rounded-full px-2.5 py-1",
                  active ? "bg-primary" : "bg-muted",
                )}
              >
                <RNText
                  className={cn(
                    "text-[11px] font-medium",
                    active ? "text-primary-foreground" : "text-foreground",
                  )}
                >
                  {label}
                </RNText>
              </Pressable>
            );
          })}
          {sortedAgents.length > TOP_OFFENDER_LIMIT ? (
            <Pressable onPress={() => setShowAll((v) => !v)} accessibilityRole="button">
              <RNText className="text-[11px] text-muted-foreground underline">
                {showAll
                  ? t("errors.show_less", "Show top {{count}}", { count: TOP_OFFENDER_LIMIT })
                  : t("errors.show_all", "Show all {{count}}", { count: sortedAgents.length })}
              </RNText>
            </Pressable>
          ) : null}
        </View>
      </View>
      {sortedAgents.length === 0 ? (
        <RNText className="px-4 py-8 text-center text-xs text-muted-foreground">
          {t("errors.no_data", "No failed runs in this window.")}
        </RNText>
      ) : (
        <View className="px-4 pb-2 pt-1">
          {visibleAgents.map((row) => {
            const name = agents.find((a) => a.id === row.agentId)?.name ?? null;
            const segments = FAILURE_CLASSES.filter((c) => row.classes[c] > 0);
            const value = OFFENDER_METRIC[sortBy](row);
            const pct =
              maxValue > 0 ? Math.min(100, (value / maxValue) * 100) : 0;
            // Below MIN_RATE_SAMPLE runs the rate is arithmetic, not signal
            // — those rows sort last under Rate and render muted (web rule).
            const weakSample = !hasRateSample(row);
            return (
              <View key={row.agentId} className="py-2">
                <View className="flex-row items-center gap-2">
                  <View className="size-6 items-center justify-center rounded-full bg-secondary">
                    <RNText className="text-[10px] font-medium text-foreground">
                      {(name ?? t("errors.other_agents", "Other agents"))
                        .charAt(0)
                        .toUpperCase()}
                    </RNText>
                  </View>
                  <RNText
                    className={cn(
                      "flex-1 text-xs font-medium",
                      name ? "text-foreground" : "italic text-muted-foreground",
                    )}
                    numberOfLines={1}
                  >
                    {name ?? t("errors.other_agents", "Other agents")}
                  </RNText>
                  <RNText className="text-xs font-semibold tabular-nums text-foreground">
                    {sortBy === "failed" ? String(row.failed) : formatRatePercent(row.failed, row.total)}
                  </RNText>
                </View>
                {/* Stacked bar per failure class — a row failing one way is
                    a solid block, five ways is visibly striped (web rule). */}
                <View className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted">
                  <View className="h-full flex-row" style={{ width: `${pct}%` }}>
                    {segments.map((c) => (
                      <View
                        key={c}
                        className="h-full"
                        style={{
                          width: `${(row.classes[c] / row.failed) * 100}%`,
                          backgroundColor: theme.destructive,
                          opacity: FAILURE_CLASS_OPACITY[c],
                        }}
                      />
                    ))}
                  </View>
                </View>
                <View className="mt-1 flex-row justify-between">
                  <RNText className="text-[11px] tabular-nums text-muted-foreground">
                    {row.failed}
                  </RNText>
                  <RNText className="text-[11px] tabular-nums text-muted-foreground">
                    {row.total}
                  </RNText>
                  <RNText
                    className={cn(
                      "text-[11px] tabular-nums",
                      weakSample ? "text-muted-foreground" : "text-foreground",
                    )}
                  >
                    {formatRatePercent(row.failed, row.total)}
                    {weakSample
                      ? ` · ${t("errors.low_sample", "Fewer than {{count}} runs in this window — this rate is not meaningful.", { count: MIN_RATE_SAMPLE })}`
                      : null}
                  </RNText>
                </View>
              </View>
            );
          })}
        </View>
      )}
    </View>
  );
}
