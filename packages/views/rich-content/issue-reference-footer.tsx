"use client";

/**
 * Tail aggregation of issue references (RUYI-635 + 增量 + RUYI-724).
 *
 * Issue mentions used to render inline as chips, which buried the prose under
 * chrome whenever a comment referenced an issue more than once. RichContent now
 * strips them to plain text in the body and hands the collected references to
 * this footer. The block renders per viewport (RUYI-724):
 *
 *   - Desktop (>=768px): the preview rows ARE the navigation — each resolved
 *     row is a direct link (one click, no popover round-trip; the desktop has
 *     room to show rows at full width, per Owner feedback). The popover exists
 *     only when rows fold past the preview budget, and the collapsed
 *     「N more references」 line is its only trigger; it still lists every
 *     reference so the block stays the one place to scan them all.
 *   - Mobile (<768px): the RUYI-635 增量 anti-mistouch two-level surface is
 *     kept — one `<button>` for the WHOLE block (header 「引用 · N」 + at most
 *     2 preview rows + a fold-count line), inline rows are spans, and pressing
 *     the block opens the popover. Rows group 「任务 / Pull Request」 only when
 *     both kinds mix; issue rows navigate in-app (same tab); PR rows open the
 *     canonical URL in a new tab.
 *
 * Rows are a three-kind union: resolved issue, resolved PR (`owner/repo#N`,
 * the 增量's text-reference aggregation), and degraded — an unresolved
 * reference keeps its slot as non-clickable plain text, the same "miss reads
 * as what the author typed" contract the inline chips had. The state → icon/
 * color semantics come from `derivePullRequestStateVisual` in core (one
 * derivation, renderers own glyphs/palettes).
 *
 * The fold cut and counts are shared with mobile via
 * `partitionReferencePreview` (core) so both ends fold at the same row with
 * the same "N more (incl. M PRs)" arithmetic.
 *
 * Nothing renders when there are no references: content without mentions must
 * not grow a trailing block.
 */
import { useMemo, useState } from "react";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import {
  ChevronDown,
  GitMerge,
  GitPullRequest,
  GitPullRequestArrow,
  GitPullRequestClosed,
  GitPullRequestDraft,
  type LucideIcon,
} from "lucide-react";
import {
  derivePullRequestStateVisual,
  type PullRequestVisualState,
} from "@multica/core/github";
import {
  dedupeIssueReferences,
  extractIssueReferences,
  partitionReferencePreview,
} from "@multica/core/markdown";
import { issueStatusCategory } from "@multica/core/issues";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import type { IssueReference } from "@multica/core/markdown";
import type { GitHubPullRequest, Issue } from "@multica/core/types";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { AppLink } from "../navigation";
import { useT } from "../i18n";
import { StatusIcon } from "../issues/components/status-icon";
import {
  isPullRequestResolution,
  useIssueReferenceResolutions,
} from "../issues/hooks/use-issue-reference-resolutions";

type TailRow =
  | { kind: "issue"; key: string; issue: Issue }
  | { kind: "pr"; key: string; pr: GitHubPullRequest }
  | { kind: "degraded"; key: string; fallback: string };

// Web glyph + color per visual state. The core spec owns the SEMANTICS (which
// state, which color token, dimmed?); these classes are this renderer's
// palette. `open` rides the --success token (was hardcoded emerald-600);
// merged keeps the unified violet #7c3aed; closed keeps rose; draft/unknown
// are muted, draft additionally dimmed.
const PR_STATE_ICON: Record<PullRequestVisualState, {
  Icon: LucideIcon;
  className: string;
}> = {
  open: { Icon: GitPullRequestArrow, className: "text-success" },
  draft: { Icon: GitPullRequestDraft, className: "text-muted-foreground" },
  merged: { Icon: GitMerge, className: "text-violet-600 dark:text-violet-400" },
  closed: {
    Icon: GitPullRequestClosed,
    className: "text-rose-600 dark:text-rose-400",
  },
  unknown: { Icon: GitPullRequest, className: "text-muted-foreground" },
};

