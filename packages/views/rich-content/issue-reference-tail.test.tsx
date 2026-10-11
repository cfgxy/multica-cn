/**
 * IssueReferenceTail (RUYI-635 + 增量) — the content-to-footer block shared by
 * RichContent and the issue body's ContentEditor host. The fixtures drive it
 * from raw markdown the way both hosts do, pinning: extraction from prose
 * (mention links + bare identifiers + `owner/repo#N` text references), raw
 * dedup + cross-form collapse, the no-references negative case, the code-span
 * skip contract, and the anti-mistouch invariant that the rendered block
 * contains no inline anchors at all. Resolution is stubbed at the hook seam;
 * row rendering policy is exercised for real in
 * issue-reference-footer.test.tsx.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";
import type { GitHubPullRequest, Issue } from "@multica/core/types";
import type { IssueReferenceResolution } from "../issues/hooks/use-issue-reference-resolutions";

// `null` = unresolved (loading, 404, wrong prefix all look alike here).
let resolutions: Record<string, IssueReferenceResolution> = {};

vi.mock("../issues/hooks/use-issue-reference-resolutions", async () => {
  const actual = await vi.importActual<
    typeof import("../issues/hooks/use-issue-reference-resolutions")
  >("../issues/hooks/use-issue-reference-resolutions");
  return {
    ...actual,
    useIssueReferenceResolutions: (references: {
      form: string;
      ref: string;
    }[]) =>
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

import { IssueReferenceTail } from "./issue-reference-footer";

const UUID = "1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed";

function issue(id: string, identifier: string, title: string): Issue {
  return {
    id,
    identifier,
    title,
    status: "in_progress",
    status_category: "started",
  } as unknown as Issue;
}

function pr(number: number, state = "open"): GitHubPullRequest {
  return {
    id: `pr-${number}`,
    workspace_id: "ws-1",
    repo_owner: "cfgxy",
    repo_name: "multica-cn",
    number,
    title: `Pull request #${number}`,
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

function renderTail(content: string) {
  const adapter: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p) => `https://app.multica.ai${p}`,
  };
  return render(
    <NavigationProvider value={adapter}>
      <IssueReferenceTail content={content} />
    </NavigationProvider>,
  );
}

describe("IssueReferenceTail", () => {
  beforeEach(() => {
    resolutions = {};
  });

  it("aggregates repeated and cross-form references into one row", () => {
    resolutions = {
      [`mention:${UUID}`]: issue(UUID, "QA-2", "引用目标"),
      "identifier:QA-2": issue(UUID, "QA-2", "引用目标"),
    };
    const { container } = renderTail(
      `重复引用 [QA-2](mention://issue/${UUID}) 与 [QA-2](mention://issue/${UUID})、QA-2。`,
    );

    // Inline rows are spans: the block is one button and nothing inside it
    // navigates (anti-mistouch 增量) — the row count is the contract here.
    expect(container.querySelectorAll("a.issue-reference-row")).toHaveLength(0);
    const rows = container.querySelectorAll(".issue-reference-row");
    expect(rows).toHaveLength(1);
    expect(rows[0]!.textContent).toContain("QA-2");
  });

  it("renders nothing for content without references", () => {
    const { container } = renderTail("正文没有引用，不应出现尾部区块。");
    expect(container.querySelector("[data-issue-reference-footer]")).toBeNull();
  });

  it("skips mention links inside code spans", () => {
    const { container } = renderTail(
      `代码里的链接不算引用：\`[QA-2](mention://issue/${UUID})\`。`,
    );
    expect(container.querySelector("[data-issue-reference-footer]")).toBeNull();
  });

  it("aggregates a bare owner/repo#N text reference into a PR row", () => {
    resolutions = { "pull_request:cfgxy/multica-cn#338": pr(338, "merged") };
    const { container } = renderTail(
      "看 cfgxy/multica-cn#338 的实现。",
    );

    const row = container.querySelector(".issue-reference-row")!;
    expect(row.textContent).toContain("cfgxy/multica-cn#338");
    expect(container.querySelectorAll(".issue-reference-row")).toHaveLength(1);
  });

  it("degrades an unresolved mention to its authored label", () => {
    const { container } = renderTail(`见 [QA-2](mention://issue/${UUID})。`);

    expect(container.querySelector("a.issue-reference-row")).toBeNull();
    const row = container.querySelector(".issue-reference-degraded")!;
    expect(row.textContent).toBe("QA-2");
  });
});
