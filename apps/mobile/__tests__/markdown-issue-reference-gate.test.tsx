// @ts-nocheck

import React from "react";
import { render, waitFor, within } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

/**
 * RUYI-705 rework: the mobile runtime `workspacePrefix` chain.
 *
 * QA (2026-10-11 FAIL report) reproduced on device: `AC-1`~`AC-5` / `XX-7`
 * were still extracted as references and each fired a 404 resolution
 * request, deterministically across 4 fresh sessions. The extraction gate
 * itself is sound (web 3/3 on the same core function); the mobile wiring
 * resolved the prefix from the store's `currentWorkspaceId` — an
 * in-memory mirror written by a layout effect — so any state where the
 * id mirror lags while the route slug is already correct falls through
 * to "unknown prefix" and the gate opens. The route slug is the truth the
 * API layer already trusts (it IS the X-Workspace-Slug header), so the
 * fixed chain resolves the workspace by slug first, id second.
 *
 * These tests exercise the REAL Markdown + IssueReferenceList + zod schema
 * + query pipeline; only native rendering leaves (enriched, code/image/
 * table/mermaid blocks) are stubbed. The api methods are spied at the
 * instance boundary, so responses flow through the production
 * parseWithFallback path.
 */

jest.mock("react-native-enriched-markdown", () => ({
  EnrichedMarkdownText: ({ markdown }: { markdown: string }) => {
    const { createElement } = jest.requireActual("react");
    const { Text: RNText } = jest.requireActual("react-native");
    return createElement(RNText, null, markdown);
  },
}));
jest.mock("expo-router", () => ({ router: { push: jest.fn() } }));
jest.mock("@/lib/attachment-open", () => ({
  openAttachmentDownload: jest.fn(),
}));
jest.mock("@/lib/attachment-url", () => ({
  resolveAttachmentUrl: (u: string) => u,
}));
jest.mock("@/lib/markdown/code-block", () => ({ CodeBlock: () => null }));
jest.mock("@/lib/markdown/mermaid-diagram", () => ({
  MermaidDiagram: () => null,
}));
jest.mock("@/lib/markdown/markdown-image", () => ({ MarkdownImage: () => null }));
jest.mock("@/lib/markdown/table-block", () => ({ TableBlock: () => null }));
jest.mock("@/lib/markdown/markdown-style", () => ({
  useMarkdownStyle: () => ({}),
}));
jest.mock("@/lib/use-issue-statuses", () => ({
  useIssueStatuses: () => ({ colorOf: () => "#000" }),
}));
// IssueReferenceList's 增量 sheet (RUYI-635) reads insets for its bottom
// padding; jest renders need a deterministic value.
jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));
jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (key: string, fallback?: string) => fallback ?? key }),
}));
// split-markdown pulls in `marked`, an ESM-only package jest's default
// transform cannot parse; the splitter is vitest-covered elsewhere. One
// prose island per render keeps this suite on the extraction/resolution
// chain under test.
jest.mock("@/lib/markdown/split-markdown", () => ({
  splitMarkdown: (content: string) => [{ type: "prose", content }],
}));

import {
  IssueReferenceTailVisibilityContext,
  Markdown,
} from "@/lib/markdown/markdown";
import { IssueReferenceList } from "@/components/issue/issue-reference-list";
import { useWorkspaceStore } from "@/data/workspace-store";
import { api } from "@/data/api";
import type { Workspace, Issue } from "@multica/core/types";

// Raw server shape for /api/workspaces — flows through the REAL zod schema
// in data/schemas.ts, so a field rename or type drift fails here too.
const RAW_WORKSPACE = {
  id: "ws-1",
  name: "E2ER Workspace",
  slug: "e2er",
  issue_prefix: "E2ER",
};

const REAL_ISSUE: Partial<Issue> = {
  id: "issue-9",
  identifier: "E2ER-9",
  title: "Real referenced issue",
  status: "todo",
};

