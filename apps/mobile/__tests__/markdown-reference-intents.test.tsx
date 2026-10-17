// @ts-nocheck

import React from "react";
import { act, fireEvent, render, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const mockPush = jest.fn();
const mockOpenExternalUrl = jest.fn();
const mockWorkspaceStore = {
  state: {
    currentWorkspaceId: "ws-1",
    currentWorkspaceSlug: "e2er",
    currentWorkspaceServerId: "srv-1",
  },
};

jest.mock("react-native-enriched-markdown", () => ({
  EnrichedMarkdownText: ({ markdown, onLinkPress }) => {
    const { createElement } = jest.requireActual("react");
    const { Text, View } = jest.requireActual("react-native");
    const targets = [...markdown.matchAll(/\]\(([^)]+)\)/g)].map((match) => match[1]);
    return createElement(View, null,
      createElement(
        Text,
        {
          testID: "enriched-markdown",
          onPress: () => targets[0] && onLinkPress?.({ url: targets[0] }),
        },
        markdown,
      ),
      ...targets.slice(1).map((target, index) => createElement(Text, {
        key: `${index}:${target}`,
        testID: `enriched-link-${index + 1}`,
        onPress: () => onLinkPress?.({ url: target }),
      }, target)),
    );
  },
}));
jest.mock("expo-router", () => ({ router: { push: (...args) => mockPush(...args) } }));
jest.mock("@/lib/attachment-open", () => ({ openAttachmentDownload: jest.fn() }));
jest.mock("@/lib/pull-request-link", () => ({
  openExternalUrl: (...args) => mockOpenExternalUrl(...args),
}));
jest.mock("@/lib/attachment-url", () => ({ resolveAttachmentUrl: (url) => url }));
jest.mock("@/lib/markdown/code-block", () => ({ CodeBlock: () => null }));
jest.mock("@/lib/markdown/mermaid-diagram", () => ({ MermaidDiagram: () => null }));
jest.mock("@/lib/markdown/markdown-image", () => ({ MarkdownImage: () => null }));
jest.mock("@/lib/markdown/table-block", () => ({ TableBlock: () => null }));
jest.mock("@/lib/markdown/markdown-style", () => ({ useMarkdownStyle: () => ({}) }));
jest.mock("@/lib/use-issue-statuses", () => ({ useIssueStatuses: () => ({ colorOf: () => "#000" }) }));
jest.mock("@/lib/use-t", () => ({ useT: () => ({ t: (_key, fallback) => fallback ?? _key }) }));
jest.mock("@/lib/use-workspace-prefix", () => ({
  useWorkspaceIssuePrefix: () => "RUYI",
}));
jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector) => selector(mockWorkspaceStore.state),
}));
jest.mock("@/lib/comment-anchor-context", () => ({ useCommentAnchor: () => ({ focus: jest.fn() }) }));
jest.mock("@/components/issue/issue-reference-list", () => ({
  IssueReferenceList: ({ references }) => {
    const { createElement } = jest.requireActual("react");
    const { Text } = jest.requireActual("react-native");
    const refs = references.map((ref) => `${ref.form}:${ref.ref}`).join(",");
    return createElement(Text, { testID: "issue-references" }, refs);
  },
}));
jest.mock("@/lib/markdown/split-markdown", () => ({
  splitMarkdown: (content) => [{ type: "prose", content }],
}));

import { Markdown } from "@/lib/markdown/markdown";
import { api } from "@/data/api";

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

async function renderMarkdown(content, client = makeClient()) {
  const utils = await render(
    <QueryClientProvider client={client}><Markdown content={content} /></QueryClientProvider>,
  );
  return { utils, client };
}

function prResult() {
  return {
    html_url: "https://github.com/cfgxy/multica-cn/pull/412",
    repo_owner: "cfgxy",
    repo_name: "multica-cn",
    number: 412,
    title: "Example PR",
    state: "open",
  };
}

beforeEach(() => {
  mockWorkspaceStore.state = {
    currentWorkspaceId: "ws-1",
    currentWorkspaceSlug: "e2er",
    currentWorkspaceServerId: "srv-1",
  };
  jest.clearAllMocks();
  mockOpenExternalUrl.mockResolvedValue({ ok: true });
});

