/**
 * IssueReferenceTail (RUYI-635) — the content-to-footer block shared by
 * RichContent and the issue body's ContentEditor host. The fixtures drive it
 * from raw markdown the way both hosts do, pinning: extraction from prose
 * (mention links + bare identifiers), raw dedup + cross-form collapse, the
 * no-references negative case, and the code-span skip contract. Resolution
 * is stubbed at the hook seam; row rendering policy is exercised for real in
 * issue-reference-footer.test.tsx.
 */
import { describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";
import type { Issue } from "@multica/core/types";

// `null` = unresolved (loading, 404, wrong prefix all look alike here).
let resolutions: Record<string, Issue | null> = {};
// The tail gates bare identifiers on the current workspace's issue_prefix
// (RUYI-705); fixtures below use QA-* so the default workspace matches them.
let workspace: { id: string; issue_prefix: string } | null = {
  id: "ws-1",
  issue_prefix: "QA",
};

vi.mock("../issues/hooks/use-issue-reference-resolutions", () => ({
  useIssueReferenceResolutions: (references: { form: string; ref: string }[]) =>
    new Map(
      references.map((ref) => [
        `${ref.form}:${ref.ref}`,
        resolutions[`${ref.form}:${ref.ref}`] ?? null,
      ]),
    ),
}));

vi.mock("../issues/hooks", () => ({
  useResolveIssueIdentifier: () => null,
}));

vi.mock("../i18n", async () => {
  const issues = (await import("../locales/en/issues.json")).default;
  return {
    useT: () => ({
      t: (select: (bundle: typeof issues) => string) => select(issues),
    }),
  };
});

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/acme/issues/${id}`,
  }),
  useWorkspaceSlug: () => "acme",
  useCurrentWorkspace: () => workspace,
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
  it("aggregates repeated and cross-form references into one row", () => {
    resolutions = {
      [`mention:${UUID}`]: issue(UUID, "QA-2", "引用目标"),
      "identifier:QA-2": issue(UUID, "QA-2", "引用目标"),
    };
    const { container } = renderTail(
      `重复引用 [QA-2](mention://issue/${UUID}) 与 [QA-2](mention://issue/${UUID})、QA-2。`,
    );

    const rows = container.querySelectorAll("a.issue-reference-row");
    expect(rows).toHaveLength(1);
    expect(rows[0]!.getAttribute("href")).toBe(`/acme/issues/${UUID}`);
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

  it("degrades an unresolved mention to its authored label", () => {
    resolutions = {};
    const { container } = renderTail(`见 [QA-2](mention://issue/${UUID})。`);

    expect(container.querySelector("a.issue-reference-row")).toBeNull();
    const row = container.querySelector(".issue-reference-degraded")!;
    expect(row.textContent).toBe("QA-2");
  });

  it("drops bare identifiers whose prefix does not match the workspace (RUYI-705)", () => {
    resolutions = {
      "identifier:QA-2": issue(UUID, "QA-2", "引用目标"),
    };
    const { container } = renderTail(
      "绩效达成情况：\n- AC-1 验收条目\n- AC-2 验收条目\n- AC-3 验收条目\n- AC-4 验收条目\n- AC-5 验收条目\n真实引用 QA-2。",
    );

    const rows = container.querySelectorAll(".issue-reference-row");
    expect(rows).toHaveLength(1);
    expect(rows[0]!.textContent).toContain("QA-2");
    expect(container.textContent).not.toContain("AC-1");
  });

  it("keeps every bare identifier while the workspace is unknown", () => {
    workspace = null;
    try {
      const { container } = renderTail("AC-1 与 QA-2 都先放行。");
      expect(container.querySelectorAll(".issue-reference-row")).toHaveLength(2);
    } finally {
      workspace = { id: "ws-1", issue_prefix: "QA" };
    }
  });

  it("never applies the prefix filter to mention-form references", () => {
    resolutions = {};
    const { container } = renderTail(
      `见 [AC-1](mention://issue/${UUID})。`,
    );

    const row = container.querySelector(".issue-reference-degraded")!;
    expect(row.textContent).toBe("AC-1");
  });
});
