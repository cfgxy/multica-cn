/**
 * Mobile-only zustand store for the My Issues view (scope + status/priority
 * filters). Mirrors the field shape of web's
 * `packages/core/issues/stores/my-issues-view-store.ts` so the same filter
 * input produces the same visible issue set on both clients (the "same N
 * rule" in apps/mobile/CLAUDE.md). Mobile cannot import core's runtime, so
 * this is re-implemented locally.
 *
 * Empty filter array = "show all" (matches web's predicate semantics in
 * packages/views/issues/utils/filter.ts).
 *
 * No persist middleware in v1 — matches the existing mobile pattern
 * (auth-store / workspace-store use SecureStore manually for the few values
 * that need restart survival; everything else is in-memory). v2 can add
 * AsyncStorage persistence if cross-restart filter survival is desired.
 */
import { create } from "zustand";
import type { IssuePriority, IssueStatus } from "@multica/core/types";
import type { MyIssuesScope } from "@/data/queries/issue-keys";

interface MyIssuesViewState {
  scope: MyIssuesScope;
  statusFilters: IssueStatus[];
  priorityFilters: IssuePriority[];
  setScope: (scope: MyIssuesScope) => void;
  toggleStatusFilter: (status: IssueStatus) => void;
  togglePriorityFilter: (priority: IssuePriority) => void;
  clearFilters: () => void;
}

export const useMyIssuesViewStore = create<MyIssuesViewState>((set) => ({
  // Mobile default is the merged 待我推进 view, not web's "assigned": in
  // squad-driven workspaces `assignee_id = me` is legitimately empty most
  // days (issues carry the squad's UUID), which read as "the tab is broken"
  // (RUYI-76 ①). The actionable union answers "what waits on me" instead.
  scope: "actionable",
  statusFilters: [],
  priorityFilters: [],
  setScope: (scope) => set({ scope }),
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
  clearFilters: () => set({ statusFilters: [], priorityFilters: [] }),
}));