function prIdentifier(pr: GitHubPullRequest): string {
  return `${pr.repo_owner}/${pr.repo_name}#${pr.number}`;
}

// Popover content portals out of `.rich-text-editor`, so the scoped CSS file
// can't reach it — these rows carry their own complete Tailwind class set.
// The `issue-reference-popover-row` hook name stays on for tests and QA.
const POPOVER_ROW_CLASS =
  "issue-reference-popover-row flex min-h-[44px] w-full items-center gap-2 rounded-lg px-2 py-1 text-left text-label no-underline outline-none hover:bg-accent focus-visible:bg-accent";
const POPOVER_ROW_TITLE_CLASS =
  "min-w-0 line-clamp-2 text-body text-foreground";
const POPOVER_ROW_IDENTIFIER_CLASS =
  "shrink-0 text-label font-medium text-muted-foreground";

// Icon/identifier/title glyph classes per surface. Inline (block preview +
// desktop direct rows) renders inside `.rich-text-editor` scope and uses the
// scoped CSS classes; popover rows are portaled outside that scope and carry
// their own complete Tailwind set.
const INLINE_ROW_CONTENT_CLASSES = {
  icon: "h-3.5 w-3.5 shrink-0",
  identifier: "issue-reference-identifier",
  title: "issue-reference-title",
} as const;
const POPOVER_ROW_CONTENT_CLASSES = {
  icon: "h-4 w-4 shrink-0",
  identifier: POPOVER_ROW_IDENTIFIER_CLASS,
  title: POPOVER_ROW_TITLE_CLASS,
} as const;

// Structural (not `typeof`): the two variants share the shape, not the
// literal class strings.
type RowContentClasses = {
  icon: string;
  identifier: string;
  title: string;
};

