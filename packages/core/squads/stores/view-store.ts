"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import {
  createWorkspaceAwareStorage,
  registerForWorkspaceRehydration,
} from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";

// View preferences for the squads list page: scope, sort, column visibility.
// Persisted per workspace, per user/device. No filters (the set is tiny);
// no search (scope-bearing list). Mirrors the agents/skills view stores.

// Scope is the ownership lens (creator-based). No "archived" scope: the
// list endpoint hard-filters archived squads and there is no restore
// endpoint, so archived squads can't be surfaced or managed.
export type SquadsScope = "mine" | "all";

export const SQUAD_SCOPES: SquadsScope[] = ["mine", "all"];

export type SquadSortField = "name" | "members" | "created";

export type SquadSortDirection = "asc" | "desc";

/** Per-field direction applied when the user switches TO that field. */
export const SQUAD_SORT_DEFAULT_DIRECTION: Record<
  SquadSortField,
  SquadSortDirection
> = {
  name: "asc",
  members: "desc",
  created: "desc",
};

// User-hideable columns. Name and leader (the squad's defining relationship)
// are always visible.
export type SquadColumnKey = "members" | "creator" | "created";

/** Creator and created are opt-in: hidden until the user enables them. */
export const SQUAD_DEFAULT_HIDDEN_COLUMNS: SquadColumnKey[] = [
  "creator",
  "created",
];

export interface SquadsViewState {
  scope: SquadsScope;
  sortField: SquadSortField;
  sortDirection: SquadSortDirection;
  hiddenColumns: SquadColumnKey[];
  setScope: (scope: SquadsScope) => void;
  /** Header click: toggles direction on the active field, otherwise switches
   *  to the field with its default direction. */
  toggleSort: (field: SquadSortField) => void;
  /** Display panel select: switches field (default direction), no toggle. */
  setSortField: (field: SquadSortField) => void;
  setSortDirection: (direction: SquadSortDirection) => void;
  toggleColumn: (key: SquadColumnKey) => void;
}

const DEFAULTS = {
  scope: "mine" as SquadsScope,
  sortField: "name" as SquadSortField,
  sortDirection: SQUAD_SORT_DEFAULT_DIRECTION.name,
  hiddenColumns: SQUAD_DEFAULT_HIDDEN_COLUMNS,
};

export const useSquadsViewStore = create<SquadsViewState>()(
  persist(
    (set) => ({
      ...DEFAULTS,
      setScope: (scope) => set({ scope }),
      toggleSort: (field) =>
        set((state) =>
          state.sortField === field
            ? {
                sortDirection: state.sortDirection === "asc" ? "desc" : "asc",
              }
            : {
                sortField: field,
                sortDirection: SQUAD_SORT_DEFAULT_DIRECTION[field],
              },
        ),
      setSortField: (field) =>
        set((state) =>
          state.sortField === field
            ? {}
            : {
                sortField: field,
                sortDirection: SQUAD_SORT_DEFAULT_DIRECTION[field],
              },
        ),
      setSortDirection: (direction) => set({ sortDirection: direction }),
      toggleColumn: (key) =>
        set((state) => ({
          hiddenColumns: state.hiddenColumns.includes(key)
            ? state.hiddenColumns.filter((k) => k !== key)
            : [...state.hiddenColumns, key],
        })),
    }),
    {
      name: "multica_squads_view",
      storage: createJSONStorage(() =>
        createWorkspaceAwareStorage(defaultStorage),
      ),
      partialize: (state) => ({
        scope: state.scope,
        sortField: state.sortField,
        sortDirection: state.sortDirection,
        hiddenColumns: state.hiddenColumns,
      }),
      // On rehydrate, if the new workspace has no persisted value, reset to
      // the defaults instead of leaking the previous workspace's state.
      merge: (persisted, current) => {
        if (!persisted) return { ...current, ...DEFAULTS };
        return { ...current, ...(persisted as Partial<SquadsViewState>) };
      },
    },
  ),
);

registerForWorkspaceRehydration(() => useSquadsViewStore.persist.rehydrate());
