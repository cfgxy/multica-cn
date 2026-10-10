import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import type { ReactNode } from "react";
import type { Issue } from "@multica/core/types";
import type { IssueReference } from "@multica/core/markdown";
import { renderWithI18n } from "../test/i18n";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";
import {
  CurrentIssueRenderContextProvider,
  type CurrentIssueRenderContextValue,
} from "../issues/current-issue-render-context";

const resolvedIssues = vi.hoisted(() => ({
  current: null as Issue | null,
  other: null as Issue | null,
}));

vi.mock("../issues/hooks", () => ({
  useResolveIssueIdentifier: (identifier: string) => {
    if (identifier === "MUL-7") return resolvedIssues.current;
    if (identifier === "MUL-8") return resolvedIssues.other;
    return null;
  },
}));

// Footer resolution seam (RUYI-635): mention-form references aggregate into
// the tail footer, so its hook must resolve without a QueryClient here.
vi.mock(
  "../issues/hooks/use-issue-reference-resolutions",
  () => ({
    useIssueReferenceResolutions: (references: IssueReference[]) =>
      new Map(
        references.map((ref) => [
          `${ref.form}:${ref.ref}`,
          ref.ref === resolvedIssues.current?.id
            ? resolvedIssues.current
            : ref.ref === "MUL-8"
              ? resolvedIssues.other
              : null,
        ]),
      ),
  }),
);

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({ colorOf: () => "#123456" }),
}));

vi.mock("../issues/components/issue-chip", () => ({
  IssueChip: ({
    issueId,
    children,
  }: {
    issueId: string;
    children?: ReactNode;
  }) => (
    <span
      data-testid="issue-chip"
      data-issue-id={issueId}
      data-current={children !== undefined ? "true" : "false"}
    >
      {children ?? issueId}
    </span>
  ),
}));

vi.mock("../issues/components/issue-hover-card", () => ({
  IssueHoverCard: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

vi.mock("../editor/link-hover-card", () => ({
  useLinkHover: () => ({}),
  LinkHoverCard: () => null,
}));

vi.mock("@multica/core/api", () => ({
  api: { getAttachmentTextContent: vi.fn() },
  PreviewTooLargeError: class extends Error {},
  PreviewUnsupportedError: class extends Error {},
}));

vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/acme/issues/${id}`,
    projectDetail: (id: string) => `/acme/projects/${id}`,
  }),
  useWorkspaceSlug: () => "acme",
}));

vi.mock("mermaid", () => ({
  default: { initialize: vi.fn(), render: vi.fn() },
}));

import { RichContent } from "./rich-content";

const APP_ORIGIN = "https://app.example";
const CURRENT_ID = "11111111-1111-4111-8111-111111111111";
const OTHER_ID = "22222222-2222-4222-8222-222222222222";
const CURRENT_CONTEXT: CurrentIssueRenderContextValue = {
  id: CURRENT_ID,
  identifier: "MUL-7",
};

/**
 * RUYI-635 split inline issue-reference rendering in two, and the
 * current-issue marker split with it:
 *
 *   - Mention forms — `[MUL-7](mention://issue/<uuid>)` and the autolinked
 *     bare identifier — read as plain body text and aggregate into the tail
 *     footer. No chip renders, so "This issue" marking no longer applies to
 *     them, provider or not.
 *   - Pasted in-app issue URLs keep their chip (the author wrote a link), and
 *     a URL pointing at the issue being viewed still marks itself "This issue"
 *     through CurrentIssueRenderContext → IssueMentionCard.
 *
 * Each fixture pairs the self-reference with an other-issue reference on the
 * SAME path, so a failure means the current-issue check broke, not that the
 * path stopped resolving at all.
 */
const URL_FORMS: ReadonlyArray<readonly [string, string]> = [
  [
    "a UUID URL",
    `See ${APP_ORIGIN}/acme/issues/${CURRENT_ID} and ${APP_ORIGIN}/acme/issues/${OTHER_ID}.`,
  ],
  [
    "an identifier URL",
    `See ${APP_ORIGIN}/acme/issues/MUL-7 and ${APP_ORIGIN}/acme/issues/MUL-8.`,
  ],
];

const MENTION_CONTENT = `See [MUL-7](mention://issue/${CURRENT_ID}) and MUL-8.`;

function makeIssue(id: string, identifier: string): Issue {
  return {
    id,
    identifier,
    title: `Issue ${identifier}`,
    status: "in_progress",
    status_category: "started",
  } as unknown as Issue;
}

function adapter(): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (path) => `${APP_ORIGIN}${path}`,
  };
}

function renderContent(
  context?: CurrentIssueRenderContextValue,
  content = MENTION_CONTENT,
) {
  return renderWithI18n(
    <NavigationProvider value={adapter()}>
      {context ? (
        <CurrentIssueRenderContextProvider value={context}>
          <RichContent content={content} />
        </CurrentIssueRenderContextProvider>
      ) : (
        <RichContent content={content} />
      )}
    </NavigationProvider>,
  );
}

beforeEach(() => {
  resolvedIssues.current = makeIssue(CURRENT_ID, "MUL-7");
  resolvedIssues.other = makeIssue(OTHER_ID, "MUL-8");
});

describe("RichContent current-issue rendering", () => {
  it("renders mention forms as plain text and aggregates them into the footer, without the current-issue chip", () => {
    const { container } = renderContent(CURRENT_CONTEXT);

    expect(screen.queryByTestId("issue-chip")).toBeNull();
    const mentions = Array.from(
      container.querySelectorAll("[data-issue-mention-text]"),
    );
    expect(mentions.map((m) => m.textContent)).toEqual(["MUL-7", "MUL-8"]);

    const rows = Array.from(container.querySelectorAll("a.issue-reference-row"));
    expect(rows.map((row) => row.getAttribute("href"))).toEqual([
      `/acme/issues/${CURRENT_ID}`,
      `/acme/issues/${OTHER_ID}`,
    ]);
    expect(rows[0]!.textContent).toContain("MUL-7");
    expect(rows[0]!.textContent).toContain("Issue MUL-7");
  });

  it.each(URL_FORMS)(
    "keeps the chip for a pasted self-reference written as %s, and only it, marked current",
    (_form, content) => {
      renderContent(CURRENT_CONTEXT, content);

      const chips = screen.getAllByTestId("issue-chip");
      expect(chips.map((chip) => chip.getAttribute("data-issue-id"))).toEqual([
        CURRENT_ID,
        OTHER_ID,
      ]);
      expect(chips.map((chip) => chip.getAttribute("data-current"))).toEqual([
        "true",
        "false",
      ]);
      expect(chips[0]).toHaveTextContent("This issue · MUL-7");
    },
  );

  it("keeps pasted URL chips unmarked without a current-issue provider", () => {
    renderContent(undefined, URL_FORMS[0]![1]);

    const chips = screen.getAllByTestId("issue-chip");
    expect(chips).toHaveLength(2);
    for (const chip of chips) {
      expect(chip).toHaveAttribute("data-current", "false");
    }
  });
});
