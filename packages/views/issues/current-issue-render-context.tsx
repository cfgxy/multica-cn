"use client";

import {
  createContext,
  useContext,
  type ReactNode,
} from "react";

/** What a comment anchor needs in order to render as a resolved chip.
 *  Deliberately the minimum: enough to label the chip and to say what the
 *  screen reader should announce, nothing that would need a fetch. */
export type ResolvedAnchorComment = Readonly<{
  id: string;
  /** Display name of the comment's author. */
  author: string;
  /** ISO timestamp — the chip renders it relative. */
  createdAt: string;
  /** Short plain-text excerpt (Markdown already stripped). */
  excerpt: string;
}>;

export type CurrentIssueRenderContextValue = Readonly<{
  id: string;
  identifier: string;
  /**
   * Resolve a comment id against the comments THIS client already has for
   * this issue. Returns null for anything else — deleted, not permitted,
   * belonging to another issue, or simply not fetched yet.
   *
   * Must never issue a request. The four misses are deliberately
   * indistinguishable to the caller: separating "exists but you can't see
   * it" from "doesn't exist" is itself the disclosure the anchor design
   * forbids (RUYI-108).
   */
  resolveComment?: (commentId: string) => ResolvedAnchorComment | null;
  /**
   * Bring a comment of this issue into view: expand its thread if
   * collapsed, centre it, flash it. Same landing path the inbox deep link
   * uses, replayed on every call so re-clicking one anchor works.
   */
  requestCommentFocus?: (commentId: string) => void;
}>;

/**
 * The issue identity that owns the content currently being rendered.
 *
 * It intentionally defaults to null: content renderers are shared by issue
 * detail, inbox, chat, and other surfaces, and only an explicit issue-detail
 * owner may opt into current-issue semantics.
 *
 * `resolveComment` / `requestCommentFocus` are optional for the same reason,
 * one level finer: a surface can legitimately know which issue it renders
 * without hosting that issue's comment list (the inbox preview does). Where
 * they are absent every comment anchor renders in its degraded state, which
 * is the correct reading — there is nothing on this screen to jump to.
 */
const CurrentIssueRenderContext =
  createContext<CurrentIssueRenderContextValue | null>(null);

export function CurrentIssueRenderContextProvider({
  value,
  children,
}: {
  value: CurrentIssueRenderContextValue | null;
  children: ReactNode;
}) {
  return (
    <CurrentIssueRenderContext.Provider value={value}>
      {children}
    </CurrentIssueRenderContext.Provider>
  );
}

export function useCurrentIssueRenderContext(): CurrentIssueRenderContextValue | null {
  return useContext(CurrentIssueRenderContext);
}
