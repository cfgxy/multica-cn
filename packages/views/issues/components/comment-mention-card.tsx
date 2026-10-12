"use client";

import { MessageSquareQuote } from "lucide-react";
import { toast } from "sonner";
import { useWorkspacePaths } from "@multica/core/paths";
import type { CommentAnchor } from "@multica/core/types";
import { useActorName } from "@multica/core/workspace/hooks";
import { AppLink } from "../../navigation";
import { useT, useTimeAgo } from "../../i18n";
import { commentPreview } from "./thread-minimap";
import { useCurrentIssueRenderContext } from "../current-issue-render-context";
import { useResolveCommentAnchor } from "../hooks";

/**
 * `mention://comment/<id>` rendered as a chip (RUYI-108; cross-issue jump
 * added by RUYI-643).
 *
 * Three states:
 *
 *   - resolved by the current render context → a button that jumps to that
 *     comment in this issue's timeline (expand its thread, centre it, flash
 *     it). It is a button and not a link on purpose: nothing navigates, the
 *     URL does not change, and a middle-click "open in new tab" would be a
 *     lie.
 *   - local miss, but the server anchor lookup finds the comment in ANOTHER
 *     (or a not-yet-fetched part of this) issue → an AppLink to the owning
 *     issue's `#comment-<id>` deep link, whose landing machinery is the same
 *     one the inbox uses. This really navigates and really changes the URL,
 *     so a link — with its real new-tab semantics — is honest here. The
 *     probe runs only when an issue-detail-class host context exists and
 *     missed locally; surfaces without a hosted comment list (inbox previews)
 *     never fetch.
 *   - no host context, or the server folds the target into the uniform 404 →
 *     an inert, dashed chip. Deleted, not permitted, or not fetched yet all
 *     land here and look identical: telling them apart would leak that a
 *     comment the reader may not see exists. Clicking says so once, via
 *     toast, and nothing else happens.
 *
 * The degraded state deliberately shows NO author and NO excerpt — the only
 * thing it can show is the label the author of the referencing text typed.
 *
 * Shares the chip shell with IssueChip / ProjectChip (same cap, padding and
 * caption size) so a line of prose carrying all three reads evenly.
 */

const BASE_CLASS =
  "comment-mention inline-flex min-w-0 max-w-[min(18rem,100%)] items-center gap-1.5 rounded-md border mx-0.5 px-2 py-0.5 text-caption align-middle";

/** Cross-issue jump target: the owning issue's canonical comment deep link. */
function anchorHref(paths: ReturnType<typeof useWorkspacePaths>, anchor: CommentAnchor, commentId: string) {
  return `${paths.issueDetail(anchor.identifier)}#comment-${commentId}`;
}

export function CommentMentionCard({
  commentId,
  label,
}: {
  commentId: string;
  label?: string;
}) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const paths = useWorkspacePaths();
  const { getActorName } = useActorName();
  const ctx = useCurrentIssueRenderContext();
  const resolved = ctx?.resolveComment?.(commentId) ?? null;
  const focus = ctx?.requestCommentFocus;
  // Probe only when a host context exists but missed locally; the anchor
  // naming below reads out of the probe result, mirroring the in-issue chip.
  const anchor = useResolveCommentAnchor(!resolved && ctx ? commentId : null);

  if (!resolved && anchor) {
    const author = getActorName(anchor.author_type, anchor.author_id);
    const excerpt = (
      commentPreview(anchor.excerpt).title ||
      commentPreview(anchor.excerpt).body ||
      ""
    ).slice(0, 60);
    const href = anchorHref(paths, anchor, commentId);
    return (
      <AppLink
        href={href}
        newTabTitle={label ?? excerpt}
        // The chip sits inside prose that may itself be clickable (a collapsed
        // comment's expander). Stopping propagation here keeps a jump from
        // also toggling whatever encloses it — same guard the resolved-state
        // button and IssueMentionLink apply.
        onClick={(e) => e.stopPropagation()}
        className={`${BASE_CLASS} cursor-pointer hover:bg-accent active:bg-accent/80 transition-colors focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50`}
        // Announces who and when so a screen-reader user knows where the
        // jump lands before taking it — the same contract as the in-issue
        // button.
        aria-label={t(($) => $.comment.anchor_aria, {
          author,
          when: timeAgo(anchor.created_at),
          excerpt,
        })}
      >
        <MessageSquareQuote className="h-3 w-3 shrink-0" aria-hidden="true" />
        <span className="min-w-0 truncate text-foreground">
          {label ?? excerpt}
        </span>
      </AppLink>
    );
  }

  if (!resolved || !focus) {
    return (
      <span
        // Dashed border + muted tone carry the degraded reading on their own.
        // No `opacity-*`: transparency standing in for a text tone is what
        // apps/web's text-contrast guard rejects.
        className={`${BASE_CLASS} border-dashed cursor-not-allowed text-muted-foreground`}
        aria-disabled="true"
        title={t(($) => $.comment.anchor_unavailable)}
        // Still says something on click. A chip that looks like a reference
        // and does nothing at all reads as broken UI; one toast names the
        // outcome without naming the comment.
        onClick={(e) => {
          e.stopPropagation();
          toast.error(t(($) => $.comment.anchor_unavailable));
        }}
      >
        <MessageSquareQuote className="h-3 w-3 shrink-0" aria-hidden="true" />
        <span className="min-w-0 truncate">
          {label ?? t(($) => $.comment.anchor_unavailable)}
        </span>
      </span>
    );
  }

  return (
    <button
      type="button"
      // The chip sits inside prose that may itself be clickable (a collapsed
      // comment's expander). Stopping propagation here keeps a jump from
      // also toggling whatever encloses it — same guard IssueMentionLink and
      // ProjectMentionLink apply.
      onClick={(e) => {
        e.stopPropagation();
        focus(commentId);
      }}
      className={`${BASE_CLASS} cursor-pointer hover:bg-accent active:bg-accent/80 transition-colors focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50`}
      // Announces who and when so a screen-reader user knows where the jump
      // lands before taking it — the sighted reader gets the same from the
      // label plus the chip's own context.
      aria-label={t(($) => $.comment.anchor_aria, {
        author: resolved.author,
        when: timeAgo(resolved.createdAt),
        excerpt: resolved.excerpt,
      })}
    >
      <MessageSquareQuote className="h-3 w-3 shrink-0" aria-hidden="true" />
      <span className="min-w-0 truncate text-foreground">
        {label ?? resolved.excerpt}
      </span>
    </button>
  );
}
