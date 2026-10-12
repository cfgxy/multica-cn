import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import en from "../../locales/en/settings.json";
import { WorkspaceLinuxUsersTab } from "./workspace-linux-users-tab";

const api = vi.hoisted(() => ({
  getComputerSettings: vi.fn(),
  listWorkspaces: vi.fn(),
  listComputers: vi.fn(),
  listComputerBindings: vi.fn(),
  operateComputer: vi.fn(),
}));
vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api,
}));
vi.mock("@multica/core/auth", () => {
  const state = { user: { id: "alice", email: "alice@example.test" } };
  return {
    useAuthStore: Object.assign(
      (selector: (value: typeof state) => unknown) => selector(state),
      { getState: () => state },
    ),
  };
});
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useCurrentWorkspace: () => ({
    id: "new-workspace",
    slug: "new",
    name: "New",
  }),
}));
const push = vi.hoisted(() => vi.fn());
vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    pathname: "/new/settings",
    searchParams: new URLSearchParams("tab=linux-users"),
    push,
  }),
  AppLink: ({
    href,
    children,
  }: {
    href: string;
    children: React.ReactNode;
  }) => <a href={href}>{children}</a>,
}));

const removed = {
  id: "available",
  computer_id: "machine",
  workspace_id: "old-workspace",
  username: "alice-worker",
  state: "removed",
  last_error: "",
  verified: true,
  account_state: "present",
  operation_busy: false,
};

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <I18nProvider locale="en" resources={{ en: { settings: en } }}>
      <QueryClientProvider client={client}>
        <WorkspaceLinuxUsersTab />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return client;
}

beforeEach(() => {
  vi.clearAllMocks();
  api.getComputerSettings.mockResolvedValue({
    operator: false,
    settings: { multica_pat: "personal-token" },
  });
  api.listComputers.mockResolvedValue([
    { id: "machine", name: "Host", enabled: true },
  ]);
  api.listComputerBindings.mockResolvedValue([
    removed,
    { ...removed, id: "occupied", username: "alice-busy", state: "ready" },
  ]);
  api.operateComputer.mockResolvedValue({
    ...removed,
    workspace_id: "new-workspace",
  });
  api.listWorkspaces.mockResolvedValue([
    { id: "new-workspace", name: "New", slug: "new" },
  ]);
});

it("keeps the connection form out of the landing page and disables occupied accounts in the dialog", async () => {
  mount();
  const user = userEvent.setup();
  expect(
    await screen.findByText(en.workspace_linux_users.none_connected),
  ).toBeInTheDocument();
  expect(screen.queryByLabelText("Linux password")).not.toBeInTheDocument();
  await user.click(
    screen.getByRole("button", { name: en.workspace_linux_users.connect }),
  );
  expect(
    await screen.findByRole("radio", { name: /alice-busy/ }),
  ).toBeDisabled();
  expect(
    screen.getByText(/Remove its managed daemon there first/),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("link", { name: "Linux User details" }),
  ).toHaveAttribute("href", expect.stringContaining("linux_user=occupied"));
  await user.click(screen.getByRole("radio", { name: /alice-worker/ }));
  await user.type(screen.getByLabelText("Linux password"), "secret");
  await user.click(screen.getByRole("button", { name: "Connect account" }));
  await waitFor(() =>
    expect(api.operateComputer).toHaveBeenCalledWith({
      computer_id: "machine",
      workspace_id: "new-workspace",
      username: "alice-worker",
      password: "secret",
      action: "create_account",
    }),
  );
  expect(push).toHaveBeenCalledWith(
    expect.stringContaining("linux_user_from=workspace"),
  );
  expect(api.getComputerSettings).not.toHaveBeenCalled();
});
it("clears the password when switching modes and shares the create form", async () => {
  mount();
  const user = userEvent.setup();
  await user.click(
    screen.getByRole("button", { name: en.workspace_linux_users.connect }),
  );
  await user.type(screen.getByLabelText("Linux password"), "first-secret");
  await user.click(
    screen.getByRole("button", { name: en.workspace_linux_users.new }),
  );
  expect(screen.getByLabelText("Linux password")).toHaveValue("");
  await user.click(screen.getByRole("combobox", { name: en.computers.choose }));
  await user.click(await screen.findByRole("option", { name: "Host" }));
  await user.type(screen.getByLabelText(en.computers.username), "new-user");
  await user.type(screen.getByLabelText("Linux password"), "secret");
  await user.click(
    screen.getByRole("button", { name: en.computers.actions.create_account }),
  );
  await waitFor(() =>
    expect(api.operateComputer).toHaveBeenCalledWith({
      computer_id: "machine",
      workspace_id: "new-workspace",
      username: "new-user",
      password: "secret",
      action: "create_account",
    }),
  );
});
it("gives every connected account a direct detail link, including failed creation", async () => {
  api.listComputerBindings.mockResolvedValue([
    {
      ...removed,
      workspace_id: "new-workspace",
      state: "failed",
      verified: false,
    },
  ]);
  mount();
  const link = await screen.findByRole("link", { name: "Linux User details" });
  expect(link).toHaveAttribute(
    "href",
    "/new/settings?tab=computers&linux_user=available&linux_user_view=overview&linux_user_from=workspace",
  );
  expect(screen.getByText("Unverified")).toBeInTheDocument();
});
