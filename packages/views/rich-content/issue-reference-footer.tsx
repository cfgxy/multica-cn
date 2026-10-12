"use client";

/**
 * Tail aggregation of issue references (RUYI-635 + 增量).
 *
 * Issue mentions used to render inline as chips, which buried the prose under
 * chrome whenever a comment referenced an issue more than once. RichContent now
 * strips them to plain text in the body and hands the collected references to
 * this footer. The 增量 rework (Owner points ①③④) makes the block an
 * anti-mistouch two-level surface:
 *
 *   - Level 1 — one `<button>` for the WHOLE block (header 「引用 · N」 + at
 *     most 2 preview rows + a fold-count line). Inline rows are spans: the
 *     only press target is the block itself, and pressing it opens the
 *     popover. Nothing inline navigates any more.
 *   - Level 2 — the popover lists every reference, grouped 「任务 /
 *     Pull Request」 only when both kinds mix (single-kind lists keep
 *     first-appearance order, no headers). Issue rows navigate in-app
 *     (same tab); PR rows open the canonical URL in a new tab. Esc /
 *     outside click close and focus returns to the block (Base UI).
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
import {
  ChevronDown,
  CircleDashed,
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
// Hover rides `bg-accent/50`, the same token as SubIssueRow rows (RUYI-730).
const POPOVER_ROW_CLASS =
  "issue-reference-popover-row flex min-h-[44px] w-full items-center gap-2 rounded-lg px-2 py-1 text-left text-label no-underline outline-none hover:bg-accent/50 focus-visible:bg-accent";
const POPOVER_ROW_TITLE_CLASS =
  "min-w-0 line-clamp-2 text-body text-foreground";
const POPOVER_ROW_IDENTIFIER_CLASS =
  "shrink-0 text-label font-medium text-muted-foreground";

function IssueReferenceFooter({ references }: { references: IssueReference[] }) {
  const { t } = useT("issues");
  const p = useWorkspacePaths();
  const wsId = useWorkspaceId();
  const { colorOf: statusColorOf } = useIssueStatuses(wsId);
  const resolutions = useIssueReferenceResolutions(references);
  const [open, setOpen] = useState(false);

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

  const renderInlineRow = (row: TailRow) => {
    if (row.kind === "issue") {
      return (
        <span key={row.key} className="issue-reference-row">
          <StatusIcon
            status={row.issue.status}
            category={issueStatusCategory(row.issue) ?? undefined}
            color={statusColorOf(row.issue.status)}
            className="h-[15px] w-[15px] shrink-0"
          />
          <span className="issue-reference-identifier" translate="no">
            {row.issue.identifier}
          </span>
          <span className="issue-reference-title">{row.issue.title}</span>
        </span>
      );
    }
    if (row.kind === "pr") {
      const { Icon, className } = PR_STATE_ICON[
        derivePullRequestStateVisual(row.pr.state).state
      ];
      return (
        <span key={row.key} className="issue-reference-row">
          <Icon
            aria-hidden
            className={`h-[15px] w-[15px] shrink-0 ${className} ${
              derivePullRequestStateVisual(row.pr.state).dimmed
                ? "opacity-80"
                : ""
            }`}
          />
          <span className="issue-reference-identifier" translate="no">
            {prIdentifier(row.pr)}
          </span>
          <span className="issue-reference-title">{row.pr.title}</span>
        </span>
      );
    }
    // Degraded slot keeps a placeholder glyph on the icon column so its text
    // starts on the same grid as resolved rows (RUYI-730).
    return (
      <span
        key={row.key}
        className="issue-reference-row issue-reference-degraded"
      >
        <CircleDashed
          aria-hidden
          className="h-[15px] w-[15px] shrink-0 text-muted-foreground opacity-60"
        />
        {row.fallback}
      </span>
    );
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
          <StatusIcon
            status={row.issue.status}
            category={issueStatusCategory(row.issue) ?? undefined}
            color={statusColorOf(row.issue.status)}
            className="h-4 w-4 shrink-0"
          />
          <span className={POPOVER_ROW_IDENTIFIER_CLASS} translate="no">
            {row.issue.identifier}
          </span>
          <span className={POPOVER_ROW_TITLE_CLASS}>{row.issue.title}</span>
        </AppLink>
      );
    }
    if (row.kind === "pr") {
      const visual = derivePullRequestStateVisual(row.pr.state);
      const { Icon, className } = PR_STATE_ICON[visual.state];
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
          <Icon
            aria-hidden
            className={`h-4 w-4 shrink-0 ${className} ${
              visual.dimmed ? "opacity-80" : ""
            }`}
          />
          <span className={POPOVER_ROW_IDENTIFIER_CLASS} translate="no">
            {prIdentifier(row.pr)}
          </span>
          <span className={POPOVER_ROW_TITLE_CLASS}>{row.pr.title}</span>
        </a>
      );
    }
    return (
      <span
        key={row.key}
        className={`${POPOVER_ROW_CLASS} issue-reference-degraded text-muted-foreground`}
      >
        {row.fallback}
      </span>
    );
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        data-issue-reference-footer=""
        aria-label={t(($) => $.detail.referenced_issues)}
        className="issue-reference-footer"
      >
        <span className="issue-reference-header">
          <ChevronDown aria-hidden className="issue-reference-chevron" />
          {header}
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
        {grouped ? (
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
        )}
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