function resetStore() {
  useWorkspaceStore.setState({
    currentWorkspaceId: null,
    currentWorkspaceSlug: null,
    currentWorkspaceServerId: null,
  });
}

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
}

async function renderWithClient(ui: React.ReactElement) {
  const client = makeClient();
  // RNTL v14 render is async — awaiting it yields the settled query set
  // bound to this tree (same discipline as issue-reference-list.test.tsx).
  return await render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>,
  );
}

describe("mobile markdown issue-reference gate (RUYI-705 rework)", () => {
  beforeEach(resetStore);

  it("mounts and unmounts the reference tail with its visibility range", async () => {
    useWorkspaceStore.setState({
      currentWorkspaceId: "ws-1",
      currentWorkspaceSlug: "e2er",
      currentWorkspaceServerId: "srv-1",
    });
    jest.spyOn(api, "listWorkspaces").mockResolvedValue([RAW_WORKSPACE]);
    const getIssue = jest
      .spyOn(api, "getIssue")
      .mockResolvedValue(REAL_ISSUE);
    const content = "See E2ER-9";
    const client = makeClient();
    client.setQueryData(["workspaces"], [RAW_WORKSPACE] as never);

    const utils = await render(
      <QueryClientProvider client={client}>
        <IssueReferenceTailVisibilityContext.Provider value={false}>
          <Markdown content={content} />
        </IssueReferenceTailVisibilityContext.Provider>
      </QueryClientProvider>,
    );

    expect(utils.queryByLabelText("Referenced issues")).toBeNull();
    expect(getIssue).not.toHaveBeenCalled();

    await utils.rerender(
      <QueryClientProvider client={client}>
        <IssueReferenceTailVisibilityContext.Provider value>
          <Markdown content={content} />
        </IssueReferenceTailVisibilityContext.Provider>
      </QueryClientProvider>,
    );
    await waitFor(() =>
      expect(getIssue).toHaveBeenCalledWith("E2ER-9", expect.anything()),
    );
    await waitFor(() =>
      expect(utils.getByLabelText("Referenced issues")).toBeTruthy(),
    );

    await utils.rerender(
      <QueryClientProvider client={client}>
        <IssueReferenceTailVisibilityContext.Provider value={false}>
          <Markdown content={content} />
        </IssueReferenceTailVisibilityContext.Provider>
      </QueryClientProvider>,
    );
    expect(utils.queryByLabelText("Referenced issues")).toBeNull();
    getIssue.mockRestore();
  });

  it("gates bare identifiers while the id mirror lags but the route slug is known", async () => {
    // QA failure mode: restoreSlug() has restored the slug but the layout
    // effect has not confirmed the id yet — the exact state the store docs
    // describe for cold start ("the id stays null until the workspaces list
    // query resolves").
    useWorkspaceStore.setState({
      currentWorkspaceId: null,
      currentWorkspaceSlug: "e2er",
      currentWorkspaceServerId: "srv-1",
    });
    const listWorkspaces = jest
      .spyOn(api, "listWorkspaces")
      .mockResolvedValue([RAW_WORKSPACE]);
    const getIssue = jest.spyOn(api, "getIssue");

    const utils = await renderWithClient(
      <Markdown content={"AC-1\nAC-2\nAC-3\nAC-4\nAC-5\nXX-7\nE2ER-9"} />,
    );

    // Wait for the SETTLED state, not just the fetch: the first frame still
    // carries the unknown-prefix extraction, and the filtered tail only
    // exists after the list resolves and the memo recomputes.
    await waitFor(() => {
      const tail = utils.getByLabelText("Referenced issues");
      expect(within(tail).queryByText("AC-1")).toBeNull();
    });

    // Extraction must have dropped the foreign-prefix tokens: the tail list
    // keeps only the one bare identifier that matches the workspace prefix.
    const tail = utils.getByLabelText("Referenced issues");
    expect(within(tail).queryByText("XX-7")).toBeNull();
    expect(within(tail).queryByText("E2ER-9")).not.toBeNull();
    // The id mirror is still null, so no resolution request can fire yet.
    expect(getIssue).not.toHaveBeenCalled();
  });

  it("reports the actual runtime gate context once the list is loaded", async () => {
    useWorkspaceStore.setState({
      currentWorkspaceId: null,
      currentWorkspaceSlug: "e2er",
      currentWorkspaceServerId: "srv-1",
    });
    jest.spyOn(api, "listWorkspaces").mockResolvedValue([RAW_WORKSPACE]);
    const info = jest.spyOn(console, "info").mockImplementation(() => {});

    const utils = await renderWithClient(<Markdown content={"AC-1\nE2ER-9"} />);
    await waitFor(() => {
      const tail = utils.getByLabelText("Referenced issues");
      expect(within(tail).queryByText("E2ER-9")).not.toBeNull();
    });

    // The first frame logs the still-unknown prefix (list in flight); once
    // the list lands the gate re-logs with the resolved value. Assert the
    // resolved frame — the payload carries the runtime values QA could not
    // observe from the outside (the black-box boundary in the 2026-10-11
    // FAIL report).
    await waitFor(() => {
      const resolved = info.mock.calls
        .map((args) => args.join(" "))
        .find((line) => line.includes('"workspacePrefix":"E2ER"'));
      expect(resolved).toBeTruthy();
      expect(resolved).toContain('"slug":"e2er"');
    });
    info.mockRestore();
  });

  it("does not resolve known-prefix-mismatched identifiers in the tail list (web parity)", async () => {
    // Defense in depth: even if a mismatched identifier reaches the tail
    // (extraction fall-through, or a future caller bypassing the extractor),
    // the resolver must not spend a request on it once the prefix is known —
    // the same gate web's useIssueReferenceResolutions applies. Seeded cache
    // = the production steady state (the workspace layout resolves the list
    // before any issue screen can render, so the gate is active at mount).
    useWorkspaceStore.setState({
      currentWorkspaceId: "ws-1",
      currentWorkspaceSlug: "e2er",
      currentWorkspaceServerId: "srv-1",
    });
    const client = makeClient();
    client.setQueryData(["workspaces"], [RAW_WORKSPACE] as never);
    const getIssue = jest
      .spyOn(api, "getIssue")
      .mockResolvedValue(REAL_ISSUE);

    const references = [
      { form: "identifier", ref: "AC-1", label: null },
      { form: "identifier", ref: "E2ER-9", label: null },
      { form: "mention", ref: "issue-9", label: "E2ER-9" },
    ];
    await render(
      <QueryClientProvider client={client}>
        <IssueReferenceList references={references} />
      </QueryClientProvider>,
    );

    await waitFor(() =>
      expect(getIssue).toHaveBeenCalledWith("E2ER-9", expect.anything()),
    );
    expect(getIssue).not.toHaveBeenCalledWith("AC-1", expect.anything());
  });

  it("keeps the happy path: matching bare identifier resolves and folds with its mention form", async () => {
    useWorkspaceStore.setState({
      currentWorkspaceId: "ws-1",
      currentWorkspaceSlug: "e2er",
      currentWorkspaceServerId: "srv-1",
    });
    jest.spyOn(api, "listWorkspaces").mockResolvedValue([RAW_WORKSPACE]);
    const getIssue = jest
      .spyOn(api, "getIssue")
      .mockResolvedValue(REAL_ISSUE);

    const utils = await renderWithClient(
      <Markdown content={"see E2ER-9 and [E2ER-9](mention://issue/issue-9)"} />,
    );

    await waitFor(() =>
      expect(getIssue).toHaveBeenCalledWith("E2ER-9", expect.anything()),
    );
    await waitFor(() => {
      const tail = utils.getByLabelText("Referenced issues");
      expect(within(tail).queryAllByTestId("issue-reference-row")).toHaveLength(1);
    });
    const tail = utils.getByLabelText("Referenced issues");
    expect(within(tail).queryByText("AC-1")).toBeNull();
  });
});
