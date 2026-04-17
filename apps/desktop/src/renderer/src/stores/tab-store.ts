import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { arrayMove } from "@dnd-kit/sortable";
import { createPersistStorage, defaultStorage } from "@multica/core/platform";
import { createSafeId } from "@multica/core/utils";
import { isReservedSlug } from "@multica/core/paths";
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
   * a valid workspace (or opens the new-workspace overlay if the user now
   * has none).
   */
  validateWorkspaceSlugs: (validSlugs: Set<string>) => void;
  /**
   * Wipe all in-memory tabs and reseed with a single default tab at `/`.
   * Called on logout so the next user doesn't inherit the previous user's
   * tab layout — Zustand persist only writes to localStorage; clearing
   * localStorage alone leaves the live store untouched until app restart.
   */
  reset: () => void;
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
 * Resolve a route icon from a pathname.
 *
 * Tab paths are always workspace-scoped on desktop: `/{slug}/{route}/...`,
 * so the route segment lives at index 1. Pre-workspace flows (create,
 * invite) are rendered by the window overlay, never as tabs.
 *
 * Title is NOT determined here — it comes from document.title.
 */
export function resolveRouteIcon(pathname: string): string {
  const segments = pathname.split("/").filter(Boolean);
  return ROUTE_ICONS[segments[1] ?? ""] ?? "ListTodo";
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

/**
 * Defensive: catch paths that don't belong in the tab store.
 *
 * Two kinds of rejects:
 *  1. **Transition paths** (`/workspaces/new`, `/invite/...`). These are
 *     pre-workspace flows rendered by the window overlay on desktop, not
 *     tab routes. They must never persist as tab state — otherwise the user
 *     reopens the app into a stale form/invite. Navigation to these paths
 *     is intercepted by the navigation adapter, so they shouldn't reach
 *     tab-store in normal flow; this guard catches older persisted state.
 *  2. **Malformed workspace-scoped paths** like a stray `/issues/abc` that
 *     was constructed without the workspace prefix. Router would interpret
 *     `issues` as a workspace slug → NoAccessPage.
 *
 * Both rewrite to `/` so `IndexRedirect` picks a valid destination (or
 * opens the new-workspace overlay if the user has none).
 */
export function sanitizeTabPath(path: string): string {
  if (path === DEFAULT_PATH) return path;
  const firstSegment = path.split("/").filter(Boolean)[0] ?? "";
  if (isReservedSlug(firstSegment)) {
    // Don't log for known transition paths — these are legitimate inputs
    // at the interception boundary (older persisted state or stale callers).
    // Log only for truly buggy cases where a view forgot the workspace prefix.
    const isTransition = path === "/workspaces/new" || path.startsWith("/invite/");
    if (!isTransition) {
      // eslint-disable-next-line no-console
      console.warn(
        `[tab-store] tab path "${path}" starts with reserved slug "${firstSegment}" — ` +
          `caller likely forgot the workspace prefix. Falling back to "/".`,
      );
    }
    return DEFAULT_PATH;
  }
  return path;
}

function makeTab(path: string, title: string, icon: string): Tab {
  const safePath = sanitizeTabPath(path);
  return {
    id: createId(),
    path: safePath,
    title,
    icon,
    router: createTabRouter(safePath),
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
      // Root sentinel doesn't carry a slug — nothing to validate.
      if (t.path === "/") return t;

      const firstSegment = t.path.split("/").filter(Boolean)[0] ?? "";
      if (validSlugs.has(firstSegment)) return t;

      // Stale slug: dispose the old router and replace with a fresh one
      // pointing at `/`. IndexRedirect will send the tab to a valid
      // workspace, or open the new-workspace overlay if none remain.
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

  reset() {
    const { tabs } = get();
    for (const t of tabs) t.router.dispose();
    const fresh = makeTab(DEFAULT_PATH, "Issues", resolveRouteIcon(DEFAULT_PATH));
    set({ tabs: [fresh], activeTabId: fresh.id });
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
          // Sanitize persisted paths against reserved-slug rules. Catches
          // both pre-refactor paths like "/issues/abc" (missing workspace
          // slug) and any other malformed paths that slipped past the
          // write-time guard. The defense across makeTab + merge + runtime
          // validate ensures stale or malformed paths never reach the
          // router.
          const path = sanitizeTabPath(tab.path);
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
