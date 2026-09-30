// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { ApiError } from "@multica/core/api";
import type { PromptQualityDashboard } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import enPromptMarket from "../../locales/en/prompt-market.json";
import { VersionsTab } from "./versions-tab";

/**
 * The version lifecycle tab's wiring (RUYI-285).
 *
 * What only a mount can show is asserted here: the lifecycle actions exist and
 * reach their mutations with the right payloads (save carries the preloaded
 * effective content plus the change note; switch carries the target version),
 * a non-owner sees no write controls, and the per-version quality binding is
 * visible on the row.
 */

const TEST_RESOURCES = {
  en: {
    common: enCommon,
    "self-evolution": enSelfEvolution,
    "prompt-market": enPromptMarket,
  },
};

const state = vi.hoisted(() => {
  function version(n: number, source: string, note: string) {
    return {
      id: `pv-${n}`,
      scope: "agent",
      scope_id: "agent-1",
      version: n,
      content: n === 1 ? "first baseline\n" : "first baseline\nsecond line\n",
      content_sha256: `sha-${n}`,
      source,
      change_note: note,
      scanner_revision: "rev-1",
      created_at: "2026-09-29T12:00:00.000Z",
    };
  }
  return {
    role: "owner" as string | null,
    agents: [{ id: "agent-1", name: "Gu Xiaoyu" }],
    instructions: "live instructions text",
    versions: {
      versions: [
        version(2, "edit", "收紧工具使用纪律"),
        version(1, "import", "v1 基线：创建时内容"),
      ],
      total: 2,
    },
    qualityVersions: [
      { version: 2, runs: 12 },
      { version: 1, runs: 0 },
    ],
    saved: [] as unknown[],
    switched: [] as unknown[],
    saveError: null as unknown,
  };
});

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({
    queryKey: ["agents"],
    queryFn: () => Promise.resolve(state.agents),
  }),
  agentDetailOptions: (_wsId: string, agentId: string) => ({
    queryKey: ["agent", agentId],
    queryFn: () =>
      Promise.resolve({ id: agentId, name: "Gu Xiaoyu", instructions: state.instructions }),
  }),
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ userId: "u-1", role: state.role, member: null, isLoading: false }),
}));

vi.mock("@multica/core/api", () => ({
  api: {},
  ApiError: class ApiError extends Error {
    status: number;
    body: unknown;
    constructor(message: string, status: number, _statusText: string, body?: unknown) {
      super(message);
      this.status = status;
      this.body = body;
    }
  },
  clientErrorMessage: (e: unknown) => (e instanceof Error ? e.message : undefined),
}));

vi.mock("@multica/core/self-evolution", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/self-evolution")>(
      "@multica/core/self-evolution",
    );
  return {
    ...actual,
    promptGovernanceVersionsOptions: () => ({
      queryKey: ["prompt-governance-versions", "agent-1"],
      queryFn: () => Promise.resolve(state.versions),
    }),
    promptQualityDashboardOptions: () => ({
      queryKey: ["prompt-quality", "agent-1"],
      queryFn: () =>
        Promise.resolve({
          versions: state.qualityVersions,
        } as unknown as PromptQualityDashboard),
    }),
    useSavePromptVersion: () => ({
      isPending: false,
      mutate: (body: unknown) => {
        // A refused save lands nowhere; the component reads the error below.
        if (state.saveError) return;
        state.saved.push(body);
      },
      error: state.saveError,
    }),
    useSwitchPromptVersion: () => ({
      isPending: false,
      mutate: (version: number) => state.switched.push(version),
      error: null,
    }),
  };
});

function renderTab({ initialAgentId = "agent-1" }: { initialAgentId?: string } = {}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <VersionsTab wsId="ws-1" initialAgentId={initialAgentId} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  state.role = "owner";
  state.saved = [];
  state.switched = [];
  state.saveError = null;
});

