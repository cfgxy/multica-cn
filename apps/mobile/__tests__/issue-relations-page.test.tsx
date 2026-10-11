/**
 * Issue relations page (RUYI-689 stage-3 redesign) — the list re-styled
 * after the home Tasks tab: sectioned SectionList with per-group count
 * headers, the tasks row container (bg-background active:bg-secondary
 * px-4 py-3), IssuesLoading skeleton and pull-to-refresh. Pins the
 * stage-3 acceptance items that are assertable without a device:
 *
 *   - every structured relation group renders with its type label (the
 *     relation-type semantics survive the restyle);
 *   - empty groups render no header (no data spends no space);
 *   - an all-empty payload renders the empty state;
 *   - tapping a row routes to that issue (back keeps the original issue
 *     underneath);
 *   - the page renders NO filter bar — Owner 2026-10-11 15:13: 筛选栏
 *     去掉 (negative assertion on the Tasks toolbar a11y labels);
 *   - rows keep identifier + title + status semantics.
 */
import React from "react";
import { fireEvent, render, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { notifyManager } from "@tanstack/react-query";

// Flush react-query's batched notifications synchronously — jest-expo's
// fake-timer environment never advances the default scheduler (same setup
// as issue-reference-list.test.tsx).
notifyManager.setScheduler((cb) => {
  cb();
  return 0;
});

const PARENT_ID = "1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed";
const CHILD_ID = "8f14e45f-ceea-4670-9b1c-0d3e4f5a6b7c";
const BLOCKS_ID = "c9bf9e57-1685-4c89-bafb-ff5af830be8a";
const BLOCKED_BY_ID = "45a1133c-b34d-4f10-90e8-2e0b24d1db19";
const RELATES_ID = "9bf8a0f0-1c2d-4e3f-8a7b-6c5d4e3f2a1b";
const SUPERSEDES_ID = "f47ac10b-58cc-4372-a567-0e02b2c3d479";
const SUPERSEDED_BY_ID = "0b8df6b0-4f1a-4c7e-9d3a-2b1c0d9e8f7a";
const ISSUE_ID = "7c9e6679-7425-40de-944b-e07fc1f90ae7";

const ref = (id: string, identifier: string, title: string, status: string) => ({
  id,
  identifier,
  title,
  status,
});

const FULL_RELATIONS = {
  parent: ref(PARENT_ID, "MUL-1", "Parent issue title", "in_progress"),
  blocks: [
    ref(BLOCKS_ID, "MUL-2", "Blocking target", "todo"),
    ref("cbbfce2a-1f4e-4d8a-9c3b-7e6f5a4b3c2d", "MUL-8", "Second blocked", "done"),
  ],
  blocked_by: [ref(BLOCKED_BY_ID, "MUL-3", "Blocker", "blocked")],
  relates_to: [ref(RELATES_ID, "MUL-4", "Related issue", "done")],
  supersedes: [ref(SUPERSEDES_ID, "MUL-5", "Superseded issue", "done")],
  superseded_by: [ref(SUPERSEDED_BY_ID, "MUL-6", "Superseder", "todo")],
};

const CHILDREN = {
  issues: [
    {
      id: CHILD_ID,
      identifier: "MUL-7",
      title: "Child issue title",
      status: "done",
    },
  ],
};

const EMPTY_RELATIONS = {
  parent: null,
  blocks: [],
  blocked_by: [],
  relates_to: [],
  supersedes: [],
  superseded_by: [],
};

const mockPush = jest.fn();
const mockGetIssueRelations = jest.fn();
const mockListChildIssues = jest.fn();

// VirtualizedList only renders its initial window (~10 cells) in the test
// environment — later sections would be invisible to the assertions. The
// unit under test is the PAGE (grouping, labels, routing), not RN's list
// virtualization, so SectionList renders sequentially here.
jest.mock("react-native", () => {
  const rn = jest.requireActual<typeof import("react-native")>("react-native");
  const React = jest.requireActual<typeof import("react")>("react");
  const MockSectionList = (props: {
    sections: { key: string; data: unknown[] }[];
    renderItem: (info: { item: unknown }) => React.ReactNode;
    renderSectionHeader: (info: { section: unknown }) => React.ReactNode;
  }) => (
    <>
      {props.sections.map((section) => (
        <React.Fragment key={section.key}>
          {props.renderSectionHeader({ section })}
          {section.data.map((item, idx) => (
            <React.Fragment key={idx}>
              {props.renderItem({ item })}
            </React.Fragment>
          ))}
        </React.Fragment>
      ))}
    </>
  );
  Object.defineProperty(rn, "SectionList", {
    value: MockSectionList,
    configurable: true,
  });
  return rn;
});

jest.mock("expo-router", () => ({
  router: { push: (...args: unknown[]) => mockPush(...args) },
  useLocalSearchParams: () => ({ id: ISSUE_ID }),
}));

jest.mock("@/data/api", () => ({
  api: {
    getIssueRelations: (...args: unknown[]) => mockGetIssueRelations(...args),
    listChildIssues: (...args: unknown[]) => mockListChildIssues(...args),
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
  useIssueStatuses: () => ({ colorOf: () => "#123456" }),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    // Minimal i18next substitution: the error copy interpolates {{reason}}.
    t: (key: string, fallback?: string, options?: Record<string, unknown>) => {
      if (typeof fallback !== "string") return key;
      return fallback.replace(/\{\{(\w+)\}\}/g, (_m, k: string) =>
        String(options?.[k] ?? ""),
      );
    },
  }),
}));

// ui/text pulls @rn-primitives/slot whose dist ships untranspiled JSX —
// same workaround as issue-limit-recovery-dialog.test.tsx.
jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return { Text };
});

