/**
 * IssueReferenceList — the RUYI-635 two-level tail list, restored by
 * RUYI-731 (Owner correction 2026-10-12: "no second-level surface" was a
 * web-only directive; mobile keeps preview + fold line + bottom sheet).
 *
 * Pins the behaviours the acceptance criteria call out: the whole block is
 * one anti-mistouch press target that opens the sheet (preview rows never
 * navigate inline), at most two preview rows render inline with the rest
 * behind a fold-count line, the sheet lists EVERY reference (N=1 behaves
 * like N=10 — no cap behind the fold), a UUID mention and the bare
 * identifier of the same issue collapse into one row, an unresolved
 * reference degrades to non-tappable plain text, content without references
 * renders no trailing block at all, and a reference that resolves to the
 * CURRENT issue appears in neither preview nor sheet (self-filter,
 * RUYI-731) while surfaces without the context keep every row.
 *
 * RNTL v14 made `render` async — awaiting it yields the full query set,
 * bound to this suite's own QueryClientProvider tree. Modal-opening tests
 * stay at the END of the suite and close the sheet before finishing: a
 * Modal still visible at cleanup (jest-expo fake timers never settle the
 * slide animation) poisons every render this suite does afterwards.
 */
import React from "react";
import {
  fireEvent,
  render,
  waitFor,
  within,
  type RenderResult,
} from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { notifyManager } from "@tanstack/react-query";

// Flush react-query's batched notifications synchronously — jest-expo's
// fake-timer environment never advances the default scheduler (same setup
// as agent-header-badge.test.tsx).
notifyManager.setScheduler((cb) => {
  cb();
  return 0;
});

const UUID_A = "1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed";
const UUID_B = "8f14e45f-ceea-4670-9b1c-0d3e4f5a6b7c";
const ISSUE_A = {
  id: UUID_A,
  identifier: "MUL-7",
  title: "Fix the thing",
  status: "in_progress",
};
const ISSUE_B = {
  id: UUID_B,
  identifier: "MUL-9",
  title: "Later issue",
  status: "done",
};
const PR_12 = {
  id: "pr-12",
  workspace_id: "workspace-1",
  repo_owner: "cfgxy",
  repo_name: "multica-cn",
  number: 12,
  title: "Add the thing",
  state: "open",
  html_url: "https://github.com/cfgxy/multica-cn/pull/12",
};

const UUID_C = "c9ca8b1c-3e77-4b19-9c0f-7de5a1b2c3d4";
const ISSUE_C = {
  id: UUID_C,
  identifier: "MUL-11",
  title: "Third issue",
  status: "todo",
};

const mockPush = jest.fn();
const mockGetIssue = jest.fn();
const mockLookupPullRequest = jest.fn();
const mockOpenExternalUrl = jest.fn();

jest.mock("expo-router", () => ({
  router: { push: (...args: unknown[]) => mockPush(...args) },
}));

jest.mock("@/data/api", () => ({
  api: {
    getIssue: (...args: unknown[]) => mockGetIssue(...args),
    lookupPullRequest: (...args: unknown[]) => mockLookupPullRequest(...args),
    // useWorkspaceIssuePrefix (RUYI-705 rework) reads the workspace list for
    // the prefix gate; an empty list means "unknown prefix", so resolution
    // falls through to the exact match these fixtures were written against.
    listWorkspaces: () => Promise.resolve([]),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({
      currentWorkspaceId: "workspace-1",
      currentWorkspaceSlug: "ws",
    }),
}));

jest.mock("@/lib/use-issue-statuses", () => ({
  useIssueStatuses: () => ({
    colorOf: () => "#123456",
    categoryOf: () => "in_progress",
  }),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, fallback?: string) =>
      typeof fallback === "string" ? fallback : key,
  }),
}));

jest.mock("@/lib/pull-request-link", () => ({
  openExternalUrl: (...args: unknown[]) => mockOpenExternalUrl(...args),
}));

// The bottom sheet reads insets for its bottom padding; jest renders need a
// deterministic value.
jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

import { IssueReferenceList } from "@/components/issue/issue-reference-list";
import { CurrentIssueIdProvider } from "@/lib/current-issue-context";

async function renderList(
  references: unknown[],
  { currentIssueId }: { currentIssueId?: string } = {},
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const list = <IssueReferenceList references={references as never} />;
  return await render(
    <QueryClientProvider client={client}>
      {currentIssueId ? (
        <CurrentIssueIdProvider value={currentIssueId}>
          {list}
        </CurrentIssueIdProvider>
      ) : (
        list
      )}
    </QueryClientProvider>,
  );
}

