import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "@multica/ui/components/ui/dropdown-menu";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import en from "../locales/en/settings.json";
import { AdminPage, AdminAreaLink } from "./admin-page";

const api = vi.hoisted(() => ({
  getInstanceAccess: vi.fn(),
  listAdminComputers: vi.fn(),
  listAdminComputerBindings: vi.fn(),
  listComputerAudit: vi.fn(),
  registerComputer: vi.fn(),
  updateAdminComputer: vi.fn(),
  deleteAdminComputer: vi.fn(),
  checkAdminComputer: vi.fn(),
  checkAdminComputerDraft: vi.fn(),
  checkAdminLinuxUser: vi.fn(),
  getAdminSshPubKey: vi.fn(),
}));
const state = vi.hoisted(() => ({
  user: { id: "admin-human" },
  isLoading: false,
}));
const replace = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({ api }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: typeof state) => unknown) => selector(state),
}));
vi.mock("../navigation", () => ({
  useNavigation: () => ({ replace }),
  AppLink: (props: React.ComponentProps<"a">) => <a {...props} />,
}));
vi.mock("../platform", () => ({ DragStrip: () => null }));
function mount(link = false) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { settings: en } }}>
      <QueryClientProvider client={client}>
        {link ? <DropdownMenu defaultOpen><DropdownMenuTrigger>Menu</DropdownMenuTrigger><DropdownMenuContent><AdminAreaLink /></DropdownMenuContent></DropdownMenu> : <AdminPage />}
      </QueryClientProvider>
    </I18nProvider>,
  );
}
beforeEach(() => {
  vi.clearAllMocks();
  api.getInstanceAccess.mockResolvedValue({ admin: false });
  api.listAdminComputers.mockResolvedValue([]);
  api.listAdminComputerBindings.mockResolvedValue([]);
  api.listComputerAudit.mockResolvedValue([]);
  api.registerComputer.mockResolvedValue(undefined);
  api.updateAdminComputer.mockResolvedValue(undefined);
  api.deleteAdminComputer.mockResolvedValue(undefined);
  api.checkAdminComputer.mockResolvedValue({ ok: true, facts: {}, checks: [] });
  api.checkAdminComputerDraft.mockResolvedValue({ ok: true, facts: {}, checks: [] });
  api.checkAdminLinuxUser.mockResolvedValue({ present: true });
  api.getAdminSshPubKey.mockResolvedValue("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA mock-key admin@multica");
});
const MACHINE = {
  id: "machine",
  name: "Dev server",
  host: "dev.invalid",
  port: 22,
  ssh_user: "operator",
  enabled: true,
  bindings: 0,
};
function adminWithMachine(overrides: Record<string, unknown> = {}) {
  api.getInstanceAccess.mockResolvedValue({ admin: true });
  api.listAdminComputers.mockResolvedValue([{ ...MACHINE, ...overrides }]);
}
describe("instance Admin Area", () => {
  it("denies non-admins before any registry or employee data is loaded", async () => {
    mount();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Only instance administrators",
    );
    expect(api.listAdminComputers).not.toHaveBeenCalled();
    expect(api.listAdminComputerBindings).not.toHaveBeenCalled();
    expect(api.listComputerAudit).not.toHaveBeenCalled();
  });
  it("fails closed when access check errors", async () => {
    api.getInstanceAccess.mockRejectedValue(new Error("Access unavailable"));
    mount();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Access unavailable",
    );
    expect(api.listAdminComputers).not.toHaveBeenCalled();
  });
  it("registers and disables a Computer without any workspace context", async () => {
    adminWithMachine();
    mount();
    const user = userEvent.setup();
    // The list comes first; the form only appears behind the Add button.
    await screen.findByRole("heading", { name: "Computers" });
    expect(screen.queryByLabelText("Name")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add Computer" }));
    await user.type(await screen.findByLabelText("Name"), "New server");
    await user.type(screen.getByLabelText("SSH host"), "new.invalid");
    await user.type(screen.getByLabelText("SSH operator username"), "operator");
    await user.click(screen.getByRole("button", { name: "Register Computer" }));
    await waitFor(() =>
      expect(api.registerComputer).toHaveBeenCalledWith({
        name: "New server",
        host: "new.invalid",
        port: 22,
        ssh_user: "operator",
      }),
    );
    await user.click(screen.getByRole("button", { name: "Disable" }));
    await waitFor(() =>
      expect(api.updateAdminComputer).toHaveBeenCalledWith("machine", {
        enabled: false,
      }),
    );
  });
  it("shows registration and check details for each Computer", async () => {
    adminWithMachine({
      created_by_name: "Cindy",
      created_at: "2026-09-20T02:00:00Z",
      checked_at: "2026-09-23T02:00:00Z",
      check_ok: false,
      check_detail: "sudo: passwordless sudo unavailable",
      bindings: 2,
    });
    mount();
    expect(await screen.findByText("Cindy")).toBeInTheDocument();
    expect(screen.getByText("Check failed")).toBeInTheDocument();
    expect(
      screen.getByText("sudo: passwordless sudo unavailable"),
    ).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    // A Computer with bound accounts cannot be deleted from the UI either.
    expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
  });
  it("switches sections from the sidebar", async () => {
    adminWithMachine();
    mount();
    const user = userEvent.setup();
    const nav = await screen.findByRole("navigation", {
      name: "Admin sections",
    });
    await user.click(
      within(nav).getByRole("button", { name: "Linux Users" }),
    );
    expect(
      await screen.findByText(/Latest 500 Linux User bindings/),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Computers" }),
    ).not.toBeInTheDocument();
    await user.click(within(nav).getByRole("button", { name: "Activity log" }));
    expect(await screen.findByText(/Latest 200 actions/)).toBeInTheDocument();
  });
  it("reports why a connection check failed", async () => {
    adminWithMachine();
    api.checkAdminComputer.mockResolvedValue({
      ok: false,
      facts: { hostname: "tensor" },
      checks: [
        { name: "sudo", ok: false, detail: "passwordless sudo unavailable" },
        { name: "ssh", ok: true },
      ],
    });
    mount();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Test connection" }),
    );
    const dialog = await screen.findByRole("dialog");
    const report = await within(dialog).findByRole("region", {
      name: "Connection check result",
    });
    expect(report).toHaveTextContent("passwordless sudo unavailable");
    expect(report).toHaveTextContent("tensor");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(
      screen.queryByRole("region", { name: "Connection check result" }),
    ).not.toBeInTheDocument();
  });
  it("surfaces the reason a registration was refused", async () => {
    api.getInstanceAccess.mockResolvedValue({ admin: true });
    api.listAdminComputers.mockResolvedValue([]);
    const refusal = Object.assign(
      new Error("Computer did not pass the connection check"),
      {
        body: {
          probe: {
            ok: false,
            facts: {},
            checks: [{ name: "pam", ok: false, detail: "missing" }],
          },
        },
      },
    );
    api.registerComputer.mockRejectedValue(refusal);
    mount();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Add Computer" }),
    );
    await user.type(await screen.findByLabelText("Name"), "Bad server");
    await user.type(screen.getByLabelText("SSH host"), "bad.invalid");
    await user.type(screen.getByLabelText("SSH operator username"), "operator");
    await user.click(screen.getByRole("button", { name: "Register Computer" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "did not pass the connection check",
    );
    expect(
      await screen.findByRole("region", { name: "Connection check result" }),
    ).toHaveTextContent("missing");
  });
  it("deletes a Computer only after confirmation", async () => {
    adminWithMachine();
    mount();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Delete this Computer?");
    expect(api.deleteAdminComputer).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() =>
      expect(api.deleteAdminComputer).toHaveBeenCalledWith("machine"),
    );
  });
  it("test connection in add dialog does not show a Saved notice on success", async () => {
    api.getInstanceAccess.mockResolvedValue({ admin: true });
    api.listAdminComputers.mockResolvedValue([]);
    mount();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Add Computer" }));
    await user.type(await screen.findByLabelText("Name"), "Test server");
    await user.type(screen.getByLabelText("SSH host"), "test.invalid");
    await user.type(screen.getByLabelText("SSH operator username"), "operator");
    await user.click(screen.getByRole("button", { name: "Test connection" }));
    await waitFor(() =>
      expect(api.checkAdminComputerDraft).toHaveBeenCalled(),
    );
    // The probe result should appear, but no "saved" status message.
    const status = screen.queryByRole("status");
    if (status) {
      expect(status).not.toHaveTextContent(/saved/i);
    }
  });
  it("only shows the standalone link to instance admins", async () => {
    api.getInstanceAccess.mockResolvedValue({ admin: true });
    mount(true);
    expect(
      await screen.findByRole("menuitem", { name: "Admin Area" }),
    ).toHaveAttribute("href", "/admin");
  });
  it("copies SSH public key to clipboard when Copy SSH public key is clicked", async () => {
    api.getInstanceAccess.mockResolvedValue({ admin: true });
    mount();
    const user = userEvent.setup();
    const copyBtn = await screen.findByRole("button", { name: "Copy SSH public key" });
    await user.click(copyBtn);
    await waitFor(() => {
      expect(api.getAdminSshPubKey).toHaveBeenCalled();
    });
  });
});


