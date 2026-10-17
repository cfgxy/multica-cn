/**
 * IssueReferenceFooter (RUYI-731) — the flat, navigable reference tail.
 *
 * The 增量's two-level surface (2-row preview budget + "N more" fold line +
 * popover) is retired: every reference renders as one row in
 * first-appearance order, and rows navigate per kind (issue → in-app route,
 * PR → canonical URL in a new tab). Two behaviours carry this issue:
 *   - self-filter: inside the issue-detail render context, a reference that
 *     resolves to the CURRENT issue renders no row. The comment and the
 *     issue-description surfaces share this footer, so the context seam is
 *     what's under test here (the description host is pinned in
 *     issue-reference-tail.test.tsx);
 *   - no budget: N=1 and N=10 render identically — no fold line, no second
 *     level, no "N more" arithmetic.
 * Surfaces without a current-issue context (chat, inbox) keep every row.
 *
 * Resolution is stubbed at the hook seam; row rendering, dedup, filtering and
 * navigation semantics are exercised for real.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";
import {
  CurrentIssueRenderContextProvider,
} from "../issues/current-issue-render-context";
import type { GitHubPullRequest, Issue } from "@multica/core/types";
import type { IssueReference } from "@multica/core/markdown";
import type { IssueReferenceResolution } from "../issues/hooks/use-issue-reference-resolutions";

// `null` = unresolved (loading, 404, wrong prefix all look alike here).
let resolutions: Record<string, IssueReferenceResolution> = {};

vi.mock("../issues/hooks/use-issue-reference-resolutions", async () => {
  const actual = await vi.importActual<
    typeof import("../issues/hooks/use-issue-reference-resolutions")
  >("../issues/hooks/use-issue-reference-resolutions");
  return {
    ...actual,
    useIssueReferenceResolutions: (references: IssueReference[]) =>
      new Map(
        references.map((ref) => [
          `${ref.form}:${ref.ref}`,
          resolutions[`${ref.form}:${ref.ref}`] ?? null,
        ]),
      ),
  };
});

vi.mock("../i18n", async () => {
  const issues = (await import("../locales/en/issues.json")).default;
  return {
    useT: () => ({
      t: (
        select: (bundle: typeof issues) => string,
        params?: Record<string, string | number>,
      ) => {
        const template = select(issues);
        if (!params) return template;
        return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) =>
          String(params[key]),
        );
      },
    }),
  };
});

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/acme/issues/${id}`,
  }),
  useWorkspaceSlug: () => "acme",
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({ colorOf: () => "#123456" }),
}));

import { IssueReferenceFooter } from "./issue-reference-footer";

const UUID = "1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed";
const UUID2 = "8f14e45f-ceea-4670-9b1c-0d3e4f5a6b7c";

function issue(id: string, identifier: string, title: string): Issue {
  // Fixture: only the fields the footer reads — the rest of the Issue shape
  // is irrelevant here, hence the double cast.
  return {
    id,
    identifier,
    title,
    status: "in_progress",
    status_category: "started",
  } as unknown as Issue;
}

function pr(
  number: number,
  state = "open",
  title = `Pull request #${number}`,
): GitHubPullRequest {
  return {
    id: `pr-${number}`,
    workspace_id: "ws-1",
    repo_owner: "cfgxy",
    repo_name: "multica-cn",
    number,
    title,
    state,
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

function mention(ref: string, label: string): IssueReference {
  return { ref, label, form: "mention" };
}

function bare(ref: string): IssueReference {
  return { ref, label: null, form: "identifier" };
}

function prRef(token: string): IssueReference {
  return { ref: token, label: null, form: "pull_request" };
}

const PR338 = "pull_request:cfgxy/multica-cn#338";

function renderFooter(
  references: IssueReference[],
  currentIssueId?: string | null,
) {
  const adapter: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p) => `https://app.multica.ai${p}`,
  };
  const footer = <IssueReferenceFooter references={references} />;
  return render(
    <NavigationProvider value={adapter}>
      {currentIssueId ? (
        <CurrentIssueRenderContextProvider
          value={{ id: currentIssueId, identifier: "MUL-7" }}
        >
          {footer}
        </CurrentIssueRenderContextProvider>
      ) : (
        footer
      )}
    </NavigationProvider>,
  );
}

describe("IssueReferenceFooter", () => {
  beforeEach(() => {
    resolutions = {};
  });

  it("renders one navigable row per reference", () => {
    resolutions = { [`mention:${UUID}`]: issue(UUID, "MUL-7", "Fix the thing") };
    const { container } = renderFooter([mention(UUID, "MUL-7")]);

    const footer = container.querySelector("[data-issue-reference-footer]")!;
    expect(footer.textContent).toContain("References · 1");
    // Rows are the navigation surface now (RUYI-731): a resolved issue row is
    // an in-app link, not a span behind a popover.
    const row = container.querySelector("a.issue-reference-row")!;
    expect(row).not.toBeNull();
    expect(row.getAttribute("href")).toBe(`/acme/issues/${UUID}`);
    expect(row.textContent).toContain("MUL-7");
    expect(row.textContent).toContain("Fix the thing");
  });

  it("collapses repeated and cross-form mentions of the same issue into one row", () => {
    resolutions = {
      [`mention:${UUID}`]: issue(UUID, "MUL-7", "Fix the thing"),
      [`identifier:MUL-7`]: issue(UUID, "MUL-7", "Fix the thing"),
    };
    const { container } = renderFooter([
      mention(UUID, "MUL-7"),
      mention(UUID, "MUL-7"),
      bare("MUL-7"),
    ]);

    expect(container.querySelectorAll(".issue-reference-row")).toHaveLength(1);
  });

  it("renders nothing when there are no references", () => {
    const { container } = renderFooter([]);
    expect(container.querySelector("[data-issue-reference-footer]")).toBeNull();
  });

  it("renders every reference flat — N rows for N references, no fold line, no second level", () => {
    const ids = Array.from({ length: 10 }, (_, i) => `id-${i}`);
    resolutions = Object.fromEntries(
      ids.map((id, i) => [`identifier:MUL-${i}`, issue(id, `MUL-${i}`, `T${i}`)]),
    );
    const refs = ids.map((_, i) => bare(`MUL-${i}`));
    const { container } = renderFooter(refs);

    const footer = container.querySelector("[data-issue-reference-footer]")!;
    expect(footer.textContent).toContain("References · 10");
    expect(container.querySelectorAll(".issue-reference-row")).toHaveLength(10);
    // The fold affordances are retired outright: no "N more" line, no
    // popover/bottom-sheet layer, N=1 and N=10 behave the same.
    expect(container.querySelector(".issue-reference-collapsed")).toBeNull();
    expect(container.querySelector(".issue-reference-chevron")).toBeNull();
  });

  it("navigates rows per kind: issue in-app, PR canonical URL in a new tab", () => {
    resolutions = {
      "identifier:MUL-1": issue(UUID, "MUL-1", "First"),
      [PR338]: pr(338, "merged"),
    };
    const { container } = renderFooter([
      bare("MUL-1"),
      prRef("cfgxy/multica-cn#338"),
    ]);

    const issueRow = container.querySelector("a[data-reference-kind='issue']")!;
    expect(issueRow.getAttribute("href")).toBe(`/acme/issues/${UUID}`);

    const prRow = container.querySelector("a[data-reference-kind='pr']")!;
    expect(prRow.getAttribute("href")).toBe(
      "https://github.com/cfgxy/multica-cn/pull/338",
    );
    expect(prRow.getAttribute("target")).toBe("_blank");
    expect(prRow.getAttribute("rel")).toContain("noreferrer");
    expect(prRow.querySelector(".issue-reference-identifier")?.textContent)
      .toBe("#338");
    expect(prRow.querySelector(".issue-reference-pr-project")?.textContent)
      .toBe("multica-cn/");
    expect(prRow.querySelector(".issue-reference-title")?.textContent)
      .toBe("Pull request #338");
  });

  it("keeps the owner visible for PRs outside the cfgxy organization", () => {
    const otherOwnerPr = {
      ...pr(339, "open", "Support another repository"),
      repo_owner: "example",
      repo_name: "project",
    } as GitHubPullRequest;
    resolutions = { ["pull_request:example/project#339"]: otherOwnerPr };
    const { container } = renderFooter([prRef("example/project#339")]);

    const row = container.querySelector("a[data-reference-kind='pr']")!;
    expect(row.querySelector(".issue-reference-identifier")?.textContent)
      .toBe("#339");
    expect(row.querySelector(".issue-reference-pr-project")?.textContent)
      .toBe("example/project/");
  });

  it("keeps unresolved references as non-clickable plain-text slots in first-appearance order", () => {
    resolutions = {
      [`mention:${UUID2}`]: issue(UUID2, "MUL-9", "Later issue"),
      "identifier:MUL-2": null,
      [PR338]: null,
    };
    const { container } = renderFooter([
      bare("MUL-2"),
      mention(UUID2, "MUL-9"),
      prRef("cfgxy/multica-cn#338"),
    ]);

    const rows = Array.from(
      container.querySelectorAll(".issue-reference-row"),
    );
    expect(rows.map((r) => r.textContent)).toEqual([
      "MUL-2",
      "MUL-9Later issue",
      "cfgxy/multica-cn#338",
    ]);
    // Degraded slots stay plain text — no authored-label navigation.
    expect(
      container.querySelectorAll("a.issue-reference-degraded"),
    ).toHaveLength(0);
    const degraded = container.querySelector(".issue-reference-degraded")!;
    expect(degraded.tagName).toBe("SPAN");
    // RUYI-730: the degraded slot keeps a placeholder glyph so its text
    // starts on the same grid as resolved rows (icon column aligned).
    expect(degraded.querySelector("svg")).not.toBeNull();
  });

  it("drops a reference that resolves to the current issue (self-filter)", () => {
    resolutions = { [`mention:${UUID}`]: issue(UUID, "MUL-7", "Fix the thing") };
    const { container } = renderFooter([mention(UUID, "MUL-7")], UUID);

    // Every reference was a self-reference: no rows, no block. This assertion
    // fails the moment the filter is removed — the resolved row would render.
    expect(container.querySelector("[data-issue-reference-footer]")).toBeNull();
  });

  it("drops a cross-form self-reference (mention + bare identifier of the current issue)", () => {
    resolutions = {
      [`mention:${UUID}`]: issue(UUID, "MUL-7", "Fix the thing"),
      "identifier:MUL-7": issue(UUID, "MUL-7", "Fix the thing"),
    };
    const { container } = renderFooter(
      [mention(UUID, "MUL-7"), bare("MUL-7")],
      UUID,
    );

    expect(container.querySelector("[data-issue-reference-footer]")).toBeNull();
  });

  it("drops only the self row and keeps the rest, with the header count following", () => {
    resolutions = {
      [`mention:${UUID}`]: issue(UUID, "MUL-7", "Self"),
      "identifier:MUL-9": issue(UUID2, "MUL-9", "Other"),
    };
    const { container } = renderFooter(
      [mention(UUID, "MUL-7"), bare("MUL-9")],
      UUID,
    );

    const rows = container.querySelectorAll(".issue-reference-row");
    expect(rows).toHaveLength(1);
    expect(rows[0]!.textContent).toContain("MUL-9");
    expect(
      container.querySelector("[data-issue-reference-footer]")!.textContent,
    ).toContain("References · 1");
  });

  it("keeps every row on surfaces without a current-issue context (chat, inbox)", () => {
    resolutions = { [`mention:${UUID}`]: issue(UUID, "MUL-7", "Fix the thing") };
    const { container } = renderFooter([mention(UUID, "MUL-7")], null);

    expect(
      container.querySelectorAll(".issue-reference-row"),
    ).toHaveLength(1);
  });

  it("keeps a row whose resolved id differs from the current issue", () => {
    resolutions = { "identifier:MUL-9": issue(UUID2, "MUL-9", "Other") };
    const { container } = renderFooter([bare("MUL-9")], UUID);

    expect(
      container.querySelectorAll(".issue-reference-row"),
    ).toHaveLength(1);
  });
});
