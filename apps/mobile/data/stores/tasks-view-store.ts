/**
 * Mobile-only zustand store for the full-space Tasks tab (RUYI-344) — the
 * bottom "任务" tab that manages every issue in the workspace.
 *
 * Two invariants come from the design spec and are pinned by
 * `tasks-view-store.test.ts`:
 *   - switching quadrant TABs keeps the status multi-select (it belongs to
 *     the "全部" tab and re-appears when the user returns there);
 *   - `clearFilters` (the sheet's 重置) clears filters but NOT the current
 *     TAB and NOT the sort key — same divergence-from-URL convention as the
 *     pre-existing `my-issues-view-store` documented on workspace switch.
 *
 * Field notes:
 *   - `mineRelations` are the three checkboxes backing the personal
 *     relations (assignee/creator/involves). One checked → a single server
 *     predicate; ≥2 checked → the three queries + client union (the same
 *     pattern the old actionable scope used).
 *   - `assigneeRefs`/`includeNoAssignee`/`creatorRefs` mirror the actor
 *     picker (`assignee_filters` / `include_no_assignee` /
 *     `creator_filters` server params, same semantics as web's
 *     ActorSubContent facet).
 *   - `agentRunning` toggles the "智能体执行中" window restriction fed by
 *     the agent-task-snapshot running-issue set.
 *   - `sortBy` has no ascending counterpart in v1 by design (mobile
 *     simplification; both options are descending).
 *
 * Filters persist across cold starts (RUYI-344 增量, extended by RUYI-531):
 * the eight filter dimensions ride in AsyncStorage via zustand persist
 * (`partialize` whitelist below), so reopening the app restores the status
 * multi-select — set AND check order, which the 全部 tab turns into section
 * order. The segment TAB persists too (RUYI-531 验收 1); the sort key stays
 * session-scoped.
 *
 * The memory is per workspace: the blob keeps a `byWs` map of each visited
 * workspace's last filter set beside the active view, so a workspace or
 * server-connection round trip lands back on its own selection instead of a
 * wipe (RUYI-531 — the Owner's 切换空间/切换连接 scenarios; supersedes
 * RUYI-344 item 12's clear-on-switch). A workspace with no memory opens
 * clean. The map is capped (`MAX_WORKSPACE_SLOTS`, oldest slot evicted).
 * AsyncStorage, not SecureStore: nothing here is a credential.
 *
 * `merge` restores a blob only under the workspace it was written for (the
 * active one), and `syncWorkspace(null)` — the unresolved-workspace window
 * while the workspaces list query runs on cold start / server switch — is a
 * no-op so it can no longer wipe what hydration just restored.
 */
import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import AsyncStorage from "@react-native-async-storage/async-storage";
import type { IssuePriority, IssueStatus } from "@multica/core/types";

export type TaskTab = "all" | "open" | "active" | "blocked" | "completed";

export type MineRelation = "assigned" | "created" | "involved";

export type TaskActorRef = { type: "member" | "agent" | "squad"; id: string };

export type TaskSortKey = "updated_at" | "created_at";

/** The eight filter dimensions, as remembered per workspace in `byWs`. */
interface TaskFilterSnapshot {
  statusFilters: IssueStatus[];
  priorityFilters: IssuePriority[];
  mineRelations: Record<MineRelation, boolean>;
  assigneeRefs: TaskActorRef[];
  includeNoAssignee: boolean;
  creatorRefs: TaskActorRef[];
  agentRunning: boolean;
}

/** Remembered workspaces before the oldest slot is evicted. */
const MAX_WORKSPACE_SLOTS = 20;

const EMPTY_MINE_RELATIONS: Record<MineRelation, boolean> = {
  assigned: false,
  created: false,
  involved: false,
};

const TASK_TABS: TaskTab[] = ["all", "open", "active", "blocked", "completed"];

const isTaskTab = (v: unknown): v is TaskTab =>
  typeof v === "string" && (TASK_TABS as string[]).includes(v);

interface TasksViewState {
  tab: TaskTab;
  /** Owning workspace of the current view state (see `syncWorkspace`). */
  wsId: string | null;
  /** Last filter set per visited workspace (see `syncWorkspace`). */
  byWs: Record<string, TaskFilterSnapshot>;
  statusFilters: IssueStatus[];
  priorityFilters: IssuePriority[];
  mineRelations: Record<MineRelation, boolean>;
  assigneeRefs: TaskActorRef[];
  includeNoAssignee: boolean;
  creatorRefs: TaskActorRef[];
  agentRunning: boolean;
  sortBy: TaskSortKey;
  setTab: (tab: TaskTab) => void;
  syncWorkspace: (wsId: string | null) => void;
  toggleStatusFilter: (status: IssueStatus) => void;
  togglePriorityFilter: (priority: IssuePriority) => void;
  toggleMineRelation: (relation: MineRelation) => void;
  setAssigneeRefs: (refs: TaskActorRef[]) => void;
  setIncludeNoAssignee: (include: boolean) => void;
  setCreatorRefs: (refs: TaskActorRef[]) => void;
  toggleAgentRunning: () => void;
  setSortBy: (key: TaskSortKey) => void;
  clearFilters: () => void;
}

/** Shape of the persisted blob (what `partialize` writes to AsyncStorage). */
interface PersistedTasksView {
  tab: TaskTab;
  wsId: string | null;
  byWs: Record<string, TaskFilterSnapshot>;
  statusFilters: IssueStatus[];
  priorityFilters: IssuePriority[];
  mineRelations: Record<MineRelation, boolean>;
  assigneeRefs: TaskActorRef[];
  includeNoAssignee: boolean;
  creatorRefs: TaskActorRef[];
  agentRunning: boolean;
}

