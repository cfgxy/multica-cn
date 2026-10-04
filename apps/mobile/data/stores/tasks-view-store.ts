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
 * No persist middleware — session-scoped, matching the other view stores.
 * Workspace switches clear the filters via `syncWorkspace`: the owning wsId
 * lives in the state because the Tasks screen remounts per workspace with
 * the new id already in props, which a ref-guard hook cannot see (RUYI-344
 * item 12; TAB and sort survive the switch).
 */
import { create } from "zustand";
import type { IssuePriority, IssueStatus } from "@multica/core/types";

export type TaskTab = "all" | "open" | "active" | "blocked" | "completed";

export type MineRelation = "assigned" | "created" | "involved";

export type TaskActorRef = { type: "member" | "agent" | "squad"; id: string };

export type TaskSortKey = "updated_at" | "created_at";

interface TasksViewState {
  tab: TaskTab;
  /** Owning workspace of the current view state (see `syncWorkspace`). */
  wsId: string | null;
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

export const useTasksViewStore = create<TasksViewState>((set) => ({
  tab: "all",
  wsId: null,
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
  // Filters are workspace-scoped: the owning wsId rides in the state, so a
  // real switch is detected even when this runs on a freshly remounted
  // screen whose props already carry the new id. Same-workspace syncs are
  // no-ops. A switch clears every filter; TAB and sort survive (保 TAB).
  syncWorkspace: (wsId) =>
    set((state) =>
      state.wsId === wsId
        ? {}
        : {
            wsId,
            statusFilters: [],
            priorityFilters: [],
            mineRelations: { assigned: false, created: false, involved: false },
            assigneeRefs: [],
            includeNoAssignee: false,
            creatorRefs: [],
            agentRunning: false,
          },
    ),
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
}));