/** Close an open sheet via the × button so the Modal is gone before cleanup. */
async function closeSheet(utils: RenderResult) {
  const close = await utils.findByLabelText("Close");
  fireEvent.press(close);
}

beforeEach(() => {
  mockPush.mockReset();
  mockGetIssue.mockReset();
  mockLookupPullRequest.mockReset();
  mockOpenExternalUrl.mockReset();
  mockOpenExternalUrl.mockResolvedValue({ ok: true });
  mockGetIssue.mockImplementation((idOrIdentifier: string) => {
    if (idOrIdentifier === UUID_A || idOrIdentifier === "MUL-7") {
      return Promise.resolve(ISSUE_A);
    }
    if (idOrIdentifier === UUID_B || idOrIdentifier === "MUL-9") {
      return Promise.resolve(ISSUE_B);
    }
    if (idOrIdentifier === UUID_C || idOrIdentifier === "MUL-11") {
      return Promise.resolve(ISSUE_C);
    }
    return Promise.reject(
      Object.assign(new Error("not found"), { name: "ApiError", status: 404 }),
    );
  });
  mockLookupPullRequest.mockResolvedValue(PR_12);
});

describe("IssueReferenceList — level 1 (block, previews, fold)", () => {
  it("renders a resolved mention as a preview row that does NOT navigate (anti-mistouch)", async () => {
    const { findByTestId } = await renderList([
      { ref: UUID_A, label: "MUL-7", form: "mention" },
    ]);

    // The preview row itself is not pressable — pressing it must NOT
    // navigate (routing moved to the bottom sheet).
    const previewRow = await findByTestId("issue-reference-row");
    fireEvent.press(previewRow);
    expect(mockPush).not.toHaveBeenCalled();
  });

  it("collapses a UUID mention and the bare identifier of the same issue into one row", async () => {
    const { getAllByTestId, findByTestId } = await renderList([
      { ref: UUID_A, label: "MUL-7", form: "mention" },
      { ref: "MUL-7", label: null, form: "identifier" },
    ]);

    await findByTestId("issue-reference-row");
    expect(getAllByTestId("issue-reference-row")).toHaveLength(1);
    expect(mockGetIssue).toHaveBeenCalledWith(UUID_A, expect.anything());
    expect(mockGetIssue).toHaveBeenCalledWith("MUL-7", expect.anything());
  });

  it("keeps first-mention order across two distinct issues", async () => {
    const { getAllByTestId } = await renderList([
      { ref: "MUL-9", label: null, form: "identifier" },
      { ref: UUID_A, label: "MUL-7", form: "mention" },
    ]);

    await waitFor(() =>
      expect(getAllByTestId("issue-reference-row")).toHaveLength(2),
    );

    // Preview rows render `<icon> <identifier> <title>`; order is
    // first-mention order. (The a11y label lives on the sheet rows, not here.)
    const rows = getAllByTestId("issue-reference-row");
    expect(within(rows[0]).getByText("MUL-9")).toBeTruthy();
    expect(within(rows[1]).getByText("MUL-7")).toBeTruthy();
  });

  it("folds the tail beyond two preview rows behind a count line", async () => {
    // Three distinct issues → the inline block caps at two preview rows and
    // a "1 more references" line; the third row only exists in the sheet.
    // (The useT mock skips interpolation, hence the raw {{count}} template.)
    const { getAllByTestId, findByText } = await renderList([
      { ref: "MUL-9", label: null, form: "identifier" },
      { ref: UUID_A, label: "MUL-7", form: "mention" },
      { ref: "MUL-11", label: null, form: "identifier" },
    ]);

    await waitFor(() =>
      expect(getAllByTestId("issue-reference-row")).toHaveLength(2),
    );
    expect(await findByText(/more references/)).toBeTruthy();
  });

  it("degrades an unresolved reference to plain text that is not tappable", async () => {
    const { findByText, queryByTestId } = await renderList([
      { ref: "MUL-404", label: null, form: "identifier" },
    ]);

    expect(await findByText("MUL-404")).toBeTruthy();
    expect(queryByTestId("issue-reference-row")).toBeNull();
    expect(mockPush).not.toHaveBeenCalled();
  });

  it("renders nothing when there are no references", async () => {
    const { queryByTestId } = await renderList([]);
    expect(queryByTestId("issue-reference-block")).toBeNull();
  });

  it("drops a reference that resolves to the current issue (self-filter)", async () => {
    const { queryByTestId } = await renderList(
      [{ ref: UUID_A, label: "MUL-7", form: "mention" }],
      { currentIssueId: UUID_A },
    );

    // The block disappears once the reference resolves to the current issue
    // and the filter skips it. Without the filter the resolved row renders
    // and the block stays — this wait fails then.
    await waitFor(() =>
      expect(queryByTestId("issue-reference-block")).toBeNull(),
    );
  });

  it("keeps other-issue rows and drops only the self row", async () => {
    const { getAllByTestId } = await renderList(
      [
        { ref: UUID_A, label: "MUL-7", form: "mention" },
        { ref: "MUL-9", label: null, form: "identifier" },
      ],
      { currentIssueId: UUID_A },
    );

    await waitFor(() =>
      expect(getAllByTestId("issue-reference-row")).toHaveLength(1),
    );
    expect(within(getAllByTestId("issue-reference-row")[0]).getByText("MUL-9")).toBeTruthy();
  });

  it("keeps the block fold-free when the self row was the only overflow", async () => {
    // Self row + two others: the filter runs BEFORE the fold, so the two
    // others fill the preview and no fold line appears.
    const { getAllByTestId, queryByText } = await renderList(
      [
        { ref: UUID_A, label: "MUL-7", form: "mention" },
        { ref: "MUL-9", label: null, form: "identifier" },
        { ref: "MUL-11", label: null, form: "identifier" },
      ],
      { currentIssueId: UUID_A },
    );

    await waitFor(() =>
      expect(getAllByTestId("issue-reference-row")).toHaveLength(2),
    );
    expect(queryByText(/more references/)).toBeNull();
  });

  it("keeps every row on surfaces without a current-issue context (chat, inbox)", async () => {
    const { getAllByTestId } = await renderList([
      { ref: UUID_A, label: "MUL-7", form: "mention" },
    ]);

    await waitFor(() =>
      expect(getAllByTestId("issue-reference-row")).toHaveLength(1),
    );
  });
});

