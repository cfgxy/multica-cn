import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockUpdateWorkspace = vi.hoisted(() => vi.fn());
const mockGetGitHubConnectURL = vi.hoisted(() => vi.fn());
const mockFetchNextPage = vi.hoisted(() => vi.fn());
const mockGitLabFetchNextPage = vi.hoisted(() => vi.fn());
const mockNavReplace = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const workspaceRef = vi.hoisted(() => ({
  current: {
    id: "workspace-1",
    name: "Test Workspace",
    slug: "test-workspace",
    repos: [{ url: "https://github.com/multica-ai/multica" }] as {
      url: string;
      description?: string;
    }[],
  },
}));
const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as "owner" | "admin" | "member" }],
}));
const githubRef = vi.hoisted(() => ({
  current: {
    installations: [] as { id: string; account_login: string }[],
    configured: true,
    repository_browse_configured: true,
    can_manage: true,
  },
}));
const githubQueryStateRef = vi.hoisted(() => ({
  current: {
    isPending: false,
    isFetching: false,
  },
}));
const githubRepositoriesRef = vi.hoisted(() => ({
  current: [] as {
    id: number;
    full_name: string;
    html_url: string;
    clone_url: string;
    description: string | null;
    private: boolean;
    archived: boolean;
    default_branch: string;
  }[],
}));
const vcsRef = vi.hoisted(() => ({
  current: { available: true, configured: true, can_manage: true, connections: [] as { id: string; provider: string; instance_url: string; account_login: string }[] },
}));
const gitLabRef = vi.hoisted(() => ({
  current: { pages: [] as { repositories: { id: number; full_name: string; clone_url: string; description: string | null; private: boolean; archived: boolean }[]; next_page: number | null }[], isPending: false, isError: false, hasNextPage: false } as {
    pages: { repositories: { id: number; full_name: string; clone_url: string; description: string | null; private: boolean; archived: boolean }[]; next_page: number | null }[];
    isPending: boolean; isError: boolean; hasNextPage: boolean; error?: { status: number };
  },
}));
const searchParamsRef = vi.hoisted(() => ({
  current: new URLSearchParams("tab=repositories"),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey: readonly unknown[] }) => {
    if (options.queryKey[0] === "vcs") return { data: vcsRef.current };
    if (options.queryKey.includes("installations")) {
      return { data: githubRef.current, ...githubQueryStateRef.current };
    }
    return { data: membersRef.current };
  },
  useInfiniteQuery: (options: { queryKey: readonly unknown[] }) => options.queryKey[0] === "vcs" ? {
    data: { pages: gitLabRef.current.pages },
    ...gitLabRef.current,
    isFetchingNextPage: false,
    fetchNextPage: mockGitLabFetchNextPage,
  } : ({
    data: {
      pages: [
        {
          repositories: githubRepositoriesRef.current,
          total_count: githubRepositoriesRef.current.length,
          next_page: null,
        },
      ],
    },
    isPending: false,
    isError: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: mockFetchNextPage,
  }),
  useQueryClient: () => ({ setQueryData: vi.fn() }),
  queryOptions: <T,>(options: T) => options,
  infiniteQueryOptions: <T,>(options: T) => options,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => workspaceRef.current,
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: vi.fn() }),
  workspaceKeys: { list: () => ["workspaces"] },
}));

vi.mock("@multica/core/api", () => ({
  api: {
    updateWorkspace: mockUpdateWorkspace,
    getGitHubConnectURL: mockGetGitHubConnectURL,
  },
}));

vi.mock("@multica/core/auth", () => {
  const useAuthStore = Object.assign(
    (selector?: (state: { user: { id: string } }) => unknown) =>
      selector ? selector({ user: { id: "user-1" } }) : { user: { id: "user-1" } },
    { getState: () => ({ user: { id: "user-1" } }) },
  );
  return { useAuthStore };
});

vi.mock("sonner", () => ({
  toast: { success: mockToastSuccess, error: vi.fn() },
}));

vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    replace: mockNavReplace,
    back: vi.fn(),
    pathname: "/acme/settings",
    searchParams: searchParamsRef.current,
    hash: "",
    getShareableUrl: (path: string) => `https://app.example${path}`,
  }),
}));

import { RepositoriesTab, repositoryIdentity } from "./repositories-tab";

