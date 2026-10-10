import { it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import en from "../../locales/en/settings.json";
import { CredentialsTab, AccountCredentials } from "./credentials-tab";

const api = vi.hoisted(() => ({
  getComputerSettings: vi.fn(),
  readComputerCredentials: vi.fn(),
  writeComputerCredentials: vi.fn(),
  listComputers: vi.fn(),
  listComputerBindings: vi.fn(),
  saveComputerSettings: vi.fn(),
  operateComputer: vi.fn(),
}));
vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api,
}));
vi.mock("@multica/core/auth", () => {
  const state = { user: { id: "human-1", email: "alice@example.com" } };
  return {
    useAuthStore: Object.assign(
      (selector: (s: typeof state) => unknown) => selector(state),
      { getState: () => state },
    ),
  };
});
const settings = {
  git_name: "Alice",
  git_email: "alice@example.com",
  gitlab_url: "https://git.example.com",
  gitlab_token: "fake-token",
  git_ssh_key: "",
  git_known_hosts: "",
  model_env: "OPENAI_API_KEY=fake-key",
  multica_pat: "mul_fake",
};
function mount(account = false) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrap = (node: React.ReactNode) => (
    <I18nProvider locale="en" resources={{ en: { settings: en } }}>
      <QueryClientProvider client={client}>{node}</QueryClientProvider>
    </I18nProvider>
  );
  const result = render(
    wrap(
      account ? (
        <AccountCredentials userId="human-1" binding={binding} />
      ) : (
        <CredentialsTab />
      ),
    ),
  );
  return {
    ...result,
    switchAccount: () =>
      result.rerender(
        wrap(
          <AccountCredentials
            userId="human-1"
            binding={{ ...binding, id: "bob", username: "bob" }}
          />,
        ),
      ),
  };
}
const binding = {
  id: "alice",
  computer_id: "machine",
  workspace_id: "workspace",
  workspace_name: "Team",
  workspace_access: "accessible" as const,
  latest_operation: null,
  username: "alice",
  state: "pending",
  verified: true,
  account_state: "present" as const,
  operation_busy: false,
  last_error: "",
};
beforeEach(() => {
  vi.clearAllMocks();
  api.getComputerSettings.mockResolvedValue({ operator: false, settings });
  api.listComputers.mockResolvedValue([]);
  api.listComputerBindings.mockResolvedValue([]);
  api.saveComputerSettings.mockResolvedValue(undefined);
});

it("saves only the personal template, with no remote target or password", async () => {
  mount();
  const user = userEvent.setup();
  const name = await screen.findByLabelText("Git author name");
  expect(name).toHaveAccessibleDescription(/git config --global user.name/);
  expect(screen.queryByLabelText("Linux password")).not.toBeInTheDocument();
  await user.clear(name);
  await user.type(name, "Changed");
  await user.click(
    screen.getByRole("button", { name: "Save personal template" }),
  );
  await waitFor(() =>
    expect(api.saveComputerSettings).toHaveBeenCalledWith({
      ...settings,
      git_name: "Changed",
    }),
  );
  expect(api.writeComputerCredentials).not.toHaveBeenCalled();
});
it("does not read either secret source until explicitly requested, and writes only to the fixed account", async () => {
  api.readComputerCredentials.mockResolvedValue({
    operator: false,
    settings: { ...settings, git_name: "Remote" },
  });
  api.writeComputerCredentials.mockResolvedValue(undefined);
  mount(true);
  const user = userEvent.setup();
  expect(api.getComputerSettings).not.toHaveBeenCalled();
  expect(api.readComputerCredentials).not.toHaveBeenCalled();
  expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  await authorize(user, "Read from server", "secret");
  await waitFor(() =>
    expect(screen.getByLabelText("Git author name")).toHaveValue("Remote"),
  );
  expect(screen.queryByLabelText("Linux password")).not.toBeInTheDocument();
  expect(api.saveComputerSettings).not.toHaveBeenCalled();
  await authorize(user, "Write to Linux user", "secret-again");
  await waitFor(() =>
    expect(api.writeComputerCredentials).toHaveBeenCalledWith(
      "alice",
      "secret-again",
      { ...settings, git_name: "Remote" },
    ),
  );
});
it("requires explicit confirmation before exporting the remote draft to a personal template", async () => {
  api.readComputerCredentials.mockResolvedValue({ operator: false, settings });
  mount(true);
  const user = userEvent.setup();
  await authorize(user, "Read from server", "secret");
  await waitFor(() =>
    expect(screen.getByLabelText("Git author name")).toHaveValue("Alice"),
  );
  await user.click(
    screen.getByText("Save personal template", { selector: "summary" }),
  );
  expect(
    screen.getByRole("button", { name: "Save personal template" }),
  ).toBeDisabled();
  await user.click(
    screen.getByRole("checkbox", { name: /Save these remote credentials/ }),
  );
  await user.click(
    screen.getByRole("button", { name: "Save personal template" }),
  );
  await waitFor(() =>
    expect(api.saveComputerSettings).toHaveBeenCalledWith(settings),
  );
});
it("discards account A's draft and ignores its late response after switching accounts", async () => {
  let resolve!: (value: unknown) => void;
  api.readComputerCredentials.mockImplementation(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  const mounted = mount(true);
  const user = userEvent.setup();
  await authorize(user, "Read from server", "alice-secret");
  mounted.switchAccount();
  const { act } = await import("@testing-library/react");
  await act(async () =>
    resolve({
      operator: false,
      settings: { ...settings, multica_pat: "alice-private" },
    }),
  );
  expect(screen.queryByLabelText("Linux password")).not.toBeInTheDocument();
  expect(screen.getByLabelText("Git author name")).toHaveValue("");
  expect(screen.getByLabelText(en.computers.multica_pat)).toHaveValue("");
  expect(
    screen.getByRole("button", { name: "Write to Linux user" }),
  ).toBeDisabled();
  expect(api.saveComputerSettings).not.toHaveBeenCalled();
});
it("loads a template only by explicit action and localizes password errors", async () => {
  const { ApiError } = await import("@multica/core/api");
  api.readComputerCredentials.mockRejectedValue(
    new ApiError("server message", 403, "Forbidden", {
      code: "password_mismatch",
    }),
  );
  mount(true);
  const user = userEvent.setup();
  await authorize(user, "Read from server", "wrong");
  expect(await screen.findByRole("alert")).toHaveTextContent(
    en.linux_user.errors.password_mismatch,
  );
  expect(screen.getByLabelText("Linux password")).toHaveValue("");
  await user.click(screen.getByRole("button", { name: "Cancel" }));
  await user.click(
    screen.getByRole("button", { name: "Load personal template" }),
  );
  await waitFor(() =>
    expect(screen.getByLabelText("Git author name")).toHaveValue("Alice"),
  );
  expect(api.writeComputerCredentials).not.toHaveBeenCalled();
});
it("does not turn a failed personal template response into an editable empty form", async () => {
  api.getComputerSettings.mockRejectedValue(new Error("Settings unavailable"));
  mount();
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Settings unavailable",
  );
  expect(
    screen.queryByRole("button", { name: "Save personal template" }),
  ).not.toBeInTheDocument();
});

