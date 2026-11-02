// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { KnowledgeDir, KnowledgeEntry, KnowledgeEntryVersion, KnowledgeGovernanceRun, KnowledgeLifecycleEvent } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { KnowledgePageBody } from "./knowledge-page";

/**
 * The knowledge page (RUYI-551 §2.3, reworking RUYI-265 §K): entries are the
 * browsing default so operations stop crowding the first screen (walk #18),
 * dirs and external sources live in their own segments, rows open detail
 * drawers, adding an external source is the page CTA with the kind forced to
 * candidate_cli (RUYI-289), and every write stays owner-only — exactly as
 * the server enforces.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const m = vi.hoisted(() => ({
  role: null as string | null,
  dirs: [] as KnowledgeDir[],
  entries: [] as KnowledgeEntry[],
  versions: [] as KnowledgeEntryVersion[],
  events: [] as KnowledgeLifecycleEvent[],
  governanceRuns: [] as KnowledgeGovernanceRun[],
  governanceDetail: null as { run: KnowledgeGovernanceRun; findings: [] } | null,
  registerMutate: vi.fn(),
  scanMutate: vi.fn(),
  unregisterMutate: vi.fn(),
  adoptMutate: vi.fn(),
  editMutate: vi.fn(),
  deleteMutate: vi.fn(),
  mergeMutate: vi.fn(),
  setManagedMutate: vi.fn(),
  updateLifecycleMutate: vi.fn(),
  restoreMutate: vi.fn(),
  startGovernanceMutate: vi.fn(),
  cancelGovernanceMutate: vi.fn(),
  retryGovernanceMutate: vi.fn(),
}));

vi.mock("../../editor", () => ({
  ContentEditor: ({ value, onUpdate }: { value: string; onUpdate?: (next: string) => void }) => (
    <textarea aria-label="Knowledge content" value={value} onChange={(event) => onUpdate?.(event.target.value)} />
  ),
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
    knowledgeDirsOptions: (wsId: string) => ({
      queryKey: ["knowledge-dirs", wsId],
      queryFn: () => Promise.resolve(m.dirs),
    }),
    knowledgeEntriesOptions: (wsId: string, dirId: string, query: string, lifecycle = "active") => ({
      queryKey: ["knowledge-entries", wsId, dirId, query, lifecycle],
      queryFn: () => Promise.resolve(m.entries),
    }),
    knowledgeEntryVersionsOptions: (wsId: string, entryId: string) => ({
      queryKey: ["knowledge-versions", wsId, entryId],
      queryFn: () => Promise.resolve(m.versions),
      enabled: Boolean(entryId),
    }),
    knowledgeEntryLifecycleOptions: (wsId: string, entryId: string) => ({
      queryKey: ["knowledge-lifecycle", wsId, entryId],
      queryFn: () => Promise.resolve(m.events),
      enabled: Boolean(entryId),
    }),
    knowledgeGovernanceRunsOptions: (wsId: string) => ({
      queryKey: ["knowledge-governance-runs", wsId],
      queryFn: () => Promise.resolve(m.governanceRuns),
    }),
    knowledgeGovernanceRunOptions: (wsId: string, runId: string) => ({
      queryKey: ["knowledge-governance-run", wsId, runId],
      queryFn: () => Promise.resolve(m.governanceDetail),
      enabled: !!runId,
    }),
    useRegisterKnowledgeDir: () => ({ mutate: m.registerMutate, isPending: false }),
    useScanKnowledgeDir: () => ({ mutate: m.scanMutate, isPending: false }),
    useUnregisterKnowledgeDir: () => ({ mutate: m.unregisterMutate, isPending: false }),
    useAdoptKnowledgeEntry: () => ({ mutate: m.adoptMutate, isPending: false }),
    useEditKnowledgeEntry: () => ({ mutate: m.editMutate, isPending: false }),
    useDeleteKnowledgeEntry: () => ({ mutate: m.deleteMutate, isPending: false }),
    useMergeKnowledgeEntries: () => ({ mutate: m.mergeMutate, isPending: false }),
    useSetKnowledgeDirManaged: () => ({ mutate: m.setManagedMutate, isPending: false }),
    useUpdateKnowledgeEntryLifecycle: () => ({ mutate: m.updateLifecycleMutate, isPending: false }),
    useRestoreKnowledgeEntry: () => ({ mutate: m.restoreMutate, isPending: false }),
    useStartKnowledgeGovernanceRun: () => ({ mutate: m.startGovernanceMutate, isPending: false }),
    useCancelKnowledgeGovernanceRun: () => ({ mutate: m.cancelGovernanceMutate, isPending: false }),
    useRetryKnowledgeGovernanceRun: () => ({ mutate: m.retryGovernanceMutate, isPending: false }),
  };
});

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

function governanceRunFixture(overrides: Partial<KnowledgeGovernanceRun> = {}): KnowledgeGovernanceRun {
  return {
    id: "governance-run-1",
    workspace_id: "ws-1",
    dir_id: "dir-1",
    task_id: "task-1",
    agent_id: "agent-1",
    status: "succeeded",
    title: "Knowledge organization · project-x memories · 2026-10-31 09:00",
    prompt_version: "knowledge-governance-v1",
    trigger: "manual",
    detail: {},
    report: { counts: { valid: 1 }, backup_reference: "backup:run-1" },
    created_by: "owner-1",
    created_at: "2026-10-31T09:00:00Z",
    finished_at: "2026-10-31T09:01:00Z",
    ...overrides,
  };
}

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <KnowledgePageBody wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

async function switchSegment(name: RegExp) {
  await userEvent.click(screen.getByRole("tab", { name: name }));
}

beforeEach(() => {
  m.role = null;
  m.dirs = [];
  m.entries = [];
  m.versions = [];
  m.events = [];
  m.governanceRuns = [];
  m.governanceDetail = null;
  vi.clearAllMocks();
});

describe("KnowledgePageBody", () => {
  it("shows the read-only mirror to a non-owner with no write controls at all", async () => {
    m.role = "member";
    m.dirs = [dirFixture()];
    m.entries = [entryFixture()];
    mount();

    await waitFor(() =>
      expect(screen.getByTestId("knowledge-entry-deploy-rollback")).toBeTruthy(),
    );
    expect(screen.getByText("Synced")).toBeTruthy();
    expect(screen.getByText("Pending")).toBeTruthy();
    // No page CTA, no adopt in the row, and the owner-only note explains why.
    expect(screen.queryByTestId("knowledge-add-source")).toBeNull();
    expect(screen.queryByRole("button", { name: "Adopt" })).toBeNull();
    expect(
      screen.getByText(
        "Registering, scanning, unregistering and adopting are workspace-owner only.",
      ),
    ).toBeTruthy();
  });

  it("keeps directories and external sources off the first screen (walk #18)", async () => {
    m.role = "owner";
    m.dirs = [
      dirFixture(),
      dirFixture({
        id: "dir-ultimate",
        kind: "ultimate",
        label: "workspace library",
        path: "/srv/memories/ultimate",
        entry_count: 7,
      }),
    ];
    m.entries = [entryFixture()];
    mount();

    await waitFor(() => expect(screen.getByTestId("knowledge-entries-table")).toBeTruthy());
    // Entries are the default segment; the dir table is not on screen.
    expect(screen.queryByTestId("knowledge-dirs-table")).toBeNull();
    expect(screen.queryByTestId("knowledge-external-table")).toBeNull();

    await switchSegment(/Directories/);
    expect(screen.getByTestId("knowledge-dirs-table")).toBeTruthy();
    const ultimate = screen.getByTestId("knowledge-dir-dir-ultimate");
    expect(within(ultimate).getByRole("button", { name: "Organize knowledge" })).toBeTruthy();
    const candidate = screen.getByTestId("knowledge-dir-dir-1");
    expect(within(candidate).getByRole("button", { name: "Scan now" })).toBeTruthy();
    expect(within(candidate).getByRole("button", { name: "Unregister" })).toBeTruthy();

    // External sources exclude the knowledge hub by construction.
    await switchSegment(/External sources/);
    expect(screen.getByTestId("knowledge-external-table")).toBeTruthy();
    expect(screen.queryByTestId("knowledge-dir-dir-ultimate")).toBeNull();
    expect(screen.getByRole("button", { name: "Go to Directories" })).toBeTruthy();
  });

  it("starts a directory-scoped governance run only for an owner", async () => {
    m.role = "owner";
    m.dirs = [dirFixture()];
    mount();

    await switchSegment(/Directories/);
    await userEvent.click(screen.getByRole("button", { name: "Organize knowledge" }));
    expect(m.startGovernanceMutate).toHaveBeenCalledWith("dir-1", expect.anything());
  });

  it("shows governance reports to members without write controls", async () => {
    m.role = "member";
    m.dirs = [dirFixture()];
    const run = governanceRunFixture();
    m.governanceRuns = [run];
    m.governanceDetail = { run, findings: [] };
    mount();

    await switchSegment(/Governance runs/);
    await userEvent.click(await screen.findByTestId("knowledge-governance-run-governance-run-1"));
    const drawer = await screen.findByTestId("knowledge-governance-drawer");
    expect(within(drawer).getByText("knowledge-governance-v1")).toBeTruthy();
    expect(within(drawer).getByText(/backup:run-1/)).toBeTruthy();
    expect(within(drawer).queryByRole("button", { name: "Retry run" })).toBeNull();
    expect(within(drawer).queryByRole("button", { name: "Cancel run" })).toBeNull();
  });

  it("adds an external source through the page CTA with the kind forced to candidate_cli", async () => {
    m.role = "owner";
    mount();

    await userEvent.click(await screen.findByTestId("knowledge-add-source"));
    const dialog = await screen.findByTestId("knowledge-register-dialog");
    await userEvent.type(within(dialog).getByLabelText("Directory path"), "/srv/memories/legacy");
    await userEvent.type(within(dialog).getByLabelText("Display label"), "legacy project");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register" }));

    expect(m.registerMutate).toHaveBeenCalledTimes(1);
    expect(m.registerMutate).toHaveBeenCalledWith(
      {
        kind: "candidate_cli",
        path: "/srv/memories/legacy",
        label: "legacy project",
      },
      expect.anything(),
    );
    // A manual path must never claim the designated hub kind.
    expect(m.registerMutate).not.toHaveBeenCalledWith(
      expect.objectContaining({ kind: "ultimate" }),
    );
  });

  it("opens a full Markdown reader and adopts from it (owner only)", async () => {
    m.role = "owner";
    m.dirs = [dirFixture()];
    m.entries = [entryFixture()];
    mount();

    await userEvent.click(await screen.findByTestId("knowledge-entry-deploy-rollback"));
    const dialog = await screen.findByTestId("knowledge-entry-dialog");
    expect(within(dialog).getByTestId("knowledge-entry-markdown")).toBeTruthy();
    expect(within(dialog).getByText(entryFixture().content)).toBeTruthy();
    expect(within(dialog).getByText("Mirror of project-x memories")).toBeTruthy();

    await userEvent.click(within(dialog).getByRole("button", { name: "Adopt" }));
    expect(m.adoptMutate).toHaveBeenCalledTimes(1);
    expect(m.adoptMutate).toHaveBeenCalledWith("entry-1", expect.anything());
  });

  it("labels a structured, detailed entry as high value", async () => {
    m.role = "owner";
    m.dirs = [dirFixture()];
    m.entries = [entryFixture({
      content: `# Release rollback\n\n${"Keep each release artifact and its checksums available for a verified rollback. ".repeat(8)}\n\n- Confirm the deployed version\n- Restore the tagged artifact\n- Validate health checks`,
    })];
    mount();

    const row = await screen.findByTestId("knowledge-entry-deploy-rollback");
    expect(within(row).getByText("High value")).toBeTruthy();
    await userEvent.click(await screen.findByTestId("knowledge-entry-deploy-rollback"));
    const dialog = await screen.findByTestId("knowledge-entry-dialog");
    expect(within(dialog).getByText("Estimated value")).toBeTruthy();
    expect(within(dialog).getByText("High value")).toBeTruthy();
  });

  it("labels only unstructured, very short content as low value", async () => {
    m.role = "owner";
    m.dirs = [dirFixture()];
    m.entries = [entryFixture({ content: "Keep rollback notes." })];
    mount();

    const row = await screen.findByTestId("knowledge-entry-deploy-rollback");
    expect(within(row).getByText("Low value")).toBeTruthy();
    await userEvent.click(await screen.findByTestId("knowledge-entry-deploy-rollback"));
    const dialog = await screen.findByTestId("knowledge-entry-dialog");
    expect(within(dialog).getByText("Estimated value")).toBeTruthy();
    expect(within(dialog).getByText("Low value")).toBeTruthy();
  });

  it("leaves ordinary unstructured content unclassified", async () => {
    m.role = "owner";
    m.dirs = [dirFixture()];
    m.entries = [entryFixture({ content: "Keep the rollback procedure current with each release and validate it before publishing. This note records the operational expectation without a detailed runbook." })];
    mount();

    const row = await screen.findByTestId("knowledge-entry-deploy-rollback");
    expect(within(row).queryByText("High value")).toBeNull();
    expect(within(row).queryByText("Low value")).toBeNull();
    await userEvent.click(await screen.findByTestId("knowledge-entry-deploy-rollback"));
    const dialog = await screen.findByTestId("knowledge-entry-dialog");
    expect(within(dialog).queryByText("Estimated value")).toBeNull();
    expect(within(dialog).queryByText("High value")).toBeNull();
    expect(within(dialog).queryByText("Low value")).toBeNull();
  });

  it("queues an edit only for a synced ultimate-library entry", async () => {
    m.role = "owner";
    m.dirs = [dirFixture({ id: "dir-ultimate", kind: "ultimate", label: "workspace library" })];
    m.entries = [entryFixture({ dir_id: "dir-ultimate", content: "# Rollback\n\nUse the tagged release." })];
    mount();

    await userEvent.click(await screen.findByTestId("knowledge-entry-deploy-rollback"));
    const dialog = await screen.findByTestId("knowledge-entry-dialog");
    expect(within(dialog).getByRole("heading", { name: "Rollback" })).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Edit" }));
    const editor = within(dialog).getByRole("textbox", { name: "Knowledge content" });
    await userEvent.clear(editor);
    await userEvent.type(editor, "# Safer rollback");
    await userEvent.click(within(dialog).getByRole("button", { name: "Queue edit" }));

    expect(m.editMutate).toHaveBeenCalledWith(
      { id: "entry-1", content: "# Safer rollback" },
      expect.anything(),
    );
  });

  it("shows a daemon failure for a queued write and clears it after replay succeeds", async () => {
    m.role = "owner";
    m.dirs = [dirFixture({ id: "dir-ultimate", kind: "ultimate", label: "workspace library" })];
    m.entries = [entryFixture({
      dir_id: "dir-ultimate",
      pending_operation: {
        id: "operation-1",
        status: "queued",
        operation_type: "edit",
        error: "bd recall mismatch",
        requested_at: "2026-11-05T07:00:00Z",
      },
    })];
    const view = mount();

    const row = await screen.findByTestId("knowledge-entry-deploy-rollback");
    expect(within(row).getByTestId("knowledge-entry-operation")).toHaveTextContent("Edit sync failed");
    await userEvent.click(row);
    const dialog = await screen.findByTestId("knowledge-entry-dialog");
    expect(within(dialog).getByTestId("knowledge-entry-operation")).toHaveTextContent("bd recall mismatch");
    expect(within(dialog).queryByRole("button", { name: "Edit" })).toBeNull();

    view.unmount();
    m.entries = [entryFixture({ dir_id: "dir-ultimate" })];
    mount();

    const replayedRow = await screen.findByTestId("knowledge-entry-deploy-rollback");
    expect(within(replayedRow).queryByTestId("knowledge-entry-operation")).toBeNull();
    await userEvent.click(replayedRow);
    expect(within(await screen.findByTestId("knowledge-entry-dialog")).getByRole("button", { name: "Edit" })).toBeTruthy();
  });

  it("requires an explicit confirmation before queueing a delete", async () => {
    m.role = "owner";
    m.dirs = [dirFixture({ id: "dir-ultimate", kind: "ultimate" })];
    m.entries = [entryFixture({ dir_id: "dir-ultimate" })];
    mount();

    await userEvent.click(await screen.findByTestId("knowledge-entry-deploy-rollback"));
    const dialog = await screen.findByTestId("knowledge-entry-dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(m.deleteMutate).not.toHaveBeenCalled();
    await userEvent.click(within(confirm).getByRole("button", { name: "Delete" }));

    expect(m.deleteMutate).toHaveBeenCalledWith("entry-1", expect.anything());
  });

  it("lists duplicate entries and requires confirmation before queueing a merge", async () => {
    m.role = "owner";
    m.dirs = [dirFixture({ id: "dir-ultimate", kind: "ultimate" })];
    m.entries = [
      entryFixture({ dir_id: "dir-ultimate" }),
      entryFixture({ id: "entry-2", key: "deploy-rollback-copy", dir_id: "dir-ultimate" }),
    ];
    mount();

    await userEvent.click(await screen.findByTestId("knowledge-entry-deploy-rollback"));
    const dialog = await screen.findByTestId("knowledge-entry-dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Merge selected" }));
    const candidates = await within(dialog).findByTestId("knowledge-merge-candidates");
    expect(within(candidates).getByText("Duplicate")).toBeTruthy();
    await userEvent.click(within(candidates).getByRole("checkbox"));
    await userEvent.click(within(dialog).getByRole("button", { name: "Merge selected" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(m.mergeMutate).not.toHaveBeenCalled();
    await userEvent.click(within(confirm).getByRole("button", { name: "Merge selected" }));

    expect(m.mergeMutate).toHaveBeenCalledWith(
      { id: "entry-1", content: entryFixture().content, sourceEntryIds: ["entry-2"] },
      expect.anything(),
    );
  });

  it("enables managed writeback only for a local candidate directory", async () => {
    m.role = "owner";
    m.dirs = [dirFixture()];
    mount();

    await switchSegment(/Directories/);
    await userEvent.click(screen.getByTestId("knowledge-dir-dir-1"));
    const drawer = await screen.findByTestId("knowledge-dir-drawer");
    await userEvent.click(within(drawer).getByRole("switch"));
    expect(m.setManagedMutate).toHaveBeenCalledWith({ id: "dir-1", managed: true }, expect.anything());
  });

  it("does not offer managed writeback for a project directory", async () => {
    m.role = "owner";
    m.dirs = [dirFixture({ project_id: "project-1" })];
    mount();

    await switchSegment(/Directories/);
    await userEvent.click(screen.getByTestId("knowledge-dir-dir-1"));
    const drawer = await screen.findByTestId("knowledge-dir-drawer");
    expect(within(drawer).queryByText("Enable managed write-back")).toBeNull();
  });

  it("submits evidence-backed lifecycle and restoration candidates without a writeback flag", async () => {
    m.role = "owner";
    m.dirs = [dirFixture()];
    m.entries = [entryFixture({ lifecycle_state: "deprecated" })];
    m.versions = [{
      id: "version-1",
      entry_id: "entry-1",
      version: 1,
      content: "Earlier rollback procedure.",
      content_sha256: "1".repeat(64),
      source: "initial",
      created_at: "2026-09-28T00:00:00Z",
    }];
    mount();

    await userEvent.click(await screen.findByTestId("knowledge-entry-deploy-rollback"));
    const dialog = await screen.findByTestId("knowledge-entry-dialog");
    await userEvent.type(within(dialog).getByPlaceholderText("Reason"), "Needs Owner review");
    await userEvent.type(
      within(dialog).getByPlaceholderText("Evidence reference"),
      "issue:00000000-0000-0000-0000-000000000916",
    );

    await userEvent.click(within(dialog).getByRole("button", { name: "Apply lifecycle" }));
    expect(m.updateLifecycleMutate).toHaveBeenCalledWith({
      id: "entry-1",
      data: {
        state: "needs_review",
        reason: "Needs Owner review",
        evidence_ref: "issue:00000000-0000-0000-0000-000000000916",
      },
    }, expect.anything());

    await userEvent.click(within(dialog).getByRole("button", { name: "Request restore v1" }));
    expect(m.restoreMutate).toHaveBeenCalledWith({
      id: "entry-1",
      data: {
        version_id: "version-1",
        reason: "Needs Owner review",
        evidence_ref: "issue:00000000-0000-0000-0000-000000000916",
      },
    }, expect.anything());
  });

  it("submits physical writeback only for an enabled managed source", async () => {
    m.role = "owner";
    m.dirs = [dirFixture({ managed_active: true })];
    m.entries = [entryFixture()];
    mount();

    await userEvent.click(await screen.findByTestId("knowledge-entry-deploy-rollback"));
    const dialog = await screen.findByTestId("knowledge-entry-dialog");
    expect(within(dialog).getByText("Write back to managed source")).toBeTruthy();
    const writeback = within(dialog).getByRole("switch");
    expect(writeback).toHaveAttribute("aria-disabled", "true");

    await userEvent.click(within(dialog).getByRole("combobox", { name: "Lifecycle" }));
    await userEvent.click(screen.getByRole("option", { name: "Deprecated" }));
    expect(writeback).not.toHaveAttribute("aria-disabled", "true");
    await userEvent.click(writeback);
    await userEvent.type(within(dialog).getByPlaceholderText("Reason"), "Superseded by SSH guidance");
    await userEvent.type(
      within(dialog).getByPlaceholderText("Evidence reference"),
      "issue:00000000-0000-0000-0000-000000000916",
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Apply lifecycle" }));

    expect(m.updateLifecycleMutate).toHaveBeenCalledWith({
      id: "entry-1",
      data: {
        state: "deprecated",
        reason: "Superseded by SSH guidance",
        evidence_ref: "issue:00000000-0000-0000-0000-000000000916",
        writeback: true,
      },
    }, expect.anything());
  });

  it("shows the server-validated evidence reference in entry history", async () => {
    m.role = "owner";
    m.dirs = [dirFixture()];
    m.entries = [entryFixture()];
    m.events = [{
      id: "event-1",
      entry_id: "entry-1",
      from_state: "active",
      to_state: "needs_review",
      operation: "transition",
      reason: "Review the source evidence",
      evidence_ref: "knowledge_scan_batch:00000000-0000-0000-0000-000000000001",
      writeback_state: "not_requested",
      created_at: "2026-10-31T09:00:00Z",
    }];
    mount();

    await userEvent.click(await screen.findByTestId("knowledge-entry-deploy-rollback"));
    const dialog = await screen.findByTestId("knowledge-entry-dialog");
    expect(within(dialog).getByText(/knowledge_scan_batch:00000000-0000-0000-0000-000000000001/)).toBeTruthy();
  });

  it("unregisters a directory only through the drawer's confirm dialog", async () => {
    m.role = "owner";
    m.dirs = [dirFixture({ health_state: "unhealthy", health_note: "path missing" })];
    mount();

    await switchSegment(/Directories/);
    await userEvent.click(screen.getByTestId("knowledge-dir-dir-1"));
    const drawer = await screen.findByTestId("knowledge-dir-drawer");
    // Status closure strip: state, note, last scan in one place (walk #5).
    const status = within(drawer).getByTestId("knowledge-dir-status");
    expect(status.querySelector("[data-status]")?.getAttribute("data-status")).toBe(
      "unhealthy",
    );
    expect(within(drawer).getByText("Unhealthy")).toBeTruthy();
    expect(within(drawer).getByText("path missing")).toBeTruthy();
    expect(
      within(drawer).getByText(/Last scan \(changed\): \+1, ~0, -0/),
    ).toBeTruthy();

    // First click only opens the confirm; nothing is destroyed yet.
    await userEvent.click(
      within(drawer).getAllByRole("button", { name: "Unregister" })[0]!,
    );
    const confirm = await screen.findByRole("alertdialog");
    expect(m.unregisterMutate).not.toHaveBeenCalled();
    expect(
      within(confirm).getByText(
        'Unregister "project-x memories"? Its mirrored entries are kept but stop updating.',
      ),
    ).toBeTruthy();
    await userEvent.click(
      within(confirm).getAllByRole("button", { name: "Unregister" })[0]!,
    );
    expect(m.unregisterMutate).toHaveBeenCalledWith("dir-1", expect.anything());
  });
});
