import { useEffect } from "react";
import {
  createMemoryRouter,
  Navigate,
  Outlet,
  useMatches,
} from "react-router-dom";
import type { RouteObject } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { IssueDetailPage } from "./pages/issue-detail-page";
import { ProjectDetailPage } from "./pages/project-detail-page";
import { AutopilotDetailPage } from "./pages/autopilot-detail-page";
import { IssuesPage } from "@multica/views/issues/components";
import { ProjectsPage } from "@multica/views/projects/components";
import { AutopilotsPage } from "@multica/views/autopilots/components";
import { MyIssuesPage } from "@multica/views/my-issues";
import { RuntimesPage } from "@multica/views/runtimes";
import { SkillsPage } from "@multica/views/skills";
import { DaemonRuntimeCard } from "./components/daemon-runtime-card";
import { AgentsPage } from "@multica/views/agents";
import { InboxPage } from "@multica/views/inbox";
import { SettingsPage } from "@multica/views/settings";
import { paths } from "@multica/core/paths";
import { workspaceListOptions } from "@multica/core/workspace/queries";
import { Server } from "lucide-react";
import { DaemonSettingsTab } from "./components/daemon-settings-tab";
import { WorkspaceRouteLayout } from "./components/workspace-route-layout";
import { useWindowOverlayStore } from "./stores/window-overlay-store";

/**
 * Sets document.title from the deepest matched route's handle.title.
 * The tab system observes document.title via MutationObserver.
 * Pages with dynamic titles (e.g. issue detail) override by setting
 * document.title directly via useDocumentTitle().
 */
function TitleSync() {
  const matches = useMatches();
  const title = [...matches]
    .reverse()
    .find((m) => (m.handle as { title?: string })?.title)
    ?.handle as { title?: string } | undefined;

  useEffect(() => {
    if (title?.title) document.title = title.title;
  }, [title?.title]);

  return null;
}

/** Wrapper that renders route children + TitleSync */
function PageShell() {
  return (
    <>
      <TitleSync />
      <Outlet />
    </>
  );
}

/**
 * Root index route: resolves the URL-less `/` path to a concrete destination.
 *
 * Runs both on first login (App.tsx seeded the cache) and on app reopen
 * (AuthInitializer seeded the cache). Reading from React Query avoids
 * duplicate fetches across tabs — each tab's memory router hits this
 * component independently but the query is deduped.
 *
 * Sends users with workspaces to the first workspace's issues page.
 * Users with zero workspaces get the window-level new-workspace overlay —
 * desktop treats pre-workspace flows as application state, not tab routes,
 * so there's no URL to navigate to.
 */
function IndexRedirect() {
  const { data: wsList, isFetched } = useQuery(workspaceListOptions());

  // Bidirectional overlay lifecycle: open the new-workspace overlay when
  // the user has zero workspaces AND no other overlay is already showing,
  // and close it when the list becomes non-empty (e.g. a realtime workspace
  // event arrived while the overlay was open on a different code path).
  // Only touches the new-workspace type — an active invite overlay is the
  // user's in-flight task and must not be interrupted.
  useEffect(() => {
    if (!isFetched) return;
    const { overlay, open, close } = useWindowOverlayStore.getState();
    const isEmpty = !wsList || wsList.length === 0;
    if (isEmpty) {
      if (!overlay) open({ type: "new-workspace" });
    } else if (overlay?.type === "new-workspace") {
      close();
    }
  }, [isFetched, wsList]);

  // Wait for the query to settle so we don't flash the empty-state overlay
  // on the initial render before the seeded/fetched data arrives.
  if (!isFetched) return null;

  const firstWorkspace = wsList?.[0];
  if (firstWorkspace) {
    return <Navigate to={paths.workspace(firstWorkspace.slug).issues()} replace />;
  }

  // Zero workspaces — overlay is opened via the effect above. Tab stays on
  // `/`; the overlay covers the window. When the user creates a workspace,
  // onSuccess navigates to the new workspace path, closing the overlay.
  return null;
}

/**
 * Route definitions shared by all tabs.
 *
 * Only workspace-scoped ("session") routes live here. Pre-workspace
 * transitions (create workspace, accept invite) are NOT routes on desktop —
 * they render as a window-level overlay via WindowOverlay, dispatched by
 * the navigation adapter's transition-path interception. See
 * `platform/navigation.tsx` and `stores/window-overlay-store.ts`.
 */
export const appRoutes: RouteObject[] = [
  {
    element: <PageShell />,
    children: [
      // Top-level index: no slug yet. `IndexRedirect` reads the workspace
      // list from React Query cache (seeded by AuthInitializer on reopen
      // or App.tsx on deep-link login) and bounces to the first
      // workspace's issues page — or opens the new-workspace overlay if
      // the user has none.
      { index: true, element: <IndexRedirect /> },
      {
        path: ":workspaceSlug",
        element: <WorkspaceRouteLayout />,
        children: [
          { index: true, element: <Navigate to="issues" replace /> },
          { path: "issues", element: <IssuesPage />, handle: { title: "Issues" } },
          {
            path: "issues/:id",
            element: <IssueDetailPage />,
            handle: { title: "Issue" },
          },
          {
            path: "projects",
            element: <ProjectsPage />,
            handle: { title: "Projects" },
          },
          {
            path: "projects/:id",
            element: <ProjectDetailPage />,
            handle: { title: "Project" },
          },
          {
            path: "autopilots",
            element: <AutopilotsPage />,
            handle: { title: "Autopilot" },
          },
          {
            path: "autopilots/:id",
            element: <AutopilotDetailPage />,
            handle: { title: "Autopilot" },
          },
          {
            path: "my-issues",
            element: <MyIssuesPage />,
            handle: { title: "My Issues" },
          },
          {
            path: "runtimes",
            element: <RuntimesPage topSlot={<DaemonRuntimeCard />} />,
            handle: { title: "Runtimes" },
          },
          { path: "skills", element: <SkillsPage />, handle: { title: "Skills" } },
          { path: "agents", element: <AgentsPage />, handle: { title: "Agents" } },
          { path: "inbox", element: <InboxPage />, handle: { title: "Inbox" } },
          {
            path: "settings",
            element: (
              <SettingsPage
                extraAccountTabs={[
                  {
                    value: "daemon",
                    label: "Daemon",
                    icon: Server,
                    content: <DaemonSettingsTab />,
                  },
                ]}
              />
            ),
            handle: { title: "Settings" },
          },
        ],
      },
    ],
  },
];

/** Create an independent memory router for a tab. */
export function createTabRouter(initialPath: string) {
  return createMemoryRouter(appRoutes, {
    initialEntries: [initialPath],
  });
}
