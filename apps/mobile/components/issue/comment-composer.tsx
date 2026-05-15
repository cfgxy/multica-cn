/**
 * Issue-comment composer — visually aligned with `chat-composer.tsx`.
 *
 * Always-on form: input + action row (@ · 📷 · 📎 · Send) rendered immediately.
 * No tap-to-expand pill — tapping the input opens the keyboard directly.
 *
 * RN limitation: text inside `<TextInput>` can't be color-styled inline. The
 * mention text shows plain grey while editing; after send the comment
 * renders as a coloured chip in the timeline via mention-chip.tsx.
 */
import { useRef, useState } from "react";
import { Pressable, TextInput, View } from "react-native";
import { Image } from "expo-image";
import { Text } from "@/components/ui/text";
import { AutosizeTextArea } from "@/components/ui/autosize-textarea";
import { useFileAttach } from "@/components/editor/use-file-attach";
import { cn } from "@/lib/utils";
import { useMentionInput } from "@/lib/use-mention-input";
import { MentionSuggestionBar } from "./mention-suggestion-bar";

interface Props {
  /** Owning issue id — attached to uploads so the backend knows where this
   *  file belongs. Required because comments always live under an issue. */
  issueId: string;
  onSubmit: (vars: {
    content: string;
    parentId?: string;
  }) => Promise<unknown> | void;
  /** When set, the composer renders a "Replying to <name>" chip above
   *  the pill/card and submits with `parentId` set to this comment id. */
  replyingTo?: { commentId: string; name: string } | null;
  onCancelReply?: () => void;
}

const ICON_COLOR = "#71717a"; // muted-foreground
const ICON_SIZE = 18;

export function CommentComposer({
  issueId,
  onSubmit,
  replyingTo,
  onCancelReply,
}: Props) {
  const mention = useMentionInput();
  const fileAttach = useFileAttach();
  const [submitting, setSubmitting] = useState(false);
  const [focused, setFocused] = useState(false);
  const inputRef = useRef<TextInput>(null);

  const handleAttachImage = async () => {
    const result = await fileAttach.pickAndUploadImage({ issueId });
    if (result) mention.insertAtCursor(`![](${result.url})`);
  };

  const handleAttachFile = async () => {
    const result = await fileAttach.pickAndUploadFile({ issueId });
    if (result) {
      // Mobile preprocess converts `[📎 name](url)` to the file-card visual,
      // round-tripping identically to web.
      mention.insertAtCursor(`[📎 ${result.filename}](${result.url})`);
    }
  };

  const trimmed = mention.text.trim();
  // Gate on `!fileAttach.uploading` to prevent the upload's deferred
  // `insertAtCursor` from racing with a send that already cleared the
  // input (would orphan the inserted markdown into the next message).
  const canSend = trimmed.length > 0 && !submitting && !fileAttach.uploading;

  async function handleSend() {
    if (!canSend) return;
    setSubmitting(true);
    const snap = mention.snapshot();
    const content = mention.serialize().trim();
    mention.reset();
    try {
      await onSubmit({ content, parentId: replyingTo?.commentId });
    } catch {
      // Restore snapshot so the user doesn't lose what they typed.
      mention.restore(snap);
    } finally {
      setSubmitting(false);
    }
  }

  const Chip = replyingTo ? (
    <View className="flex-row items-center gap-2 rounded-2xl bg-secondary/40 mx-3 mb-1.5 px-3 py-2">
      <Text className="text-xs text-muted-foreground">↩</Text>
      <Text
        className="flex-1 text-xs text-muted-foreground"
        numberOfLines={1}
      >
        Replying to{" "}
        <Text className="text-foreground font-medium">{replyingTo.name}</Text>
      </Text>
      <Pressable
        onPress={onCancelReply}
        hitSlop={8}
        className="h-6 w-6 items-center justify-center rounded-full active:bg-secondary"
        accessibilityLabel="Cancel reply"
      >
        <Text className="text-base text-muted-foreground">✕</Text>
      </Pressable>
    </View>
  ) : null;

  return (
    <View>
      <MentionSuggestionBar {...mention.suggestionBar} />
      {Chip}
      <View className="px-3 pt-3 pb-2">
        <View
          className={cn(
            "rounded-3xl border bg-secondary",
            focused ? "border-primary/30" : "border-border",
          )}
          style={{ borderCurve: "continuous" }}
        >
          <AutosizeTextArea
            ref={inputRef}
            value={mention.text}
            onChangeText={mention.handlers.onChangeText}
            selection={mention.selection}
            onSelectionChange={mention.handlers.onSelectionChange}
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
            placeholder="Add a comment…"
            className="px-4 pt-3 pb-1"
            editable={!submitting}
          />
          <View className="flex-row items-center px-2 pb-2 pt-1">
            <Pressable
              onPress={mention.handlers.onAtButtonPress}
              disabled={submitting || fileAttach.uploading}
              className="h-8 w-8 items-center justify-center rounded-full active:opacity-60"
              hitSlop={6}
              accessibilityRole="button"
              accessibilityLabel="Mention"
            >
              <Image
                source="sf:at"
                tintColor={ICON_COLOR}
                style={{ width: ICON_SIZE, height: ICON_SIZE }}
              />
            </Pressable>
            <Pressable
              onPress={handleAttachImage}
              disabled={submitting || fileAttach.uploading}
              className="h-8 w-8 items-center justify-center rounded-full active:opacity-60"
              hitSlop={6}
              accessibilityRole="button"
              accessibilityLabel="Attach image"
            >
              <Image
                source="sf:photo"
                tintColor={ICON_COLOR}
                style={{ width: ICON_SIZE, height: ICON_SIZE }}
              />
            </Pressable>
            <Pressable
              onPress={handleAttachFile}
              disabled={submitting || fileAttach.uploading}
              className="h-8 w-8 items-center justify-center rounded-full active:opacity-60"
              hitSlop={6}
              accessibilityRole="button"
              accessibilityLabel="Attach file"
            >
              <Image
                source="sf:paperclip"
                tintColor={ICON_COLOR}
                style={{ width: ICON_SIZE, height: ICON_SIZE }}
              />
            </Pressable>
            <View className="flex-1" />
            <Pressable
              onPress={handleSend}
              disabled={!canSend}
              className={cn(
                "h-8 w-8 items-center justify-center rounded-full",
                canSend ? "bg-primary active:opacity-80" : "bg-background",
              )}
              hitSlop={8}
              accessibilityRole="button"
              accessibilityLabel="Send"
              accessibilityState={{ disabled: !canSend }}
            >
              <Image
                source="sf:arrow.up"
                tintColor={canSend ? "#ffffff" : "#a1a1aa"}
                style={{ width: 16, height: 16 }}
              />
            </Pressable>
          </View>
        </View>
      </View>
    </View>
  );
}
