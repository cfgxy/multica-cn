/**
 * Chat 输入条（RUYI-694 板1）：ChatGPT 风格单行胶囊条，四形态切换
 * （`resolveInputShape`），听写长条与「+」面板装配于此。
 *
 * 行为面从共享 `MessageComposer` 复刻 chat 所需子集（受控草稿、
 * mention/skill 触发与 strip、附件上传通道、乐观清空 + 回滚）——共享
 * composer 本身零改动，issue 评论路径不受影响。与 composer 的差异点：
 *
 *   - 无 pill 展开/折叠（bar 常驻；disabled 链仍是灰 pill，R1 不变）。
 *   - 发送位优先级沿用：Stop > 🎤🔵（语音）> 发送/灰化发送。
 *   - 🎤 听写与 🔵 全屏语音并排；🎤 端侧可用走端侧识别，不可用
 *     （API<31 / 无识别服务）经服务端转写降级（RUYI-710，需语音门禁
 *     可用 + workspace 就绪）；权限被拒或非 Android 隐藏。
 */
import { useCallback, useEffect, useRef, useState } from "react";
import {
  Alert,
  Keyboard,
  Platform,
  Pressable,
  TextInput,
  ToastAndroid,
  View,
  useWindowDimensions,
  type LayoutChangeEvent,
  type StyleProp,
  type TextStyle,
  type ViewStyle,
} from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { router, type Href } from "expo-router";
import * as Haptics from "expo-haptics";
import Animated, { FadeIn, FadeOut } from "react-native-reanimated";
import { ActionSheetModal } from "@/components/ui/action-sheet";
import { AttachmentZone } from "@/components/issue/attachment-zone";
import { PlusPanel } from "@/components/chat/plus-panel";
import { DictationBar } from "@/components/chat/dictation-bar";
import { useFileAttach } from "@/components/editor/use-file-attach";
import { assetFromSharedFile } from "@/lib/picked-asset";
import type { SharedFile } from "@/lib/share-payload";
import type { Attachment } from "@multica/core/types";
import {
  canSubmitMessageDraft,
  completedAttachmentIds,
} from "@/lib/attachment-zone";
import { serializeMentionChips } from "@/lib/mention-serialize";
import { mentionTriggerFromInput } from "@/lib/mention-trigger";
import {
  insertSkillReference,
  serializeSkillReference,
  skillTriggerFromInput,
} from "@/lib/skill-reference";
import { useMentionDraftStore } from "@/data/stores/mention-draft-store";
import { useSkillDraftStore } from "@/data/stores/skill-draft-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { api } from "@/data/api";
import {
  resolveInputShape,
  voiceGateVerdict,
} from "@/lib/voice/input-form";
import type { VoiceAvailabilityOutcome } from "@/lib/voice/availability";
import { useDictation } from "@/lib/voice/use-dictation";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";
import {
  CHAT_COMPOSER_INPUT_MAX_HEIGHT,
  CHAT_COMPOSER_INPUT_MIN_HEIGHT,
  CHAT_COMPOSER_MIN_HEIGHT,
  resolveChatComposerInputHeight,
  resolvePlusPanelMaxHeight,
} from "@/lib/chat-composer-layout";
import { Text } from "@/components/ui/text";

interface Props {
  value: string;
  onChangeText: (next: string) => void;
  onSend: (
    content: string,
    attachmentIds: string[],
  ) => Promise<void> | void;
  onStop: () => void;
  sending: boolean;
  allowStop?: boolean;
  allowSubmitWhileRunning?: boolean;
  stopPending?: boolean;
  disabled?: boolean;
  disabledReason?: string;
  incomingSharedFiles?: SharedFile[];
  onIncomingSharedFilesConsumed?: () => void;
  /** 语音门禁（D2 供应商池解析链）；null = 调用方未接（隐藏 🎤🔵）。 */
  voiceGate: {
    availability: VoiceAvailabilityOutcome | null;
    resolving: boolean;
  } | null;
  onOpenVoice: () => void;
  /** 技能指令 picker 路由；不传则不渲染该项与 / 触发。 */
  skillPickerPath?: Href;
  restoredAttachments?: Attachment[];
  onRestoredAttachmentsConsumed?: () => void;
}

