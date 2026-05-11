/**
 * Bottom-sticky comment input. Supports `@mention` via two paths:
 *
 *   1. Inline `@` typing — keys `@`, suggestion bar appears with candidates
 *      filtered by the trailing query. Standard iOS pattern.
 *   2. Left-side `@` button — for users who don't know inline `@` works.
 *      (Will be subsumed by the shared MarkdownToolbar in a follow-up step;
 *      kept here for now so behaviour is unchanged during the hook refactor.)
 *
 * State + handlers come from `useMentionInput` so this composer and the
 * new-issue Description share a single source of truth for the mention
 * pipeline (sentinel-marker text, suggestion bar wiring, serialisation).
 *
 * Visual notes:
 *   - Send button hidden while empty (Linear / Slack iOS idiom).
 *   - TextInput shows a subtle border-tinted bg on focus.
 *   - `@` button is intentionally compact and visually subordinate.
 *
 * RN limitation: text inside `<TextInput>` can't be color-styled inline. The
 * mention text shows plain grey while editing; after send the comment
 * renders as a coloured chip in the timeline via mention-chip.tsx.
 */
import { useState } from "react";
import { Pressable, TextInput, View } from "react-native";
import Svg, { Path } from "react-native-svg";
import { Text } from "@/components/ui/text";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { MarkdownToolbar } from "@/components/editor/markdown-toolbar";
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
   *  the input row and submits with `parentId` set to this comment id. */
  replyingTo?: { commentId: string; name: string } | null;
  onCancelReply?: () => void;
}

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

  const handleAttachImage = async () => {
    const result = await fileAttach.pickAndUploadImage({ issueId });
    if (result) mention.insertAtCursor(`![](${result.url})`);
  };

  const handleAttachFile = async () => {
    const result = await fileAttach.pickAndUploadFile({ issueId });
    if (result) {
      // Mobile preprocess already converts `[📎 name](url)` to the file-card
      // visual, so insert it directly — round-trips identically to web.
      mention.insertAtCursor(`[📎 ${result.filename}](${result.url})`);
    }
  };

  const trimmed = mention.text.trim();
  const canSend = trimmed.length > 0 && !submitting;

  async function handleSend() {
    if (!canSend) return;
    setSubmitting(true);
    const snap = mention.snapshot();
    const content = mention.serialize().trim();
    mention.reset();
    try {
      await onSubmit({ content, parentId: replyingTo?.commentId });
    } catch {
      // Restore the snapshot so the user doesn't lose what they typed.
      mention.restore(snap);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <View className="border-t border-border bg-background">
      <MentionSuggestionBar {...mention.suggestionBar} />
      {replyingTo ? (
        <View className="flex-row items-center gap-2 px-4 py-2 border-b border-border bg-secondary/40">
          <Text className="text-xs text-muted-foreground">↩</Text>
          <Text
            className="flex-1 text-xs text-muted-foreground"
            numberOfLines={1}
          >
            Replying to{" "}
            <Text className="text-foreground font-medium">
              {replyingTo.name}
            </Text>
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
      ) : null}
      <MarkdownToolbar
        onAt={mention.handlers.onAtButtonPress}
        onList={() => mention.insertAtLineStart("- ")}
        onCheckbox={() => mention.insertAtLineStart("- [ ] ")}
        onCode={() => mention.insertAtCursor("\n```\n\n```", 4)}
        onQuote={() => mention.insertAtLineStart("> ")}
        onImage={handleAttachImage}
        onFile={handleAttachFile}
        disabled={submitting || fileAttach.uploading}
      />
      <View className="px-3 py-2 flex-row items-end gap-1.5">
        <View
          className={cn(
            "flex-1 rounded-2xl border",
            focused
              ? "border-primary/30 bg-secondary"
              : "border-transparent bg-secondary",
          )}
        >
          <TextInput
            value={mention.text}
            onChangeText={mention.handlers.onChangeText}
            selection={mention.selection}
            onSelectionChange={mention.handlers.onSelectionChange}
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
            placeholder="Add a comment…"
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            multiline
            className="px-4 py-2 text-base text-foreground max-h-32 min-h-8"
            editable={!submitting}
          />
        </View>
        {canSend ? (
          <Pressable
            onPress={handleSend}
            className="h-8 w-8 rounded-full items-center justify-center bg-primary active:opacity-80"
            hitSlop={8}
            accessibilityLabel="Send"
          >
            <SendArrow />
          </Pressable>
        ) : null}
      </View>
    </View>
  );
}

/** Up-arrow glyph used for the Send button. Inline SVG so we don't pull
 *  lucide-react-native into the bundle for a single icon. Geometry mirrors
 *  iOS messaging apps' Send affordance. */
function SendArrow() {
  return (
    <Svg width={16} height={16} viewBox="0 0 16 16" fill="none">
      <Path
        d="M8 13V3M8 3l-4 4M8 3l4 4"
        stroke="#fff"
        strokeWidth={1.8}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </Svg>
  );
}
