/**
 * RUYI-635 rework: the issue body renders through ContentEditor (the
 * always-mounted inline editor), not RichContent — so the markdown
 * pipeline's mention degradation never reached the body surface and
 * `[QA-2](mention://issue/<uuid>)` kept drawing inline IssueMentionCards
 * there (QA FAIL 2026-10-10). `demoteIssueMentions` opts an editor host
 * into the same display contract RichContent already applies: issue
 * mentions read as plain text (identifier form shows the identifier, a
 * UUID mention shows its authored label) and navigation moves to the tail
 * list. Composers keep the chips — the default is asserted here so the
 * opt-in cannot silently widen.
 */
import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../test/i18n";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";

vi.mock("../issues/components/issue-chip", () => ({
  IssueChip: ({
    issueId,
    children,
  }: {
    issueId: string;
    children?: React.ReactNode;
  }) => (
    <span data-testid="issue-chip" data-issue-id={issueId}>
      {children}
    </span>
  ),
}));

vi.mock("../issues/components/issue-hover-card", () => ({
  IssueHoverCard: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

vi.mock("./link-hover-card", () => ({
  useLinkHover: () => ({}),
  LinkHoverCard: () => null,
}));

vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/acme/issues/${id}`,
    projectDetail: (id: string) => `/acme/projects/${id}`,
  }),
  useWorkspaceSlug: () => "acme",
}));

import { ContentEditor } from "./content-editor";

const ISSUE_UUID = "22222222-2222-4222-8222-222222222222";

function adapter(): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (path) => `https://app.example${path}`,
  };
}

function renderEditor(content: string, demote: boolean) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return renderWithI18n(
    <NavigationProvider value={adapter()}>
      <QueryClientProvider client={queryClient}>
        <ContentEditor
          defaultValue={content}
          showBubbleMenu={false}
          {...(demote ? { demoteIssueMentions: true } : {})}
        />
      </QueryClientProvider>
    </NavigationProvider>,
  );
}

describe("ContentEditor demoteIssueMentions", () => {
  it("renders issue mentions as plain text with no inline card when demoted", async () => {
    renderEditor(
      [
        `重复引用 [QA-2](mention://issue/${ISSUE_UUID}) 与 [QA-2](mention://issue/${ISSUE_UUID})；`,
        `[MUL-7](mention://issue/MUL-7) 结尾。`,
      ].join(""),
      true,
    );

    // The chip subtree never mounts — zero inline cards on the body surface.
    await waitFor(() => {
      expect(screen.getAllByText(/^(QA-2|MUL-7)$/, { selector: "[data-issue-mention-text]" })).toHaveLength(3);
    });
    expect(screen.queryByTestId("issue-chip")).not.toBeInTheDocument();

    // Display contract matches the markdown pipeline: a UUID mention shows
    // its authored label, an identifier-form mention shows the identifier.
    const demoted = screen.getAllByText(/^(QA-2|MUL-7)$/, { selector: "[data-issue-mention-text]" });
    expect(demoted.map((el) => el.textContent)).toEqual(["QA-2", "QA-2", "MUL-7"]);
  });

  it("keeps the inline chip by default so composers are untouched", async () => {
    renderEditor(`[QA-2](mention://issue/${ISSUE_UUID})`, false);

    await waitFor(() => {
      expect(screen.getByTestId("issue-chip")).toBeInTheDocument();
    });
  });
});