describe("mobile Markdown reference intents (RUYI-728)", () => {
  it.each(["PR-first", "MR-first"])(
    "keeps the last PR intent for PR→MR→PR while lookup resolves %s",
    async (firstResponse) => {
      const prResolvers = [];
      const lookupPr = jest.spyOn(api, "lookupPullRequestByNumber").mockImplementation(
        () => new Promise((resolve) => prResolvers.push(resolve)),
      );
      const client = makeClient();
      const { utils } = await renderMarkdown("PR #412 MR !412", client);

      await fireEvent.press(utils.getByTestId("enriched-markdown"));
      await fireEvent.press(utils.getByTestId("enriched-link-1"));
      await fireEvent.press(utils.getByTestId("enriched-markdown"));

      expect(lookupPr).toHaveBeenCalledTimes(2);

      await act(async () => {
        if (firstResponse === "PR-first") {
          prResolvers[0](prResult());
          prResolvers[1](prResult());
        } else {
          prResolvers[1](prResult());
          prResolvers[0](prResult());
        }
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(mockOpenExternalUrl).toHaveBeenCalledTimes(1);
      expect(mockOpenExternalUrl).toHaveBeenCalledWith(prResult().html_url, expect.anything());
      expect(mockPush).not.toHaveBeenCalled();
    },
  );

  it.each([401, 403])(
    "does not open a body PR reference after a %i lookup rejection",
    async (status) => {
      const lookup = jest.spyOn(api, "lookupPullRequestByNumber").mockRejectedValue(
        Object.assign(new Error("lookup rejected"), { status }),
      );
      const client = makeClient();
      const { utils } = await renderMarkdown("PR #401", client);

      await fireEvent.press(utils.getByTestId("enriched-markdown"));
      await act(async () => {
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(lookup).toHaveBeenCalledTimes(1);
      expect(mockOpenExternalUrl).not.toHaveBeenCalled();
    },
  );

  it("links body Issues, bounds duplicate taps, and invalidates stale responses", async () => {
    const prResolvers = [];
    const lookupPr = jest.spyOn(api, "lookupPullRequestByNumber").mockImplementation(
      () => new Promise((resolve) => prResolvers.push(resolve)),
    );
    const client = makeClient();
    const { utils } = await renderMarkdown(
      "RUYI-355, **RUYI-355**\n- RUYI-355\n> RUYI-355",
      client,
    );
    const update = async (content) => {
      await utils.rerender(
        <QueryClientProvider client={client}><Markdown content={content} /></QueryClientProvider>,
      );
    };

    const body = utils.getByTestId("enriched-markdown");
    expect(body.props.children).toContain("[RUYI-355](mention://issue/RUYI-355)");
    expect(utils.getByTestId("issue-references").props.children)
      .toBe("identifier:RUYI-355");
    await fireEvent.press(body);
    expect(mockPush).toHaveBeenCalledTimes(1);
    expect(mockPush).toHaveBeenCalledWith("/e2er/issue/RUYI-355");

    await update("PR #412");
    await fireEvent.press(utils.getByTestId("enriched-markdown"));
    await fireEvent.press(utils.getByTestId("enriched-markdown"));
    expect(lookupPr).toHaveBeenCalledTimes(1);
    await act(async () => prResolvers[0](prResult()));
    await waitFor(() => expect(mockOpenExternalUrl).toHaveBeenCalledTimes(1));

    await update("PR #413");
    await fireEvent.press(utils.getByTestId("enriched-markdown"));
    await waitFor(() => expect(lookupPr).toHaveBeenCalledTimes(2));
    await update("MR !414");
    expect(utils.getByTestId("enriched-markdown").props.children)
      .toContain("mention://merge-request/414");
    await fireEvent.press(utils.getByTestId("enriched-markdown"));
    await waitFor(() => expect(lookupPr).toHaveBeenCalledTimes(3));
    await act(async () => prResolvers[2](prResult()));
    await waitFor(() => expect(mockOpenExternalUrl).toHaveBeenCalledTimes(2));
    await act(async () => prResolvers[1](prResult()));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(mockOpenExternalUrl).toHaveBeenCalledTimes(2);

    await update("PR #415");
    await fireEvent.press(utils.getByTestId("enriched-markdown"));
    await waitFor(() => expect(lookupPr).toHaveBeenCalledTimes(4));
    await update("No reference");
    await act(async () => prResolvers[3](prResult()));
    expect(mockOpenExternalUrl).toHaveBeenCalledTimes(2);

    await update("PR #416");
    await fireEvent.press(utils.getByTestId("enriched-markdown"));
    await waitFor(() => expect(lookupPr).toHaveBeenCalledTimes(5));
    mockWorkspaceStore.state = {
      currentWorkspaceId: "ws-2",
      currentWorkspaceSlug: "other",
      currentWorkspaceServerId: "srv-1",
    };
    await update("PR #416");
    await act(async () => prResolvers[4](prResult()));
    expect(mockOpenExternalUrl).toHaveBeenCalledTimes(2);
  });
});
