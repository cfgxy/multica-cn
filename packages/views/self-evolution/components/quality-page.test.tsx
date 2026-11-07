// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, cleanup, waitFor } from "@testing-library/react";
import { useMemo, useState, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import type {
  PromptQualityDashboard,
  SelfEvolutionModelConfig,
} from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { QualityPage, QualityPageBody } from "./quality-page";

/**
 * The quality page (RUYI-551 §2.5, fig 7): a four-state status card per data
 * source and a degraded banner that is visible without a hover — the banner
 * replaces the old in-tab tooltip footnote (walk #8). Source semantics follow
 * §2.5 v1.1: only the module-config source can sit in "error"; a
 * deployment default never claims a check it never ran. The measure surface
 * itself is covered by quality-tab.test.tsx and stubbed here.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

vi.mock("@multica/core/hooks", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/hooks")>("@multica/core/hooks");
  return { ...actual, useWorkspaceId: () => "ws-1" };
});

vi.mock("./quality-tab", async () => {
  const React = await import("react");
  return {
    QualityTab: () => {
      const [draft, setDraft] = React.useState("");
      return <input aria-label="Quality draft" value={draft} onChange={(event) => setDraft(event.target.value)} />;
    },
  };
});

vi.mock("./versions-tab", async () => {
  const React = await import("react");
  return {
    VersionsTab: () => {
      const [draft, setDraft] = React.useState("");
      return <input aria-label="Versions draft" value={draft} onChange={(event) => setDraft(event.target.value)} />;
    },
  };
});

vi.mock("./quiz-tab", async () => {
  const React = await import("react");
  return {
    QuizTab: () => {
      const [draft, setDraft] = React.useState("");
      return <input aria-label="Quiz draft" value={draft} onChange={(event) => setDraft(event.target.value)} />;
    },
  };
});

const m = vi.hoisted(() => ({
  config: null as SelfEvolutionModelConfig | null,
  dashboard: null as PromptQualityDashboard | null,
  push: vi.fn(),
  goBack: (() => {}) as () => void,
  goForward: (() => {}) as () => void,
}));

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
    promptQualityDashboardOptions: (
      wsId: string,
      scope: string,
      scopeId: string,
      days: number,
    ) => ({
      queryKey: ["prompt-quality", wsId, scope, scopeId, days],
      queryFn: () => Promise.resolve(m.dashboard),
      enabled: scopeId !== "",
    }),
  };
});

function makeAdapter(
  overrides: Partial<NavigationAdapter> = {},
): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/test-workspace/self-evolution/quality",
    searchParams: new URLSearchParams(),
    hash: "#quality-status-scoring",
    getShareableUrl: (p) => p,
    ...overrides,
  };
}

function dashboardFixture(degraded: boolean, langfuseAvailable: boolean) {
  return {
    scope: "workspace",
    scope_id: "ws-1",
    since: "2026-09-01",
    window: { version: 0, days: 30, runs: 0, measures: {} },
    versions: [],
    perplexity: [],
    data_sources: {
      degraded,
      items: [
        { kind: "platform", available: true, required: true, degraded: false },
        { kind: "langfuse", available: langfuseAvailable, required: false, degraded: !langfuseAvailable },
      ],
    },
  } as unknown as PromptQualityDashboard;
}

function configFixture(
  resolved: SelfEvolutionModelConfig["resolved"],
  override: SelfEvolutionModelConfig["override"] = null,
): SelfEvolutionModelConfig {
  return {
    override,
    resolved,
    scoring_enabled: true,
    encryption_ready: true,
  };
}

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceSlugProvider slug="test-workspace">
        <NavigationProvider value={makeAdapter()}>
          <I18nProvider locale="en" resources={TEST_RESOURCES}>
            <QualityPageBody wsId="ws-1" />
          </I18nProvider>
        </NavigationProvider>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
}

function NavigationHarness({ children, initialSearch }: { children: ReactNode; initialSearch: string }) {
  const [search, setSearch] = useState(initialSearch);
  const [history, setHistory] = useState([initialSearch]);
  const [index, setIndex] = useState(0);
  const adapter = useMemo<NavigationAdapter>(() => ({
    push: (path) => {
      m.push(path);
      const nextSearch = new URL(path, "http://test.local").search;
      setHistory((current) => [...current.slice(0, index + 1), nextSearch]);
      setIndex((current) => current + 1);
      setSearch(nextSearch);
    },
    replace: (path) => setSearch(path.includes("?") ? path.slice(path.indexOf("?")) : ""),
    back: () => {},
    pathname: "/test-workspace/self-evolution/quality",
    searchParams: new URLSearchParams(search),
    hash: "#quality-status-scoring",
    getShareableUrl: (path) => path,
  }), [index, search]);

  m.goBack = () => {
    if (index === 0) return;
    const nextIndex = index - 1;
    setIndex(nextIndex);
    setSearch(history[nextIndex] ?? "");
  };
  m.goForward = () => {
    if (index >= history.length - 1) return;
    const nextIndex = index + 1;
    setIndex(nextIndex);
    setSearch(history[nextIndex] ?? "");
  };

  return <NavigationProvider value={adapter}>{children}</NavigationProvider>;
}

function mountPage(initialSearch = "") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceSlugProvider slug="test-workspace">
        <NavigationHarness initialSearch={initialSearch}>
          <I18nProvider locale="en" resources={TEST_RESOURCES}>
            <QualityPage />
          </I18nProvider>
        </NavigationHarness>
      </WorkspaceSlugProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  m.config = configFixture({ status: "unconfigured", source: "" });
  m.dashboard = dashboardFixture(false, false);
});

describe("QualityPageBody", () => {
  it("surfaces degraded data as a banner, not a hover-only footnote", async () => {
    m.dashboard = dashboardFixture(true, false);
    mount();

    const banner = await screen.findByTestId("quality-degraded-banner");
    expect(banner.textContent).toContain(
      "Some data sources are degraded — recent numbers may be incomplete.",
    );
    const link = screen.getByRole("link", { name: "View data sources" });
    expect(link.getAttribute("href")).toBe("#quality-status-scoring");
    // The old in-tab footnote is gone for good.
    expect(screen.queryByTestId("degraded-langfuse")).toBeNull();
  });

  it("stays quiet when no source is degraded", async () => {
    m.dashboard = dashboardFixture(false, true);
    mount();
    await screen.findByTestId("quality-status-scoring");
    expect(screen.queryByTestId("quality-degraded-banner")).toBeNull();
  });

  it("maps the scoring source through the four states, deploy default without a borrowed check", async () => {
    // ok + module config, validated recently
    m.config = configFixture(
      { status: "ok", source: "module_config" },
      {
        base_url: "https://gw/v1",
        model: "gpt-4o-mini",
        has_api_key: true,
        scoring_enabled: true,
        last_validated_at: "2026-10-01T08:00:00Z",
        last_validation_ok: true,
      },
    );
    mount();
    // The config query resolves async — wait for the reason line, not the card.
    await screen.findByText(/Connectivity check passed at/);
    const okCard = screen.getByTestId("quality-status-scoring");
    expect(okCard.querySelector("[data-status]")?.getAttribute("data-status")).toBe("ok");
    expect(okCard.textContent).toContain("Source: Module config");

    // ok + deploy default: healthy, but it never ran a check of its own.
    cleanup();
    m.config = configFixture({ status: "ok", source: "deploy_default" });
    mount();
    await screen.findByText(/A deployment-injected default is in effect/);
    const deployCard = screen.getByTestId("quality-status-scoring");
    expect(
      deployCard.querySelector("[data-status]")?.getAttribute("data-status"),
    ).toBe("ok");
    expect(deployCard.textContent).toContain("Source: Deploy default");
    expect(deployCard.textContent).not.toContain("Connectivity check passed");

    // error: the only state that names a config failure, with the reason.
    cleanup();
    m.config = configFixture(
      { status: "error", source: "module_config" },
      {
        base_url: "https://gw/v1",
        model: "gpt-4o-mini",
        has_api_key: true,
        scoring_enabled: true,
        last_validation_ok: false,
        last_validation_error: "401 from gateway",
      },
    );
    mount();
    await screen.findByText(/401 from gateway/);
    const errorCard = screen.getByTestId("quality-status-scoring");
    expect(
      errorCard.querySelector("[data-status]")?.getAttribute("data-status"),
    ).toBe("error");
    expect(screen.getByRole("link", { name: "View error" })).toBeTruthy();

    // disabled: healthy but switched off — an action, not a confession of failure.
    cleanup();
    m.config = configFixture({ status: "disabled", source: "" });
    mount();
    await screen.findByText("The source is healthy but the workspace switch is off.");
    const disabledCard = screen.getByTestId("quality-status-scoring");
    expect(
      disabledCard.querySelector("[data-status]")?.getAttribute("data-status"),
    ).toBe("disabled");
    expect(screen.getByRole("link", { name: "Enable" })).toBeTruthy();
  });

  it("keeps Langfuse a three-state system-level readout", async () => {
    mount();
    // The dashboard query resolves async — wait for the state copy.
    await screen.findByText("No conversation export.");
    const card = screen.getByTestId("quality-status-langfuse");
    expect(card.querySelector("[data-status]")?.getAttribute("data-status")).toBe("unconfigured");
    expect(
      screen.getByRole("button", { name: "Deployment config only" }),
    ).toBeTruthy();
    cleanup();

    m.dashboard = dashboardFixture(false, true);
    mount();
    await screen.findByText("Conversation export can run.");
    const okCard = screen.getByTestId("quality-status-langfuse");
    expect(okCard.querySelector("[data-status]")?.getAttribute("data-status")).toBe("ok");
    expect(okCard.textContent).toContain("Source: System-level");
    // No workspace-side action exists for an instance-level source.
    expect(screen.queryByRole("button", { name: "Deployment config only" })).toBeNull();
  });

  it("drives tabs from the URL, pushes history, and keeps inactive tab state mounted", async () => {
    mountPage("?days=90");
    const qualityDraft = screen.getByRole("textbox", { name: "Quality draft" });
    fireEvent.change(qualityDraft, { target: { value: "unsaved quality" } });

    fireEvent.click(screen.getByRole("tab", { name: "Versions" }));
    await waitFor(() => expect(m.push).toHaveBeenLastCalledWith(
      "/test-workspace/self-evolution/quality?days=90&tab=versions#quality-status-scoring",
    ));
    const versionsDraft = await screen.findByRole("textbox", { name: "Versions draft" });
    fireEvent.change(versionsDraft, { target: { value: "unsaved version" } });

    fireEvent.click(screen.getByRole("tab", { name: "Quiz" }));
    await waitFor(() => expect(m.push).toHaveBeenLastCalledWith(
      "/test-workspace/self-evolution/quality?days=90&tab=quiz#quality-status-scoring",
    ));
    fireEvent.change(await screen.findByRole("textbox", { name: "Quiz draft" }), {
      target: { value: "unsaved quiz" },
    });

    m.goBack();
    await waitFor(() => expect(screen.getByRole("tab", { name: "Versions" }).getAttribute("aria-selected")).toBe("true"));
    expect(screen.getByRole("textbox", { name: "Versions draft" })).toHaveValue("unsaved version");
    m.goBack();
    await waitFor(() => expect(screen.getByRole("tab", { name: "Quality & measures" }).getAttribute("aria-selected")).toBe("true"));
    expect(screen.getByRole("textbox", { name: "Quality draft" })).toHaveValue("unsaved quality");
    m.goForward();
    await waitFor(() => expect(screen.getByRole("tab", { name: "Versions" }).getAttribute("aria-selected")).toBe("true"));
    m.goForward();
    await waitFor(() => expect(screen.getByRole("tab", { name: "Quiz" }).getAttribute("aria-selected")).toBe("true"));
    expect(screen.getByRole("textbox", { name: "Quiz draft" })).toHaveValue("unsaved quiz");
  });
});
