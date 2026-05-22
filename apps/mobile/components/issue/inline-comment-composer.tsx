/**
 * Inline comment composer with two visual states:
 *
 *   collapsed → a pill button ("Add a comment, @ to mention…"). Minimal
 *               vertical footprint so the timeline gets the full screen
 *               in the common case (user reading, not writing).
 *   expanded  → optional reply chip → thumbnail row (if any pending
 *               attachments) → TextInput → toolbar (📷 image · 📎 file ·
 *               @ mention · ➤ send).
 *
 * Attachments do NOT live inside the markdown content. They're picked into
 * an out-of-band `pendingAttachments` array, shown as thumbnails above the
 * text input, and submitted alongside content via `attachmentIds`. The
 * server-side comment row carries them as a separate `attachments` field;
 * the timeline renders inline `![](url)` if the user authored it that way
 * (web/desktop can — they have a rich editor) and otherwise falls back to
 * `<AttachmentList>` below the body. This mirrors web's contract
 * (packages/views/issues/components/comment-card.tsx:124-159 `AttachmentList`)
 * without requiring mobile to fake inline markdown insertion.
 *
 * Why no inline markdown insertion: RN `<TextInput>` is a pure UITextView,
 * no contentEditable equivalent, no NodeView. The earlier sentinel-based
 * "[文件上传中…]" placeholder + post-upload `text.replace` approach worked
 * but exposed markdown source code in the input AND blocked concurrent
 * uploads (sentinel string was singular). The thumbnail row is what every
 * mobile-native composer does (iMessage / Slack iOS / Lark / WhatsApp /
 * Telegram) and the data contract still parity-matches web.
 *
 * Toolbar order: `[📷] [📎] [@] ──── [➤]`. The `@` button is here despite
 * the iOS UIKit `resignFirstResponder` behavior on sibling-Pressable taps
 * (facebook/react-native#9404) because in a task-management context the
 * discoverability is worth more than the social-app "char trigger only"
 * convention. Focus is preserved via the documented `onPressIn` `.focus()`
 * workaround.
 *
 * State transitions:
 *   - pill tap → expanded + autoFocus (Haptics.Light)
 *   - send success → reset draft + thumbnails + back to pill (Haptics.Light)
 *   - send failure → stay expanded, restore draft + thumbnails + reply
 *     target; optimistic timeline row carries Failed · Retry · Discard
 *   - TextInput blur with empty draft AND no pending attachments → collapse
 *     to pill (deferred to let toolbar-button .focus() re-acquire first
 *     responder before we collapse)
 *   - blur with non-empty draft OR non-empty attachments → stay expanded
 *
 * Layout / safe-area:
 *   The composer self-owns the home-indicator inset via internal
 *   `paddingBottom: insets.bottom`. KSV uses `offset={{ closed: 0, opened:
 *   insets.bottom }}`. This matches the keyboard-controller official
 *   `AwareScrollViewStickyFooter v2` example: closed → no translate, the
 *   inner padding fills the home-indicator area with bg color; opened →
 *   KSV lifts the box to keyboard top, then `+insets.bottom` pushes it
 *   back down so content (above the padding) sits flush on keyboard top,
 *   padding hidden behind keyboard. Earlier `offset.closed: -insets.bottom`
 *   was wrong here — it lifted the rendered pill INTO the timeline's
 *   layout space and covered the last 34pt of messages.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Pressable, TextInput, View } from "react-native";
import { KeyboardStickyView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { Ionicons } from "@expo/vector-icons";
import * as Haptics from "expo-haptics";
import * as ImagePicker from "expo-image-picker";
import * as DocumentPicker from "expo-document-picker";
import { useMentionInput } from "@/lib/use-mention-input";
import { MentionSuggestionBar } from "@/components/issue/mention-suggestion-bar";
import { useCreateComment } from "@/data/mutations/issues";
import { api, MAX_FILE_SIZE } from "@/data/api";
import { useReplyTargetStore } from "@/data/stores/reply-target-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { stripMarkdown } from "@/lib/strip-markdown";
import { THEME } from "@/lib/theme";
import { Text } from "@/components/ui/text";
import {
  ComposerAttachmentRow,
  type ComposerAttachmentItem,
} from "@/components/issue/composer-attachment-row";

/** Per-pick local id. Short random + timestamp — collision-free enough for
 *  a single composing session (max maybe 10 picks). Lives only on the
 *  client side; replaced by server-side `attachment.id` once upload
 *  resolves. */
