/**
 * Long-press handler for a comment bubble. Exposes `onLongPress` (drives a
 * cross-platform ActionSheet) and `isPressed` (drives the caller's highlight
 * ring while the sheet is on screen).
 *
 * Uses useActionSheet() hook: iOS delegates to ActionSheetIOS native,
 * Android uses a Modal-based bottom sheet. The main menu and the nested
 * React… sheet each own their own modalProps — the caller must mount one
 * <ActionSheetModal> per prop set. (Merging the two into a single spread
 * bound the modal to whichever sheet came last, so on Android the main
 * menu never became visible.)
 *
 * Item set (conditional, mirrors web's comment context menu):
 *   Reply (stub) · React… (opens nested sheet) · Copy · Select Text ·
 *   Copy Link · Resolve/Unresolve Thread (root only) · Edit (own, or
 *   moderator on member comments) · Delete (own only) · Cancel
 *
 * Edit routes through `isEditing`/`closeEdit` — the caller mounts
 * <CommentEditModal> when isEditing is true.
 *
 * The nested React… sheet (5 quick emojis + More reactions… + Cancel) is
 * fired from INSIDE the outer sheet's completion callback rather than
 * inline, because iOS will refuse to present a second ActionSheet while the
 * first is still dismissing — the callback runs after dismissal completes.
 * On Android the same ordering works because we dismiss the modal before
 * calling onSelect.
 */
import React, { useCallback, useState } from "react";
import { Alert } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import * as Clipboard from "expo-clipboard";
import * as Haptics from "expo-haptics";
import type { Reaction, TimelineEntry } from "@multica/core/types";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { getWebUrl } from "@/data/server-store";
import { useCommentSelectStore } from "@/data/comment-select-store";
import { useReplyTargetStore } from "@/data/stores/reply-target-store";
import { useActorLookup } from "@/data/use-actor-name";
import { memberListOptions } from "@/data/queries/members";
import {
  useDeleteComment,
  useResolveComment,
  useToggleCommentReaction,
} from "@/data/mutations/issues";
import {
  buildCommentMenu,
  canEditCommentEntry,
  type CommentMenuActionKind,
} from "@/lib/comment-menu";
import { QUICK_EMOJIS } from "@/lib/quick-emojis";
import {
  useActionSheet,
  ActionSheetModal,
} from "@/components/ui/action-sheet";
import { useT } from "@/lib/use-t";

const QUICK_ROW_SIZE = 5;