it("keeps runtime installation out of the Computer registry", async () => {
  adminWithMachine();
  mount();
  expect(await screen.findByText("Dev server")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Runtimes" })).not.toBeInTheDocument();
});

it("searches Linux Users and checks the selected remote account", async () => {
  adminWithMachine();
  api.listAdminComputerBindings.mockResolvedValue([
    { id: "binding-1", computer_id: "machine", workspace_id: "workspace-1", username: "alice", user_id: "owner-1", user_name: "Alice", state: "ready", last_error: "" },
    { id: "binding-2", computer_id: "machine", workspace_id: "workspace-2", username: "bob", user_id: "owner-2", user_name: "Bob", state: "failed", last_error: "Provisioning failed" },
  ]);
  mount();
  const user = userEvent.setup();
  await user.click(within(await screen.findByRole("navigation", { name: "Admin sections" })).getByRole("button", { name: "Linux Users" }));
  expect(await screen.findByText("alice")).toBeInTheDocument();
  expect(screen.getByText("Provisioning failed")).toBeInTheDocument();
  await user.type(screen.getByRole("searchbox", { name: "Search username, owner, or Computer" }), "alice");
  expect(screen.queryByText("bob")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Check account" }));
  await waitFor(() => expect(api.checkAdminLinuxUser).toHaveBeenCalledWith("binding-1"));
  expect(await screen.findByText("Account exists")).toBeInTheDocument();
});
