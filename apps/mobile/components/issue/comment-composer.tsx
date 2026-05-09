/**
 * Bottom-sticky comment input. Supports `@mention` insertion via two paths:
 *
 *   1. Inline `@` typing — user keys `@`, the suggestion bar appears with
 *      candidates filtered by the trailing query. Standard iOS-app pattern
 *      (Slack / Linear / Discord).
 *
 *   2. `@` button on the left — for users who don't know inline `@` works.
 *      Inserts a literal `@` at the cursor, which triggers path 1.
 *
 * Storage uses a zero-width sentinel (U+2063) before each picker-inserted
 * `@` plus an ordered `markers` list. On send, `serializeMentions`
 * walks the text and zips sentinel runs with markers to produce
 * `[@<name>](mention://<type>/<id>)` markdown — the canonical format
 * recognised by `server/internal/util/mention.go`'s ParseMentions and the
 * mobile `mention-chip.tsx` renderer.
 *
 * Visual notes:
 *   - Send button is hidden while the input is empty (Linear / Slack iOS
 *     idiom — appears only when there's something to send).
 *   - TextInput shows a subtle border-tinted bg on focus so the user
 *     gets feedback the field is active.
 *   - `@` button is intentionally compact (32×32) and visually subordinate
 *     to the TextInput, never competing for attention.
 *
 * RN limitation: text inside <TextInput> can't be color-styled inline. The
 * mention text shows as plain grey while editing. After send, the comment
 * renders as a coloured chip in the timeline via mention-chip.tsx.
 */
import { useCallback, useState } from "react";
import {
  Pressable,
  TextInput,
  View,
  type NativeSyntheticEvent,
  type TextInputSelectionChangeEventData,
} from "react-native";
import Svg, { Path } from "react-native-svg";
import { Text } from "@/components/ui/text";
import { cn } from "@/lib/utils";
import {
  insertMention,
  serializeMentions,
  tokenAtCursor,
  type MentionMarker,
} from "@/lib/mention-serialize";
import { MentionSuggestionBar } from "./mention-suggestion-bar";

interface Props {
  onSubmit: (vars: {
    content: string;
    parentId?: string;
  }) => Promise<unknown> | void;
  /** When set, the composer renders a "Replying to <name>" chip above
   *  the input row and submits with `parentId` set to this comment id. */
  replyingTo?: { commentId: string; name: string } | null;
  onCancelReply?: () => void;
}

interface MentioningState {
  start: number;
  query: string;
}

export function CommentComposer({
  onSubmit,
  replyingTo,
  onCancelReply,
}: Props) {
  const [value, setValue] = useState("");
  const [markers, setMarkers] = useState<MentionMarker[]>([]);
  const [selection, setSelection] = useState<{ start: number; end: number }>({
    start: 0,
    end: 0,
  });
  const [mentioning, setMentioning] = useState<MentioningState | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [focused, setFocused] = useState(false);

  const trimmed = value.trim();
  const canSend = trimmed.length > 0 && !submitting;

  const recomputeMentioning = useCallback(
    (text: string, cursor: number) => {
      const token = tokenAtCursor(text, cursor);
      if (token) {
        setMentioning({ start: token.start, query: token.query });
      } else if (mentioning) {
        setMentioning(null);
      }
    },
    [mentioning],
  );

  const handleChangeText = useCallback(
    (next: string) => {
      setValue(next);
      // Approximate the cursor as end of new text — onSelectionChange will
      // correct on the next event. This is enough for inline @ detection.
      recomputeMentioning(next, selection.end);
    },
    [recomputeMentioning, selection.end],
  );

  const handleSelectionChange = useCallback(
    (e: NativeSyntheticEvent<TextInputSelectionChangeEventData>) => {
      const sel = e.nativeEvent.selection;
      setSelection(sel);
      recomputeMentioning(value, sel.end);
    },
    [recomputeMentioning, value],
  );

  const handleAtButton = useCallback(() => {
    const before = value.slice(0, selection.start);
    const after = value.slice(selection.end);
    // Auto-pad with a leading space if previous char isn't whitespace —
    // mention tokens require a word boundary before `@`.
    const needsPad = before.length > 0 && !/\s$/.test(before);
    const inserted = (needsPad ? " " : "") + "@";
    const next = before + inserted + after;
    const cursor = before.length + inserted.length;
    setValue(next);
    setSelection({ start: cursor, end: cursor });
    recomputeMentioning(next, cursor);
  }, [value, selection, recomputeMentioning]);

  const handleSelectMention = useCallback(
    (mention: MentionMarker) => {
      if (!mentioning) return;
      const { newText, newSelection, marker } = insertMention(
        value,
        { start: mentioning.start, queryLength: mentioning.query.length },
        mention,
      );
      setValue(newText);
      setSelection(newSelection);
      setMarkers((prev) => [...prev, marker]);
      setMentioning(null);
    },
    [mentioning, value],
  );

  async function handleSend() {
    if (!canSend) return;
    setSubmitting(true);
    const snapshot = { value, markers, selection };
    const content = serializeMentions(value, markers).trim();
    setValue("");
    setMarkers([]);
    setSelection({ start: 0, end: 0 });
    setMentioning(null);
    try {
      await onSubmit({ content, parentId: replyingTo?.commentId });
    } catch {
      setValue(snapshot.value);
      setMarkers(snapshot.markers);
      setSelection(snapshot.selection);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <View className="border-t border-border bg-background">
      <MentionSuggestionBar
        visible={mentioning !== null}
        query={mentioning?.query ?? ""}
        onSelect={handleSelectMention}
      />
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
      <View className="px-3 py-2 flex-row items-end gap-1.5">
        <Pressable
          onPress={handleAtButton}
          disabled={submitting}
          className="h-8 w-8 rounded-full items-center justify-center active:bg-secondary"
          hitSlop={8}
          accessibilityLabel="Mention someone"
        >
          <Text className="text-base text-muted-foreground">@</Text>
        </Pressable>
        <View
          className={cn(
            "flex-1 rounded-2xl border",
            focused ? "border-primary/30 bg-secondary" : "border-transparent bg-secondary",
          )}
        >
          <TextInput
            value={value}
            onChangeText={handleChangeText}
            selection={selection}
            onSelectionChange={handleSelectionChange}
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
            placeholder="Add a comment…"
            placeholderTextColor="#a1a1aa"
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
