/**
 * Chat composer — thin wrapper around the new `<ChatInputBar>` (RUYI-694
 * 板1 ChatGPT 风格改版) with chat-specific wiring:
 *
 *   - **Controlled text**: parent (chat.tsx) owns the draft via
 *     `useChatDraftsStore` so switching sessions rehydrates the right
 *     draft. Pass `value` + `onChangeText` through.
 *   - **Stop button**: while an agent task is running for the active
 *     session, `sending` flips true and the send slot becomes Stop.
 *   - **Voice gate**: RUYI-694 D2 供应商池解析链（`useVoiceAvailability`）
 *     由父层算好传入；🎤 听写 + 🔵 全屏语音的显隐全部由 gate 决定
 *     （unreachable 仍可发起——回退门禁只有 missing/invalid）。
 *   - **Mention picker mode=chat**: chat is user ↔ single agent so
 *     @member / @agent / @squad / @all are noise. Picker route honors
 *     `?mode=chat`（`ChatInputBar` 内部自构路由）。
 *   - **No upload context**: chat attachments are session-scoped; the
 *     server back-fills `chat_message_id` on each row.
 *   - **Parent owns keyboard**: chat.tsx wraps in KeyboardAvoidingView.
 *
 * 旧实现包装共享 `<MessageComposer>`（pill 展开式）；本单换芯为板1
 * 常驻胶囊条，行为面（受控草稿/mention 触发/附件通道/乐观回滚）在
 * `ChatInputBar` 内等价复刻，共享 composer 零改动。
 */
import { useCallback } from "react";
import * as Haptics from "expo-haptics";
import { ChatInputBar } from "@/components/chat/chat-input-bar";
import type { Attachment } from "@multica/core/types";
import type { SharedFile } from "@/lib/share-payload";
import type { VoiceAvailability } from "@/data/voice-providers";
import type { Href } from "expo-router";

const IS_IOS = process.env.EXPO_OS === "ios";

interface Props {
  /** Current draft text (controlled). Empty string = no draft. */
  value: string;
  /** Fired on every keystroke. The caller writes to the drafts store. */
  onChangeText: (next: string) => void;
  /** Send the serialised markdown content + the completed attachments'
   *  server ids. Caller resets the input by setting `value=""` after a
   *  successful send. */
  onSend: (content: string, attachmentIds: string[]) => Promise<void> | void;
  /** Cancel the in-flight agent task. Only callable while `sending===true`. */
  onStop: () => void;
  /** True while an agent task is running for the active session. */
  sending: boolean;
  /** Queued tasks remain busy, but do not expose Stop without draft restore. */
  allowStop?: boolean;
  allowSubmitWhileRunning?: boolean;
  hasQueuedMessages?: boolean;
  stopPending?: boolean;
  /** Hard-disable typing + send（no agent / revoked / archived / runtime
   *  missing——R1 灰 pill 语义不变）。 */
  disabled?: boolean;
  /** When `disabled`, replaces the pill label with the reason. */
  disabledReason?: string;

  /** RUYI-463: 系统分享的附件，原样透传（语义见 `ChatInputBar`）。 */
  incomingSharedFiles?: SharedFile[];
  onIncomingSharedFilesConsumed?: () => void;

  /** RUYI-694: D2 语音可用性门禁（供应商池解析链结果）。 */
  voiceGate: VoiceAvailability | null;
  /** 发起全屏语音会话（🔵）。 */
  onOpenVoice: () => void;
  skillPickerPath?: Href;
  projectId?: string | null;
  onProjectChange?: (projectId: string | null) => void;
  restoredAttachments?: Attachment[];
  onRestoredAttachmentsConsumed?: () => void;
}

export function ChatComposer({
  value,
  onChangeText,
  onSend,
  onStop,
  sending,
  allowStop = true,
  allowSubmitWhileRunning = false,
  hasQueuedMessages = false,
  stopPending = false,
  disabled = false,
  disabledReason,
  incomingSharedFiles,
  onIncomingSharedFilesConsumed,
  voiceGate,
  onOpenVoice,
  skillPickerPath,
  projectId,
  onProjectChange,
  restoredAttachments,
  onRestoredAttachmentsConsumed,
}: Props) {
  const handleStop = useCallback(() => {
    if (IS_IOS) {
      void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Medium);
    }
    onStop();
  }, [onStop]);

  return (
    <ChatInputBar
      value={value}
      onChangeText={onChangeText}
      onSend={onSend}
      onStop={handleStop}
      sending={sending}
      allowStop={allowStop}
      allowSubmitWhileRunning={allowSubmitWhileRunning}
      hasQueuedMessages={hasQueuedMessages}
      stopPending={stopPending}
      disabled={disabled}
      disabledReason={disabledReason}
      incomingSharedFiles={incomingSharedFiles}
      onIncomingSharedFilesConsumed={onIncomingSharedFilesConsumed}
      voiceGate={voiceGate}
      onOpenVoice={onOpenVoice}
      skillPickerPath={skillPickerPath}
      projectId={projectId}
      onProjectChange={onProjectChange}
      restoredAttachments={restoredAttachments}
      onRestoredAttachmentsConsumed={onRestoredAttachmentsConsumed}
    />
  );
}