function IssueReferenceFooter({ references }: { references: IssueReference[] }) {
  const { t } = useT("issues");
  const p = useWorkspacePaths();
  const wsId = useWorkspaceId();
  const { colorOf: statusColorOf } = useIssueStatuses(wsId);
  const resolutions = useIssueReferenceResolutions(references);
  const [open, setOpen] = useState(false);
  // RUYI-724: desktop-width viewports navigate from the visible rows directly;
  // mobile keeps the anti-mistouch two-level surface.
  const isMobile = useIsMobile();

  const rows = useMemo<TailRow[]>(() => {
    const rows: TailRow[] = [];
    const seenIssueIds = new Set<string>();
    const seenPrIds = new Set<string>();
    for (const token of references) {
      const key = `${token.form}:${token.ref}`;
      const resolved = resolutions.get(key) ?? null;
      if (isPullRequestResolution(resolved)) {
        if (seenPrIds.has(resolved.id)) continue;
        seenPrIds.add(resolved.id);
        rows.push({ kind: "pr", key, pr: resolved });
      } else if (resolved) {
        // Cross-form collapse: the same issue reached via a UUID mention and a
        // bare identifier is one row, at its first mention's position.
        if (seenIssueIds.has(resolved.id)) continue;
        seenIssueIds.add(resolved.id);
        rows.push({ kind: "issue", key, issue: resolved });
      } else {
        rows.push({ kind: "degraded", key, fallback: token.label || token.ref });
      }
    }
    return rows;
  }, [references, resolutions]);

  const { previews, hidden, hiddenPrCount } = useMemo(
    () => partitionReferencePreview(rows, (row) => row.kind),
    [rows],
  );

  if (references.length === 0) return null;

  const header = t(($) => $.detail.reference_tail_header, {
    count: rows.length,
  });
  const collapsedLine =
    hidden.length > 0
      ? hiddenPrCount > 0
        ? t(($) => $.detail.reference_tail_more_with_pr, {
            count: hidden.length,
            prCount: hiddenPrCount,
          })
        : t(($) => $.detail.reference_tail_more, { count: hidden.length })
      : null;

  // Grouping only appears when both kinds mix (Board 3: 单一类型不显示组头);
  // degraded rows never join a group — they render as plain-text slots.
  const hasIssues = rows.some((row) => row.kind === "issue");
  const hasPrs = rows.some((row) => row.kind === "pr");
  const grouped = hasIssues && hasPrs;
  const issueRows = rows.filter(
    (row): row is Extract<TailRow, { kind: "issue" }> => row.kind === "issue",
  );
  const prRows = rows.filter(
    (row): row is Extract<TailRow, { kind: "pr" }> => row.kind === "pr",
  );
  const degradedRows = rows.filter(
    (row): row is Extract<TailRow, { kind: "degraded" }> =>
      row.kind === "degraded",
  );

  const close = () => setOpen(false);

  const renderIssueContents = (issue: Issue, classes: RowContentClasses) => (
    <>
      <StatusIcon
        status={issue.status}
        category={issueStatusCategory(issue) ?? undefined}
        color={statusColorOf(issue.status)}
        className={classes.icon}
      />
      <span className={classes.identifier} translate="no">
        {issue.identifier}
      </span>
      <span className={classes.title}>{issue.title}</span>
    </>
  );

  const renderPrContents = (pr: GitHubPullRequest, classes: RowContentClasses) => {
    const visual = derivePullRequestStateVisual(pr.state);
    const { Icon, className } = PR_STATE_ICON[visual.state];
    return (
      <>
        <Icon
          aria-hidden
          className={`${classes.icon} ${className} ${
            visual.dimmed ? "opacity-80" : ""
          }`}
        />
        <span className={classes.identifier} translate="no">
          {prIdentifier(pr)}
        </span>
        <span className={classes.title}>{pr.title}</span>
      </>
    );
  };

  const renderDegradedContents = (row: Extract<TailRow, { kind: "degraded" }>) =>
    row.fallback;

  const renderInlineRow = (row: TailRow) => {
    if (row.kind === "issue") {
      return (
        <span key={row.key} className="issue-reference-row">
          {renderIssueContents(row.issue, INLINE_ROW_CONTENT_CLASSES)}
        </span>
      );
    }
    if (row.kind === "pr") {
      return (
        <span key={row.key} className="issue-reference-row">
          {renderPrContents(row.pr, INLINE_ROW_CONTENT_CLASSES)}
        </span>
      );
    }
    return (
      <span
        key={row.key}
        className="issue-reference-row issue-reference-degraded"
      >
        {renderDegradedContents(row)}
      </span>
    );
  };

  // Desktop direct rows (RUYI-724): a visible reference navigates in one
  // click, with the same per-kind semantics as the popover rows (issue →
  // in-app route, PR → canonical URL in a new tab). Degraded rows keep the
  // non-clickable slot.
  const renderDirectRow = (row: TailRow) => {
    if (row.kind === "issue") {
      return (
        <AppLink
          key={row.key}
          href={p.issueDetail(row.issue.id)}
          newTabTitle={row.issue.identifier}
          className="issue-reference-row"
          data-reference-kind="issue"
        >
          {renderIssueContents(row.issue, INLINE_ROW_CONTENT_CLASSES)}
        </AppLink>
      );
    }
    if (row.kind === "pr") {
      return (
        <a
          key={row.key}
          href={row.pr.html_url}
          target="_blank"
          rel="noreferrer noopener"
          className="issue-reference-row"
          data-reference-kind="pr"
        >
          {renderPrContents(row.pr, INLINE_ROW_CONTENT_CLASSES)}
        </a>
      );
    }
    return renderInlineRow(row);
  };

  const renderPopoverRow = (row: TailRow) => {
    if (row.kind === "issue") {
      return (
        <AppLink
          key={row.key}
          href={p.issueDetail(row.issue.id)}
          newTabTitle={row.issue.identifier}
          className={POPOVER_ROW_CLASS}
          data-reference-kind="issue"
          onClick={close}
        >
          {renderIssueContents(row.issue, POPOVER_ROW_CONTENT_CLASSES)}
        </AppLink>
      );
    }
    if (row.kind === "pr") {
      return (
        <a
          key={row.key}
          href={row.pr.html_url}
          target="_blank"
          rel="noreferrer noopener"
          className={POPOVER_ROW_CLASS}
          data-reference-kind="pr"
          onClick={close}
        >
          {renderPrContents(row.pr, POPOVER_ROW_CONTENT_CLASSES)}
        </a>
      );
    }
    return (
      <span
        key={row.key}
        className={`${POPOVER_ROW_CLASS} issue-reference-degraded text-muted-foreground`}
      >
        {renderDegradedContents(row)}
      </span>
    );
  };

  const popoverRows = grouped ? (
    <>
      <div className="issue-reference-popover-group px-2 pt-1.5 pb-1 text-micro font-medium tracking-[0.06em] text-muted-foreground">
        {t(($) => $.detail.reference_group_tasks)}
      </div>
      {issueRows.map(renderPopoverRow)}
      <div className="issue-reference-popover-group px-2 pt-1.5 pb-1 text-micro font-medium tracking-[0.06em] text-muted-foreground">
        {t(($) => $.detail.reference_group_prs)}
      </div>
      {prRows.map(renderPopoverRow)}
      {degradedRows.map(renderPopoverRow)}
    </>
  ) : (
    rows.map(renderPopoverRow)
  );

  if (!isMobile) {
    // Desktop (RUYI-724): the block is not a button — visible rows navigate
    // in place. The popover mounts only for folded rows, and the collapsed
    // 「N more references」 line is its sole trigger; it still lists every
    // reference so the block remains the one place to scan them all.
    return (
      <div data-issue-reference-footer="" className="issue-reference-footer">
        <span className="issue-reference-header">{header}</span>
        {previews.map(renderDirectRow)}
        {hidden.length > 0 ? (
          <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger className="issue-reference-collapsed">
              {collapsedLine}
              <ChevronDown aria-hidden className="issue-reference-chevron" />
            </PopoverTrigger>
            <PopoverContent
              align="start"
              className="issue-reference-popover w-[420px] max-h-[360px] overflow-y-auto gap-1 p-1.5"
            >
              {popoverRows}
            </PopoverContent>
          </Popover>
        ) : null}
      </div>
    );
  }

  // Mobile: the RUYI-635 增量 anti-mistouch surface — one button for the
  // whole block, inline rows are spans, the popover carries the full list.
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        data-issue-reference-footer=""
        aria-label={t(($) => $.detail.referenced_issues)}
        className="issue-reference-footer"
      >
        <span className="issue-reference-header">
          {header}
          <ChevronDown aria-hidden className="issue-reference-chevron" />
        </span>
        {previews.map(renderInlineRow)}
        {collapsedLine ? (
          <span className="issue-reference-collapsed">{collapsedLine}</span>
        ) : null}
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="issue-reference-popover w-[420px] max-h-[360px] overflow-y-auto gap-1 p-1.5"
      >
        {popoverRows}
      </PopoverContent>
    </Popover>
  );
}

