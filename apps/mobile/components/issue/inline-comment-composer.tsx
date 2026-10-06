/**
 * Inline issue-comment composer — thin wrapper around the shared
 * `<MessageComposer>` with comment-specific wiring:
 *
 *   - `onSubmit` → `useCreateComment(issueId).mutateAsync`
 *   - Reply target sourced from `useReplyTargetStore` (set by the
 *     comment long-press action sheet)
 *   - Mention picker path → `/[workspace]/mention-picker?mode=comment`
 *   - Upload context binds attachments to this issue
 *
 * All UI / state / chip plumbing lives in `MessageComposer`. The chat
 * composer (`components/chat/chat-composer.tsx`) uses the same component
 * with chat-mode props.
 */
import { useCallback, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useCreateComment } from "@/data/mutations/issues";
import { useReplyTargetStore } from "@/data/stores/reply-target-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { quickReplyListOptions } from "@/data/queries/quick-replies";
import { MessageComposer } from "@/components/composer/message-composer";
import { IconButton } from "@/components/ui/icon-button";
import { VoiceSessionOverlay } from "@/components/voice/voice-session-overlay";
import { appendVoiceTurnToDraft } from "@/lib/voice/draft-backfill";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export function InlineCommentComposer({
  issueId,
  onPublished,
  voiceAgentId = null,
}: {
  issueId: string;
  /** Called with the SERVER comment id after this user's own publish is
   *  accepted (RUYI-28 auto-expand). Not fired for optimistic inserts or
   *  other users' realtime arrivals — only the local success path. */
  onPublished?: (commentId: string) => void;
  /** RUYI-474: 本单绑定的 agent id。非空且草稿为空时出现语音入口；
   *  null（未绑定 / 绑定人类或 squad）保持纯文本编辑器。 */
  voiceAgentId?: string | null;
}) {
  const { t } = useT("issues");
  const { t: tVoice } = useT("voice");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const createComment = useCreateComment(issueId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const replyTarget = useReplyTargetStore((s) => s.target);
  const clearReplyTarget = useReplyTargetStore((s) => s.clear);
  // Workspace quick-reply catalog (RUYI-435). Read-open to every member;
  // rows render dynamically — the defaults live only in the server seed.
  const { data: quickReplies } = useQuery(quickReplyListOptions(wsId ?? null));

  // RUYI-474 三态入口：草稿受控持有；语音会话挂载期间隐藏麦克风（防重入）。
  // 口述定稿经 appendVoiceTurnToDraft 回填，发送仍走 MessageComposer 的
  // 手动提交路径（成功清空 / 失败回滚都经受控 onChangeText）。
  const [voiceOpen, setVoiceOpen] = useState(false);
  const [draft, setDraft] = useState("");

  const onSubmit = useCallback(
    async ({
      content,
      attachmentIds,
    }: {
      content: string;
      attachmentIds: string[];
    }) => {
      try {
        const created = await createComment.mutateAsync({
          content,
          parentId: replyTarget?.commentId,
          attachmentIds: attachmentIds.length > 0 ? attachmentIds : undefined,
        });
        if (created?.id) onPublished?.(created.id);
      } catch (err) {
        // Rethrow so MessageComposer's catch path restores text + chips.
        // The optimistic timeline row stays with its inline
        // Failed · Retry · Discard affordance.
        throw err;
      }
    },
    [createComment, replyTarget?.commentId, onPublished],
  );

  const handleUserTurn = useCallback(
    (spoken: string) =>
      setDraft((prev) => appendVoiceTurnToDraft(prev, spoken)),
    [],
  );

  return (
    <>
      <MessageComposer
        value={draft}
        onChangeText={setDraft}
        onSubmit={onSubmit}
        mentionPickerPath={{
          pathname: "/[workspace]/mention-picker",
          params: { workspace: wsSlug ?? "", mode: "comment" },
        }}
        skillPickerPath={{ pathname: "/[workspace]/skill-picker", params: { workspace: wsSlug ?? "" } }}
        quickReplies={(quickReplies ?? []).map((reply) => ({
          id: reply.id,
          name: reply.name,
          content: reply.content,
        }))}
        uploadContext={{ issueId }}
        requireVisibleText
        placeholder={t("mobile.composer.placeholder", "Add a comment…")}
        pillLabel={t(
          "mobile.composer.pill_label",
          "Add a comment, @ to mention…",
        )}
        pillIcon="chatbubble-ellipses-outline"
        replyTarget={
          replyTarget
            ? {
                actorName: replyTarget.actorName,
                preview: replyTarget.preview,
              }
            : null
        }
        onClearReplyTarget={clearReplyTarget}
        expandTrigger={replyTarget?.commentId ?? null}
        renderVoiceWhenEmpty={
          voiceAgentId !== null && !voiceOpen
            ? () => (
                <IconButton
                  name="mic-outline"
                  iconSize={18}
                  color={theme.primaryForeground}
                  variant="default"
                  onPress={() => setVoiceOpen(true)}
                  hitSlop={12}
                  className="h-8 w-8 rounded-full"
                  accessibilityLabel={tVoice(
                    "button.start",
                    "Start voice conversation",
                  )}
                />
              )
            : undefined
        }
      />
      <VoiceSessionOverlay
        agentId={voiceOpen && voiceAgentId !== null ? voiceAgentId : null}
        workspaceSlug={wsSlug ?? ""}
        onClose={() => setVoiceOpen(false)}
        onUserTurn={handleUserTurn}
      />
    </>
  );
}
