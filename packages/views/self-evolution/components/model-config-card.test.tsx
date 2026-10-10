// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import type { SelfEvolutionModelConfig } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { ModelConfigCard } from "./model-config-card";

/**
 * The one model-service card (RUYI-551 §3). What only a mount can show:
 * save is validate-first (a failed check never reaches the PUT — the server
 * refuses too, but here the classified reason lands next to the fields),
 * the stored key is never echoed (walk #13), and restore-default sits
 * behind a confirmation. The two consumers are named on the module-config
 * page variant and nowhere else.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const m = vi.hoisted(() => ({
  role: "owner" as string | null,
  config: null as SelfEvolutionModelConfig | null,
  validateResult: { ok: true, error_kind: "", message: "" },
  validateMutate: vi.fn(),
  saveMutate: vi.fn(),
  restoreMutate: vi.fn(),
  push: vi.fn(),
}));

vi.mock("@multica/core/permissions", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/permissions")>(
      "@multica/core/permissions",
    );
  return {
    ...actual,
    useCurrentMember: () => ({
      userId: "u-1",
      role: m.role as never,
      member: null,
      isLoading: false,
    }),
  };
});

vi.mock("@multica/core/self-evolution", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/self-evolution")>(
      "@multica/core/self-evolution",
    );
  return {
    ...actual,
    selfEvolutionModelConfigOptions: (wsId: string) => ({
      queryKey: ["se-model-config", wsId],
      queryFn: () => Promise.resolve(m.config),
    }),
    useValidateSelfEvolutionModelConfig: () => ({
      mutate: m.validateMutate.mockImplementation((_vars, opts) => {
        opts?.onSuccess?.(m.validateResult);
      }),
      isPending: false,
    }),
    useSaveSelfEvolutionModelConfig: () => ({
      mutate: m.saveMutate.mockImplementation((_vars, opts) => {
        opts?.onSuccess?.();
      }),
      isPending: false,
    }),
    useRestoreSelfEvolutionModelDefault: () => ({
      mutate: m.restoreMutate.mockImplementation((_vars, opts) => {
        opts?.onSuccess?.();
      }),
      isPending: false,
    }),
  };
});

function makeAdapter(
  overrides: Partial<NavigationAdapter> = {},
): NavigationAdapter {
  return {
    push: m.push,
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/test-workspace/self-evolution/config",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p) => p,
    ...overrides,
  };
}

function configFixture(
  overrides: Partial<SelfEvolutionModelConfig> = {},
): SelfEvolutionModelConfig {
  return {
    override: null,
    resolved: { status: "unconfigured", source: "" },
    scoring_enabled: true,
    encryption_ready: true,
    ...overrides,
  };
}

function mount(showConsumers = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceSlugProvider slug="test-workspace">
        <NavigationProvider value={makeAdapter()}>
          <I18nProvider locale="en" resources={TEST_RESOURCES}>
            <ModelConfigCard wsId="ws-1" showConsumers={showConsumers} />
          </I18nProvider>
        </NavigationProvider>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
}

async function fillAndSave(baseUrl: string, model: string) {
  await userEvent.type(await screen.findByLabelText("Base URL"), baseUrl);
  await userEvent.type(screen.getByLabelText("API key"), "sk-live-secret");
  await userEvent.type(screen.getByLabelText("Model"), model);
  await userEvent.click(screen.getByRole("button", { name: "Validate and save" }));
}

beforeEach(() => {
  m.config = configFixture();
  m.validateResult = { ok: true, error_kind: "", message: "" };
  m.validateMutate.mockReset();
  m.saveMutate.mockReset();
  m.restoreMutate.mockReset();
  m.push.mockClear();
});

describe("ModelConfigCard", () => {
  it("never persists a config that failed its connectivity check", async () => {
    m.validateResult = {
      ok: false,
      error_kind: "credentials",
      message: "401 from gateway",
    };
    mount();
    await fillAndSave("https://gw.example.com/v1", "gpt-4o-mini");

    await waitFor(() =>
      expect(screen.getByTestId("model-config-validation-error")).toBeTruthy(),
    );
    expect(screen.getByTestId("model-config-validation-error").textContent).toContain(
      "Validation failed — nothing was saved: 401 from gateway",
    );
    // The PUT never fired: the classified failure stays next to the fields.
    expect(m.validateMutate).toHaveBeenCalledTimes(1);
    expect(m.saveMutate).not.toHaveBeenCalled();
  });

  it("saves only after a passing check, carrying scoring_enabled and clearing the key", async () => {
    mount();
    await fillAndSave("https://gw.example.com/v1", "gpt-4o-mini");

    await waitFor(() =>
      expect(screen.getByTestId("model-config-validation-ok")).toBeTruthy(),
    );
    expect(m.validateMutate).toHaveBeenCalledWith(
      {
        base_url: "https://gw.example.com/v1",
        api_key: "sk-live-secret",
        model: "gpt-4o-mini",
      },
      expect.anything(),
    );
    expect(m.saveMutate).toHaveBeenCalledTimes(1);
    expect(m.saveMutate).toHaveBeenCalledWith(
      {
        base_url: "https://gw.example.com/v1",
        api_key: "sk-live-secret",
        model: "gpt-4o-mini",
        scoring_enabled: true,
      },
      expect.anything(),
    );
    // After a successful save the typed key is dropped from the DOM.
    expect((screen.getByLabelText("API key") as HTMLInputElement).value).toBe("");
  });

  it("never echoes the stored key and marks the emptiness as 'keep existing'", async () => {
    m.config = configFixture({
      override: {
        base_url: "https://gw.example.com/v1",
        model: "gpt-4o-mini",
        has_api_key: true,
        scoring_enabled: true,
      },
    });
    mount();

    const key = (await screen.findByLabelText("API key")) as HTMLInputElement;
    await waitFor(() =>
      expect((screen.getByLabelText("Base URL") as HTMLInputElement).value).toBe(
        "https://gw.example.com/v1",
      ),
    );
    // The server returns has_api_key only — there is nothing to render.
    expect(key.value).toBe("");
    expect(key.placeholder).toBe("Leave empty to keep the existing value");
  });

  it("restores the deployment default only through the confirmation", async () => {
    m.config = configFixture({
      override: {
        base_url: "https://gw.example.com/v1",
        model: "gpt-4o-mini",
        has_api_key: true,
        scoring_enabled: true,
      },
    });
    mount();

    await userEvent.click(
      await screen.findByRole("button", { name: "Restore deploy default" }),
    );
    const confirm = await screen.findByRole("alertdialog");
    expect(m.restoreMutate).not.toHaveBeenCalled();
    expect(
      within(confirm).getByText(
        "This removes this workspace's override; the deployment default applies again. Continue?",
      ),
    ).toBeTruthy();
    await userEvent.click(
      within(confirm).getByRole("button", { name: "Restore deploy default" }),
    );
    expect(m.restoreMutate).toHaveBeenCalledTimes(1);
  });

  it("names the scoring consumer only on the module-config variant", async () => {
    const { unmount } = mount(true);
    const consumers = await screen.findByTestId("model-config-consumers");
    expect(within(consumers).getByText("Quality scoring")).toBeTruthy();
    // RUYI-552's agent-based rework took the daily retrospective off this
    // endpoint, so it must not be listed as a consumer of the model config.
    expect(within(consumers).queryByText("Daily retrospective")).toBeNull();
    unmount();

    // The overview/quality variant stays silent about the switches.
    mount();
    await screen.findAllByLabelText("Base URL");
    expect(screen.queryByTestId("model-config-consumers")).toBeNull();
  });

  // P2-1: the stored-config re-check entry is persistent, not error-only —
  // a failed re-check is what makes the error state reachable from pure UI
  // operations (RevalidateStored persists it server-side).
  it("offers re-check outside the error state and surfaces its failure", async () => {
    m.config = configFixture({
      override: {
        base_url: "https://gw.example.com/v1",
        model: "gpt-4o-mini",
        has_api_key: true,
        scoring_enabled: true,
      },
      resolved: { status: "ok", source: "module_config", model: "gpt-4o-mini" },
    });
    mount();

    const recheck = await screen.findByRole("button", { name: "Re-check" });
    expect(m.validateMutate).not.toHaveBeenCalled();
    m.validateResult = {
      ok: false,
      error_kind: "credentials",
      message: "网关拒绝凭据（HTTP 401）",
    };
    await userEvent.click(recheck);
    // No payload: the re-check targets the stored config, not the form.
    expect(m.validateMutate).toHaveBeenCalledWith(undefined, expect.anything());
    await waitFor(() =>
      expect(
        screen.getByTestId("model-config-validation-error").textContent,
      ).toContain("网关拒绝凭据（HTTP 401）"),
    );
  });

  // The deploy-default source never promised a check (walkthrough #23): no
  // stored config to re-check, so no entry — the card stays green.
  it("offers no re-check where no stored config exists", async () => {
    m.config = configFixture({
      resolved: { status: "ok", source: "deploy_default", model: "env-model" },
    });
    mount();

    await screen.findAllByLabelText("Base URL");
    expect(screen.queryByRole("button", { name: "Re-check" })).toBeNull();
  });
});
