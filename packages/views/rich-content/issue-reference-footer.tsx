"use client";

/**
 * Tail aggregation of issue references (RUYI-635).
 *
 * Issue mentions used to render inline as chips, which buried the prose under
 * chrome whenever a comment referenced an issue more than once. RichContent now
 * strips them to plain text in the body and hands the collected references to
 * this footer: one deduplicated, clickable row per issue —
 * `<status> <identifier> <title>` — rendered after the content on every
 * RichContent surface (comments, issue descriptions, chat timeline), with no
 * per-surface fork.
 *
 * Dedup is two-stage:
 *   - raw token dedup happened upstream (`dedupeIssueReferences` over the
 *     extractor output);
 *   - here, resolved issues collapse across FORMS: a UUID mention and the same
 *     issue's bare identifier both resolve to one issue id and render one row.
 * Unresolved references keep their slot as non-clickable plain text — a bare
 * identifier degrades to the identifier itself, a UUID mention to its authored
 * label — the same "miss reads as what the author typed" contract the inline
 * chips had.
 *
 * Nothing renders when there are no references: content without mentions must
 * not grow a trailing block.
 */
import { useMemo } from "react";
import { AppLink } from "../navigation";
import { useWorkspacePaths } from "@multica/core/paths";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueStatusCategory } from "@multica/core/issues";
import {
  dedupeIssueReferences,
  extractIssueReferences,
} from "@multica/core/markdown";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import type { IssueReference } from "@multica/core/markdown";
import type { Issue } from "@multica/core/types";
import { useT } from "../i18n";
import { StatusIcon } from "../issues/components/status-icon";
import { useIssueReferenceResolutions } from "../issues/hooks/use-issue-reference-resolutions";

interface ResolvedRow {
  key: string;
  issue: Issue;
  token: IssueReference;
}

interface DegradedRow {
  key: string;
  fallback: string;
}

function IssueReferenceFooter({ references }: { references: IssueReference[] }) {
  const { t } = useT("issues");
  const p = useWorkspacePaths();
  const wsId = useWorkspaceId();
  const { colorOf: statusColorOf } = useIssueStatuses(wsId);
  const resolutions = useIssueReferenceResolutions(references);

  const rows = useMemo(() => {
    const rows: (ResolvedRow | DegradedRow)[] = [];
    const seenIssueIds = new Set<string>();
    for (const token of references) {
      const issue = resolutions.get(`${token.form}:${token.ref}`) ?? null;
      if (issue) {
        // Cross-form collapse: the same issue reached via a UUID mention and a
        // bare identifier is one row, at its first mention's position.
        if (seenIssueIds.has(issue.id)) continue;
        seenIssueIds.add(issue.id);
        rows.push({ key: `${token.form}:${token.ref}`, issue, token });
      } else {
        rows.push({
          key: `${token.form}:${token.ref}`,
          fallback: token.label || token.ref,
        });
      }
    }
    return rows;
  }, [references, resolutions]);

  if (references.length === 0) return null;

  return (
    <div
      className="issue-reference-footer"
      data-issue-reference-footer=""
      aria-label={t(($) => $.detail.referenced_issues)}
    >
      {rows.map((row) =>
        "issue" in row ? (
          <AppLink
            key={row.key}
            href={p.issueDetail(row.issue.id)}
            newTabTitle={row.issue.identifier}
            className="issue-reference-row"
          >
            <StatusIcon
              status={row.issue.status}
              category={issueStatusCategory(row.issue) ?? undefined}
              color={statusColorOf(row.issue.status)}
              className="h-3.5 w-3.5 shrink-0"
            />
            <span className="issue-reference-identifier" translate="no">
              {row.issue.identifier}
            </span>
            <span className="issue-reference-title">{row.issue.title}</span>
          </AppLink>
        ) : (
          <span
            key={row.key}
            className="issue-reference-row issue-reference-degraded"
          >
            {row.fallback}
          </span>
        ),
      )}
    </div>
  );
}

/**
 * The full tail block, from raw content to rendered rows. Scans the RAW
 * markdown (not a preprocessed copy): the extractor shares its skip rules
 * (code, links, URLs) with the preprocessors, so scanning the source finds
 * exactly the references the body would have chipped, while fences the
 * highlighter already turned into HTML stay out of scope. Raw-dedup keeps the
 * first occurrence of each token; cross-form collapse by resolved issue id
 * happens in the footer.
 *
 * RichContent and the issue body's ContentEditor host (RUYI-635 rework: the
 * body renders through the editor, so its tail list is the host's job) both
 * mount this — one extraction and one dedup policy, no per-surface fork.
 * Nothing renders when there are no references.
 */
function IssueReferenceTail({ content }: { content: string }) {
  const references = useMemo(
    () => dedupeIssueReferences(extractIssueReferences(content)),
    [content],
  );
  if (references.length === 0) return null;
  return <IssueReferenceFooter references={references} />;
}

export { IssueReferenceFooter, IssueReferenceTail };
