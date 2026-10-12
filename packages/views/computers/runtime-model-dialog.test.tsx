import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { RuntimeModelDialog } from "./runtime-model-dialog";
import en from "../locales/en/settings.json";

const api = vi.hoisted(() => ({
  getRuntimeModelKeys: vi.fn(),
  runtimeModelConfig: vi.fn(),
}));
vi.mock("@multica/core/api", async (original) => ({
  ...(await original<typeof import("@multica/core/api")>()),
  api,
}));
const configPath = ".codex/config.toml";
const authPath = ".codex/auth.json";
const base = {
  runtimeKind: "codex",
  templateRevision: 0,
  saved: false,
  valid: false,
  apiKeyId: "",
  modelId: "",
  apiType: "",
  operationId: "op",
  endpoints: [],
  models: [],
  snapshot: {
    files: { [configPath]: null, [authPath]: null },
    revision: "original",
  },
};
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <I18nProvider locale="en" resources={{ en: { settings: en } }}>
      <QueryClientProvider client={client}>
        <RuntimeModelDialog
          userId="owner"
          bindingId="linux"
          kind="codex"
          label="Codex"
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return client;
}
beforeEach(() => {
  vi.clearAllMocks();
  api.getRuntimeModelKeys.mockResolvedValue([
    {
      id: "key-b",
      apiKeyName: "Default in grant order",
      allowedRuntimes: ["codex"],
      status: "enabled",
    },
    {
      id: "key-a",
      apiKeyName: "Another key",
      allowedRuntimes: ["codex"],
      status: "enabled",
    },
  ]);
  api.runtimeModelConfig.mockImplementation(
    async (_binding, _kind, action, input) => {
      if (action === "connection")
        return {
          ...base,
          apiKeyId: input.apiKeyId,
          endpoints: [
            {
              apiType: "openai-responses",
              baseURL: "https://model.example/v1",
              defaultModel: "test-model",
            },
          ],
          models: [
            {
              id: "test-model",
              name: "Test model",
              apiTypes: ["openai-responses"],
            },
          ],
        };
      if (action === "preview")
        return {
          ...base,
          apiKeyId: input.apiKeyId,
          modelId: "test-model",
          apiType: "openai-responses",
          snapshot: {
            revision: "original",
            files: {
              [configPath]: 'model = "test-model"',
              [authPath]: '{"OPENAI_API_KEY":"fixture-key"}',
            },
          },
        };
      if (action === "save")
        return {
          ...base,
          saved: true,
          snapshot: { revision: "saved", files: input.files },
        };
      if (action === "test")
        return {
          ...base,
          test: {
            status: "succeeded",
            errorCode: "",
            durationMs: 10,
            fileRevision: "saved",
            model: "test-model",
          },
        };
      return base;
    },
  );
});
async function openAndRead() {
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Configure model" }));
  await user.type(screen.getByLabelText("Linux password"), "fixture-password");
  await user.click(screen.getByRole("button", { name: "Read configuration" }));
  await screen.findByLabelText(configPath);
  return user;
}
it("previews the first granted key without writing, then saves before testing the new revision", async () => {
  const client = mount();
  const user = await openAndRead();
  expect(
    screen.getByRole("combobox", { name: "API key name" }),
  ).toHaveTextContent("Default in grant order");
  await user.click(screen.getByRole("button", { name: "Load models" }));
  await user.click(
    await screen.findByRole("button", { name: "Preview selection" }),
  );
  await waitFor(() =>
    expect(screen.getByLabelText(authPath)).toHaveValue(
      '{"OPENAI_API_KEY":"fixture-key"}',
    ),
  );
  expect(api.runtimeModelConfig.mock.calls.some((c) => c[2] === "save")).toBe(
    false,
  );
  expect(
    api.runtimeModelConfig.mock.calls.find((c) => c[2] === "preview")?.[3],
  ).toMatchObject({ apiKeyId: "key-b", expectedRevision: "original" });
  await user.click(screen.getByRole("button", { name: "Save and test CLI" }));
  await screen.findByText("Test passed");
  const calls = api.runtimeModelConfig.mock.calls;
  expect(calls.map((c) => c[2]).slice(-2)).toEqual(["save", "test"]);
  expect(calls.at(-1)?.[3]).toMatchObject({
    expectedRevision: "saved",
    password: "fixture-password",
  });
  await user.click(screen.getAllByRole("button", { name: "Close" }).at(-1)!);
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  await user.click(screen.getByRole("button", { name: "Configure model" }));
  expect(screen.getByLabelText("Linux password")).toHaveValue("");
  expect(screen.queryByLabelText(authPath)).not.toBeInTheDocument();
  await waitFor(() =>
    expect(client.getMutationCache().getAll()).toHaveLength(0),
  );
});
it("keeps the edited files on save conflict and does not continue to a CLI test", async () => {
  mount();
  const user = await openAndRead();
  await user.type(screen.getByLabelText(configPath), "model='mine'");
  await user.type(screen.getByLabelText(authPath), "{{}");
  api.runtimeModelConfig.mockRejectedValueOnce(
    Object.assign(new Error("Revision conflict"), { status: 409 }),
  );
  await user.click(screen.getByRole("button", { name: "Save and test CLI" }));
  await screen.findByText(en.model_configuration.conflict);
  expect(screen.getByLabelText(configPath)).toHaveValue("model='mine'");
  expect(screen.getByLabelText("Linux password")).toHaveValue(
    "fixture-password",
  );
  expect(api.runtimeModelConfig.mock.calls.some((c) => c[2] === "test")).toBe(
    false,
  );
  expect(
    within(screen.getByRole("alert")).getByRole("button", {
      name: "Copy error details",
    }),
  ).toBeEnabled();
});
it("allows existing configurations to be read and edited when no key is assigned", async () => {
  api.getRuntimeModelKeys.mockResolvedValue([]);
  api.runtimeModelConfig.mockResolvedValue({
    ...base,
    snapshot: {
      revision: "existing",
      files: { [configPath]: "model='custom'", [authPath]: "{}" },
    },
  });
  mount();
  await openAndRead();
  expect(screen.getByText(en.model_configuration.no_keys)).toBeInTheDocument();
  expect(screen.getByLabelText(configPath)).toHaveValue("model='custom'");
  expect(
    screen.getByRole("button", { name: "Test CLI connection" }),
  ).toBeEnabled();
});

it("checks without a password and refreshes computer queries only after writes or CLI tests", async () => {
  const client = mount();
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const user = await openAndRead();
  await user.type(screen.getByLabelText(configPath), "model='mine'");
  await user.click(screen.getByRole("button", { name: en.model_configuration.validate }));
  await waitFor(() => expect(api.runtimeModelConfig.mock.calls.at(-1)?.[2]).toBe("check"));
  expect(api.runtimeModelConfig.mock.calls.at(-1)?.[3]).toEqual({ files: { [configPath]: "model='mine'" } });
  expect(invalidate).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: en.model_configuration.save }));
  await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["computers", "owner"] }));
});

it("shows an unconfigured source as guidance and keeps manual configuration accessible", async () => {
  api.getRuntimeModelKeys.mockRejectedValue(Object.assign(new Error("Source is not configured"), { body: { code: "source_not_configured" } }));
  mount();
  await openAndRead();
  expect(await screen.findByText(en.model_configuration.errors.source_not_configured)).toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(screen.getByLabelText(configPath)).toBeEnabled();
});
