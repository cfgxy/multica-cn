/**
 * Markdown wrapper that remounts the pipeline when selection mode flips.
 *
 * RUYI-416: flipping `selectable` on a live Markdown drives the Android
 * TextView through setTextIsSelectable(false→true) on an already-laid-out
 * view — the library's applySelectableState re-sets the text buffer and
 * rebuilds the selection/cursor controllers mid-flight. On that path the
 * selection pipeline can come up in the "select all, no handles" state
 * (selectAllText() fills the whole buffer without insertion anchors)
 * instead of word selection at the press point. Swapping the React key
 * instead makes selectable a creation-time property of the native views —
 * the same steady state as reader surfaces (issue description), where
 * native long-press word selection works on the same devices.
 *
 * Callers: comment bodies and chat message bubbles, which toggle
 * `selectable` when the user picks "Select Text" from the long-press
 * action sheet (comment-select-store / chat-select-store).
 */
import type { ComponentProps } from "react";
import { Markdown } from "@/lib/markdown";

type MarkdownProps = ComponentProps<typeof Markdown>;

// Selection mode is always explicit at the call sites — no default that
// could silently disagree with the remount key derived from it.
type Props = Omit<MarkdownProps, "selectable"> & { selectable: boolean };

export function SelectableMarkdown({
  content,
  attachments,
  selectable,
  compact,
}: Props) {
  return (
    <Markdown
      key={selectable ? "selectable" : "static"}
      content={content}
      attachments={attachments}
      selectable={selectable}
      compact={compact}
    />
  );
}