export function ChatInputBar({
  value,
  onChangeText,
  onSend,
  onStop,
  sending,
  allowStop = true,
  allowSubmitWhileRunning = false,
  stopPending = false,
  disabled = false,
  disabledReason,
  incomingSharedFiles,
  onIncomingSharedFilesConsumed,
  voiceGate,
  onOpenVoice,
  skillPickerPath,
  restoredAttachments,
  onRestoredAttachmentsConsumed,
}: Props) {
  const { t } = useT("chat");
  const { t: tVoice } = useT("voice");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const mentionPickerPath: Href = {
    pathname: "/[workspace]/mention-picker",
    params: { workspace: wsSlug ?? "", mode: "chat" },
  };
  const hasSkillPicker = !!skillPickerPath;

  const inputRef = useRef<TextInput>(null);
  const [submitting, setSubmitting] = useState(false);
  const [plusOpen, setPlusOpen] = useState(false);
  const [dictationOpen, setDictationOpen] = useState(false);
  const [inputHeight, setInputHeight] = useState(CHAT_COMPOSER_INPUT_MIN_HEIGHT);
  const [composerTop, setComposerTop] = useState(0);
  const { height: windowHeight } = useWindowDimensions();
  const plusPanelMaxHeight = resolvePlusPanelMaxHeight(windowHeight, composerTop);

  const handleComposerLayout = useCallback((event: LayoutChangeEvent) => {
    setComposerTop(event.nativeEvent.layout.y);
  }, []);

  const focusInput = useCallback(() => {
    requestAnimationFrame(() => inputRef.current?.focus());
  }, []);

  const {
    attachments,
    pickAndUploadImages,
    takeAndUploadPhoto,
    pickAndUploadFiles,
    imageSourceModalProps,
    removeAttachment,
    retryAttachment,
    clearAttachments,
    restoreAttachments,
    enqueueAssets,
    uploading,
  } = useFileAttach({ alertOnError: false, onAttachmentsEnqueued: focusInput });

  useEffect(() => {
    if (!restoredAttachments?.length) return;
    restoreAttachments(restoredAttachments.map((attachment) => ({
      localId: attachment.id,
      localUri: attachment.url || attachment.download_url,
      filename: attachment.filename,
      mimeType: attachment.content_type,
      status: "completed" as const,
      id: attachment.id,
      url: attachment.url,
      downloadUrl: attachment.download_url,
    })));
    onRestoredAttachmentsConsumed?.();
  }, [restoredAttachments, restoreAttachments, onRestoredAttachmentsConsumed]);

  // 系统分享附件一次性入队（与 MessageComposer 同语义：ref 防重复）。
  const consumedSharedRef = useRef<SharedFile[] | null>(null);
  useEffect(() => {
    if (!incomingSharedFiles || incomingSharedFiles.length === 0) return;
    if (consumedSharedRef.current === incomingSharedFiles) return;
    consumedSharedRef.current = incomingSharedFiles;
    enqueueAssets(incomingSharedFiles.map(assetFromSharedFile));
    onIncomingSharedFilesConsumed?.();
  }, [incomingSharedFiles, onIncomingSharedFilesConsumed, enqueueAssets]);

  const mentions = useMentionDraftStore((s) => s.mentions);
  const removeMention = useMentionDraftStore((s) => s.remove);
  const clearMentions = useMentionDraftStore((s) => s.clear);
  const pendingToken = useMentionDraftStore((s) => s.token);
  const selectedSkill = useSkillDraftStore((s) => s.selected);
  const slashTypedRef = useRef(false);

  useEffect(() => {
    if (!hasSkillPicker) return;
    return () => useSkillDraftStore.getState().clear();
  }, [hasSkillPicker]);

  // 卸载清 mention 草稿，防 chips 泄漏进下一个 composer 会话。
  useEffect(() => {
    return () => {
      clearMentions();
      useMentionDraftStore.getState().setToken(null);
    };
  }, [clearMentions]);

  const selectionRef = useRef<{ start: number; end: number }>({
    start: 0,
    end: 0,
  });
  const onSelectionChange = useCallback(
    (e: { nativeEvent: { selection: { start: number; end: number } } }) => {
      selectionRef.current = e.nativeEvent.selection;
    },
    [],
  );

  // 技能 picker 回落：strip token、插入引用、恢复光标（chat 传
  // skillPickerPath 时才生效）。
  useEffect(() => {
    if (!selectedSkill || !hasSkillPicker) return;
    const store = useSkillDraftStore.getState();
    store.setSelected(null);
    const token = store.token;
    store.setToken(null);
    let next: string;
    let cursor: number;
    if (token) {
      next = insertSkillReference(value, token, selectedSkill);
      if (next === value) return;
      cursor = token.start + serializeSkillReference(selectedSkill).length + 1;
    } else {
      const selection = selectionRef.current;
      const reference = serializeSkillReference(selectedSkill) + " ";
      next = value.slice(0, selection.start) + reference + value.slice(selection.end);
      cursor = selection.start + reference.length;
    }
    onChangeText(next);
    selectionRef.current = { start: cursor, end: cursor };
    requestAnimationFrame(() => {
      inputRef.current?.focus();
      inputRef.current?.setNativeProps({ selection: { start: cursor, end: cursor } });
    });
  }, [selectedSkill, hasSkillPicker, value, onChangeText]);

  // mention picker 回落：strip 掉 `@<query>` 触发片段。
  const prevMentionsLenRef = useRef(mentions.length);
  useEffect(() => {
    const prevLen = prevMentionsLenRef.current;
    prevMentionsLenRef.current = mentions.length;
    if (!pendingToken || mentions.length <= prevLen) return;

    const { start, query } = pendingToken;
    useMentionDraftStore.getState().setToken(null);
    const expected = `@${query}`;
    if (value.slice(start, start + expected.length) !== expected) return;
    const next = value.slice(0, start) + value.slice(start + expected.length);
    selectionRef.current = { start, end: start };
    onChangeText(next);
    requestAnimationFrame(() => inputRef.current?.focus());
  }, [pendingToken, mentions.length, value, onChangeText]);

  const canSend = canSubmitMessageDraft({
    text: value,
    mentions,
    attachments,
    disabled,
    isSending: sending && !allowSubmitWhileRunning,
    submitting: submitting || stopPending,
  });

  const handleSubmit = useCallback(async () => {
    if (!canSend) return;
    useMentionDraftStore.getState().setToken(null);
    useSkillDraftStore.getState().setToken(null);
    const textSnap = value;
    const mentionsSnap = mentions;
    const attachmentsSnap = attachments;

    const trimmed = textSnap.trim();
    const content = serializeMentionChips(trimmed, mentionsSnap);
    const activeIds = completedAttachmentIds(attachmentsSnap);

    // 乐观清空 + 失败回滚（与 MessageComposer 同语义）。
    onChangeText("");
    clearAttachments();
    clearMentions();
    setSubmitting(true);
    Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
    try {
      await onSend(content, activeIds);
      inputRef.current?.blur();
      Keyboard.dismiss();
    } catch {
      onChangeText(textSnap);
      restoreAttachments(attachmentsSnap);
      mentionsSnap.forEach((m) => useMentionDraftStore.getState().toggle(m));
    } finally {
      setSubmitting(false);
    }
  }, [
    canSend,
    value,
    mentions,
    attachments,
    onChangeText,
    clearAttachments,
    clearMentions,
    onSend,
    restoreAttachments,
  ]);

  const handleChangeText = useCallback(
    (next: string) => {
      const skillToken = hasSkillPicker
        ? skillTriggerFromInput(value, next, selectionRef.current.end, slashTypedRef.current)
        : null;
      slashTypedRef.current = false;
      const token = mentionTriggerFromInput(value, next, selectionRef.current.end);
      onChangeText(next);
      if (skillToken && hasSkillPicker && skillPickerPath) {
        useSkillDraftStore.getState().setToken(skillToken);
        router.push(skillPickerPath);
      } else if (token) {
        useMentionDraftStore.getState().setToken(token);
        router.push(mentionPickerPath);
      }
    },
    [value, onChangeText, mentionPickerPath, hasSkillPicker, skillPickerPath],
  );

  const openMentionPicker = useCallback(() => {
    useMentionDraftStore.getState().setToken(null);
    router.push(mentionPickerPath);
  }, [mentionPickerPath]);

  const openSkillPicker = useCallback(() => {
    if (!skillPickerPath) return;
    useSkillDraftStore.getState().setToken(null);
    router.push(skillPickerPath);
  }, [skillPickerPath]);

  const showDictationToast = useCallback(
    (kind: "not_recognized" | "transcribe_failed") => {
      const message =
        kind === "not_recognized"
          ? tVoice("dictation.not_recognized", "No speech detected")
          : tVoice("dictation.transcribe_failed", "Transcription failed, try again");
      if (Platform.OS === "android") {
        ToastAndroid.show(message, ToastAndroid.SHORT);
      } else {
        Alert.alert(message);
      }
    },
    [tVoice],
  );

  const dictation = useDictation({
    onSend: (text) => {
      // 听写终稿以纯文本消息发送；发送失败时文本回填输入框（口述内容
      // 不丢，可编辑重发）。成功后长条保持 rest（连续口述），× 或点
      // 文本域退出。
      Promise.resolve(onSend(text, [])).catch(() => onChangeText(text));
    },
    onToast: showDictationToast,
    // 降级转写通道（RUYI-710）：内存 WAV 上传服务端转写端点。wsId 未
    // 就绪时通道为空，降级入口保持隐藏（resolveDictationEntry）。
    transcribe: wsId
      ? (audio, signal) => api.transcribeVoiceAudio(wsId, audio, { signal })
      : undefined,
  });

  const openDictation = useCallback(() => {
    Keyboard.dismiss();
    setDictationOpen(true);
    dictation.open();
  }, [dictation]);

  const closeDictation = useCallback(() => {
    dictation.close();
    setDictationOpen(false);
  }, [dictation]);

  const voice = voiceGateVerdict(voiceGate);
  const shape = resolveInputShape({
    disabled,
    sending,
    allowStop,
    allowSubmitWhileRunning,
    hasDraft: value.trim().length > 0,
    dictationOpen,
    voice,
  });

  const hasZone = mentions.length > 0 || attachments.length > 0;

  if (disabled) {
    return <DisabledPill reason={disabledReason} />;
  }

  if (shape.dictation) {
    return (
      <View className="px-3 pb-2" style={{ paddingTop: 2 }}>
        <DictationBar
          state={dictation.state}
          rms={dictation.rms}
          onClose={closeDictation}
          onPressStart={dictation.press}
          onPressMove={dictation.move}
          onPressEnd={dictation.release}
          sending={sending}
          onStop={allowStop ? onStop : undefined}
        />
      </View>
    );
  }

  const barStyle: StyleProp<ViewStyle> = {
    borderRadius: CHAT_COMPOSER_MIN_HEIGHT / 2,
    backgroundColor: theme.secondary,
    shadowColor: "#0f172a",
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.07,
    shadowRadius: 14,
    elevation: 3,
  };
  const textStyle: StyleProp<TextStyle> = {
    flex: 1,
    minHeight: CHAT_COMPOSER_INPUT_MIN_HEIGHT,
    maxHeight: CHAT_COMPOSER_INPUT_MAX_HEIGHT,
    paddingVertical: 8,
    fontSize: 16,
    color: theme.foreground,
  };

  return (
    <View
      className="relative px-3 pb-2"
      style={{ paddingTop: 2 }}
      onLayout={handleComposerLayout}
    >
      {hasZone ? (
        <View className="mb-1.5 rounded-2xl bg-secondary/70 px-2 py-1.5">
          <AttachmentZone
            mentions={mentions}
            attachments={attachments}
            onRemoveMention={removeMention}
            onRemoveAttachment={removeAttachment}
            onRetryAttachment={retryAttachment}
          />
        </View>
      ) : null}
      <View className="flex-row items-center gap-1 px-1.5" style={[{ minHeight: CHAT_COMPOSER_MIN_HEIGHT }, barStyle]}>
        <Pressable
          onPress={() => {
            Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
            setPlusOpen(true);
          }}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={t("mobile.plus.open", "Add")}
          className="h-9 w-9 items-center justify-center rounded-full active:opacity-70"
        >
          <Ionicons name="add" size={24} color={theme.mutedForeground} />
        </Pressable>
        <TextInput
          ref={inputRef}
          value={value}
          onChangeText={handleChangeText}
          onSelectionChange={onSelectionChange}
          onContentSizeChange={(e) =>
            setInputHeight(resolveChatComposerInputHeight(e.nativeEvent.contentSize.height))
          }
          editable={!disabled}
          multiline
          scrollEnabled
          placeholder={sending ? t("mobile.composer.agent_working", "Agent is working…") : t("mobile.composer.placeholder", "Message…")}
          placeholderTextColor={theme.mutedForeground}
          style={[textStyle, { height: inputHeight }]}
          accessibilityLabel={t("mobile.composer.placeholder", "Message…")}
        />
        <View className="flex-row items-center gap-1">
          {shape.showStopAction ? (
            <StopAction onStop={onStop} disabled={stopPending} />
          ) : null}
          <RightSlot
            shape={shape.rightSlot}
            canSend={canSend}
            submitting={submitting}
            uploading={uploading}
            onSubmit={handleSubmit}
            dictationReady={
              dictation.entry === "ondevice" || dictation.entry === "degraded"
            }
            disabled={stopPending}
            onOpenDictation={openDictation}
            onOpenVoice={onOpenVoice}
            onStop={onStop}
            stopPending={stopPending}
          />
        </View>
      </View>
      <PlusPanel
        visible={plusOpen}
        maxHeight={plusPanelMaxHeight}
        onClose={() => setPlusOpen(false)}
        onMention={openMentionPicker}
        onCamera={() => {
          setPlusOpen(false);
          void takeAndUploadPhoto();
        }}
        onImage={() => {
          setPlusOpen(false);
          void pickAndUploadImages();
        }}
        onFiles={() => {
          setPlusOpen(false);
          void pickAndUploadFiles();
        }}
        onSkill={hasSkillPicker ? openSkillPicker : undefined}
      />
      <ActionSheetModal {...imageSourceModalProps} />
    </View>
  );
}

