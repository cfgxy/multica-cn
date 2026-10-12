/**
 * Tail aggregation of issue references (RUYI-635 + 增量 + RUYI-724).
 *
 * RichContent hands every collected reference to IssueReferenceFooter. The
 * fixtures below pin the per-viewport contract:
 *   - desktop (>=768px, RUYI-724): preview rows are DIRECT links — clicking a
 *     visible row navigates in one click and no popover mounts. The popover
 *     exists only when rows fold past the preview budget, reachable from the
 *     collapsed-line trigger;
 *   - mobile (<768px): the anti-mistouch two-level surface is preserved — the
 *     whole block is ONE button, inline rows are spans and never navigate, and
 *     the full list lives in the popover;
 *   - fold partition: at most 2 preview rows, the rest collapse into an
 *     "N more references (incl. M PRs)" line;
 *   - PR text references (`owner/repo#N`) render as first-class rows;
 *   - unresolved references keep their slot as non-clickable plain text;
 *   - popover rows group Tasks / Pull Request only when both kinds mix, keep
 *     first-appearance order, and navigate per kind (issue → in-app route,
 *     PR → canonical URL in a new tab).
 *
 * Resolution is stubbed at the hook seam; row rendering, dedup, grouping, and
 * fold policy are exercised for real. The popover shell is stubbed to a
 * controlled div (Base UI owns its own open/close mechanics) — grouping and
 * row semantics are what's under test. `useIsMobile` reads
 * `window.innerWidth`, so the viewport is set per test.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render } from "@testing-library/react";
import type { ReactNode } from "react";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";
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

// Controlled stub of the Base UI popover: the trigger toggles `open` through
// the component's own onOpenChange, content mounts only while open.
const popoverState: {
  open: boolean;
  onOpenChange?: (open: boolean) => void;
} = { open: false };

vi.mock("@multica/ui/components/ui/popover", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    Popover: ({
      children,
      open,
      onOpenChange,
    }: {
      children: ReactNode;
      open: boolean;
      onOpenChange?: (open: boolean) => void;
    }) => {
      popoverState.open = open;
      popoverState.onOpenChange = onOpenChange;
      return <div data-testid="popover">{children}</div>;
    },
    PopoverTrigger: ({
      children,
      ...props
    }: { children?: ReactNode } & Record<string, unknown>) =>
      React.createElement(
        "button",
        {
          type: "button",
          ...props,
          onClick: () => popoverState.onOpenChange?.(!popoverState.open),
        },
        children,
      ),
    PopoverContent: ({
      children,
      ...props
    }: { children?: ReactNode } & Record<string, unknown>) =>
      popoverState.open ? (
        <div data-testid="popover-content" {...props}>
          {children}
        </div>
      ) : null,
  };
});

import { IssueReferenceFooter } from "./issue-reference-footer";

const UUID = "1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed";
const UUID2 = "8f14e45f-ceea-4670-9b1c-0d3e4f5a6b7c";
const UUID3 = "c4ca4238-a0b9-3382-8dcc-509a6f75849b";

// `useIsMobile` reads `window.innerWidth` against the 768px breakpoint.
function setViewport(width: number) {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: width,
  });
}

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
const PR341 = "pull_request:cfgxy/multica-cn#341";

function renderFooter(references: IssueReference[], width = 1024) {
  setViewport(width);
  const adapter: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p) => `https://app.multica.ai${p}`,
  };
  const view = render(
    <NavigationProvider value={adapter}>
      <IssueReferenceFooter references={references} />
    </NavigationProvider>,
  );
  return { ...view, adapter };
}

function openMobilePopover(container: HTMLElement) {
  fireEvent.click(
    container.querySelector("button[data-issue-reference-footer]")!,
  );
  return container.querySelector("[data-testid='popover-content']")!;
}

function openDesktopPopover(container: HTMLElement) {
  fireEvent.click(
    container.querySelector("button.issue-reference-collapsed")!,
  );
  return container.querySelector("[data-testid='popover-content']")!;
}

describe("IssueReferenceFooter", () => {
  beforeEach(() => {
    resolutions = {};
    popoverState.open = false;
    popoverState.onOpenChange = undefined;
  });

  afterEach(() => {
    setViewport(1024);
  });

  it("desktop: preview rows are direct links and no popover mounts (RUYI-724)", () => {
    resolutions = { [`mention:${UUID}`]: issue(UUID, "MUL-7", "Fix the thing") };
    const { container, adapter } = renderFooter([mention(UUID, "MUL-7")]);

    // The whole-block popover trigger is gone at desktop width.
    expect(
      container.querySelector("button[data-issue-reference-footer]"),
    ).toBeNull();
    const block = container.querySelector("[data-issue-reference-footer]")!;
    expect(block.tagName).toBe("DIV");
    expect(block.textContent).toContain("References · 1");
    // Nothing folds, nothing pops: no expand affordance, no popover shell.
    expect(container.querySelector(".issue-reference-chevron")).toBeNull();
    expect(container.querySelector("[data-testid='popover']")).toBeNull();

    // The visible row navigates in one click.
    const row = container.querySelector("a.issue-reference-row")!;
    expect(row.getAttribute("href")).toBe(`/acme/issues/${UUID}`);
    expect(row.getAttribute("data-reference-kind")).toBe("issue");
    fireEvent.click(row);
    expect(adapter.push).toHaveBeenCalledWith(`/acme/issues/${UUID}`);
  });

  it("desktop: PR rows open the canonical URL in a new tab", () => {
    resolutions = { [PR338]: pr(338, "merged") };
    const { container } = renderFooter([prRef("cfgxy/multica-cn#338")]);

    const row = container.querySelector("a[data-reference-kind='pr']")!;
    expect(row.getAttribute("href")).toBe(
      "https://github.com/cfgxy/multica-cn/pull/338",
    );
    expect(row.getAttribute("target")).toBe("_blank");
    expect(row.getAttribute("rel")).toContain("noreferrer");
    expect(row.textContent).toContain("cfgxy/multica-cn#338");
  });

  it("desktop: folded rows stay reachable through the collapsed-line trigger", () => {
    resolutions = {
      "identifier:MUL-1": issue(UUID, "MUL-1", "First"),
      "identifier:MUL-2": issue(UUID2, "MUL-2", "Second"),
      "identifier:MUL-3": issue(UUID3, "MUL-3", "Third"),
      [PR338]: pr(338, "merged"),
      [PR341]: pr(341, "open"),
    };
    const { container, adapter } = renderFooter([
      bare("MUL-1"),
      bare("MUL-2"),
      bare("MUL-3"),
      prRef("cfgxy/multica-cn#338"),
      prRef("cfgxy/multica-cn#341"),
    ]);

    expect(container.textContent).toContain("References · 5");
    // Both preview rows navigate directly.
    expect(container.querySelectorAll("a.issue-reference-row")).toHaveLength(2);
    const collapsed = container.querySelector(".issue-reference-collapsed")!;
    expect(collapsed.textContent).toBe("3 more references (incl. 2 PRs)");

    // The collapsed line is the only popover trigger, and the popover still
    // lists every reference (grouped, mixed kinds).
    const trigger = container.querySelector("button.issue-reference-collapsed")!;
    expect(trigger).not.toBeNull();
    expect(trigger.querySelector(".issue-reference-chevron")).not.toBeNull();
    fireEvent.click(trigger);
    const panel = container.querySelector("[data-testid='popover-content']")!;
    expect(panel.querySelectorAll(".issue-reference-popover-group")).toHaveLength(
      2,
    );
    expect(panel.querySelectorAll("a[data-reference-kind='issue']")).toHaveLength(
      3,
    );
    expect(panel.querySelectorAll("a[data-reference-kind='pr']")).toHaveLength(2);

    // Popover rows keep navigating in-app.
    fireEvent.click(panel.querySelector("a[data-reference-kind='issue']")!);
    expect(adapter.push).toHaveBeenCalledWith(`/acme/issues/${UUID}`);
  });

  it("desktop: unresolved references stay non-clickable plain text", () => {
    resolutions = { "identifier:MUL-404": null };
    const { container } = renderFooter([bare("MUL-404")]);

    expect(container.querySelector("a.issue-reference-row")).toBeNull();
    const row = container.querySelector(".issue-reference-degraded")!;
    expect(row.textContent).toBe("MUL-404");
    // Nothing folded: no trigger, no popover.
    expect(container.querySelector("button.issue-reference-collapsed")).toBeNull();
    expect(container.querySelector("[data-testid='popover']")).toBeNull();
  });

  it("desktop: popover rows render unresolved references as non-clickable plain-text slots", () => {
    resolutions = {
      "identifier:MUL-1": issue(UUID, "MUL-1", "First"),
      "identifier:MUL-404": null,
      [PR338]: pr(338, "open"),
    };
    const { container } = renderFooter([
      bare("MUL-1"),
      bare("MUL-404"),
      prRef("cfgxy/multica-cn#338"),
    ]);

    const panel = openDesktopPopover(container);

    const degraded = panel.querySelector(
      "span.issue-reference-popover-row.issue-reference-degraded",
    )!;
    expect(degraded.textContent).toBe("MUL-404");
    // The degraded slot must not leak into either clickable group.
    expect(
      panel.querySelectorAll("a[data-reference-kind='issue']"),
    ).toHaveLength(1);
    expect(panel.querySelectorAll("a[data-reference-kind='pr']")).toHaveLength(
      1,
    );
  });

  it("desktop: popover keeps single-kind lists ungrouped in first-appearance order", () => {
    resolutions = {
      "identifier:MUL-1": issue(UUID, "MUL-1", "First"),
      "identifier:MUL-2": issue(UUID2, "MUL-2", "Second"),
      "identifier:MUL-3": issue(UUID3, "MUL-3", "Third"),
    };
    const { container } = renderFooter([
      bare("MUL-1"),
      bare("MUL-2"),
      bare("MUL-3"),
    ]);

    const panel = openDesktopPopover(container);

    expect(
      panel.querySelectorAll(".issue-reference-popover-group"),
    ).toHaveLength(0);
    const rows = Array.from(
      panel.querySelectorAll("a[data-reference-kind='issue']"),
    );
    expect(rows.map((r) => r.textContent)).toEqual([
      "MUL-1First",
      "MUL-2Second",
      "MUL-3Third",
    ]);
  });

  it("mobile: the whole block is one trigger and inline rows never navigate", () => {
    resolutions = { [`mention:${UUID}`]: issue(UUID, "MUL-7", "Fix the thing") };
    const { container } = renderFooter([mention(UUID, "MUL-7")], 390);

    const trigger = container.querySelector(
      "button[data-issue-reference-footer]",
    )!;
    expect(trigger).not.toBeNull();
    expect(trigger.textContent).toContain("References · 1");
    // Inline rows are spans — the anti-mistouch contract forbids any inline
    // navigation; the whole block is the only press target.
    expect(container.querySelectorAll("a.issue-reference-row")).toHaveLength(0);
    const row = container.querySelector(".issue-reference-row")!;
    expect(row.textContent).toContain("MUL-7");
    expect(row.textContent).toContain("Fix the thing");
    expect(trigger.querySelector(".issue-reference-chevron")).not.toBeNull();
  });

  it("mobile: block click opens the popover whose rows navigate", () => {
    resolutions = {
      "identifier:MUL-1": issue(UUID, "MUL-1", "First"),
      "identifier:MUL-2": issue(UUID2, "MUL-2", "Second"),
      "identifier:MUL-3": issue(UUID3, "MUL-3", "Third"),
      [PR338]: pr(338, "merged"),
      [PR341]: pr(341, "open"),
    };
    const { container, adapter } = renderFooter(
      [
        bare("MUL-1"),
        bare("MUL-2"),
        bare("MUL-3"),
        prRef("cfgxy/multica-cn#338"),
        prRef("cfgxy/multica-cn#341"),
      ],
      390,
    );

    const panel = openMobilePopover(container);

    expect(panel.querySelectorAll(".issue-reference-popover-group")).toHaveLength(
      2,
    );
    const issueRows = Array.from(
      panel.querySelectorAll("a[data-reference-kind='issue']"),
    );
    expect(issueRows.map((r) => r.getAttribute("href"))).toEqual([
      `/acme/issues/${UUID}`,
      `/acme/issues/${UUID2}`,
      `/acme/issues/${UUID3}`,
    ]);
    fireEvent.click(issueRows[0]!);
    expect(adapter.push).toHaveBeenCalledWith(`/acme/issues/${UUID}`);
  });

  it("mobile: PR popover rows open the canonical URL in a new tab", () => {
    resolutions = { [PR338]: pr(338, "open") };
    const { container } = renderFooter([prRef("cfgxy/multica-cn#338")], 390);

    const panel = openMobilePopover(container);
    const prRows = Array.from(
      panel.querySelectorAll("a[data-reference-kind='pr']"),
    );
    expect(prRows).toHaveLength(1);
    expect(prRows[0]!.getAttribute("target")).toBe("_blank");
    expect(prRows[0]!.getAttribute("rel")).toContain("noreferrer");
  });

  it("collapses repeated mentions of the same issue into one row", () => {
    resolutions = { [`mention:${UUID}`]: issue(UUID, "MUL-7", "Fix the thing") };
    const { container } = renderFooter([
      mention(UUID, "MUL-7"),
      mention(UUID, "MUL-7"),
    ]);

    expect(container.querySelectorAll(".issue-reference-row")).toHaveLength(1);
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

    expect(container.querySelectorAll(".issue-reference-row")).toHaveLength(1);
  });

  it("renders nothing when there are no references", () => {
    const { container } = renderFooter([]);
    expect(container.querySelector("[data-issue-reference-footer]")).toBeNull();
  });

  it("shows every row inline when the list fits the preview budget", () => {
    resolutions = {
      "identifier:MUL-1": issue(UUID, "MUL-1", "First"),
      "identifier:MUL-2": issue(UUID2, "MUL-2", "Second"),
    };
    const { container } = renderFooter([bare("MUL-1"), bare("MUL-2")]);

    expect(container.querySelectorAll(".issue-reference-row")).toHaveLength(2);
    expect(container.querySelector(".issue-reference-collapsed")).toBeNull();
  });

  it("degrades an unresolved UUID mention to its authored label", () => {
    const { container } = renderFooter([mention(UUID, "MUL-7")]);

    const row = container.querySelector(".issue-reference-degraded")!;
    expect(row.textContent).toBe("MUL-7");
  });

  it("degrades an unresolved PR reference to the raw token", () => {
    const { container } = renderFooter([prRef("cfgxy/multica-cn#99999")]);

    expect(container.querySelector("a.issue-reference-row")).toBeNull();
    const row = container.querySelector(".issue-reference-degraded")!;
    expect(row.textContent).toBe("cfgxy/multica-cn#99999");
  });

  it("keeps first-appearance order across resolved and degraded rows", () => {
    resolutions = {
      [`mention:${UUID2}`]: issue(UUID2, "MUL-9", "Later issue"),
      "identifier:MUL-2": null,
    };
    const { container } = renderFooter([
      bare("MUL-2"),
      mention(UUID2, "MUL-9"),
    ]);

    const rows = Array.from(
      container.querySelectorAll(".issue-reference-row"),
    );
    expect(rows.map((r) => r.textContent)).toEqual([
      "MUL-2",
      "MUL-9Later issue",
    ]);
  });
});
