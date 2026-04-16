import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { arrayMove } from "@dnd-kit/sortable";
import { createPersistStorage, defaultStorage } from "@multica/core/platform";
import { createSafeId } from "@multica/core/utils";
import { isGlobalPath } from "@multica/core/paths";
import type { DataRouter } from "react-router-dom";
import { createTabRouter } from "../routes";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface Tab {
  id: string;
  path: string;
  title: string;
  icon: string;
  router: DataRouter;
  historyIndex: number;
  historyLength: number;
}

interface TabStore {
  tabs: Tab[];
  activeTabId: string;

  /** Open a background tab. Deduplicates by path. Returns the tab id. */
  openTab: (path: string, title: string, icon: string) => string;
  /** Always create a new tab (no dedup). Returns the tab id. */
  addTab: (path: string, title: string, icon: string) => string;
  /** Close a tab. Disposes router. */
  closeTab: (tabId: string) => void;
  /** Switch to a tab by id. */
  setActiveTab: (tabId: string) => void;
  /** Update a tab's metadata (path, title, icon — partial). */
  updateTab: (tabId: string, patch: Partial<Pick<Tab, "path" | "title" | "icon">>) => void;
  /** Update a tab's history tracking. */
  updateTabHistory: (tabId: string, historyIndex: number, historyLength: number) => void;
  /** Reorder tabs by moving one from fromIndex to toIndex. Preserves router/history. */
  moveTab: (fromIndex: number, toIndex: number) => void;
  /**
   * Reset any tab whose first path segment references a workspace slug the
   * current user doesn't have access to. Called after login + workspace list
   * is populated (and on every subsequent list change, e.g. realtime
   * workspace:deleted). Stale tabs get reset to `/` so IndexRedirect picks
   * a valid workspace; tabs on global paths (/login, /new-workspace, etc.)
   * are untouched.
   */
  validateWorkspaceSlugs: (validSlugs: Set<string>) => void;
}

// ---------------------------------------------------------------------------
// Route → icon mapping (title comes from document.title, not from here)
// ---------------------------------------------------------------------------

const ROUTE_ICONS: Record<string, string> = {
  inbox: "Inbox",
  "my-issues": "CircleUser",
  issues: "ListTodo",
  projects: "FolderKanban",
  autopilots: "ListTodo",
  agents: "Bot",
  runtimes: "Monitor",
  skills: "BookOpenText",
  settings: "Settings",
};

/**
 * Resolve a route icon from a pathname. Title is NOT determined here — it
 * comes from document.title.
 *
 * Path shape after the workspace URL refactor:
 *  - workspace-scoped: `/{workspaceSlug}/{route}/...` → use segment index 1
 *  - global (new-workspace/invite/auth/login): `/{route}/...` → use segment index 0
 *
 * `isGlobalPath` is the single source of truth for which prefixes are global.
 */
