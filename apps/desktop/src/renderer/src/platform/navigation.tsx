import { useEffect, useMemo, useState } from "react";
import type { DataRouter } from "react-router-dom";
import {
  NavigationProvider,
  type NavigationAdapter,
} from "@multica/views/navigation";
import { useAuthStore } from "@multica/core/auth";
import { useTabStore, resolveRouteIcon } from "@/stores/tab-store";
import { useWindowOverlayStore } from "@/stores/window-overlay-store";

const ROOT_PATH = "/";

// Public web app URL — injected at build time via .env.production. Falls
// back to the production host for dev builds so "Copy link" yields a URL
// that actually points somewhere a teammate can open.
const APP_URL = import.meta.env.VITE_APP_URL || "https://multica.ai";

/**
 * Intercept navigation to "transition" paths — pre-workspace flows that on
 * desktop are rendered as a window-level overlay instead of a tab route.
 * Returns `true` if the navigation was handled (caller should NOT proceed
 * with its own router navigation).
 *
 * Side effect: when opening the new-workspace overlay, the tab router is
 * ALSO reset to "/". Rationale — the only way a push lands on
 * /workspaces/new is that the workspace context is gone (fresh install,
 * delete-last, leave-last). Leaving the tab parked on a workspace-scoped
 * path like /acme/settings keeps those components mounted under the
 * overlay; the next render after the list cache updates would then throw
 * (useWorkspaceId, useCurrentWorkspace, etc) because the slug no longer
 * resolves. Resetting to "/" unmounts them synchronously. For invite
 * overlay we intentionally do NOT reset — invite is orthogonal and the
 * user's workspace context may still be valid.
 *
 * Navigations to any other path implicitly close an open overlay, so the
 * overlay's lifetime is tied to the user staying in a transition state.
 */
function tryRouteToOverlay(path: string, router?: DataRouter): boolean {
  const overlay = useWindowOverlayStore.getState();
  if (path === "/workspaces/new") {
    overlay.open({ type: "new-workspace" });
    if (router && router.state.location.pathname !== ROOT_PATH) {
      router.navigate(ROOT_PATH, { replace: true });
    }
    return true;
  }
  if (path.startsWith("/invite/")) {
    let id = "";
    try {
      id = decodeURIComponent(path.slice("/invite/".length));
    } catch {
      // Malformed percent-encoding — treat as no-op so the caller's push
      // doesn't attempt to navigate into the (now-removed) invite route
      // and leave the tab stuck on a 404-shaped path.
      return true;
    }
    if (id) {
      overlay.open({ type: "invite", invitationId: id });
      return true;
    }
  }
  // Any other navigation cancels a live overlay.
  if (overlay.overlay) overlay.close();
  return false;
}

/**
 * Root-level navigation provider for components outside the per-tab RouterProviders
 * (sidebar, search dialog, modals, WindowOverlay contents, etc.).
 *
 * Reads from the active tab's memory router via router.subscribe().
 * Does NOT use any react-router hooks — it's above all RouterProviders.
 */
export function DesktopNavigationProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  const activeTab = useTabStore((s) => s.tabs.find((t) => t.id === s.activeTabId));
  const [pathname, setPathname] = useState(activeTab?.path ?? "/");

  // Subscribe to the active tab's router for pathname updates
  useEffect(() => {
    if (!activeTab) return;
    setPathname(activeTab.router.state.location.pathname);
    return activeTab.router.subscribe((state) => {
      setPathname(state.location.pathname);
    });
  }, [activeTab?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  const adapter: NavigationAdapter = useMemo(
    () => ({
      push: (path: string) => {
        if (path === "/login") {
          // DashboardGuard token expired — force back to login screen
          useAuthStore.getState().logout();
          return;
        }
        const tab = useTabStore.getState().tabs.find(
          (t) => t.id === useTabStore.getState().activeTabId,
        );
        if (tryRouteToOverlay(path, tab?.router)) return;
        tab?.router.navigate(path);
      },
      replace: (path: string) => {
        const tab = useTabStore.getState().tabs.find(
          (t) => t.id === useTabStore.getState().activeTabId,
        );
        if (tryRouteToOverlay(path, tab?.router)) return;
        tab?.router.navigate(path, { replace: true });
      },
      back: () => {
        const tab = useTabStore.getState().tabs.find(
          (t) => t.id === useTabStore.getState().activeTabId,
        );
        tab?.router.navigate(-1);
      },
      pathname,
      searchParams: new URLSearchParams(),
      openInNewTab: (path: string, title?: string) => {
        const icon = resolveRouteIcon(path);
        const store = useTabStore.getState();
        const tabId = store.openTab(path, title ?? path, icon);
        store.setActiveTab(tabId);
      },
      getShareableUrl: (path: string) => `${APP_URL}${path}`,
    }),
    [pathname],
  );

  return <NavigationProvider value={adapter}>{children}</NavigationProvider>;
}

/**
 * Per-tab navigation provider rendered inside each tab's Activity wrapper.
 * Subscribes to the tab's own router for up-to-date pathname.
 *
 * This is what @multica/views page components read via useNavigation().
 */
export function TabNavigationProvider({
  router,
  children,
}: {
  router: DataRouter;
  children: React.ReactNode;
}) {
  const [location, setLocation] = useState(router.state.location);

  useEffect(() => {
    setLocation(router.state.location);
    return router.subscribe((state) => {
      setLocation(state.location);
    });
  }, [router]);

  const adapter: NavigationAdapter = useMemo(
    () => ({
      push: (path: string) => {
        if (tryRouteToOverlay(path, router)) return;
        router.navigate(path);
      },
      replace: (path: string) => {
        if (tryRouteToOverlay(path, router)) return;
        router.navigate(path, { replace: true });
      },
      back: () => router.navigate(-1),
      pathname: location.pathname,
      searchParams: new URLSearchParams(location.search),
      openInNewTab: (path: string, title?: string) => {
        const icon = resolveRouteIcon(path);
        const store = useTabStore.getState();
        const newTabId = store.openTab(path, title ?? path, icon);
        store.setActiveTab(newTabId);
      },
      getShareableUrl: (path: string) => `${APP_URL}${path}`,
    }),
    [router, location],
  );

  return <NavigationProvider value={adapter}>{children}</NavigationProvider>;
}