export function useCommentLongPress(
  entry: TimelineEntry,
  issueId: string,
  issueIdentifier: string | undefined,
): {
  onLongPress: () => void;
  isPressed: boolean;
  /** True while the Edit flow is open — the caller mounts CommentEditModal. */
  isEditing: boolean;
  closeEdit: () => void;
  mainModalProps: React.ComponentProps<typeof ActionSheetModal>;
  reactModalProps: React.ComponentProps<typeof ActionSheetModal>;
} {
  const { t } = useT("issues");
  const [isPressed, setIsPressed] = useState(false);
  const [isEditing, setIsEditing] = useState(false);
  const mainSheet = useActionSheet();
  const reactSheet = useActionSheet();
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const userId = useAuthStore((s) => s.user?.id);
  // Workspace owners/admins moderate member comments (web parity:
  // packages/views/issues/components/issue-detail.tsx canModerateComments,
  // mirroring backend comment.go). React Query dedupes the request across
  // rows, same as issueAttachmentsOptions.
  const { data: members } = useQuery(memberListOptions(wsId));
  const canModerate = !!userId
    && !!members?.some(
      (m) => m.user_id === userId && (m.role === "owner" || m.role === "admin"),
    );
  const toggleReaction = useToggleCommentReaction(issueId);
  const deleteComment = useDeleteComment(issueId);
  const resolveComment = useResolveComment(issueId);
  const { getName } = useActorLookup();

  const closeEdit = useCallback(() => setIsEditing(false), []);

  // show is a stable useCallback inside useActionSheet, while the hook's
  // return object is recreated every render — destructure so the deps of
  // onLongPress stay referentially stable.
  const { show: showMainSheet } = mainSheet;
  const { show: showReactSheet } = reactSheet;

  const onLongPress = useCallback(() => {
    const isOwn = entry.actor_type === "member" && entry.actor_id === userId;
    const isRoot = !entry.parent_id;
    const resolved = !!entry.resolved_at;
    const hasContent = !!entry.content;
    // getWebUrl() 恒有值(未单独配置 Web 地址时回退当前服务器地址),
    // 能否复制链接只取决于 slug / identifier 是否就绪(RUYI-4)。
    const webUrl = getWebUrl();
    const canCopyLink = !!(wsSlug && issueIdentifier);
    const canEdit = canEditCommentEntry(entry, userId, canModerate);

    const menu = buildCommentMenu({
      hasContent,
      canCopyLink,
      isRoot,
      resolved,
      canEdit,
      isOwn,
      labels: {
        reply: "Reply",
        react: "React…",
        edit: "Edit",
        copy: "Copy",
        select: "Select Text",
        copyLink: "Copy Link",
        resolve: "Resolve Thread",
        unresolve: "Unresolve Thread",
        delete: "Delete",
        cancel: "Cancel",
      },
    });

    Haptics.selectionAsync().catch(() => {});
    setIsPressed(true);

    showMainSheet({
      options: menu.options,
      cancelButtonIndex: menu.cancelButtonIndex,
      ...(menu.destructiveButtonIndex !== undefined &&
      menu.destructiveButtonIndex >= 0
        ? { destructiveButtonIndex: menu.destructiveButtonIndex }
        : {}),
      onSelect: (i) => {
        setIsPressed(false);
        const action = menu.actions[i] as CommentMenuActionKind | undefined;
        if (!action || action === "cancel") return;

        switch (action) {
          case "reply": {
            // Set the reply target — the InlineCommentComposer subscribes
            // to this store, auto-expands, and threads the next submit
            // under entry.id via useCreateComment's `parentId`.
            const actorName =
              entry.actor_name ||
              getName(
                entry.actor_type as "member" | "agent" | null | undefined,
                entry.actor_id,
              );
            useReplyTargetStore.getState().setTarget({
              commentId: entry.id,
              actorName: actorName || "comment",
              preview: entry.content ?? "",
            });
            return;
          }
          case "react":
            // Present the nested React sheet from inside this completion
            // callback — see file header for why.
            presentReactSheet({
              entry,
              userId,
              wsSlug,
              issueId,
              toggle: (emoji, existing) =>
                toggleReaction.mutate({
                  commentId: entry.id,
                  emoji,
                  existing,
                }),
              reactSheet: { show: showReactSheet },
            });
            return;
          case "copy":
            if (entry.content) {
              Clipboard.setStringAsync(entry.content);
              Haptics.notificationAsync(
                Haptics.NotificationFeedbackType.Success,
              ).catch(() => {});
            }
            return;
          case "select":
            useCommentSelectStore.getState().setSelecting(entry.id);
            return;
          case "copyLink": {
            if (!canCopyLink) return;
            const url = `${webUrl}/${wsSlug}/issue/${issueIdentifier}#comment-${entry.id}`;
            Clipboard.setStringAsync(url);
            Haptics.notificationAsync(
              Haptics.NotificationFeedbackType.Success,
            ).catch(() => {});
            return;
          }
          case "resolve":
            resolveComment.mutate({
              commentId: entry.id,
              resolved: !entry.resolved_at,
            });
            return;
          case "edit":
            // The caller mounts <CommentEditModal> while isEditing is
            // true; the modal owns the edit mutation and its own dismiss.
            setIsEditing(true);
            return;
          case "delete":
            // web 的 comment.delete_title 无问号、delete_desc_with_replies 措辞
            // 也不同（"this comment and all its replies"），故用 mobile 专属键。
            Alert.alert(
              t("mobile.comment.delete_title", "Delete comment?"),
              t(
                "mobile.comment.delete_desc",
                "This comment will be permanently deleted. Replies in the thread will also be removed. This cannot be undone.",
              ),
              [
                {
                  text: t("common:cancel", "Cancel"),
                  style: "cancel",
                },
                {
                  text: t("common:delete", "Delete"),
                  style: "destructive",
                  onPress: () => deleteComment.mutate(entry.id),
                },
              ],
            );
            return;
        }
      },
    });
  }, [
    entry,
    issueId,
    issueIdentifier,
    userId,
    wsSlug,
    canModerate,
    showMainSheet,
    showReactSheet,
    toggleReaction,
    deleteComment,
    resolveComment,
    t,
    getName,
  ]);

  return {
    onLongPress,
    isPressed,
    isEditing,
    closeEdit,
    mainModalProps: mainSheet.modalProps,
    reactModalProps: reactSheet.modalProps,
  };
}

function presentReactSheet(args: {
  entry: TimelineEntry;
  userId: string | undefined;
  wsSlug: string | null;
  issueId: string;
  toggle: (emoji: string, existing: Reaction | undefined) => void;
  reactSheet: { show: ReturnType<typeof useActionSheet>["show"] };
}) {
  const { entry, userId, wsSlug, issueId, toggle, reactSheet } = args;
  const emojis = QUICK_EMOJIS.slice(0, QUICK_ROW_SIZE);
  const options = [...emojis, "More reactions…", "Cancel"];
  const cancelButtonIndex = options.length - 1;

  reactSheet.show({
    options,
    cancelButtonIndex,
    onSelect: (i) => {
      if (i === cancelButtonIndex) return;
      if (i === emojis.length) {
        if (!wsSlug) return;
        router.push({
          pathname:
            "/[workspace]/issue/[id]/comment/[commentId]/emoji-picker",
          params: {
            workspace: wsSlug,
            id: issueId,
            commentId: entry.id,
          },
        });
        return;
      }
      const emoji = emojis[i];
      if (!emoji) return;
      const existing = ((entry.reactions ?? []) as Reaction[]).find(
        (r) =>
          r.emoji === emoji &&
          r.actor_type === "member" &&
          r.actor_id === userId,
      );
      toggle(emoji, existing);
    },
  });
}
