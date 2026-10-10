import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { ModelTemplatePanel } from "./model-template-panel";
import en from "../locales/en/settings.json";
const api = vi.hoisted(() => ({
  getModelTemplates: vi.fn(),
  saveModelTemplate: vi.fn(),
}));
vi.mock("@multica/core/api", async (original) => ({
  ...(await original<typeof import("@multica/core/api")>()),
  api,
}));
const template = {
  runtimeKind: "codex",
  revision: 3,
  status: "published",
  defaultModel: "model-a",
  supportedAPITypes: ["openai-responses"],
  files: [
    {
      id: "config",
      path: ".codex/config.toml",
      format: "toml",
      content: 'model = ""',
      bindings: { model: "model_id" },
    },
  ],
};
beforeEach(() => {
  vi.clearAllMocks();
  api.getModelTemplates.mockResolvedValue([template]);
  api.saveModelTemplate.mockResolvedValue({ saved: true });
});
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <I18nProvider locale="en" resources={{ en: { settings: en } }}>
      <QueryClientProvider client={client}>
        <ModelTemplatePanel userId="admin" />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return userEvent.setup();
}
it("saves an edited template with its original revision and preserves drafts on failure", async () => {
  const user = mount();
  await user.click(
    await screen.findByRole("button", { name: en.model_configuration.edit }),
  );
  const model = screen.getByLabelText(en.model_configuration.default_model);
  await user.clear(model);
  await user.type(model, "model-b");
  api.saveModelTemplate.mockRejectedValueOnce(
    new Error("fixture save failure"),
  );
  await user.click(
    screen.getByRole("button", { name: en.model_configuration.save }),
  );
  await screen.findByRole("alert");
  expect(model).toHaveValue("model-b");
  expect(screen.getByRole("dialog")).toBeInTheDocument();
  await user.click(
    screen.getByRole("button", { name: en.model_configuration.save }),
  );
  await waitFor(() =>
    expect(api.saveModelTemplate).toHaveBeenLastCalledWith({
      ...template,
      defaultModel: "model-b",
    }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
});
it("rejects invalid field mapping JSON without sending a template", async () => {
  const user = mount();
  await user.click(
    await screen.findByRole("button", { name: en.model_configuration.edit }),
  );
  await user.click(
    screen.getByText(en.model_configuration.bindings, { selector: "summary" }),
  );
  const mapping = screen.getByLabelText(
    `config ${en.model_configuration.bindings}`,
  );
  await user.clear(mapping);
  await user.type(mapping, "invalid-json");
  await user.click(
    screen.getByRole("button", { name: en.model_configuration.save }),
  );
  await screen.findByRole("alert");
  expect(api.saveModelTemplate).not.toHaveBeenCalled();
});

it("keeps supported rows editable and prevents saving an unsupported template", async () => {
  api.getModelTemplates.mockResolvedValue([
    template,
    { ...template, runtimeKind: "future", status: "unsupported", files: [] },
  ]);
  mount();
  const row = (await screen.findByText("future")).closest("tr")!;
  expect(within(row).getByText(en.model_configuration.states.unsupported)).toBeInTheDocument();
  expect(within(row).queryByRole("button")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: en.model_configuration.edit })).toBeEnabled();
});
