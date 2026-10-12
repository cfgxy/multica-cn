/**
 * Issue mention aggregation through the full RichContent pipeline (RUYI-635,
 * reworked by RUYI-728).
 *
 * Pins the body-side contract on the real renderer: markdown `mention://issue`
 * links read as underlined in-app links in the body (a bare identifier only
 * once it resolves; a miss keeps the plain-text degradation), `MR !N` and
 * `owner/repo#N` autolink to GitHub, and every reference still aggregates
 * into the tail block. Everything the extractor must skip (code spans and
 * fences, member mentions, pasted in-app URLs) stays out of it. Resolution is
 * stubbed at the hook seams — see issue-reference-footer tests for the
 * footer's internal policy.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";
import type { GitHubPullRequest, Issue } from "@multica/core/types";
import type { IssueReference } from "@multica/core/markdown";

const UUID = "1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed";
const APP_ORIGIN = "https://app.multica.ai";

let resolvedByRef: Record<string, Issue | GitHubPullRequest> = {};
// `useResolveIssueIdentifier` seam — a resolved bare identifier renders as a
// navigable link (RUYI-728); `null` keeps the miss-degrades-to-text contract.
let resolvedIdentifier: Issue | null = null;

function prFixture(number: number): GitHubPullRequest {
  return {
    id: `pr-${number}`,
    workspace_id: "ws-1",
    repo_owner: "cfgxy",
    repo_name: "multica-cn",
    number,
    title: `Pull request #${number}`,
    state: "open",
    html_url: `https://github.com/cfgxy/multica-cn/pull/${number}`,
    branch: null,
    author_login: null,
    author_avatar_url: null,
    merged_at: null,
    closed_at: null,
    pr_created_at: "",
    pr_updated_at: "",
  } as unknown as GitHubPullRequest;
}

vi.mock(
  "../issues/hooks/use-issue-reference-resolutions",
  async (importOriginal) => ({
    ...(await importOriginal<
      typeof import("../issues/hooks/use-issue-reference-resolutions")
    >()),
    useIssueReferenceResolutions: (references: IssueReference[]) =>
      new Map(
        references.map((ref) => [
          `${ref.form}:${ref.ref}`,
          resolvedByRef[`${ref.form}:${ref.ref}`] ?? null,
        ]),
      ),
  }),
);

vi.mock("../issues/hooks", () => ({
  useResolveIssueIdentifier: () => resolvedIdentifier,
}));

vi.mock("../i18n", async () => {
  const issues = (await import("../locales/en/issues.json")).default;
  const editor = (await import("../locales/en/editor.json")).default;
  const bundles = { issues, editor };
  return {
    useT: (ns: keyof typeof bundles) => ({
      t: (select: (bundle: (typeof bundles)[typeof ns]) => string) =>
        select(bundles[ns]),
    }),
    useTimeAgo: () => "just now",
  };
});

vi.mock("@multica/core/api", () => ({
  api: { getAttachmentTextContent: vi.fn() },
  PreviewTooLargeError: class extends Error {},
  PreviewUnsupportedError: class extends Error {},
}));

vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  // RUYI-705: RichContent now reads the workspace prefix; null = unknown =
  // fall-through, which is what these fixtures were written against.
  useCurrentWorkspace: () => null,
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/acme/issues/${id}`,
    projectDetail: (id: string) => `/acme/projects/${id}`,
  }),
  useWorkspaceSlug: () => "acme",
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({ colorOf: () => "#123456" }),
}));

vi.mock("../issues/components/issue-mention-card", () => ({
  IssueMentionCard: ({ issueId }: { issueId: string }) => (
    <span data-testid="issue-chip">{issueId}</span>
  ),
}));

vi.mock("../projects/components/project-chip", () => ({
  ProjectChip: ({ projectId }: { projectId: string }) => (
    <span data-testid="project-chip">{projectId}</span>
  ),
}));

vi.mock("../editor/link-hover-card", () => ({
  useLinkHover: () => ({}),
  LinkHoverCard: () => null,
}));

import { RichContent } from "./rich-content";

function adapter(): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p) => `${APP_ORIGIN}${p}`,
  };
}

function renderContent(content: string) {
  return render(
    <NavigationProvider value={adapter()}>
      <RichContent content={content} />
    </NavigationProvider>,
  );
}

beforeEach(() => {
  resolvedByRef = {};
  resolvedIdentifier = null;
});

// RUYI-728: issue mentions read as underlined in-app links in the body — the
// same real-anchor family as the PR/GitHub links. The tail block below is
// unchanged (rows stay non-navigating spans).
describe("issue mentions render as links in the body", () => {
  it("renders a UUID mention as an in-app link with its authored label", () => {
    const { container, queryByTestId } = renderContent(
      `See [MUL-7](mention://issue/${UUID}) please.`,
    );

    expect(queryByTestId("issue-chip")).toBeNull();
    const link = container.querySelector(`a[href='/acme/issues/${UUID}']`)!;
    expect(link.textContent).toBe("MUL-7");
  });

  it("renders a resolved bare identifier as an in-app link", () => {
    resolvedIdentifier = {
      id: UUID,
      identifier: "MUL-7",
      title: "Fix the thing",
      status: "in_progress",
    } as Issue;
    const { container, queryByTestId } = renderContent("Blocked by MUL-7.");

    expect(queryByTestId("issue-chip")).toBeNull();
    const link = container.querySelector(`a[href='/acme/issues/${UUID}']`)!;
    expect(link.textContent).toBe("MUL-7");
  });

  it("keeps an unresolved bare identifier as plain text", () => {
    const { container } = renderContent("Blocked by MUL-404.");

    const text = container.querySelector("[data-issue-mention-text]")!;
    expect(text.textContent).toBe("MUL-404");
    expect(text.closest("a")).toBeNull();
  });
});

// RUYI-728: `MR !N` and `owner/repo#N` autolink to GitHub in the readonly
// body. A resolved MR renders the authored text as a real anchor; a miss
// (never seen / ambiguous / still loading) degrades to plain text — no dead
// GitHub links.
describe("MR and PR text references linkify in the body", () => {
  it("renders a resolved MR reference as a GitHub pull-request link", () => {
    resolvedByRef["merge_request:MR !468"] = prFixture(468);
    const { container } = renderContent("合入 MR !468 后看效果。");

    const link = container.querySelector(
      "a[href='https://github.com/cfgxy/multica-cn/pull/468']",
    )!;
    expect(link.textContent).toBe("MR !468");
  });

  it("keeps an unresolved MR reference as plain text", () => {
    const { container } = renderContent("合入 MR !468 后看效果。");

    expect(container.querySelector("a[href*='github.com']")).toBeNull();
    expect(
      container.querySelector("[data-issue-mention-text]")!.textContent,
    ).toBe("MR !468");
  });

  it("renders a pasted owner/repo#N as a GitHub pull-request link", () => {
    const { container } = renderContent("看 cfgxy/multica-cn#338 的实现。");

    const link = container.querySelector(
      "a[href='https://github.com/cfgxy/multica-cn/pull/338']",
    )!;
    expect(link.textContent).toBe("cfgxy/multica-cn#338");
  });
});

describe("tail aggregation block", () => {
  it("lists resolved references as navigable rows after the content", () => {
    const mul7 = {
      id: UUID,
      identifier: "MUL-7",
      title: "Fix the thing",
      status: "in_progress",
    } as Issue;
    resolvedByRef = {
      [`mention:${UUID}`]: mul7,
      "identifier:MUL-7": mul7,
    };
    const { container } = renderContent(
      `See [MUL-7](mention://issue/${UUID}) and MUL-7 again.`,
    );

    const footer = container.querySelector("[data-issue-reference-footer]")!;
    expect(footer).not.toBeNull();
    // Same issue via mention + bare identifier → ONE row. RUYI-731: rows are
    // the navigation surface — a resolved row is an in-app anchor.
    expect(footer.querySelectorAll(".issue-reference-row")).toHaveLength(1);
    expect(footer.querySelectorAll("a.issue-reference-row")).toHaveLength(1);
    const row = footer.querySelector(".issue-reference-row")!;
    expect(row.textContent).toContain("MUL-7");
    expect(row.textContent).toContain("Fix the thing");
  });

  it("aggregates a resolved MR reference into a PR row", () => {
    resolvedByRef["merge_request:MR !468"] = prFixture(468);
    const { container } = renderContent("合入 MR !468 后看效果。");

    const footer = container.querySelector("[data-issue-reference-footer]")!;
    // MR !468 resolves to the same snapshot row the owner/repo#N form shows:
    // the row carries the PR's own identity; at desktop width (RUYI-724)
    // the resolved row is a direct link, same as the owner/repo#N form.
    expect(footer.querySelectorAll(".issue-reference-row")).toHaveLength(1);
    expect(footer.querySelectorAll("a.issue-reference-row")).toHaveLength(1);
    expect(footer.querySelector(".issue-reference-row")!.textContent).toContain(
      "cfgxy/multica-cn#468",
    );
  });

  it("renders no tail block when there are no references", () => {
    const { container } = renderContent("Just prose, nothing referenced.");
    expect(
      container.querySelector("[data-issue-reference-footer]"),
    ).toBeNull();
  });

  it("keeps identifiers inside code out of the tail block", () => {
    const { container } = renderContent(
      "config `MUL-1` and\n\n```\nMUL-2\n```\nstay plain.",
    );
    expect(
      container.querySelector("[data-issue-reference-footer]"),
    ).toBeNull();
  });

  it("keeps member mentions out of the tail block", () => {
    const { container } = renderContent(
      `Ping [@Ada](mention://member/${UUID}) about this.`,
    );
    expect(
      container.querySelector("[data-issue-reference-footer]"),
    ).toBeNull();
  });

  it("degrades unresolved references to plain text rows", () => {
    const { container } = renderContent("Blocked by MUL-404.");
    const footer = container.querySelector("[data-issue-reference-footer]")!;
    expect(footer.querySelector("a.issue-reference-row")).toBeNull();
    expect(footer.querySelector(".issue-reference-degraded")!.textContent).toBe(
      "MUL-404",
    );
  });
});

describe("pasted in-app issue URLs keep their existing unfurl behaviour", () => {
  it("still renders an issue chip and stays out of the tail block", () => {
    const { container, getByTestId } = renderContent(
      `Copied from ${APP_ORIGIN}/acme/issues/${UUID}.`,
    );

    // The author wrote a link — the unfurl keeps it navigable in place
    // (entity-link-unfurl.test.tsx pins the chip/link split).
    expect(getByTestId("issue-chip")).not.toBeNull();
    expect(
      container.querySelector("[data-issue-reference-footer]"),
    ).toBeNull();
  });
});