function makeLocalId(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

export function InlineCommentComposer({ issueId }: { issueId: string }) {
  const mention = useMentionInput();
  const createComment = useCreateComment(issueId);
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const insets = useSafeAreaInsets();
  const inputRef = useRef<TextInput>(null);
  const [expanded, setExpanded] = useState(false);
  // Out-of-band attachment row. Each entry tracks its own upload lifecycle;
  // multiple uploads can run concurrently (unlike the prior sentinel-based
  // text-occupier approach which had to gate `if (uploading) return`).
  const [attachments, setAttachments] = useState<ComposerAttachmentItem[]>([]);

  // Reply target — set by the comment long-press action sheet
  // (`comment-context-menu.tsx`). When non-null the composer auto-expands,
  // shows a "Replying to X" chip above the input, and threads the next
  // submit under `target.commentId` via `useCreateComment`'s `parentId`.
  const replyTarget = useReplyTargetStore((s) => s.target);
  const clearReplyTarget = useReplyTargetStore((s) => s.clear);

  // When a reply target arrives (from long-press), enter expanded mode and
  // focus the input — same as tapping the pill, just bypasses the tap.
  useEffect(() => {
    if (!replyTarget) return;
    setExpanded(true);
    requestAnimationFrame(() => inputRef.current?.focus());
  }, [replyTarget]);

  const hasInFlightUpload = attachments.some((a) => a.status === "uploading");
  const canSend =
    mention.text.trim().length > 0 &&
    !createComment.isPending &&
    !hasInFlightUpload;

  const expand = useCallback(() => {
    Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
    setExpanded(true);
    // Tapping the pill = "I want to write a new root comment". Drop any
    // lingering reply target so a stale chip from a prior long-press →
    // dismiss-without-send cycle doesn't bleed into the fresh draft.
    // Only fires on user-initiated pill tap; the reply-target useEffect
    // path uses setExpanded(true) directly so this doesn't clobber its
    // own target.
    clearReplyTarget();
    // Mount-then-focus: TextInput doesn't exist yet on this tick. One RAF
    // is enough for the native view to be attached.
    requestAnimationFrame(() => inputRef.current?.focus());
  }, [clearReplyTarget]);

  const onSend = useCallback(async () => {
    if (!canSend) return;
    const snap = mention.snapshot();
    const content = mention.serialize().trim();
    // Filter to attachments that finished uploading. Failed / still-uploading
    // items are dropped from this submit; user can retry or remove them and
    // send again. `hasInFlightUpload` in `canSend` already gates against the
    // "still uploading" case so the filter is mostly defensive.
    const activeIds = attachments
      .filter((a) => a.status === "completed")
      .map((a) => a.id)
      .filter((id): id is string => !!id);
    const attachmentsSnap = attachments;
    const replySnap = replyTarget;

    // Optimistic clear: text + thumbnails empty out immediately, the
    // optimistic timeline row carries the new content. Restored from `snap`
    // / `attachmentsSnap` on failure.
    mention.reset();
    setAttachments([]);
    clearReplyTarget();
    Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
    try {
      await createComment.mutateAsync({
        content,
        parentId: replySnap?.commentId,
        attachmentIds: activeIds.length > 0 ? activeIds : undefined,
      });
      // Success → drop back to pill so the timeline reclaims the bottom.
      // Explicitly blur so the keyboard slides down cleanly.
      inputRef.current?.blur();
      setExpanded(false);
    } catch {
      // Failure: restore draft, attachments, AND reply target so the user
      // can retry without re-uploading or losing the thread context. The
      // optimistic row stays in the timeline with its inline
      // Failed · Retry · Discard affordance.
      mention.restore(snap);
      setAttachments(attachmentsSnap);
      if (replySnap) useReplyTargetStore.getState().setTarget(replySnap);
    }
  }, [canSend, mention, createComment, attachments, replyTarget, clearReplyTarget]);

  /** Streams a picked asset to /api/upload-file, updating the matching
   *  thumbnail's status as it goes. Pulled out of the picker handlers so
   *  retry can call it again without re-opening the picker. */
  const startUpload = useCallback(
    async (
      localId: string,
      asset: { uri: string; name: string; type: string },
    ) => {
      try {
        const result = await api.uploadFile(asset, { issueId });
        setAttachments((prev) =>
          prev.map((it) =>
            it.localId === localId
              ? { ...it, status: "completed", id: result.id, url: result.url }
              : it,
          ),
        );
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Unknown error";
        setAttachments((prev) =>
          prev.map((it) =>
            it.localId === localId
              ? { ...it, status: "failed", error: message }
              : it,
          ),
        );
      }
    },
    [issueId],
  );

  const onImagePress = useCallback(async () => {
    const picker = await ImagePicker.launchImageLibraryAsync({
      mediaTypes: ImagePicker.MediaTypeOptions.Images,
      quality: 1,
    });
    if (picker.canceled) return;
    const picked = picker.assets[0];
    if (!picked) return;
    if (picked.fileSize != null && picked.fileSize > MAX_FILE_SIZE) {
      Alert.alert("File too large", "Files must be smaller than 100 MB.");
      return;
    }
    const filename = picked.fileName ?? `image-${Date.now()}.jpg`;
    const mimeType = picked.mimeType ?? "image/jpeg";
    const localId = makeLocalId();
    setAttachments((prev) => [
      ...prev,
      {
        localId,
        localUri: picked.uri,
        filename,
        mimeType,
        status: "uploading",
      },
    ]);
    // Picker dismissal blurred the input — refocus so the user can keep
    // typing while the upload flies.
    requestAnimationFrame(() => inputRef.current?.focus());
    await startUpload(localId, {
      uri: picked.uri,
      name: filename,
      type: mimeType,
    });
  }, [startUpload]);

  const onFilePress = useCallback(async () => {
    const picker = await DocumentPicker.getDocumentAsync({
      type: "*/*",
      copyToCacheDirectory: true,
    });
    if (picker.canceled) return;
    const picked = picker.assets[0];
    if (!picked) return;
    if (picked.size != null && picked.size > MAX_FILE_SIZE) {
      Alert.alert("File too large", "Files must be smaller than 100 MB.");
      return;
    }
    const mimeType = picked.mimeType ?? "application/octet-stream";
    const localId = makeLocalId();
    setAttachments((prev) => [
      ...prev,
      {
        localId,
        localUri: picked.uri,
        filename: picked.name,
        mimeType,
        status: "uploading",
      },
    ]);
    requestAnimationFrame(() => inputRef.current?.focus());
    await startUpload(localId, {
      uri: picked.uri,
      name: picked.name,
      type: mimeType,
    });
  }, [startUpload]);

  const onRemoveAttachment = useCallback((localId: string) => {
    // The user dropped a thumbnail. The backend `/upload-file` already
    // accepted the bytes; the orphaned attachment row sits issue-scoped on
    // the server until the next sweep (web has the same behavior — see
    // packages/views/issues/components/comment-input.tsx:75-78 activeIds
    // filter). No client-side delete call: cheap to skip, server cleans up.
    setAttachments((prev) => prev.filter((it) => it.localId !== localId));
  }, []);

  const onRetryAttachment = useCallback(
    (localId: string) => {
      const item = attachments.find((it) => it.localId === localId);
      if (!item) return;
      setAttachments((prev) =>
        prev.map((it) =>
          it.localId === localId
            ? { ...it, status: "uploading", error: undefined }
            : it,
        ),
      );
      void startUpload(localId, {
        uri: item.localUri,
        name: item.filename,
        type: item.mimeType,
      });
    },
    [attachments, startUpload],
  );

  const onBlur = useCallback(() => {
    // Defer: when the user taps a toolbar button, iOS resigns the
    // TextInput's first responder before the Pressable's onPress fires.
    // The `@` button's onPressIn re-acquires focus, but blur fires anyway.
    // Wait a tick, then collapse only if focus is truly gone AND there's
    // nothing worth keeping the composer open for (no text, no attachments,
    // no reply target).
    setTimeout(() => {
      const empty =
        mention.text.trim().length === 0 && attachments.length === 0;
      if (empty && !inputRef.current?.isFocused()) {
        setExpanded(false);
        // Dismissed without sending → treat as cancelling the reply
        // intent. Otherwise the chip would re-appear next time the user
        // taps the pill, surprising them.
        clearReplyTarget();
      }
    }, 50);
  }, [mention.text, attachments.length, clearReplyTarget]);

  const onAtPress = useCallback(() => {
    // `onPressIn` already re-focused the input in the same gesture; this
    // runs in onPress (touchUp) and mutates text + selection through the
    // mention hook. Net result: keyboard stays up, `@` appears at the
    // caret, suggestion bar opens.
    mention.handlers.onAtButtonPress();
  }, [mention.handlers]);

  // ---------- Collapsed: pill ----------
  if (!expanded) {
    return (
      <KeyboardStickyView
        offset={{ closed: 0, opened: insets.bottom }}
      >
        <View
          className="border-t border-border bg-background px-3 pt-2"
          style={{ paddingBottom: insets.bottom + 8 }}
        >
          <Pressable
            onPress={expand}
            accessibilityRole="button"
            accessibilityLabel="Add a comment"
            className="flex-row items-center gap-2 h-11 px-4 rounded-full bg-secondary active:opacity-80"
          >
            <Ionicons
              name="chatbubble-ellipses-outline"
              size={18}
              color={theme.mutedForeground}
            />
            <Text className="text-base text-muted-foreground">
              Add a comment, @ to mention…
            </Text>
          </Pressable>
        </View>
      </KeyboardStickyView>
    );
  }

  // ---------- Expanded: input + toolbar ----------
  return (
    <KeyboardStickyView offset={{ closed: 0, opened: insets.bottom }}>
      {/* Suggestion bar renders null when no active `@<query>` — costs
       *  zero space in the common case. */}
      <MentionSuggestionBar {...mention.suggestionBar} />

      <View
        className="border-t border-border bg-background px-3 pt-2 gap-2"
        style={{ paddingBottom: insets.bottom }}
      >
        {replyTarget && (
          <View className="px-3 py-1.5 rounded-md bg-secondary/60 gap-0.5">
            <View className="flex-row items-center gap-2">
              <Ionicons
                name="return-up-back"
                size={14}
                color={theme.mutedForeground}
              />
              <Text
                className="flex-1 text-xs font-medium text-muted-foreground"
                numberOfLines={1}
              >
                Replying to {replyTarget.actorName}
              </Text>
              <Pressable
                onPress={clearReplyTarget}
                hitSlop={8}
                accessibilityRole="button"
                accessibilityLabel="Cancel reply"
              >
                <Ionicons
                  name="close-circle"
                  size={16}
                  color={theme.mutedForeground}
                />
              </Pressable>
            </View>
            {replyTarget.preview ? (
              <Text
                className="text-xs text-muted-foreground pl-5"
                numberOfLines={2}
              >
                {stripMarkdown(replyTarget.preview)}
              </Text>
            ) : null}
          </View>
        )}

        <ComposerAttachmentRow
          items={attachments}
          onRemove={onRemoveAttachment}
          onRetry={onRetryAttachment}
        />

        <TextInput
          ref={inputRef}
          value={mention.text}
          onChangeText={mention.handlers.onChangeText}
          selection={mention.selection}
          onSelectionChange={mention.handlers.onSelectionChange}
          onBlur={onBlur}
          placeholder="Add a comment…"
          placeholderTextColor={theme.mutedForeground}
          multiline
          className="px-3 py-2 rounded-2xl bg-secondary text-base text-foreground"
          style={{ minHeight: 44, maxHeight: 140, textAlignVertical: "top" }}
        />

        <View className="flex-row items-center">
          <Pressable
            onPress={onImagePress}
            hitSlop={8}
            className="h-9 w-9 items-center justify-center"
            accessibilityRole="button"
            accessibilityLabel="Upload image"
          >
            <Ionicons
              name="image-outline"
              size={22}
              color={theme.mutedForeground}
            />
          </Pressable>
          <Pressable
            onPress={onFilePress}
            hitSlop={8}
            className="h-9 w-9 items-center justify-center"
            accessibilityRole="button"
            accessibilityLabel="Upload file"
          >
            <Ionicons
              name="attach-outline"
              size={22}
              color={theme.mutedForeground}
            />
          </Pressable>
          <Pressable
            onPressIn={() => inputRef.current?.focus()}
            onPress={onAtPress}
            hitSlop={8}
            className="h-9 w-9 items-center justify-center"
            accessibilityRole="button"
            accessibilityLabel="Insert mention"
          >
            <Ionicons name="at" size={22} color={theme.mutedForeground} />
          </Pressable>
          <View className="flex-1" />
          <Pressable
            onPress={onSend}
            disabled={!canSend}
            hitSlop={8}
            className="h-9 w-9 items-center justify-center"
            accessibilityRole="button"
            accessibilityLabel="Send comment"
            accessibilityState={{ disabled: !canSend }}
          >
            <Ionicons
              name="arrow-up-circle"
              size={32}
              color={canSend ? theme.primary : theme.mutedForeground}
            />
          </Pressable>
        </View>
      </View>
    </KeyboardStickyView>
  );
}
