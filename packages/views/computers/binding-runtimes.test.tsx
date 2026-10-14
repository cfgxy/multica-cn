import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import en from "../locales/en/settings.json";
import { BindingRuntimes } from "./binding-runtimes";

const api = vi.hoisted(() => ({
  listComputerBindingRuntimes: vi.fn(),
  installComputerBindingRuntime: vi.fn(),
  listComputerOperations: vi.fn(),
}));
vi.mock("@multica/core/api", async (original) => ({
  ...(await original<typeof import("@multica/core/api")>()),
  api,
}));
const installed = {
  id: "codex",
  display_name: "Codex",
  installed_version: "codex-cli 1.2.3",
  can_install: true,
  supports_version: true,
  probe_state: "installed",
  latest_version: "1.2.3",
  latest_version_state: "ready",
  update_available: false,
};
function mount(busy = false) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const tree = (isBusy: boolean) => (
    <I18nProvider locale="en" resources={{ en: { settings: en } }}>
      <QueryClientProvider client={client}>
        <BindingRuntimes userId="user" bindingId="binding" busy={isBusy} />
      </QueryClientProvider>
    </I18nProvider>
  );
  const result = render(tree(busy));
  return {
    ...result,
    setBusy: (value: boolean) => result.rerender(tree(value)),
    client,
  };
}
beforeEach(() => {
  vi.clearAllMocks();
  api.listComputerBindingRuntimes.mockResolvedValue([installed]);
  api.installComputerBindingRuntime.mockResolvedValue({
    operation_id: "operation",
    state: "queued",
  });
});

it("shows a newly installed current CLI in a table without an Update button", async () => {
  mount();
  const table = await screen.findByRole("table");
  expect(
    within(table).getByRole("columnheader", { name: "Latest version" }),
  ).toBeInTheDocument();
  expect(within(table).getByText("Up to date")).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Update" }),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Manage Codex version" }),
  ).toBeEnabled();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Codex details" }));
  expect(screen.getByText(en.linux_user.path)).toBeInTheDocument();
});

it("shows the available release and submits that version after confirmation", async () => {
  api.listComputerBindingRuntimes.mockResolvedValue([
    { ...installed, latest_version: "1.2.4", update_available: true },
  ]);
  mount();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Update" }));
  const dialog = screen.getByRole("dialog");
  expect(within(dialog).getByText("1.2.4")).toBeInTheDocument();
  expect(api.installComputerBindingRuntime).not.toHaveBeenCalled();
  await user.click(within(dialog).getByRole("button", { name: "Update" }));
  await waitFor(() =>
    expect(api.installComputerBindingRuntime).toHaveBeenCalledWith(
      "binding",
      "codex",
      "1.2.4",
    ),
  );
});

it("keeps manual installation available when release detection is unavailable", async () => {
  api.listComputerBindingRuntimes.mockResolvedValue([
    {
      ...installed,
      latest_version_state: "unavailable",
      update_available: null,
    },
  ]);
  mount();
  expect(
    await screen.findByText(en.linux_user_pages.runtime_versions.unavailable),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Update" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByText("Up to date")).not.toBeInTheDocument();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Manage version" }));
  await user.type(screen.getByLabelText("Codex version"), "1.2.4");
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: "Install" }),
  );
  await waitFor(() =>
    expect(api.installComputerBindingRuntime).toHaveBeenCalledWith(
      "binding",
      "codex",
      "1.2.4",
    ),
  );
});

it("refreshes installed versions immediately when a remote operation completes", async () => {
  const view = mount(true);
  await screen.findByRole("table");
  expect(
    screen.getByRole("button", { name: "Manage Codex version" }),
  ).toBeDisabled();
  api.listComputerBindingRuntimes.mockResolvedValue([
    {
      ...installed,
      installed_version: "codex-cli 1.2.4",
      latest_version: "1.2.4",
    },
  ]);
  view.setBusy(false);
  expect(await screen.findByText("codex-cli 1.2.4")).toBeInTheDocument();
});

it("selects installable runtimes with mixed state and submits the batch without individual dialogs", async () => {
  api.listComputerBindingRuntimes.mockResolvedValue([
    { ...installed, latest_version: "1.2.4", update_available: true },
    {
      ...installed,
      id: "omp",
      display_name: "OMP",
      installed_version: "",
      latest_version: "18.4.0",
    },
    { ...installed, id: "custom", display_name: "Custom", can_install: false },
  ]);
  api.installComputerBindingRuntime.mockImplementation(
    async (_binding, id) => ({ operation_id: id, state: "queued" }),
  );
  api.listComputerOperations.mockResolvedValue([
    { id: "codex", state: "failed", error_summary: "Failed" },
    { id: "omp", state: "succeeded" },
  ]);
  mount();
  const user = userEvent.setup();
  const all = await screen.findByRole("checkbox", {
    name: "Select all installable runtimes",
  });
  expect(
    screen.getByRole("button", { name: "Install or update (0)" }),
  ).toBeDisabled();
  expect(
    screen.getByRole("checkbox", { name: "Select Custom" }),
  ).toHaveAttribute("aria-disabled", "true");
  await user.click(screen.getByRole("checkbox", { name: "Select Codex" }));
  expect(all).toBePartiallyChecked();
  await user.click(all);
  expect(all).toBeChecked();
  await user.click(
    screen.getByRole("button", { name: "Install or update (2)" }),
  );
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(
    await screen.findByText("2 / 2 completed · 1 failed"),
  ).toBeInTheDocument();
  expect(api.installComputerBindingRuntime.mock.calls).toEqual([
    ["binding", "codex", "1.2.4"],
    ["binding", "omp", "18.4.0"],
  ]);
  expect(
    within(screen.getByText("Codex").closest("tr")!).getByText("Failed"),
  ).toBeInTheDocument();
  expect(
    within(screen.getByText("OMP").closest("tr")!).getByText("Succeeded"),
  ).toBeInTheDocument();
});
