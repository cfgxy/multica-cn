/**
 * Tail aggregation of issue references (RUYI-635).
 *
 * RichContent hands every collected reference to IssueReferenceFooter. The
 * fixtures below pin the four behaviours the acceptance criteria call out:
 * dedup across repeated mentions, cross-form collapse (UUID mention + bare
 * identifier of the same issue = one row), the negative case (no references →
 * no block), and identifier-resolution failure degrading to plain text.
 * Resolution is stubbed at the hook seam; row rendering and dedup policy are
 * exercised for real.
 */
import { describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";
import type { Issue } from "@multica/core/types";
import type { IssueReference } from "@multica/core/markdown";

// `null` = unresolved (loading, 404, wrong prefix all look alike here).
let resolutions: Record<string, Issue | null> = {};

vi.mock("../issues/hooks/use-issue-reference-resolutions", () => ({
  useIssueReferenceResolutions: (references: IssueReference[]) =>
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

function mention(ref: string, label: string): IssueReference {
  return { ref, label, form: "mention" };
}

function bare(ref: string): IssueReference {
  return { ref, label: null, form: "identifier" };
}

function renderFooter(references: IssueReference[]) {
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
      <IssueReferenceFooter references={references} />
    </NavigationProvider>,
  );
}

describe("IssueReferenceFooter", () => {
  it("renders one clickable row per resolved reference", () => {
    resolutions = { [`mention:${UUID}`]: issue(UUID, "MUL-7", "Fix the thing") };
    const { container } = renderFooter([mention(UUID, "MUL-7")]);

    const row = container.querySelector("a.issue-reference-row")!;
    expect(row.getAttribute("href")).toBe(`/acme/issues/${UUID}`);
    expect(row.textContent).toContain("MUL-7");
    expect(row.textContent).toContain("Fix the thing");
  });

  it("collapses repeated mentions of the same issue into one row", () => {
    resolutions = { [`mention:${UUID}`]: issue(UUID, "MUL-7", "Fix the thing") };
    const { container } = renderFooter([
      mention(UUID, "MUL-7"),
      mention(UUID, "MUL-7"),
    ]);

    expect(container.querySelectorAll("a.issue-reference-row")).toHaveLength(1);
  });

  it("collapses a UUID mention and a bare identifier of the same issue into one row", () => {
    resolutions = {
      [`mention:${UUID}`]: issue(UUID, "MUL-7", "Fix the thing"),
      [`identifier:MUL-7`]: issue(UUID, "MUL-7", "Fix the thing"),
    };
    const { container } = renderFooter([
      bare("MUL-7"),
      mention(UUID, "MUL-7"),
    ]);

    expect(container.querySelectorAll("a.issue-reference-row")).toHaveLength(1);
  });

  it("renders nothing when there are no references", () => {
    const { container } = renderFooter([]);
    expect(container.querySelector("[data-issue-reference-footer]")).toBeNull();
  });

  it("degrades an unresolved identifier to plain identifier text", () => {
    resolutions = { "identifier:MUL-404": null };
    const { container } = renderFooter([bare("MUL-404")]);

    expect(container.querySelector("a.issue-reference-row")).toBeNull();
    const row = container.querySelector(".issue-reference-degraded")!;
    expect(row.textContent).toBe("MUL-404");
  });

  it("degrades an unresolved UUID mention to its authored label", () => {
    const { container } = renderFooter([mention(UUID, "MUL-7")]);

    const row = container.querySelector(".issue-reference-degraded")!;
    expect(row.textContent).toBe("MUL-7");
  });

  it("keeps first-appearance order across resolved and degraded rows", () => {
    resolutions = {
      [`mention:${UUID2}`]: issue(UUID2, "MUL-9", "Later issue"),
      [`identifier:MUL-2`]: null,
    };
    const { container } = renderFooter([
      bare("MUL-2"),
      mention(UUID2, "MUL-9"),
    ]);

    const rows = Array.from(
      container.querySelectorAll(".issue-reference-row"),
    );
    expect(rows.map((r) => r.textContent)).toEqual(["MUL-2", "MUL-9Later issue"]);
  });
});
