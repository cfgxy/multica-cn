// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { SelfEvolutionJevConfig } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { JevConfigCard } from "./jev-config-card";

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const state = vi.hoisted(() => ({
  role: "owner" as string | null,
  config: null as SelfEvolutionJevConfig | null,
  save: vi.fn(),
  remove: vi.fn(),
  recheck: vi.fn(),
}));

vi.mock("@multica/core/permissions", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/permissions")>("@multica/core/permissions");
  return {
    ...actual,
    useCurrentMember: () => ({ userId: "u-1", role: state.role as never, member: null, isLoading: false }),
  };
});

vi.mock("@multica/core/self-evolution", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/self-evolution")>("@multica/core/self-evolution");
  return {
    ...actual,
    selfEvolutionJevConfigOptions: (wsId: string) => ({
      queryKey: ["self-evolution-jev-config", wsId],
      queryFn: () => Promise.resolve(state.config),
    }),
    useSaveSelfEvolutionJevConfig: () => ({
      mutate: state.save.mockImplementation((_variables, options) => options?.onSuccess?.()),
      isPending: false,
    }),
    useDeleteSelfEvolutionJevConfig: () => ({
      mutate: state.remove.mockImplementation((_variables, options) => options?.onSuccess?.()),
      isPending: false,
    }),
    useRecheckSelfEvolutionJevConfig: () => ({
      mutate: state.recheck.mockImplementation((_variables, options) => options?.onSuccess?.()),
      isPending: false,
    }),
  };
});

function configFixture(overrides: Partial<SelfEvolutionJevConfig> = {}): SelfEvolutionJevConfig {
  return {
    status: "unconfigured",
    source: "unconfigured",
    endpoint: "",
    model: "",
    timeout_ms: 1500,
    has_api_key: false,
    encryption_ready: true,
    ...overrides,
  };
}

function mount() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <JevConfigCard wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  state.role = "owner";
  state.config = configFixture();
  state.save.mockReset();
  state.remove.mockReset();
  state.recheck.mockReset();
});

describe("JevConfigCard", () => {
  it("shows the offline state without echoing a stored API key", async () => {
    state.config = configFixture({
      status: "offline",
      source: "workspace",
      endpoint: "https://jev.example.com",
      model: "jev-local",
      has_api_key: true,
      last_checked_at: "2026-11-05T12:00:00Z",
      last_check_error: "unreachable",
    });
    mount();

    expect((await screen.findByTestId("jev-config-status")).textContent).toBe("Offline · Workspace override");
    expect((screen.getByLabelText("API key") as HTMLInputElement).value).toBe("");
    expect((screen.getByLabelText("API key") as HTMLInputElement).placeholder).toBe(
      "Leave empty to keep the existing value",
    );
    expect(screen.getByTestId("jev-config-error").textContent).toContain("unreachable");
  });

  it("allows an owner to save a new endpoint and only sends the typed key once", async () => {
    mount();
    await userEvent.type(await screen.findByLabelText("Endpoint"), "https://jev.example.com");
    await userEvent.type(screen.getByLabelText("API key"), "workspace-secret");
    await userEvent.type(screen.getByLabelText("Model"), "jev-local");
    await userEvent.clear(screen.getByLabelText("Timeout (ms)"));
    await userEvent.type(screen.getByLabelText("Timeout (ms)"), "1700");
    await userEvent.click(screen.getByRole("button", { name: "Save and check" }));

    expect(state.save).toHaveBeenCalledWith(
      {
        endpoint: "https://jev.example.com",
        api_key: "workspace-secret",
        model: "jev-local",
        timeout_ms: 1700,
      },
      expect.anything(),
    );
    await waitFor(() => expect((screen.getByLabelText("API key") as HTMLInputElement).value).toBe(""));
  });

  it("keeps save disabled when the timeout is outside the supported range", async () => {
    mount();
    await userEvent.type(await screen.findByLabelText("Endpoint"), "https://jev.example.com");
    await userEvent.type(screen.getByLabelText("Model"), "jev-local");
    await userEvent.clear(screen.getByLabelText("Timeout (ms)"));
    await userEvent.type(screen.getByLabelText("Timeout (ms)"), "99");

    expect((screen.getByRole("button", { name: "Save and check" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("makes the status member-visible while withholding owner controls", async () => {
    state.role = "member";
    state.config = configFixture({
      status: "available",
      source: "workspace",
      endpoint: "https://jev.example.com",
      model: "jev-local",
    });
    mount();

    expect((await screen.findByTestId("jev-config-status")).textContent).toBe("Available · Workspace override");
    expect((screen.getByLabelText("Endpoint") as HTMLInputElement).disabled).toBe(true);
    expect(screen.queryByRole("button", { name: "Save and check" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Re-check" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove configuration" })).toBeNull();
  });

  it("offers stored-config recheck and removal only to an owner", async () => {
    state.config = configFixture({
      status: "available",
      source: "workspace",
      endpoint: "https://jev.example.com",
      model: "jev-local",
    });
    mount();

    await userEvent.click(await screen.findByRole("button", { name: "Re-check" }));
    expect(state.recheck).toHaveBeenCalledWith(undefined, expect.anything());
    await userEvent.click(screen.getByRole("button", { name: "Remove configuration" }));
    expect(state.remove).toHaveBeenCalledWith(undefined, expect.anything());
  });

  it("shows a deployment default without offering workspace removal", async () => {
    state.config = configFixture({
      status: "available",
      source: "deployment",
      endpoint: "https://jev.example.com",
      model: "deployment-model",
    });
    mount();

    expect((await screen.findByTestId("jev-config-status")).textContent).toBe("Available · Deployment default");
    expect(screen.getByRole("button", { name: "Re-check" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Remove configuration" })).toBeNull();
  });
});