const TEST_RESOURCES = {
  en: { common: enCommon, settings: enSettings },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

describe("RepositoriesTab — automatic updates", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers({ shouldAdvanceTime: true });
    workspaceRef.current = {
      id: "workspace-1",
      name: "Test Workspace",
      slug: "test-workspace",
      repos: [{ url: "https://github.com/multica-ai/multica" }],
    };
    membersRef.current = [{ user_id: "user-1", role: "owner" }];
    githubRef.current = {
      installations: [],
      configured: true,
      repository_browse_configured: true,
      can_manage: true,
    };
    githubQueryStateRef.current = {
      isPending: false,
      isFetching: false,
    };
    githubRepositoriesRef.current = [];
    vcsRef.current = { available: true, configured: true, can_manage: true, connections: [] };
    gitLabRef.current = { pages: [], isPending: false, isError: false, hasNextPage: false };
    searchParamsRef.current = new URLSearchParams("tab=repositories");
    mockNavReplace.mockImplementation((path: string) => {
      searchParamsRef.current = new URLSearchParams(path.split("?")[1] ?? "");
    });
    mockUpdateWorkspace.mockImplementation(
      async (_id: string, payload: { repos: { url: string; description?: string }[] }) => ({
        ...workspaceRef.current,
        repos: payload.repos,
      }),
    );
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  function setupUser() {
    return userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
  }

  it("renders persisted repositories as the same shared input controls used for editing", () => {
    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    const inputs = screen.getAllByRole("textbox") as HTMLInputElement[];
    expect(inputs).toHaveLength(2);
    expect(inputs[0]!.value).toBe("https://github.com/multica-ai/multica");
    expect(screen.queryByRole("button", { name: /^Save$/ })).toBeNull();
  });

  it("updates a changed URL automatically on blur", async () => {
    const user = setupUser();
    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    const urlInput = screen.getAllByRole("textbox")[0]!;
    await user.clear(urlInput);
    await user.type(urlInput, "https://github.com/multica-ai/edited");
    await user.tab();

    await waitFor(() => {
      expect(mockUpdateWorkspace).toHaveBeenCalledWith("workspace-1", {
        repos: [{ url: "https://github.com/multica-ai/edited" }],
      });
      expect(mockToastSuccess).toHaveBeenCalledWith("Repositories saved", {
        id: "settings-auto-save",
      });
    });
  });

  it("debounces updates while the user is still typing", async () => {
    const user = setupUser();
    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    const urlInput = screen.getAllByRole("textbox")[0]!;
    await user.type(urlInput, "-next");
    expect(mockUpdateWorkspace).not.toHaveBeenCalled();

    vi.advanceTimersByTime(650);
    await waitFor(() => expect(mockUpdateWorkspace).toHaveBeenCalledTimes(1));
  });

  it("does not persist a new row until its URL is non-empty", async () => {
    const user = setupUser();
    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("button", { name: /Add repository/ }));
    expect(screen.getAllByRole("textbox")).toHaveLength(4);
    vi.advanceTimersByTime(1000);
    expect(mockUpdateWorkspace).not.toHaveBeenCalled();

    const newUrlInput = screen.getAllByRole("textbox")[2]!;
    await user.type(newUrlInput, "git@github.com:multica-ai/second.git");
    await user.tab();

    await waitFor(() => {
      expect(mockUpdateWorkspace).toHaveBeenCalledWith("workspace-1", {
        repos: [
          { url: "https://github.com/multica-ai/multica" },
          { url: "git@github.com:multica-ai/second.git" },
        ],
      });
    });
  });

  it("persists deletion immediately without a separate save action", async () => {
    const user = setupUser();
    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("button", { name: "Delete repository" }));
    expect(mockUpdateWorkspace).not.toHaveBeenCalled();
    await user.click(
      screen.getByRole("button", { name: "Delete repository" }),
    );

    await waitFor(() => {
      expect(mockUpdateWorkspace).toHaveBeenCalledWith("workspace-1", { repos: [] });
    });
    expect(screen.getByText("No repositories yet.")).toBeTruthy();
  });

  it("accepts scp-like repository shorthand", async () => {
    const user = setupUser();
    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    const urlInput = screen.getAllByRole("textbox")[0] as HTMLInputElement;
    await user.clear(urlInput);
    await user.type(urlInput, "git@github.com:multica-ai/multica.git");
    expect(urlInput.type).toBe("text");
    expect(urlInput.validity.valid).toBe(true);
    await user.tab();

    await waitFor(() => {
      expect(mockUpdateWorkspace).toHaveBeenCalledWith("workspace-1", {
        repos: [{ url: "git@github.com:multica-ai/multica.git" }],
      });
    });
  });

  it("includes the description in the automatic update payload", async () => {
    workspaceRef.current = {
      ...workspaceRef.current,
      repos: [{ url: "https://github.com/multica-ai/multica", description: "Main app" }],
    };
    const user = setupUser();
    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    const descriptionInput = screen.getAllByRole("textbox")[1] as HTMLInputElement;
    expect(descriptionInput.value).toBe("Main app");
    await user.clear(descriptionInput);
    await user.type(descriptionInput, "Updated description");
    await user.tab();

    await waitFor(() => {
      expect(mockUpdateWorkspace).toHaveBeenCalledWith("workspace-1", {
        repos: [
          {
            url: "https://github.com/multica-ai/multica",
            description: "Updated description",
          },
        ],
      });
    });
  });

  it("keeps repository controls read-only for members", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    expect(screen.getAllByRole("textbox").every((input) => input.hasAttribute("disabled"))).toBe(true);
    expect(screen.queryByRole("button", { name: /Add repository/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Choose from GitLab/ })).toBeNull();
  });

  it("imports selected GitLab subgroup projects across pages without duplicates or archived entries", async () => {
    vcsRef.current = { available: true, configured: true, can_manage: true, connections: [{ id: "gl-1", provider: "gitlab", instance_url: "https://git.test", account_login: "admin" }] };
    workspaceRef.current = { ...workspaceRef.current, repos: [{ url: "https://git.test/a/app.git" }] };
    gitLabRef.current = { pages: [
      { repositories: [
        { id: 1, full_name: "a/app", clone_url: "git@git.test:a/app.git", description: null, private: true, archived: false },
        { id: 2, full_name: "b/app", clone_url: "git@git.test:b/app.git", description: "Second", private: true, archived: false },
      ], next_page: 2 },
      { repositories: [
        { id: 3, full_name: "b/old", clone_url: "git@git.test:b/old.git", description: null, private: false, archived: true },
        { id: 4, full_name: "c/app", clone_url: "", description: null, private: true, archived: false },
      ], next_page: null },
    ], isPending: false, isError: false, hasNextPage: true };
    const user = setupUser();
    render(<RepositoriesTab />, { wrapper: I18nWrapper });
    await user.click(screen.getByRole("button", { name: "Choose from GitLab" }));
    expect(screen.getByText("a/app")).toBeTruthy();
    expect(screen.getByText("b/app")).toBeTruthy();
    const checkboxes = screen.getAllByRole("checkbox");
    expect(checkboxes).toHaveLength(4);
    expect(checkboxes[0]!.getAttribute("aria-disabled") || checkboxes[0]!.getAttribute("disabled")).toBeTruthy();
    expect(checkboxes[2]!.getAttribute("aria-disabled") || checkboxes[2]!.getAttribute("disabled")).toBeTruthy();
    expect(checkboxes[3]!.getAttribute("aria-disabled") || checkboxes[3]!.getAttribute("disabled")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Load more GitLab projects" }));
    expect(mockGitLabFetchNextPage).toHaveBeenCalledTimes(1);
    await user.click(checkboxes[1]!);
    await user.click(screen.getByRole("button", { name: "Add GitLab repositories" }));
    await waitFor(() => expect(mockUpdateWorkspace).toHaveBeenCalledWith("workspace-1", { repos: [
      { url: "https://git.test/a/app.git" },
      { url: "git@git.test:b/app.git", description: "Second" },
    ] }));
  });

  it("offers retry on GitLab failure and keeps manual/GitHub controls", async () => {
    vcsRef.current = { available: true, configured: true, can_manage: true, connections: [{ id: "gl-1", provider: "gitlab", instance_url: "https://git.test", account_login: "admin" }] };
    gitLabRef.current = { pages: [], isPending: false, isError: true, hasNextPage: false };
    render(<RepositoriesTab />, { wrapper: I18nWrapper });
    expect(screen.getByRole("button", { name: "Add repository" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Connect GitHub" })).toBeTruthy();
    await setupUser().click(screen.getByRole("button", { name: "Choose from GitLab" }));
    expect(screen.getByText("Could not load GitLab repositories. Check the connection and try again.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
  });

  it.each([
    [424, "GitLab authorization expired. Reconnect in Integrations."],
    [429, "GitLab is rate limiting requests. Try again later."],
  ])("explains upstream status %d without leaking provider details", async (status, message) => {
    vcsRef.current = { available: true, configured: true, can_manage: true, connections: [{ id: "gl-1", provider: "gitlab", instance_url: "https://git.test", account_login: "admin" }] };
    gitLabRef.current = { pages: [], isPending: false, isError: true, hasNextPage: false, error: { status } };
    render(<RepositoriesTab />, { wrapper: I18nWrapper });
    await setupUser().click(screen.getByRole("button", { name: "Choose from GitLab" }));
    expect(screen.getByText(message)).toBeTruthy();
  });

  it("starts GitHub connection with the signed repository return target", async () => {
    const user = setupUser();
    mockGetGitHubConnectURL.mockResolvedValue({
      configured: true,
      url: "https://github.com/apps/multica/installations/new",
    });
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("button", { name: "Connect GitHub" }));

    await waitFor(() => {
      expect(mockGetGitHubConnectURL).toHaveBeenCalledWith(
        "workspace-1",
        "repositories",
      );
      expect(open).toHaveBeenCalledWith(
        "https://github.com/apps/multica/installations/new",
        "_blank",
        "noopener",
      );
    });
    open.mockRestore();
  });

  it("keeps GitHub import disabled when repository browsing is unavailable", () => {
    githubRef.current = {
      installations: [],
      configured: true,
      repository_browse_configured: false,
      can_manage: true,
    };
    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    const button = screen.getByRole("button", { name: "Connect GitHub" });
    expect(
      button.hasAttribute("disabled") ||
        button.getAttribute("aria-disabled") === "true",
    ).toBe(true);
    expect(button.getAttribute("title")).toContain("GITHUB_APP_ID");
  });

  it("imports selected GitHub repositories and deduplicates HTTPS against SSH", async () => {
    workspaceRef.current = {
      ...workspaceRef.current,
      repos: [{ url: "git@github.com:multica-ai/multica.git" }],
    };
    githubRef.current = {
      installations: [{ id: "installation-row-1", account_login: "multica-ai" }],
      configured: true,
      repository_browse_configured: true,
      can_manage: true,
    };
    githubRepositoriesRef.current = [
      {
        id: 1,
        full_name: "multica-ai/multica",
        html_url: "https://github.com/multica-ai/multica",
        clone_url: "https://github.com/multica-ai/multica.git",
        description: "Existing repository",
        private: false,
        archived: false,
        default_branch: "main",
      },
      {
        id: 2,
        full_name: "multica-ai/console",
        html_url: "https://github.com/multica-ai/console",
        clone_url: "https://github.com/multica-ai/console.git",
        description: "Console app",
        private: true,
        archived: false,
        default_branch: "main",
      },
    ];
    const user = setupUser();
    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    await user.click(
      screen.getByRole("button", { name: "Choose from GitHub" }),
    );
    const checkboxes = screen.getAllByRole("checkbox");
    expect(checkboxes).toHaveLength(2);
    expect(
      checkboxes[0]!.hasAttribute("disabled") ||
        checkboxes[0]!.getAttribute("aria-disabled") === "true",
    ).toBe(true);

    await user.click(checkboxes[1]!);
    await user.click(screen.getByRole("button", { name: "Add repositories" }));

    await waitFor(() => {
      expect(mockUpdateWorkspace).toHaveBeenCalledWith("workspace-1", {
        repos: [
          { url: "git@github.com:multica-ai/multica.git" },
          {
            url: "https://github.com/multica-ai/console.git",
            description: "Console app",
          },
        ],
      });
    });
  });

  it("preserves repository path casing when comparing clone URLs", () => {
    expect(
      repositoryIdentity("https://GitHub.com/Acme/Repo.git"),
    ).toBe("github.com/Acme/Repo");
    expect(
      repositoryIdentity("git@github.com:acme/repo.git"),
    ).toBe("github.com/acme/repo");
  });

  it("never imports a credential-bearing or escaped-control clone URL", () => {
    expect(repositoryIdentity("https://user:secret@git.test/team/app.git")).toBeNull();
    expect(repositoryIdentity("https://git.test/team/app.git?token=secret")).toBeNull();
    expect(repositoryIdentity("https://git.test/team/app%0a.git")).toBeNull();
  });

  it("opens the picker after returning from a GitHub connection", async () => {
    githubRef.current = {
      installations: [{ id: "installation-row-1", account_login: "multica-ai" }],
      configured: true,
      repository_browse_configured: true,
      can_manage: true,
    };
    searchParamsRef.current = new URLSearchParams(
      "tab=repositories&github_connected=1",
    );

    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    expect(
      await screen.findByRole("heading", {
        name: "Choose GitHub repositories",
      }),
    ).toBeTruthy();
    expect(mockNavReplace).toHaveBeenCalledWith(
      "/acme/settings?tab=repositories",
    );
  });

  it("clears the GitHub callback query after an empty installation result", async () => {
    searchParamsRef.current = new URLSearchParams(
      "tab=repositories&github_connected=1",
    );

    render(<RepositoriesTab />, { wrapper: I18nWrapper });

    await waitFor(() => {
      expect(mockNavReplace).toHaveBeenCalledWith(
        "/acme/settings?tab=repositories",
      );
    });
    expect(
      screen.queryByRole("heading", {
        name: "Choose GitHub repositories",
      }),
    ).toBeNull();
  });
});