export function resolveRouteIcon(pathname: string): string {
  const segments = pathname.split("/").filter(Boolean);
  const routeSegment = isGlobalPath(pathname)
    ? (segments[0] ?? "")
    : (segments[1] ?? "");
  return ROUTE_ICONS[routeSegment] ?? "ListTodo";
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

/**
 * Sentinel path for new tabs with no explicit destination. The tab store is
 * workspace-implicit — it doesn't know which workspace is active, so it can't
 * build a `/:slug/issues` path itself. Instead we hand off to the router: `/`
 * matches the top-level index route, which redirects to the workspace default
 * (slug-aware redirect lives in routes.tsx / App.tsx).
 *
 * `title` and `icon` on the placeholder tab get overwritten by
 * useTabRouterSync + useActiveTitleSync once the redirect resolves.
 */
const DEFAULT_PATH = "/";

function createId(): string {
  return createSafeId();
}

function makeTab(path: string, title: string, icon: string): Tab {
  return {
    id: createId(),
    path,
    title,
    icon,
    router: createTabRouter(path),
    historyIndex: 0,
    historyLength: 1,
  };
}

const initialTab = makeTab(DEFAULT_PATH, "Issues", resolveRouteIcon(DEFAULT_PATH));

export const useTabStore = create<TabStore>()(
  persist(
    (set, get) => ({
  tabs: [initialTab],
  activeTabId: initialTab.id,

  openTab(path, title, icon) {
    const { tabs } = get();
    const existing = tabs.find((t) => t.path === path);
    if (existing) return existing.id;

    const tab = makeTab(path, title, icon);
    set({ tabs: [...tabs, tab] });
    return tab.id;
  },

  addTab(path, title, icon) {
    const tab = makeTab(path, title, icon);
    set((s) => ({ tabs: [...s.tabs, tab] }));
    return tab.id;
  },

  closeTab(tabId) {
    const { tabs, activeTabId } = get();

    const closingTab = tabs.find((t) => t.id === tabId);

    // Never close the last tab — replace with default
    if (tabs.length === 1) {
      closingTab?.router.dispose();
      const fresh = makeTab(DEFAULT_PATH, "Issues", resolveRouteIcon(DEFAULT_PATH));
      set({ tabs: [fresh], activeTabId: fresh.id });
      return;
    }

    const idx = tabs.findIndex((t) => t.id === tabId);
    if (idx === -1) return;

    closingTab?.router.dispose();
    const next = tabs.filter((t) => t.id !== tabId);

    if (tabId === activeTabId) {
      const newActive = next[Math.min(idx, next.length - 1)];
      set({ tabs: next, activeTabId: newActive.id });
    } else {
      set({ tabs: next });
    }
  },

  setActiveTab(tabId) {
    set({ activeTabId: tabId });
  },

  updateTab(tabId, patch) {
    set((s) => ({
      tabs: s.tabs.map((t) =>
        t.id === tabId ? { ...t, ...patch } : t,
      ),
    }));
  },

  updateTabHistory(tabId, historyIndex, historyLength) {
    set((s) => ({
      tabs: s.tabs.map((t) =>
        t.id === tabId ? { ...t, historyIndex, historyLength } : t,
      ),
    }));
  },

  moveTab(fromIndex, toIndex) {
    if (fromIndex === toIndex) return;
    set((s) => ({ tabs: arrayMove(s.tabs, fromIndex, toIndex) }));
  },

  validateWorkspaceSlugs(validSlugs) {
    const { tabs } = get();
    let changed = false;
    const nextTabs = tabs.map((t) => {
      // Skip tabs on non-workspace-scoped paths — nothing to validate.
      if (t.path === "/" || isGlobalPath(t.path)) return t;

      const firstSegment = t.path.split("/").filter(Boolean)[0] ?? "";
      if (validSlugs.has(firstSegment)) return t;

      // Stale slug: dispose the old router and replace with a fresh one
      // pointing at `/`. IndexRedirect will send the tab to a valid
      // workspace (or /new-workspace if the user now has none).
      changed = true;
      t.router.dispose();
      return {
        ...t,
        path: DEFAULT_PATH,
        title: "Issues",
        icon: resolveRouteIcon(DEFAULT_PATH),
        router: createTabRouter(DEFAULT_PATH),
        historyIndex: 0,
        historyLength: 1,
      };
    });

    if (!changed) return;
    set({ tabs: nextTabs });
  },
    }),
    {
      name: "multica_tabs",
      version: 1,
      storage: createJSONStorage(() => createPersistStorage(defaultStorage)),
      partialize: (state) => ({
        tabs: state.tabs.map(
          ({ router, historyIndex, historyLength, ...rest }) => rest,
        ),
        activeTabId: state.activeTabId,
      }),
      merge: (persistedState, currentState) => {
        const persisted = persistedState as
          | Pick<TabStore, "tabs" | "activeTabId">
          | undefined;
        if (!persisted?.tabs?.length) return currentState;

        const tabs: Tab[] = persisted.tabs.map((tab) => {
          // Migration: pre-refactor tab paths like "/issues/abc" lack a
          // workspace slug prefix. These would 404 in the new router.
          // Reset to "/" so IndexRedirect picks the right workspace.
          let path = tab.path;
          if (path !== "/" && !isGlobalPath(path)) {
            const segments = path.split("/").filter(Boolean);
            const firstSegment = segments[0] ?? "";
            // If the first segment IS a known route name (e.g. "issues",
            // "projects"), it's an old-format path missing the slug prefix.
            if (ROUTE_ICONS[firstSegment]) {
              path = "/";
            }
          }
          return {
            ...tab,
            path,
            router: createTabRouter(path),
            historyIndex: 0,
            historyLength: 1,
          };
        });

        // Validate activeTabId — fall back to first tab if stale
        const activeTabId = tabs.some((t) => t.id === persisted.activeTabId)
          ? persisted.activeTabId
          : tabs[0].id;

        return { ...currentState, tabs, activeTabId };
      },
    },
  ),
);
