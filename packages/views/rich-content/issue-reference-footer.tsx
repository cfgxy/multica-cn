"use client";

/**
 * Tail aggregation of issue references (RUYI-635, flattened by RUYI-731).
 *
 * Issue mentions used to render inline as chips, which buried the prose under
 * chrome whenever a comment referenced an issue more than once. RichContent now
 * strips them to plain text in the body and hands the collected references to
 * this footer. RUYI-731 retires the 增量's two-level surface (2-row preview
 * budget + "N more" fold line + popover): the block is a flat list — header
 * 「引用 · N」 and EVERY reference as one row, in first-appearance order —
 * and rows are the navigation surface directly:
 *
 *   - issue rows navigate in-app (same tab), PR rows (`owner/repo#N`, the
 *     增量's text-reference aggregation) open the canonical URL in a new
 *     tab, degraded rows stay non-clickable plain text — the same "miss
 *     reads as what the author typed" contract the inline chips had.
 *
 * The state → icon/color semantics come from `derivePullRequestStateVisual`
 * in core (one derivation, renderers own glyphs/palettes). The envelope's
 * visual language is RUYI-730's rebuild (card container in rich-content.css,
 * SubIssueRow grid): inline icons ride the official 15px row grid and the
 * degraded slot keeps a placeholder glyph so its text starts on that grid.
 *
 * Self-filter (RUYI-731): under the issue-detail render context a reference
 * that resolves to the CURRENT issue builds no row — an issue describing
 * itself is not a reference. Surfaces without the context (chat, inbox)
 * keep every row.
 *
 * Nothing renders when there are no references — or when every reference
 * filtered away as self-references.
 */
import { useMemo } from "react";
import {
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
} from "@multica/core/markdown";
import { issueStatusCategory } from "@multica/core/issues";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import type { IssueReference } from "@multica/core/markdown";
import type { GitHubPullRequest, Issue } from "@multica/core/types";
import { AppLink } from "../navigation";
import { useT } from "../i18n";
import {
  useCurrentIssueRenderContext,
} from "../issues/current-issue-render-context";
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

function IssueReferenceFooter({ references }: { references: IssueReference[] }) {
  const { t } = useT("issues");
  const p = useWorkspacePaths();
  const wsId = useWorkspaceId();
  const { colorOf: statusColorOf } = useIssueStatuses(wsId);
  const resolutions = useIssueReferenceResolutions(references);
  const currentIssueId = useCurrentIssueRenderContext()?.id ?? null;

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
        // RUYI-731 self-filter: the issue-detail context owns the issue this
        // content belongs to; its own row is not a reference worth showing.
        if (resolved.id === currentIssueId) continue;
        seenIssueIds.add(resolved.id);
        rows.push({ kind: "issue", key, issue: resolved });
      } else {
        rows.push({ kind: "degraded", key, fallback: token.label || token.ref });
      }
    }
    return rows;
  }, [references, resolutions, currentIssueId]);

  if (references.length === 0 || rows.length === 0) return null;

  const header = t(($) => $.detail.reference_tail_header, {
    count: rows.length,
  });

  const renderRow = (row: TailRow) => {
    if (row.kind === "issue") {
      return (
        <AppLink
          key={row.key}
          href={p.issueDetail(row.issue.id)}
          newTabTitle={row.issue.identifier}
          className="issue-reference-row no-underline"
          data-reference-kind="issue"
        >
          <StatusIcon
            status={row.issue.status}
            category={issueStatusCategory(row.issue) ?? undefined}
            color={statusColorOf(row.issue.status)}
            // Inline icons sit on the SubIssueRow grid: 15px like the
            // official sub-issue rows (RUYI-730 visual rebuild).
            className="h-[15px] w-[15px] shrink-0"
          />
          <span className="issue-reference-identifier" translate="no">
            {row.issue.identifier}
          </span>
          <span className="issue-reference-title">{row.issue.title}</span>
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
          className="issue-reference-row no-underline"
          data-reference-kind="pr"
        >
          <Icon
            aria-hidden
            className={`h-[15px] w-[15px] shrink-0 ${className} ${
              visual.dimmed ? "opacity-80" : ""
            }`}
          />
          <span className="issue-reference-identifier" translate="no">
            {prIdentifier(row.pr)}
          </span>
          <span className="issue-reference-title">{row.pr.title}</span>
        </a>
      );
    }
    // Degraded slot keeps a placeholder glyph on the icon column so its text
    // starts on the same grid as resolved rows (RUYI-730). Dimmed via the
    // sanctioned faint token — web's text-contrast law bars resting opacity
    // on guarded text tones (730's opacity-60 form fails it).
    return (
      <span
        key={row.key}
        className="issue-reference-row issue-reference-degraded"
      >
        <CircleDashed
          aria-hidden
          className="h-[15px] w-[15px] shrink-0 text-faint-foreground"
        />
        {row.fallback}
      </span>
    );
  };

  return (
    <div data-issue-reference-footer className="issue-reference-footer">
      <span className="issue-reference-header">{header}</span>
      {rows.map(renderRow)}
    </div>
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
