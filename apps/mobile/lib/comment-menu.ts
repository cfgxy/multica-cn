/**
 * Pure helpers behind the comment long-press action sheet (see
 * components/issue/comment-context-menu.tsx). Kept free of React / RN
 * imports so the vitest node lane can cover the permission gate and the
 * menu construction directly.
 *
 * Edit permission mirrors web's comment card
 * (packages/views/issues/components/comment-card.tsx:628):
 *   canEditEntry = isOwn || (canModerate && entry.actor_type === "member")
 * where canModerate means the current user is a workspace owner/admin
 * (mirrors backend comment.go permission check).
 */
import type { TimelineEntry } from "@multica/core/types";

export type CommentMenuActionKind =
  | "reply"
  | "react"
  | "edit"
  | "copy"
  | "select"
  | "copyLink"
  | "resolve"
  | "delete"
  | "cancel";

export type CommentMenuEntry = Pick<TimelineEntry, "actor_type" | "actor_id">;

export function canEditCommentEntry(
  entry: CommentMenuEntry,
  userId: string | undefined,
  canModerate: boolean,
): boolean {
  const isOwn = entry.actor_type === "member" && entry.actor_id === userId;
  return isOwn || (canModerate && entry.actor_type === "member");
}

export interface CommentMenuLabels {
  reply: string;
  react: string;
  edit: string;
  copy: string;
  select: string;
  copyLink: string;
  resolve: string;
  unresolve: string;
  delete: string;
  cancel: string;
}

export interface CommentMenuInput {
  hasContent: boolean;
  canCopyLink: boolean;
  isRoot: boolean;
  resolved: boolean;
  /** canEditCommentEntry() result — Edit item renders only when true. */
  canEdit: boolean;
  /** Own comment only: Delete renders with destructive styling. */
  isOwn: boolean;
  labels: CommentMenuLabels;
}

export interface BuiltCommentMenu {
  options: string[];
  actions: CommentMenuActionKind[];
  cancelButtonIndex: number;
  destructiveButtonIndex?: number;
}

/**
 * Item set mirrors web's comment context menu: Reply · React… · Copy ·
 * Select Text · Copy Link · Resolve/Unresolve (root only) · Edit (gated) ·
 * Delete (own only) · Cancel. Edit sits directly above Delete, the same
 * adjacency web uses. Cancel is always last so the sheet is dismissible.
 */
export function buildCommentMenu(input: CommentMenuInput): BuiltCommentMenu {
  const { hasContent, canCopyLink, isRoot, resolved, canEdit, isOwn, labels } =
    input;

  const options: string[] = [];
  const actions: CommentMenuActionKind[] = [];
  const push = (label: string, action: CommentMenuActionKind) => {
    options.push(label);
    actions.push(action);
  };

  push(labels.reply, "reply");
  push(labels.react, "react");
  if (hasContent) {
    push(labels.copy, "copy");
    push(labels.select, "select");
  }
  if (canCopyLink) push(labels.copyLink, "copyLink");
  if (isRoot) push(resolved ? labels.unresolve : labels.resolve, "resolve");
  if (canEdit) push(labels.edit, "edit");
  if (isOwn) push(labels.delete, "delete");
  push(labels.cancel, "cancel");

  const cancelButtonIndex = options.length - 1;
  const deleteIndex = actions.indexOf("delete");
  const destructiveButtonIndex =
    isOwn && deleteIndex >= 0 ? deleteIndex : undefined;

  return { options, actions, cancelButtonIndex, destructiveButtonIndex };
}
