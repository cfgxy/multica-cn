/**
 * IssueReferenceList — the RUYI-635 mobile tail list.
 *
 * Pins the four behaviours the acceptance criteria call out: a resolved
 * reference renders one tappable row that routes to the issue detail page
 * (through the same resolveLinkAction the body links used), a UUID mention
 * and the bare identifier of the same issue collapse into one row, an
 * unresolved reference degrades to non-tappable plain text, and content
 * without references renders no trailing block at all.
 *
 * RNTL v14 made `render` async — awaiting it yields the full query set,
 * bound to this suite's own QueryClientProvider tree.
 */
import React from "react";
import { fireEvent, render, waitFor, within } from "@testing-library/react-native";
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

const mockPush = jest.fn();
const mockGetIssue = jest.fn();

jest.mock("expo-router", () => ({
  router: { push: (...args: unknown[]) => mockPush(...args) },
}));

jest.mock("@/data/api", () => ({
  api: {
    getIssue: (...args: unknown[]) => mockGetIssue(...args),
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

// The 增量 sheet (RUYI-635) reads insets for its bottom padding; jest renders
// need a deterministic value.
jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

import { IssueReferenceList } from "@/components/issue/issue-reference-list";

async function renderList(references: unknown[]) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return await render(
    <QueryClientProvider client={client}>
      <IssueReferenceList references={references as never} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockPush.mockReset();
  mockGetIssue.mockReset();
  mockGetIssue.mockImplementation((idOrIdentifier: string) => {
    if (idOrIdentifier === UUID_A || idOrIdentifier === "MUL-7") {
      return Promise.resolve(ISSUE_A);
    }
    if (idOrIdentifier === UUID_B || idOrIdentifier === "MUL-9") {
      return Promise.resolve(ISSUE_B);
    }
    return Promise.reject(
      Object.assign(new Error("not found"), { name: "ApiError", status: 404 }),
    );
  });
});

describe("IssueReferenceList", () => {
  it("renders a resolved mention as a preview row that does NOT navigate (anti-mistouch)", async () => {
    const { findByTestId } = await renderList([
      { ref: UUID_A, label: "MUL-7", form: "mention" },
    ]);

    // 增量 anti-mistouch contract: the preview row itself is not pressable —
    // pressing it must NOT navigate (routing moved to the bottom sheet).
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

  it("degrades an unresolved reference to plain text that is not tappable", async () => {
    const { findByText, queryByTestId } = await renderList([
      { ref: "MUL-404", label: null, form: "identifier" },
    ]);

    expect(await findByText("MUL-404")).toBeTruthy();
    expect(queryByTestId("issue-reference-row")).toBeNull();
    expect(mockPush).not.toHaveBeenCalled();
  });

  it("renders nothing when there are no references", async () => {
    const { queryByLabelText } = await renderList([]);
    expect(queryByLabelText("Referenced issues")).toBeNull();
  });

  // Deliberately LAST: it opens the bottom-sheet Modal, and a Modal still
  // visible at cleanup (jest-expo fake timers never settle the slide
  // animation) poisons every render this suite does afterwards.
  it("routes to the issue only from the sheet row", async () => {
    const utils = await renderList([
      { ref: UUID_A, label: "MUL-7", form: "mention" },
    ]);

    // Level 2: the block opens the bottom sheet, whose issue row routes
    // through the same resolveLinkAction prose links take.
    fireEvent.press(utils.getByTestId("issue-reference-block"));
    const sheetRow = await utils.findByTestId("reference-sheet-issue-row");
    expect(sheetRow.props.accessibilityLabel).toBe("MUL-7 Fix the thing");
    fireEvent.press(sheetRow);
    expect(mockPush).toHaveBeenCalledWith(`/ws/issue/${UUID_A}`);
  });
});
