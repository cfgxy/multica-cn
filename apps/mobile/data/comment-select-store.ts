/**
 * Per-comment text-selection mode. When the user taps "Select text" in the
 * comment action sheet, the targeted comment id is parked here, and the
 * matching CommentBody flips its Markdown to `selectable={true}` so the
 * next long-press inside the bubble triggers iOS' native selection
 * magnifier + handles + edit menu instead of re-opening the action sheet.
 *
 * Why a separate Zustand store (not props / context):
 *   - The "Select text" trigger lives in a separate Expo Router route
 *     (`comment/[commentId]/actions`); routes can't pass callbacks up to
 *     CommentCard rendered in the parent screen.
 *   - Only one comment can be in selection mode at a time across the app —
 *     selecting comment B implicitly clears comment A by id replacement.
 *
 * Why this exists at all (iOS / Android constraint):
 *   - Long-press on a single View can be routed to exactly one gesture
 *     recognizer — either text selection (iOS UITextInteraction / Android
 *     TextView.setTextIsSelectable) OR a long-click handler that opens the
 *     action sheet. The two cannot fire in parallel. Mirrors the iOS 26
 *     iMessage pattern: the context menu has a "Select" entry that
 *     transitions the bubble into selection mode rather than trying to
 *     run both gestures at once.
 *
 * Lifecycle: cleared when the issue-detail screen unmounts so each fresh
 * navigation into an issue starts with no comment in selection mode.
 */
import { create } from "zustand";

interface State {
  selectingId: string | null;
  setSelecting: (commentId: string) => void;
  clear: () => void;
}

export const useCommentSelectStore = create<State>((set) => ({
  selectingId: null,
  setSelecting: (commentId) => set({ selectingId: commentId }),
  clear: () => set({ selectingId: null }),
}));
