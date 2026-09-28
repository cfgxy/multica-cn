// @vitest-environment jsdom

// Wiring only. The label/linkability matrix is canonically covered in
// ../../common/github-url.test.ts — do not re-run it through a DOM mount.
// What this file protects is the row using those helpers at all: a GitHub row
// keeps its owner/repo anchor, a GitLab subgroup row keeps every namespace
// level, and an ssh clone URL renders as text instead of a dead anchor.

import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";

const resource = (id: string, url: string) => ({
  id,
  project_id: "p1",
  workspace_id: "workspace-1",
  resource_type: "github_repo",
  resource_ref: { url },
  label: null,
  position: 0,
  created_at: "2026-09-28T00:00:00Z",
  created_by: "u1",
});

const RESOURCES = [
  resource("res-github", "https://github.com/octocat/hello-world.git"),
  resource("res-gitlab-https", "https://gitlab.test/group/sub/repo.git"),
  resource("res-gitlab-ssh", "git@gitlab.test:group/sub/other-repo.git"),
];

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: unknown[] }) => {
    const key = options?.queryKey?.[0];
    if (key === "project-resources") return { data: RESOURCES };
    return { data: [] };
  },
  queryOptions: (options: unknown) => options,
}));

vi.mock("@multica/core/projects", () => ({
  projectResourcesOptions: () => ({ queryKey: ["project-resources"], queryFn: vi.fn() }),
  useCreateProjectResource: () => ({ mutateAsync: vi.fn() }),
  useUpdateProjectResource: () => ({ mutateAsync: vi.fn() }),
  useDeleteProjectResource: () => ({ mutateAsync: vi.fn() }),
}));

vi.mock("@multica/core/config", () => ({
  useConfigStore: (selector: (state: { localWorktreeSupported: boolean }) => unknown) =>
    selector({ localWorktreeSupported: true }),
}));

vi.mock("@multica/core/runtimes", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"], queryFn: vi.fn() }),
  runtimeAdvertisesLocalWorktree: () => true,
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "workspace-1", slug: "ws", repos: [] }),
}));
vi.mock("../../platform/local-directory", () => ({
  isDesktopShell: () => false,
  pickDirectory: vi.fn(),
  validateLocalDirectory: vi.fn(),
}));
vi.mock("../../platform/use-local-daemon-status", () => ({
  useLocalDaemonStatus: () => ({ daemonId: null, deviceName: null, running: false }),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { ProjectResourcesSection } from "./project-resources-section";

describe("ProjectResourcesSection — generic git repo rows", () => {
  it("links http(s) repos and keeps the full GitLab namespace in the label", () => {
    renderWithI18n(<ProjectResourcesSection projectId="p1" />);

    const github = screen.getByText("octocat/hello-world");
    expect(github.closest("a")?.getAttribute("href")).toBe(
      "https://github.com/octocat/hello-world.git",
    );

    const gitlab = screen.getByText("gitlab.test/group/sub/repo");
    expect(gitlab.closest("a")?.getAttribute("href")).toBe(
      "https://gitlab.test/group/sub/repo.git",
    );
  });

  it("renders an ssh clone URL as text, not as an anchor", () => {
    renderWithI18n(<ProjectResourcesSection projectId="p1" />);

    const ssh = screen.getByText("gitlab.test/group/sub/other-repo");
    expect(ssh.closest("a")).toBeNull();
    // The raw clone URL must never end up in an href anywhere on the row.
    for (const anchor of Array.from(document.querySelectorAll("a"))) {
      expect(anchor.getAttribute("href")).not.toContain("git@gitlab.test");
    }
  });
});
