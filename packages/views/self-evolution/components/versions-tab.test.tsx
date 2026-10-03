// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { ApiError } from "@multica/core/api";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import enPromptMarket from "../../locales/en/prompt-market.json";
import { VersionsTab } from "./versions-tab";

/**
 * The version lifecycle tab's wiring after the RUYI-285 rework.
 *
 * The tab creates versions only by snapshotting a carrier's effective
 * content, so the load-bearing assertions are the negatives: no prompt
 * content editor exists anywhere in the tab, and nothing renders (or
 * requests) until a carrier is picked. The positives — the snapshot confirm
 * reaching its mutation, the timeline refreshing after it, secret-scan
 * findings surfacing instead of a fake success, copy-forward activation, and
 * the two-pick comparison — ride on the real core hooks with only the API
 * transport mocked, so the queries' no-subject guard and the post-snapshot
 * invalidation are exercised for real.
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
    projects: [{ id: "proj-1", title: "Atlas" }],
    versions: {
      versions: [
        version(2, "edit", "收紧工具使用纪律"),
        version(1, "import", "v1 基线：创建时内容"),
      ],
      total: 2,
    },
    versionRequests: [] as string[],
    qualityRequests: [] as string[],
    qualityVersions: [
      { version: 2, runs: 12 },
      { version: 1, runs: 0 },
    ],
    snapshotted: [] as unknown[],
    switched: [] as number[],
    snapshotError: null as unknown,
  };
});

vi.mock("@multica/core/api", () => ({
  api: {
    listAgents: () => Promise.resolve(state.agents),
    // Wrapped: projectListOptions selects `data.projects` off the envelope.
    listProjects: () => Promise.resolve({ projects: state.projects }),
    listPromptGovernanceVersions: (_scope: string, scopeId: string) => {
      state.versionRequests.push(scopeId);
      return Promise.resolve(state.versions);
    },
    getPromptQualityDashboard: (_scope: string, scopeId: string) => {
      state.qualityRequests.push(scopeId);
      return Promise.resolve({ versions: state.qualityVersions });
    },
    snapshotPromptGovernanceVersion: (
      _scope: string,
      _scopeId: string,
      body: unknown,
    ) => {
      // A refused snapshot lands nowhere; the component reads the error below.
      if (state.snapshotError) return Promise.reject(state.snapshotError);
      state.snapshotted.push(body);
      return Promise.resolve({
        id: "pv-3",
        scope: "agent",
        scope_id: "agent-1",
        version: 3,
        content: "first baseline\nsecond line\n",
        content_sha256: "sha-3",
        source: "snapshot",
        change_note: "Snapshot of current effective config",
        scanner_revision: "rev-1",
        created_at: "2026-09-29T12:00:00.000Z",
      });
    },
    switchPromptGovernanceVersion: (
      _scope: string,
      _scopeId: string,
      v: number,
    ) => {
      state.switched.push(v);
      return Promise.resolve({
        id: `pv-${v}`,
        scope: "agent",
        scope_id: "agent-1",
        version: v,
        content: "first baseline\nsecond line\n",
        content_sha256: `sha-${v}`,
        source: "revert",
        change_note: "",
        scanner_revision: "rev-1",
        created_at: "2026-09-29T12:00:00.000Z",
      });
    },
  },
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

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ userId: "u-1", role: state.role, member: null, isLoading: false }),
}));

function renderTab({ initialSubjectId = "agent-1" }: { initialSubjectId?: string } = {}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <VersionsTab wsId="ws-1" initialSubjectId={initialSubjectId} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  state.role = "owner";
  state.versionRequests = [];
  state.qualityRequests = [];
  state.snapshotted = [];
  state.switched = [];
  state.snapshotError = null;
});

// Explicit because the type-switch test drives real portals: a leftover
// tree from the previous test would double every global query.
afterEach(cleanup);

describe("VersionsTab", () => {
  it("renders the version line with source, note and the quality binding", async () => {
    renderTab();

    await screen.findByText("收紧工具使用纪律");
    // Source labels, newest first.
    expect(screen.getByText("Edit")).toBeInTheDocument();
    expect(screen.getByText("Baseline")).toBeInTheDocument();
    // Quality evaluation binding, per version.
    expect(screen.getByText("12 runs")).toBeInTheDocument();
    expect(screen.getByText("No quality data"));
    // One read of each kind, bound to the picked carrier.
    expect(state.versionRequests).toEqual(["agent-1"]);
    expect(state.qualityRequests).toEqual(["agent-1"]);
  });

  it("keeps the tab free of any prompt-content editor", async () => {
    renderTab();

    await screen.findByText("收紧工具使用纪律");
    // The rework removed the edit dialog: creation happens by snapshot in the
    // carrier's own feature entry, so no textarea can exist here.
    expect(document.querySelector("textarea")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /new version/i })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /snapshot/i })).toBeInTheDocument();
  });

  it("blocks the snapshot entry until a carrier is picked", async () => {
    renderTab({ initialSubjectId: "" });

    // The empty-state copy replaces the version line, and no read fires.
    await screen.findByText("Pick a subject");
    expect(state.versionRequests).toEqual([]);
    expect(state.qualityRequests).toEqual([]);

    const snapshotButton = screen.getByRole("button", { name: /snapshot/i });
    expect(snapshotButton).toBeDisabled();
    fireEvent.click(snapshotButton);
    expect(screen.queryByRole("button", { name: /take snapshot/i })).not.toBeInTheDocument();
    expect(state.snapshotted).toHaveLength(0);
  });

  it("snapshots the carrier's effective content after confirm and refreshes the line", async () => {
    renderTab();

    fireEvent.click(await screen.findByRole("button", { name: /snapshot/i }));
    fireEvent.click(await screen.findByRole("button", { name: /take snapshot/i }));

    await waitFor(() => expect(state.snapshotted).toEqual([{}]));
    // The confirm dialog closes, and the timeline refetches (the second read).
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /take snapshot/i })).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(state.versionRequests).toHaveLength(2));
  });

  it("surfaces a secret-scan block instead of pretending the snapshot landed", async () => {
    state.snapshotError = new ApiError(
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

    fireEvent.click(await screen.findByRole("button", { name: /snapshot/i }));
    fireEvent.click(await screen.findByRole("button", { name: /take snapshot/i }));

    expect(state.snapshotted).toHaveLength(0);
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

  it("clears the picked version and carrier when the type changes", async () => {
    const user = userEvent.setup();
    renderTab();

    // One pick only: two would open the comparison dialog, and a modal
    // dialog makes the pickers outside it inert.
    fireEvent.click((await screen.findAllByRole("button", { name: /^v\d+$/ }))[0]!);

    await user.click(screen.getByRole("combobox", { name: "Carrier type" }));
    await user.click(await screen.findByRole("option", { name: "Project" }));

    // The type switch also resets the entity, so the line is replaced by
    // the empty state until a new carrier is picked.
    await screen.findByText("Pick a subject");
    expect(screen.queryByRole("button", { name: /^v\d+$/ })).not.toBeInTheDocument();

    // Picking a carrier on the new type loads a fresh line; if the stale
    // version pick had survived the type switch, two picks would reopen the
    // comparison by themselves.
    await user.click(screen.getByRole("combobox", { name: "Carrier" }));
    await user.click(await screen.findByRole("option", { name: "Atlas" }));

    await screen.findByText("收紧工具使用纪律");
    expect(screen.queryByText(/Compare v/)).not.toBeInTheDocument();
  });
});
