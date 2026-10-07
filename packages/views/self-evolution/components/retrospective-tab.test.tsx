// @vitest-environment jsdom

import { it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { toast } from "sonner";
import { I18nProvider } from "@multica/core/i18n/react";
import type { RetrospectiveConfig, RetrospectiveRun } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { RetrospectiveTab } from "./retrospective-tab";

/**
 * The retrospective tab's LLM config wiring (RUYI-552).
 *
 * Resolution priority (workspace > deployment fallback > error state) is
 * enforced server-side and covered there. What only a mount can show is
 * asserted here: the PATCH body the form assembles (a typed key replaces,
 * an untouched field keeps the saved key by omitting it, the clear action
 * sends ""), the effective-config status line with its source badge and
 * issue text, and the localized 409 blocker when "run now" is rejected for
 * a missing LLM config. The key never renders in any input value — only
 * the server's masked hint does.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const { TestApiError } = vi.hoisted(() => {
  class TestApiError extends Error {
    readonly status: number;
    constructor(message: string, status: number) {
      super(message);
      this.name = "ApiError";
      this.status = status;
    }
  }
  return { TestApiError };
});

const state = vi.hoisted(() => ({
  role: "owner" as string | null,
  config: null as RetrospectiveConfig | null,
  runs: [] as RetrospectiveRun[],
  updateCalls: [] as unknown[],
  triggerResult: null as Promise<unknown> | null,
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ role: state.role }),
}));

vi.mock("@multica/core/api", () => ({
  ApiError: TestApiError,
  clientErrorMessage: (e: unknown) => (e instanceof Error ? e.message : undefined),
  api: {
    getRetrospectiveConfig: () => Promise.resolve(state.config),
    updateRetrospectiveConfig: (patch: unknown) => {
      state.updateCalls.push(patch);
      return Promise.resolve(state.config);
    },
    listRetrospectiveRuns: () => Promise.resolve(state.runs),
    triggerRetrospectiveRun: () =>
      state.triggerResult ? state.triggerResult : Promise.resolve({}),
  },
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

function config(overrides: Partial<RetrospectiveConfig> = {}): RetrospectiveConfig {
  return {
    enabled: true,
    include_in_review: false,
    window_days: 7,
    llm: {
      stored: { base_url: "", model: "", api_key_set: false, api_key_hint: "" },
      effective: {
        source: "none",
        base_url: "",
        base_url_source: "none",
        model: "",
        model_source: "none",
        api_key_source: "none",
        api_key_hint: "",
        issue: "",
      },
    },
    ...overrides,
  };
}

function renderTab() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <RetrospectiveTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function savedConfig(): RetrospectiveConfig {
  return config({
    llm: {
      stored: { base_url: "https://gw.internal/v1", model: "glm-4", api_key_set: true, api_key_hint: "abcd" },
      effective: {
        source: "workspace",
        base_url: "https://gw.internal/v1",
        base_url_source: "workspace",
        model: "glm-4",
        model_source: "workspace",
        api_key_source: "workspace",
        api_key_hint: "abcd",
        issue: "",
      },
    },
  });
}

function rejected(err: Error & { status: number }): Promise<never> {
  // Shadow-catch so the rejection is not "unhandled" while it waits for the
  // mutation to pick it up; consumers of the returned promise still see it.
  const p = Promise.reject(err);
  p.catch(() => {});
  return p;
}

beforeEach(() => {
  state.role = "owner";
  state.config = config();
  state.runs = [];
  state.updateCalls = [];
  state.triggerResult = null;
});

afterEach(cleanup);

it("sends a typed key for replacement plus the trimmed URL/model", async () => {
  renderTab();
  fireEvent.change(await screen.findByLabelText("API base URL"), {
    target: { value: " https://gw.internal/v1 " },
  });
  fireEvent.change(screen.getByLabelText("Model"), { target: { value: "glm-4" } });
  fireEvent.change(screen.getByLabelText("API key"), { target: { value: "sk-new-1234" } });
  fireEvent.click(screen.getByRole("button", { name: "Save config" }));

  await waitFor(() => expect(state.updateCalls).toHaveLength(1));
  expect(state.updateCalls[0]).toEqual({
    enabled: true,
    include_in_review: false,
    window_days: 7,
    llm: { base_url: "https://gw.internal/v1", model: "glm-4", api_key: "sk-new-1234" },
  });
});

it("omits api_key when the field is left untouched, keeping the saved key", async () => {
  state.config = savedConfig();
  renderTab();
  expect((await screen.findByPlaceholderText("Saved (ends with abcd)")).getAttribute("value")).toBe("");
  fireEvent.click(screen.getByRole("button", { name: "Save config" }));

  await waitFor(() => expect(state.updateCalls).toHaveLength(1));
  const body = state.updateCalls[0] as { llm: Record<string, unknown> };
  expect(Object.prototype.hasOwnProperty.call(body.llm, "api_key")).toBe(false);
  expect(body.llm.base_url).toBe("https://gw.internal/v1");
  expect(body.llm.model).toBe("glm-4");
});

it("sends an empty api_key after the clear action", async () => {
  state.config = savedConfig();
  renderTab();
  await screen.findByPlaceholderText("Saved (ends with abcd)");
  fireEvent.click(screen.getByRole("button", { name: "Clear saved key" }));
  fireEvent.click(screen.getByRole("button", { name: "Save config" }));

  await waitFor(() => expect(state.updateCalls).toHaveLength(1));
  const body = state.updateCalls[0] as { llm: Record<string, unknown> };
  expect(body.llm.api_key).toBe("");
});

it("shows the effective source badge and names a config problem", async () => {
  state.config = config({
    llm: {
      stored: { base_url: "", model: "", api_key_set: true, api_key_hint: "abcd" },
      effective: {
        source: "none",
        base_url: "",
        base_url_source: "none",
        model: "",
        model_source: "none",
        api_key_source: "workspace",
        api_key_hint: "",
        issue: "Saved LLM API key decryption failed (key may have rotated), save it again",
      },
    },
  });
  renderTab();

  expect(await screen.findByText("Not configured")).toBeTruthy();
  expect(
    screen.getByText(/Config problem: Saved LLM API key decryption failed/),
  ).toBeTruthy();
});

it("toasts the localized blocker, not the server sentence, on a 409 run rejection", async () => {
  state.triggerResult = rejected(new TestApiError("LLM 未配置，无法运行复盘", 409));
  renderTab();
  fireEvent.click(await screen.findByRole("button", { name: "Run now" }));

  await waitFor(() => expect(vi.mocked(toast.error)).toHaveBeenCalled());
  expect(vi.mocked(toast.error)).toHaveBeenCalledWith(
    "No usable LLM config: save a configuration in the LLM section, then run again.",
  );
});

it("keeps the raw server message for non-409 run failures", async () => {
  state.triggerResult = rejected(new TestApiError("retrospective queue busy", 500));
  renderTab();
  fireEvent.click(await screen.findByRole("button", { name: "Run now" }));

  await waitFor(() => expect(vi.mocked(toast.error)).toHaveBeenCalled());
  expect(vi.mocked(toast.error)).toHaveBeenCalledWith("retrospective queue busy");
});

it("offers a Configure LLM entry on a run that failed unconfigured", async () => {
  state.runs = [
    {
      id: "run-1",
      status: "failed",
      trigger: "manual",
      window_start: "2026-10-07T00:00:00Z",
      window_end: "2026-10-08T00:00:00Z",
      issues_scanned: 0,
      issues_analyzed: 0,
      proposals_created: 0,
      proposals_merged: 0,
      duplicates_skipped: 0,
      error: "LLM 未配置，无法运行复盘",
      detail: { llm_configured: false },
      created_at: "2026-10-08T00:00:00Z",
    },
  ];
  renderTab();

  const entry = await screen.findByRole("button", { name: "Configure LLM" });
  expect(entry).toBeTruthy();
  // The fix-here entry scrolls to the config section, which stays mounted.
  expect(document.getElementById("retrospective-llm-config")).toBeTruthy();
});