function RightSlot({
  shape,
  canSend,
  submitting,
  uploading,
  disabled,
  onSubmit,
  dictationReady,
  onOpenDictation,
  onOpenVoice,
  onStop,
  stopPending,
}: {
  shape: "stop" | "voice" | "send" | "muted-send";
  canSend: boolean;
  submitting: boolean;
  uploading: boolean;
  disabled: boolean;
  onSubmit: () => void;
  dictationReady: boolean;
  onOpenDictation: () => void;
  onOpenVoice: () => void;
  onStop: () => void;
  stopPending: boolean;
}) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const { t } = useT("chat");
  const { t: tVoice } = useT("voice");

  if (shape === "stop") {
    return (
      <Animated.View key="stop" entering={FadeIn.duration(120)} exiting={FadeOut.duration(120)}>
        <StopAction onStop={onStop} disabled={stopPending} />
      </Animated.View>
    );
  }

  if (shape === "voice") {
    return (
      <View className="flex-row items-center gap-0.5">
        {dictationReady ? (
          <Pressable
            onPress={onOpenDictation}
            hitSlop={6}
            accessibilityRole="button"
            disabled={disabled}
            accessibilityLabel={tVoice("dictation.open", "Dictation")}
            className="h-9 w-9 items-center justify-center rounded-full active:opacity-70"
          >
            <Ionicons name="mic-outline" size={22} color={theme.mutedForeground} />
          </Pressable>
        ) : null}
        <Pressable
          onPress={onOpenVoice}
          hitSlop={6}
          accessibilityRole="button"
          disabled={disabled}
          accessibilityLabel={tVoice("button.start", "Start voice conversation")}
          className="h-9 w-9 items-center justify-center rounded-full bg-primary active:opacity-80"
        >
          <Ionicons name="mic" size={18} color={theme.primaryForeground} />
        </Pressable>
      </View>
    );
  }

  const active = shape === "send" && canSend && !submitting && !uploading;
  return (
    <Pressable
      onPress={active ? onSubmit : undefined}
      disabled={!active}
      hitSlop={8}
      accessibilityRole="button"
      accessibilityLabel={t("common:send", "Send")}
      accessibilityState={{ disabled: !active }}
      className={
        active
          ? "h-9 w-9 items-center justify-center rounded-full bg-primary active:opacity-80"
          : "h-9 w-9 items-center justify-center rounded-full"
      }
    >
      <Ionicons
        name="arrow-up"
        size={22}
        color={active ? theme.primaryForeground : theme.mutedForeground}
      />
    </Pressable>
  );
}

