"use client";

import { MessageSquareQuote } from "lucide-react";
import { toast } from "sonner";
import { useT, useTimeAgo } from "../../i18n";
import { useCurrentIssueRenderContext } from "../current-issue-render-context";

/**
 * `mention://comment/<id>` rendered as a chip (RUYI-108).
 *
 * Two states, decided entirely by whether the current render context can
 * resolve the id out of comments this client ALREADY has:
 *
 *   - resolved → a button that jumps to that comment in this issue's
 *     timeline (expand its thread, centre it, flash it). It is a button and
 *     not a link on purpose: nothing navigates, the URL does not change, and
 *     a middle-click "open in new tab" would be a lie.
 *   - degraded → an inert, dashed chip. Deleted, not permitted, another
 *     issue, or not fetched yet all land here and look identical: telling
 *     them apart would leak that a comment the reader may not see exists.
 *     Clicking says so once, via toast, and nothing else happens.
 *
 * The degraded state deliberately shows NO author and NO excerpt — the only
 * thing it can show is the label the author of the referencing text typed.
 * Resolving never triggers a fetch either; a probe would reintroduce the
 * same disclosure through timing.
 *
 * Shares the chip shell with IssueChip / ProjectChip (same cap, padding and
 * caption size) so a line of prose carrying all three reads evenly.
 */

const BASE_CLASS =
  "comment-mention inline-flex min-w-0 max-w-[min(18rem,100%)] items-center gap-1.5 rounded-md border mx-0.5 px-2 py-0.5 text-caption align-middle";

export function CommentMentionCard({
  commentId,
  label,
}: {
  commentId: string;
  label?: string;
}) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const ctx = useCurrentIssueRenderContext();
  const resolved = ctx?.resolveComment?.(commentId) ?? null;
  const focus = ctx?.requestCommentFocus;

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
