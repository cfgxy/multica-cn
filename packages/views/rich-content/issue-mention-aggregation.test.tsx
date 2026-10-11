/**
 * Issue mention aggregation through the full RichContent pipeline (RUYI-635).
 *
 * Pins the body-side contract on the real renderer: markdown `mention://issue`
 * links and bare identifiers read as plain text in the body (no chip, no link),
 * the references surface as a tail block, and everything the extractor must
 * skip (code spans and fences, member mentions, pasted in-app URLs) stays out
 * of it. Resolution is stubbed at the hook seam — see issue-reference-footer
 * tests for the footer's internal policy.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";
import type { Issue } from "@multica/core/types";
import type { IssueReference } from "@multica/core/markdown";

const UUID = "1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed";
const APP_ORIGIN = "https://app.multica.ai";

let resolvedByRef: Record<string, Issue> = {};

vi.mock("../issues/hooks/use-issue-reference-resolutions", () => ({
  useIssueReferenceResolutions: (references: IssueReference[]) =>
    new Map(
      references.map((ref) => [
        `${ref.form}:${ref.ref}`,
        resolvedByRef[`${ref.form}:${ref.ref}`] ?? null,
      ]),
    ),
}));

vi.mock("../issues/hooks", () => ({
  useResolveIssueIdentifier: () => null,
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
});

describe("issue mentions read as plain text in the body", () => {
  it("renders a UUID mention as its authored label, not a chip", () => {
    const { container, queryByTestId } = renderContent(
      `See [MUL-7](mention://issue/${UUID}) please.`,
    );

    expect(queryByTestId("issue-chip")).toBeNull();
    const text = container.querySelector("[data-issue-mention-text]")!;
    expect(text.textContent).toBe("MUL-7");
    // Plain text — not a link.
    expect(text.closest("a")).toBeNull();
  });

  it("renders an autolinked bare identifier as plain text", () => {
    const { container, queryByTestId } = renderContent("Blocked by MUL-7.");

    expect(queryByTestId("issue-chip")).toBeNull();
    const text = container.querySelector("[data-issue-mention-text]")!;
    expect(text.textContent).toBe("MUL-7");
  });
});

describe("tail aggregation block", () => {
  it("lists resolved references as clickable rows after the content", () => {
    resolvedByRef = {
      [`mention:${UUID}`]: {
        id: UUID,
        identifier: "MUL-7",
        title: "Fix the thing",
        status: "in_progress",
      } as Issue,
    };
    const { container } = renderContent(
      `See [MUL-7](mention://issue/${UUID}) and MUL-7 again.`,
    );

    const footer = container.querySelector("[data-issue-reference-footer]")!;
    expect(footer).not.toBeNull();
    // Same issue via mention + bare identifier → ONE row.
    expect(footer.querySelectorAll("a.issue-reference-row")).toHaveLength(1);
    const row = footer.querySelector("a.issue-reference-row")!;
    expect(row.getAttribute("href")).toBe(`/acme/issues/${UUID}`);
    expect(row.textContent).toContain("MUL-7");
    expect(row.textContent).toContain("Fix the thing");
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