describe("IssueReferenceList — level 2 (bottom sheet)", () => {
  it("routes to the issue only from the sheet row", async () => {
    const utils = await renderList([
      { ref: UUID_A, label: "MUL-7", form: "mention" },
    ]);

    // The block opens the bottom sheet, whose issue row routes through the
    // same resolveLinkAction prose links take; pressing the block never
    // navigates by itself.
    fireEvent.press(utils.getByTestId("issue-reference-block"));
    const sheetRow = await utils.findByTestId("reference-sheet-issue-row");
    expect(sheetRow.props.accessibilityLabel).toBe("MUL-7 Fix the thing");
    fireEvent.press(sheetRow);
    expect(mockPush).toHaveBeenCalledWith(`/ws/issue/${UUID_A}`);
  });

  it("opens a PR row's canonical URL through the external-url channel", async () => {
    const utils = await renderList([
      { ref: "cfgxy/multica-cn#12", label: null, form: "pull_request" },
    ]);

    fireEvent.press(utils.getByTestId("issue-reference-block"));
    const sheetRow = await utils.findByTestId("reference-sheet-pr-row");
    fireEvent.press(sheetRow);
    await waitFor(() =>
      expect(mockOpenExternalUrl).toHaveBeenCalledWith(
        PR_12.html_url,
        expect.anything(),
      ),
    );
    expect(mockPush).not.toHaveBeenCalled();
    await closeSheet(utils);
  });

  it("lists every reference in the sheet, beyond the preview budget", async () => {
    const utils = await renderList([
      { ref: "MUL-9", label: null, form: "identifier" },
      { ref: UUID_A, label: "MUL-7", form: "mention" },
      { ref: "MUL-11", label: null, form: "identifier" },
    ]);

    // The fold caps the preview at two; the sheet still shows all three.
    await waitFor(() =>
      expect(utils.getAllByTestId("issue-reference-row")).toHaveLength(2),
    );
    fireEvent.press(utils.getByTestId("issue-reference-block"));
    await waitFor(() =>
      expect(utils.getAllByTestId("reference-sheet-issue-row")).toHaveLength(3),
    );
    await closeSheet(utils);
  });

  it("keeps the self row out of the sheet (self-filter at level 2)", async () => {
    const utils = await renderList(
      [
        { ref: UUID_A, label: "MUL-7", form: "mention" },
        { ref: "MUL-9", label: null, form: "identifier" },
      ],
      { currentIssueId: UUID_A },
    );

    fireEvent.press(utils.getByTestId("issue-reference-block"));
    await waitFor(() =>
      expect(utils.getAllByTestId("reference-sheet-issue-row")).toHaveLength(1),
    );
    const sheetRows = utils.getAllByTestId("reference-sheet-issue-row");
    expect(within(sheetRows[0]).getByText("MUL-9")).toBeTruthy();
    await closeSheet(utils);
  });
});