function StopAction({ onStop, disabled }: { onStop: () => void; disabled: boolean }) {
  const { t } = useT("chat");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];

  return (
    <Pressable
      onPress={onStop}
      disabled={disabled}
      className="h-9 w-9 items-center justify-center rounded-full bg-foreground active:opacity-80"
      hitSlop={12}
      accessibilityRole="button"
      accessibilityLabel={t("mobile.composer.stop_agent", "Stop agent")}
      accessibilityState={{ disabled }}
    >
      <View
        style={{
          width: 10,
          height: 10,
          backgroundColor: theme.background,
          borderRadius: 1.5,
        }}
      />
    </Pressable>
  );
}

/** 现状灰 pill（disabled 链）——R1 语义不变，仅样式并入新条容器。 */
function DisabledPill({ reason }: { reason?: string }) {
  const { t } = useT("chat");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  return (
    <View className="px-3 pb-2" style={{ paddingTop: 2 }}>
      <View
        className="flex-row items-center gap-2 px-4"
        style={{
          height: 44,
          borderRadius: 22,
          backgroundColor: theme.secondary,
        }}
        accessibilityRole="text"
        accessibilityState={{ disabled: true }}
      >
        <Ionicons name="chatbubble-ellipses-outline" size={18} color={theme.mutedForeground} />
        <Text className="flex-1 text-base" style={{ color: theme.mutedForeground }} numberOfLines={1}>
          {reason ?? t("mobile.composer.unavailable", "Chat unavailable")}
        </Text>
      </View>
    </View>
  );
}
