// @vitest-environment jsdom

import { it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { KnowledgeDir, KnowledgeEntry } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { KnowledgeTab } from "./knowledge-tab";

/**
 * The knowledge tab's wiring (RUYI-265 §K).
 *
 * The mirror's read-only scan and the single-write adoption path are enforced
 * by the server and covered by its handler tests. What only a mount can show
 * is asserted here: that every directory control is hidden from a non-owner,
 * that the ultimate library offers no scan/unregister despite being owner
 * visible, and that adoption is offered exactly for pending entries while
 * adopted ones show their provenance line.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const state = vi.hoisted(() => ({
  role: "owner" as string | null,
  dirs: [] as KnowledgeDir[],
  entries: [] as KnowledgeEntry[],
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ userId: "u-1", role: state.role, member: null, isLoading: false }),
}));

vi.mock("@multica/core/api", () => ({
  clientErrorMessage: (e: unknown) => (e instanceof Error ? e.message : undefined),
  api: {
    listKnowledgeDirs: () => Promise.resolve(state.dirs),
    listKnowledgeEntries: () => Promise.resolve(state.entries),
    registerKnowledgeDir: vi.fn(),
    scanKnowledgeDir: vi.fn(),
    unregisterKnowledgeDir: vi.fn(),
    adoptKnowledgeEntry: vi.fn(),
  },
}));

function dirFixture(overrides: Partial<KnowledgeDir> = {}): KnowledgeDir {
  return {
    id: "dir-1",
    kind: "candidate_cli",
    path: "/srv/memories/project-x",
    label: "project-x memories",
    health_state: "ok",
    health_note: "",
    removed: false,
    entry_count: 2,
    last_scan: {
      id: "batch-1",
      dir_id: "dir-1",
      trigger_source: "manual",
      result: "changed",
      added: 1,
      updated: 0,
      removed: 0,
      started_at: "2026-09-29T00:00:00Z",
      finished_at: "2026-09-29T00:00:01Z",
    },
    created_at: "2026-09-29T00:00:00Z",
    updated_at: "2026-09-29T00:00:00Z",
    ...overrides,
  };
}

function entryFixture(overrides: Partial<KnowledgeEntry> = {}): KnowledgeEntry {
  return {
    id: "entry-1",
    dir_id: "dir-1",
    key: "deploy-rollback",
    content: "Roll back via the tagged release, never via force-push.",
    content_sha256: "0".repeat(64),
    mirror_state: "synced",
    first_seen_at: "2026-09-29T00:00:00Z",
    last_confirmed_at: "2026-09-29T00:00:00Z",
    adoption_state: "pending",
    ...overrides,
  };
}

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <KnowledgeTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

it("shows the mirror to a member with no write controls at all", async () => {
  state.role = "admin";
  state.dirs = [dirFixture()];
  state.entries = [entryFixture()];
  mount();
  await waitFor(() => expect(screen.getByText("project-x memories")).toBeTruthy());
  expect(screen.getByText("2 entries")).toBeTruthy();
  expect(screen.getByText(/Last scan \(changed\): \+1, ~0, -0/)).toBeTruthy();
  expect(screen.getByText("deploy-rollback")).toBeTruthy();
  expect(screen.getByText("Pending")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Register directory" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Scan now" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Unregister" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Adopt" })).toBeNull();
  expect(
    screen.getByText("Registering, scanning, unregistering and adopting are workspace-owner only."),
  ).toBeTruthy();
});

it("offers scan and unregister for candidates but not for the ultimate library", async () => {
  state.role = "owner";
  state.dirs = [
    dirFixture(),
    dirFixture({
      id: "dir-ultimate",
      kind: "ultimate",
      label: "workspace library",
      path: "/srv/memories/ultimate",
      entry_count: 7,
    }),
  ];
  state.entries = [];
  mount();
  const candidate = await screen.findByTestId("knowledge-dir-dir-1");
  expect(candidate.querySelector("button")?.textContent).toBe("Scan now");
  expect(
    Array.from(candidate.querySelectorAll("button")).some((b) => b.textContent === "Unregister"),
  ).toBe(true);
  const ultimate = screen.getByTestId("knowledge-dir-dir-ultimate");
  expect(ultimate.querySelectorAll("button")).toHaveLength(0);
  expect(screen.getByText("Ultimate library")).toBeTruthy();
});

it("offers adoption for pending entries and shows provenance for adopted ones", async () => {
  state.role = "owner";
  state.dirs = [dirFixture()];
  state.entries = [
    entryFixture({ id: "entry-p", key: "deploy-rollback" }),
    entryFixture({
      id: "entry-a",
      key: "proposal-1a2b3c4d",
      content: "Trim the preamble before the nightly sweep.",
      adoption_state: "adopted",
      adopted_at: "2026-09-29T02:00:00Z",
      adopted_by: "u-1",
      ultimate_dir_id: "dir-ultimate",
      adopted_from_dir_id: "dir-1",
      adopted_from_key: "deploy-rollback",
    }),
    entryFixture({
      id: "entry-f",
      key: "stale-entry",
      adoption_state: "failed",
      adoption_error: "bd recall mismatch",
    }),
  ];
  mount();
  expect(await screen.findByTestId("knowledge-entry-deploy-rollback")).toBeTruthy();
  const pendingRow = screen.getByTestId("knowledge-entry-deploy-rollback");
  expect(
    Array.from(pendingRow.querySelectorAll("button")).some((b) => b.textContent === "Adopt"),
  ).toBe(true);
  const adoptedRow = screen.getByTestId("knowledge-entry-proposal-1a2b3c4d");
  expect(adoptedRow.querySelectorAll("button")).toHaveLength(0);
  expect(screen.getByText("Adopted from deploy-rollback")).toBeTruthy();
  expect(screen.getByText("Adopted")).toBeTruthy();
  expect(screen.getByText("bd recall mismatch")).toBeTruthy();
});
