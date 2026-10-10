import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { ModelKeyPanel } from "./model-key-panel";
import en from "../locales/en/settings.json";
const api = vi.hoisted(() => ({
  getModelKeySource: vi.fn(),
  getModelKeyDirectory: vi.fn(),
  getModelKeyUsers: vi.fn(),
  getUserModelKeys: vi.fn(),
  saveModelKeySource: vi.fn(),
  refreshModelKeyDirectory: vi.fn(),
  saveUserModelKeys: vi.fn(),
}));
vi.mock("@multica/core/api", async (original) => ({
  ...(await original<typeof import("@multica/core/api")>()),
  api,
}));
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <I18nProvider locale="en" resources={{ en: { settings: en } }}>
      <QueryClientProvider client={client}>
        <ModelKeyPanel userId="admin" />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return userEvent.setup();
}
beforeEach(() => {
  vi.clearAllMocks();
  api.getModelKeySource.mockResolvedValue({
    configured: true,
    baseURL: "https://directory.example",
    revision: 7,
    status: "ready",
    directoryCount: 2,
  });
  api.getModelKeyUsers.mockResolvedValue([
    { id: "user", name: "Alice", email: "alice@example.com" },
  ]);
  api.getUserModelKeys.mockResolvedValue([
    {
      id: "9",
      apiKeyName: "Existing",
      allowedRuntimes: ["claude"],
      status: "enabled",
      position: 0,
    },
  ]);
  api.getModelKeyDirectory.mockResolvedValue({
    items: [
      {
        id: "12",
        apiKeyName: "Codex key",
        allowedRuntimes: ["codex"],
        status: "enabled",
        position: 0,
      },
    ],
    page: 1,
    hasMore: false,
  });
  api.saveUserModelKeys.mockResolvedValue({ saved: true });
  api.saveModelKeySource.mockResolvedValue({ saved: true });
});
it("adds a searched key without dropping existing grants outside the filtered page", async () => {
  const user = mount();
  await user.click(await screen.findByRole("button", { name: "Assign keys" }));
  await screen.findByText("Existing");
  await user.type(screen.getByLabelText("Search key name or ID"), "Codex");
  await waitFor(() => expect(api.getModelKeyDirectory).toHaveBeenCalledWith({
    search: "Codex", runtime: "", page: 1,
  }));
  expect(api.getModelKeyDirectory.mock.calls.some(([input]) =>
    ["C", "Co", "Cod", "Code"].includes(input.search),
  )).toBe(false);
  await user.click(
    await screen.findByRole("checkbox", { name: "Codex key #12" }),
  );
  const buttons = screen.getAllByRole("button", { name: "Save" });
  await user.click(buttons.at(-1)!);
  await waitFor(() =>
    expect(api.saveUserModelKeys).toHaveBeenCalledWith("user", ["9", "12"]),
  );
  expect(api.getModelKeyDirectory).toHaveBeenCalledWith({
    search: "Codex",
    runtime: "",
    page: 1,
  });
});
it("requires a new directory credential and clears it after successful replacement", async () => {
  const user = mount();
  await screen.findByDisplayValue("https://directory.example");
  const credential = screen.getByLabelText("Directory credential");
  expect(credential).toHaveValue("");
  await user.type(credential, "fixture-directory-secret");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(api.saveModelKeySource).toHaveBeenCalledWith({
      baseURL: "https://directory.example",
      directoryToken: "fixture-directory-secret",
      revision: 7,
    }),
  );
  await waitFor(() => expect(credential).toHaveValue(""));
});
