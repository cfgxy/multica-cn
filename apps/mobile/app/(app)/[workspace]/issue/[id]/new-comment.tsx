/**
 * Comment composition modal — full-screen replacement for the old always-on
 * inline composer at the bottom of issue/[id].tsx.
 *
 * Three modes, driven by route params:
 *   - New top-level comment: no extra params
 *   - Reply:                  `parent` + `parentName`
 *   - Edit existing comment:  `edit` + `initial`
 *
 * In edit mode the textarea is pre-seeded with the comment's current
 * content, the header reads "Edit comment", and Send routes to
 * `useEditComment` instead of `useCreateComment`. The picker / mention
 * pipeline is identical — same `useMentionInput`, same toolbar.
 *
 * Why a modal route instead of inline:
 *   - Always-on inline composer competed with the timeline for vertical
 *     space and the keyboard avoiding logic was clunky (user feedback).
 *   - Modal gives the composer dedicated real estate: bigger TextArea,
 *     MentionSuggestionBar can lay out without colliding with toolbar.
 *   - iOS slide-up sheet is the platform-standard "compose" pattern
 *     (Mail, Linear, Slack thread reply / edit).
 *
 * Submit success → router.back() returns to the issue detail screen. The
 * mutation's optimistic patch has already updated the timeline cache
 * before the modal closes, so the new / edited comment is visible
 * immediately without a flash.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import {
  Alert,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  TextInput,
  View,
} from "react-native";
import { Stack, router, useLocalSearchParams } from "expo-router";
import { Image } from "expo-image";
import { Text } from "@/components/ui/text";
import { AutosizeTextArea } from "@/components/ui/autosize-textarea";
import { MentionSuggestionBar } from "@/components/issue/mention-suggestion-bar";
import { useMentionInput } from "@/lib/use-mention-input";
import { useFileAttach } from "@/components/editor/use-file-attach";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useCreateComment, useEditComment } from "@/data/mutations/issues";
import { cn } from "@/lib/utils";

const ICON_SIZE = 18;

export default function NewCommentModal() {
  const { id, parent, parentName, edit, initial } = useLocalSearchParams<{
    id: string;
    parent?: string;
    parentName?: string;
    edit?: string;
    initial?: string;
  }>();
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];

  const mention = useMentionInput();
  const fileAttach = useFileAttach();
  const inputRef = useRef<TextInput>(null);
  const [submitting, setSubmitting] = useState(false);
  const createComment = useCreateComment(id);
  const editComment = useEditComment(id);

  const isEdit = !!edit;
  const isReply = !!parent;
  const title = isEdit ? "Edit comment" : isReply ? "Reply" : "New comment";

  // In edit mode, seed the input with the existing content once on mount.
  // mention.setText is referentially stable (Zustand action shape) — guard
  // with a ref-mounted flag instead of including it in deps so the effect
  // doesn't re-fire and clobber user edits.
  const seededRef = useRef(false);
  useEffect(() => {
    if (!isEdit || seededRef.current) return;
    if (initial) {
      mention.setText(initial);
      seededRef.current = true;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isEdit, initial]);

  const trimmed = mention.text.trim();
  // Same send-gate as the old inline composer: text non-empty, not already
  // mid-submit, no upload in flight (the upload's deferred insertAtCursor
  // would otherwise race a clear and orphan markdown into the next message).
  // In edit mode, also gate on content actually changed.
  const canSend =
    trimmed.length > 0 &&
    !submitting &&
    !fileAttach.uploading &&
    (!isEdit || trimmed !== (initial ?? "").trim());

  const handleSend = useCallback(async () => {
    if (!canSend) return;
    setSubmitting(true);
    const snap = mention.snapshot();
    const content = mention.serialize().trim();
    try {
      if (isEdit && edit) {
        await editComment.mutateAsync({ commentId: edit, content });
      } else {
        await createComment.mutateAsync({ content, parentId: parent });
      }
      router.back();
    } catch (err) {
      // Restore so the user doesn't lose their text. Alert is the simplest
      // reliable surface here.
      mention.restore(snap);
      Alert.alert(
        isEdit ? "Couldn't save" : "Couldn't send",
        err instanceof Error ? err.message : "Try again in a moment.",
      );
      setSubmitting(false);
    }
  }, [canSend, mention, createComment, editComment, parent, isEdit, edit]);

  const handleAttachImage = useCallback(async () => {
    const result = await fileAttach.pickAndUploadImage({ issueId: id });
    if (result) mention.insertAtCursor(`![](${result.url})`);
  }, [fileAttach, mention, id]);

  const handleAttachFile = useCallback(async () => {
    const result = await fileAttach.pickAndUploadFile({ issueId: id });
    if (result) {
      // Mobile preprocess converts `[📎 name](url)` into the file-card visual,
      // round-tripping identically to web.
      mention.insertAtCursor(`[📎 ${result.filename}](${result.url})`);
    }
  }, [fileAttach, mention, id]);

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen
        options={{
          title,
          headerRight: () => (
            <Pressable
              onPress={handleSend}
              disabled={!canSend}
              hitSlop={8}
              className={cn(
                "h-7 px-3 items-center justify-center rounded-full",
                canSend ? "bg-primary active:opacity-80" : "bg-secondary",
              )}
              accessibilityRole="button"
              accessibilityLabel="Send comment"
              accessibilityState={{ disabled: !canSend }}
            >
              <Text
                className={cn(
                  "text-sm font-medium",
                  canSend ? "text-primary-foreground" : "text-muted-foreground",
                )}
              >
                {submitting
                  ? isEdit
                    ? "Saving…"
                    : "Sending…"
                  : isEdit
                    ? "Save"
                    : "Send"}
              </Text>
            </Pressable>
          ),
        }}
      />

      {isReply ? (
        <View className="flex-row items-center gap-2 px-4 py-2 bg-secondary/40 border-b border-border">
          <Text className="text-xs text-muted-foreground">↩</Text>
          <Text
            className="flex-1 text-xs text-muted-foreground"
            numberOfLines={1}
          >
            Replying to{" "}
            <Text className="text-foreground font-medium">{parentName}</Text>
          </Text>
        </View>
      ) : null}

      <KeyboardAvoidingView
        behavior={Platform.OS === "ios" ? "padding" : undefined}
        className="flex-1"
      >
        <View className="flex-1 px-4 pt-3">
          <AutosizeTextArea
            ref={inputRef}
            value={mention.text}
            onChangeText={mention.handlers.onChangeText}
            selection={mention.selection}
            onSelectionChange={mention.handlers.onSelectionChange}
            placeholder={isReply ? "Write a reply…" : "Add a comment…"}
            className="flex-1 text-base"
            editable={!submitting}
            autoFocus
            multiline
          />
        </View>

        <MentionSuggestionBar {...mention.suggestionBar} />

        {/* Toolbar pinned above the keyboard. SF Symbols via expo-image —
         *  tintColor pulled from THEME so light/dark flip automatically. */}
        <View className="flex-row items-center px-4 py-2 gap-1 border-t border-border bg-background">
          <Pressable
            onPress={mention.handlers.onAtButtonPress}
            disabled={submitting || fileAttach.uploading}
            className="h-9 w-9 items-center justify-center rounded-full active:bg-secondary"
            hitSlop={6}
            accessibilityRole="button"
            accessibilityLabel="Mention"
          >
            <Image
              source="sf:at"
              tintColor={theme.mutedForeground}
              style={{ width: ICON_SIZE, height: ICON_SIZE }}
            />
          </Pressable>
          <Pressable
            onPress={handleAttachImage}
            disabled={submitting || fileAttach.uploading}
            className="h-9 w-9 items-center justify-center rounded-full active:bg-secondary"
            hitSlop={6}
            accessibilityRole="button"
            accessibilityLabel="Attach image"
          >
            <Image
              source="sf:photo"
              tintColor={theme.mutedForeground}
              style={{ width: ICON_SIZE, height: ICON_SIZE }}
            />
          </Pressable>
          <Pressable
            onPress={handleAttachFile}
            disabled={submitting || fileAttach.uploading}
            className="h-9 w-9 items-center justify-center rounded-full active:bg-secondary"
            hitSlop={6}
            accessibilityRole="button"
            accessibilityLabel="Attach file"
          >
            <Image
              source="sf:paperclip"
              tintColor={theme.mutedForeground}
              style={{ width: ICON_SIZE, height: ICON_SIZE }}
            />
          </Pressable>
        </View>
      </KeyboardAvoidingView>
    </View>
  );
}