jest.mock("@/components/ui/button", () => {
  const { Pressable } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    Button: (props: React.ComponentProps<typeof Pressable>) => (
      <Pressable {...props} />
    ),
  };
});

// Stub the tasks-tab skeleton with a testID so the loading-state assertion
// distinguishes it from the old page's centered ActivityIndicator.
jest.mock("@/components/issue/issues-loading", () => ({
  IssuesLoading: () => {
    const { View } = jest.requireActual<typeof import("react-native")>(
      "react-native",
    );
    return <View testID="issues-loading-stub" />;
  },
}));

import IssueRelationsRoute from "@/app/(app)/[workspace]/issue/[id]/relations";

type RenderOpts = {
  relations?: unknown;
  children?: unknown;
  relationsError?: Error;
  pending?: boolean;
};

async function renderPage(opts: RenderOpts = {}) {
  mockGetIssueRelations.mockReset();
  mockListChildIssues.mockReset();
  mockPush.mockReset();
  if (opts.pending) {
    mockGetIssueRelations.mockImplementation(() => new Promise(() => {}));
    mockListChildIssues.mockImplementation(() => new Promise(() => {}));
  } else {
    mockGetIssueRelations.mockImplementation(
      (_id: string, { signal }: { signal?: AbortSignal }) =>
        opts.relationsError
          ? Promise.reject(opts.relationsError)
          : Promise.resolve(opts.relations ?? FULL_RELATIONS),
    );
    mockListChildIssues.mockResolvedValue(opts.children ?? CHILDREN);
  }

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const screen = await render(
    <QueryClientProvider client={client}>
      <IssueRelationsRoute />
    </QueryClientProvider>,
  );
  return { screen, client };
}

describe("issue relations page (stage-3 tasks-tab restyle)", () => {
  it("renders every structured relation group under its type label", async () => {
    const { screen } = await renderPage();
    await waitFor(() => {
      expect(screen.getByText("Parent issue title")).toBeTruthy();
    });
    // Section headers name the relation TYPE (semantics survive), and the
    // tasks-style count sits next to each header.
    expect(screen.getByText("Parent issue")).toBeTruthy();
    expect(screen.getByText("Sub-issues")).toBeTruthy();
    expect(screen.getByText("Blocks")).toBeTruthy();
    expect(screen.getByText("Blocked by")).toBeTruthy();
    expect(screen.getByText("Related")).toBeTruthy();
    expect(screen.getByText("Supersedes")).toBeTruthy();
    expect(screen.getByText("Superseded by")).toBeTruthy();
    // Per-group counts, tasks-style: blocks holds 2 refs, the other six
    // groups hold 1 each.
    expect(screen.getAllByText("2")).toHaveLength(1);
    expect(screen.getAllByText("1")).toHaveLength(6);
    // Row semantics: identifier + title for each referenced issue.
    expect(screen.getByText("MUL-1")).toBeTruthy();
    expect(screen.getByText("MUL-2")).toBeTruthy();
    expect(screen.getByText("Blocking target")).toBeTruthy();
    expect(screen.getByText("Second blocked")).toBeTruthy();
    expect(screen.getByText("Child issue title")).toBeTruthy();
  });

  it("renders the tasks-style loading skeleton while queries are pending", async () => {
    const { screen } = await renderPage({ pending: true });
    expect(screen.getByTestId("issues-loading-stub")).toBeTruthy();
  });

  it("renders no header for empty groups — no data spends no space", async () => {
    const { screen } = await renderPage({
      relations: {
        parent: null,
        blocks: FULL_RELATIONS.blocks,
        blocked_by: [],
        relates_to: [],
        supersedes: [],
        superseded_by: [],
      },
      children: { issues: [] },
    });
    await waitFor(() => {
      expect(screen.getByText("Blocking target")).toBeTruthy();
    });
    expect(screen.queryByText("Parent issue")).toBeNull();
    expect(screen.queryByText("Sub-issues")).toBeNull();
    expect(screen.queryByText("Blocked by")).toBeNull();
    expect(screen.queryByText("Related")).toBeNull();
    expect(screen.queryByText("Supersedes")).toBeNull();
    expect(screen.queryByText("Superseded by")).toBeNull();
  });

  it("renders the empty state when no structured relation exists", async () => {
    const { screen } = await renderPage({
      relations: EMPTY_RELATIONS,
      children: { issues: [] },
    });
    await waitFor(() => {
      expect(
        screen.getByText(
          "No structured relations yet. Blocks, related, and supersede links appear here.",
        ),
      ).toBeTruthy();
    });
  });

  it("routes to the referenced issue on row tap", async () => {
    const { screen } = await renderPage();
    await waitFor(() => {
      expect(screen.getByText("Blocking target")).toBeTruthy();
    });
    fireEvent.press(screen.getByText("Blocking target"));
    expect(mockPush).toHaveBeenCalledTimes(1);
    expect(String(mockPush.mock.calls[0][0])).toContain(BLOCKS_ID);
  });

  it("renders NO filter bar — Owner 2026-10-11: 筛选栏去掉", async () => {
    const { screen } = await renderPage();
    await waitFor(() => {
      expect(screen.getByText("Blocking target")).toBeTruthy();
    });
    // The Tasks tab toolbar's sort / filter buttons carry these a11y
    // labels; none of them may appear on this page.
    expect(screen.queryByLabelText("Sort")).toBeNull();
    expect(screen.queryByLabelText("Filter")).toBeNull();
  });

  it("shows the tasks-style error block with retry on query failure", async () => {
    const { screen } = await renderPage({
      relationsError: new Error("boom"),
    });
    await waitFor(() => {
      expect(screen.getByText(/boom/)).toBeTruthy();
    });
    expect(screen.getByText("Retry")).toBeTruthy();
  });
});