const filtersOf = (
  s: Partial<TasksViewState> | Partial<PersistedTasksView> | undefined,
): TaskFilterSnapshot => ({
  statusFilters: s?.statusFilters ?? [],
  priorityFilters: s?.priorityFilters ?? [],
  mineRelations: s?.mineRelations ?? EMPTY_MINE_RELATIONS,
  assigneeRefs: s?.assigneeRefs ?? [],
  includeNoAssignee: s?.includeNoAssignee ?? false,
  creatorRefs: s?.creatorRefs ?? [],
  agentRunning: s?.agentRunning ?? false,
});

export const useTasksViewStore = create<TasksViewState>()(
  persist(
    (set) => ({
      tab: "all",
      wsId: null,
      byWs: {},
      statusFilters: [],
      priorityFilters: [],
      mineRelations: { assigned: false, created: false, involved: false },
      assigneeRefs: [],
      includeNoAssignee: false,
      creatorRefs: [],
      agentRunning: false,
      sortBy: "updated_at",
      // Deliberately does not touch statusFilters: the selection belongs to the
      // 全部 tab and must survive a round-trip through a quadrant tab.
      setTab: (tab) => set({ tab }),
      // Filters are remembered per workspace: the leaving workspace's set is
      // written into `byWs` and the target's own set (empty when it has no
      // memory) becomes the view — a workspace or connection round trip
      // lands back on its own selection instead of a wipe (RUYI-531). TAB
      // and sort survive every switch (保 TAB).
      // `null` means the workspace id is not resolved yet (cold start /
      // server switch, before the workspaces list query lands): keep whatever
      // is loaded/restored — clearing here used to wipe the just-restored
      // memory on every restart (RUYI-531 root cause for the 重启 scenario).
      syncWorkspace: (wsId) => {
        if (wsId === null) return;
        set((state) => {
          if (state.wsId === wsId) return {};
          const byWs = { ...state.byWs };
          if (state.wsId !== null) {
            byWs[state.wsId] = filtersOf(state);
            const keys = Object.keys(byWs);
            if (keys.length > MAX_WORKSPACE_SLOTS) {
              const oldest = keys.find((k) => k !== wsId);
              if (oldest !== undefined) delete byWs[oldest];
            }
          }
          return { wsId, byWs, ...filtersOf(byWs[wsId]) };
        });
      },
      toggleStatusFilter: (status) =>
        set((state) => ({
          statusFilters: state.statusFilters.includes(status)
            ? state.statusFilters.filter((s) => s !== status)
            : [...state.statusFilters, status],
        })),
      togglePriorityFilter: (priority) =>
        set((state) => ({
          priorityFilters: state.priorityFilters.includes(priority)
            ? state.priorityFilters.filter((p) => p !== priority)
            : [...state.priorityFilters, priority],
        })),
      toggleMineRelation: (relation) =>
        set((state) => ({
          mineRelations: {
            ...state.mineRelations,
            [relation]: !state.mineRelations[relation],
          },
        })),
      setAssigneeRefs: (assigneeRefs) => set({ assigneeRefs }),
      setIncludeNoAssignee: (includeNoAssignee) => set({ includeNoAssignee }),
      setCreatorRefs: (creatorRefs) => set({ creatorRefs }),
      toggleAgentRunning: () =>
        set((state) => ({ agentRunning: !state.agentRunning })),
      setSortBy: (sortBy) => set({ sortBy }),
      // 重置 clears every filter but keeps the TAB and the sort choice.
      clearFilters: () =>
        set({
          statusFilters: [],
          priorityFilters: [],
          mineRelations: { assigned: false, created: false, involved: false },
          assigneeRefs: [],
          includeNoAssignee: false,
          creatorRefs: [],
          agentRunning: false,
        }),
    }),
    {
      name: "multica_mobile_tasks_view",
      storage: createJSONStorage(() => AsyncStorage),
      // TAB joins the memory (RUYI-531 验收 1); the sort choice stays
      // session-scoped. The flat fields are the ACTIVE workspace's fresh
      // snapshot — persist rewrites them on every mutation, so they are the
      // authoritative copy for the owning wsId on restore.
      partialize: (s): PersistedTasksView => ({
        tab: s.tab,
        wsId: s.wsId,
        byWs: s.byWs,
        ...filtersOf(s),
      }),
      // Restore-scoping guard: a blob may only come back under the workspace
      // it was written for (its flat fields ARE that workspace's snapshot).
      // Runs at hydration, which on a real device lands AFTER the screen's
      // first syncWorkspace (AsyncStorage reads are bridge round-trips) but
      // must also stay correct when it lands before:
      //   - persisted wsId missing → nothing owns the blob, restore nothing;
      //   - screen already in another workspace → keep its state;
      //   - screen not mounted yet (wsId null) → adopt the blob; a later
      //     syncWorkspace into a different workspace swaps per-workspace as
      //     usual (and syncWorkspace(null) keeps it — see above).
      // A flat-only blob (RUYI-344 era, no byWs/tab) migrates in place.
      merge: (persisted, current) => {
        const p = persisted as Partial<PersistedTasksView> | null;
        if (!p || typeof p.wsId !== "string") return current;
        if (current.wsId !== null && current.wsId !== p.wsId) return current;
        const snap = filtersOf(p);
        const byWs = { ...(p.byWs ?? {}), [p.wsId]: snap };
        return {
          ...current,
          tab: isTaskTab(p.tab) ? p.tab : current.tab,
          wsId: p.wsId,
          byWs,
          ...snap,
        };
      },
    },
  ),
);