async function authorize(
  user: ReturnType<typeof userEvent.setup>,
  action: string,
  password: string,
) {
  expect(screen.queryByLabelText("Linux password")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: action }));
  const dialog = screen.getByRole("dialog", { name: action });
  expect(dialog).toHaveAccessibleDescription(/alice/);
  await user.type(within(dialog).getByLabelText("Linux password"), password);
  await user.click(within(dialog).getByRole("button", { name: action }));
}

it("requests authorization after editing and clears cancelled passwords without losing the draft", async () => {
  mount(true);
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("Git author name"), "New identity");
  expect(screen.queryByLabelText("Linux password")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Write to Linux user" }));
  await user.type(screen.getByLabelText("Linux password"), "cancelled-secret");
  await user.click(screen.getByRole("button", { name: "Cancel" }));
  expect(api.writeComputerCredentials).not.toHaveBeenCalled();
  expect(screen.getByLabelText("Git author name")).toHaveValue("New identity");
  await user.click(screen.getByRole("button", { name: "Write to Linux user" }));
  expect(screen.getByLabelText("Linux password")).toHaveValue("");
});

it("keeps a failed write open with the draft intact, blocks dismissal while submitting, and allows retry", async () => {
  const { ApiError } = await import("@multica/core/api");
  let reject!: (reason: unknown) => void;
  api.writeComputerCredentials.mockImplementationOnce(
    () =>
      new Promise((_resolve, fail) => {
        reject = fail;
      }),
  );
  mount(true);
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("Git author name"), "Keep me");
  await authorize(user, "Write to Linux user", "wrong");
  await user.keyboard("{Escape}");
  expect(screen.getByRole("dialog")).toBeInTheDocument();
  const { act } = await import("@testing-library/react");
  await act(async () =>
    reject(
      new ApiError("server", 403, "Forbidden", { code: "password_mismatch" }),
    ),
  );
  expect(
    within(screen.getByRole("dialog")).getByRole("alert"),
  ).toHaveTextContent(en.linux_user.errors.password_mismatch);
  expect(screen.getByLabelText("Linux password")).toHaveValue("");
  expect(screen.getByLabelText("Git author name")).toHaveValue("Keep me");
  api.writeComputerCredentials.mockResolvedValueOnce(undefined);
  await user.type(screen.getByLabelText("Linux password"), "correct");
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Write to Linux user",
    }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(api.writeComputerCredentials).toHaveBeenLastCalledWith(
    "alice",
    "correct",
    expect.objectContaining({ git_name: "Keep me" }),
  );
});