/**
 * The full tail block, from raw content to rendered rows. Scans the RAW
 * markdown (not a preprocessed copy): the extractor shares its skip rules
 * (code, links, URLs) with the preprocessors, so scanning the source finds
 * exactly the references the body would have chipped, while fences the
 * highlighter already turned into HTML stay out of scope. Bare identifiers
 * are gated on the current workspace's `issue_prefix` (RUYI-705) — an
 * acceptance-criteria numbering like `AC-1` is not a reference; an unknown
 * prefix (workspace list still loading) falls through and the resolver side
 * stays the exact-match backstop. Raw-dedup keeps the first occurrence of
 * each token; cross-form collapse by resolved issue id happens in the footer.
 *
 * RichContent and the issue body's ContentEditor host (RUYI-635 rework: the
 * body renders through the editor, so its tail list is the host's job) both
 * mount this — one extraction and one dedup policy, no per-surface fork.
 * Nothing renders when there are no references.
 */
function IssueReferenceTail({ content }: { content: string }) {
  const workspace = useCurrentWorkspace();
  const workspacePrefix = workspace?.issue_prefix || null;
  const references = useMemo(
    () =>
      dedupeIssueReferences(
        extractIssueReferences(content, { workspacePrefix }),
      ),
    [content, workspacePrefix],
  );
  if (references.length === 0) return null;
  return <IssueReferenceFooter references={references} />;
}

export { IssueReferenceFooter, IssueReferenceTail };