describe("VersionsTab", () => {
  it("renders the version line with source, note and the quality binding", async () => {
    renderTab();

    await screen.findByText("收紧工具使用纪律");
    // Source labels, newest first.
    expect(screen.getByText("Edit")).toBeInTheDocument();
    expect(screen.getByText("Baseline")).toBeInTheDocument();
    // Quality evaluation binding, per version.
    expect(screen.getByText("12 runs")).toBeInTheDocument();
    expect(screen.getByText("No quality data")).toBeInTheDocument();
  });

  it("hides every write control from a non-owner", async () => {
    state.role = "member";
    renderTab();

    await screen.findByText("收紧工具使用纪律");
    expect(screen.queryByRole("button", { name: /new version/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /activate/i })).not.toBeInTheDocument();
    // Reads, including comparison, stay available.
    expect(screen.getAllByRole("button", { name: /^v\d+$/ }).length).toBeGreaterThan(0);
  });

  it("blocks the new-version entry until a subject is picked", async () => {
    renderTab({ initialAgentId: "" });

    // The empty-state copy replaces the version line, and the entry is inert.
    await screen.findByText("Pick a subject");
    const newButton = screen.getByRole("button", { name: /new version/i });
    expect(newButton).toBeDisabled();

    // Clicking opens no editor, so no save request can ever leave the tab.
    fireEvent.click(newButton);
    expect(screen.queryByRole("button", { name: /save version/i })).not.toBeInTheDocument();
    expect(state.saved).toHaveLength(0);
  });

  it("saves an edited draft as a new version with the change note", async () => {
    renderTab();

    fireEvent.click(await screen.findByRole("button", { name: /new version/i }));
    const content = await screen.findByRole("textbox", { name: /prompt content/i });
    expect(content).toHaveValue("live instructions text");
    fireEvent.change(screen.getByLabelText(/change note/i), {
      target: { value: "tighten tool discipline" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save version/i }));

    await waitFor(() => expect(state.saved).toHaveLength(1));
    expect(state.saved[0]).toEqual({
      content: "live instructions text",
      change_note: "tighten tool discipline",
    });
  });

  it("surfaces a secret-scan block instead of pretending the save landed", async () => {
    state.saveError = new ApiError(
      "the prompt appears to contain credentials and cannot be published",
      422,
      "Unprocessable Entity",
      {
        code: "prompt_secret_detected",
        findings: [{ category: "credential", rule: "aws_key", line: 3, mask: "AKIA****" }],
        truncated: false,
      },
    );
    renderTab();

    fireEvent.click(await screen.findByRole("button", { name: /new version/i }));
    fireEvent.click(await screen.findByRole("button", { name: /save version/i }));

    expect(state.saved).toHaveLength(0);
    expect(
      await screen.findByText("Not saved: the prompt appears to contain credentials."),
    ).toBeInTheDocument();
    expect(screen.getByText("credential")).toBeInTheDocument();
    expect(screen.getByText("aws_key")).toBeInTheDocument();
    expect(screen.getByText("line 3")).toBeInTheDocument();
  });

  it("activates a historical version through the copy-forward confirm", async () => {
    renderTab();

    // The newest row is already effective; the first Activate belongs to v1.
    fireEvent.click((await screen.findAllByRole("button", { name: /activate/i }))[0]!);
    fireEvent.click(await screen.findByRole("button", { name: /confirm/i }));

    await waitFor(() => expect(state.switched).toEqual([1]));
  });

  it("compares two selected versions line by line", async () => {
    renderTab();

    const pickers = await screen.findAllByRole("button", { name: /^v\d+$/ });
    fireEvent.click(pickers[0]!);
    fireEvent.click(pickers[1]!);

    // Picking the second version opens the comparison by itself.
    expect(await screen.findByText(/Compare v/)).toBeInTheDocument();
    expect(screen.getByText(/1 removed/)).toBeInTheDocument();
  });
});
