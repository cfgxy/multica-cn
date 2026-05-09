/**
 * Per-comment action sheet, opened by long-pressing a comment body.
 *
 * iOS-native pattern (iMessage / Slack / Telegram / Linear iOS): a
 * bottom-anchored card with a horizontal quick-reaction emoji row at the
 * top, followed by a short list of actions below.
 *
 * Modal + backdrop pattern reuses the same shape as
 * `apps/mobile/components/issue/emoji-picker-sheet.tsx` and the picker
 * sheets — no new dep.
 *
 * Action set is intentionally small in v1: react / reply / copy. Edit and
 * delete need permission checks + new mutations and are deferred to a
 * separate cut.
 */
import { Modal, Pressable, View } from "react-native";
import { Text } from "@/components/ui/text";
import { QUICK_EMOJIS } from "./emoji-picker-sheet";

interface Props {
  visible: boolean;
  onClose: () => void;
  /** Tap any quick emoji → toggle that reaction on the comment. */
  onReact: (emoji: string) => void;
  /** Tap "Reply" → set the issue page's replyingTo state, focus composer. */
  onReply: () => void;
  /** Tap "Copy" → write the markdown body to the system clipboard. */
  onCopy: () => void;
}

export function CommentActionSheet({
  visible,
  onClose,
  onReact,
  onReply,
  onCopy,
}: Props) {
  return (
    <Modal
      visible={visible}
      transparent
      animationType="fade"
      onRequestClose={onClose}
    >
      <Pressable className="flex-1 bg-black/40 justify-end" onPress={onClose}>
        {/* Inner Pressable swallows taps so backdrop tap dismisses but card
            taps don't. The card is anchored to the bottom (justify-end on
            the outer Pressable) — iOS convention for action sheets. */}
        <Pressable onPress={() => {}}>
          <View className="bg-popover rounded-t-3xl p-4 gap-3 pb-8">
            {/* Quick emoji row */}
            <View className="flex-row justify-between">
              {QUICK_EMOJIS.map((emoji) => (
                <Pressable
                  key={emoji}
                  onPress={() => {
                    onReact(emoji);
                    onClose();
                  }}
                  className="h-11 w-11 items-center justify-center rounded-full active:bg-secondary"
                >
                  <Text className="text-2xl">{emoji}</Text>
                </Pressable>
              ))}
            </View>

            <View className="h-px bg-border" />

            {/* Actions */}
            <ActionRow
              icon="↩"
              label="Reply"
              onPress={() => {
                onReply();
                onClose();
              }}
            />
            <ActionRow
              icon="⎘"
              label="Copy text"
              onPress={() => {
                onCopy();
                onClose();
              }}
            />
          </View>
        </Pressable>
      </Pressable>
    </Modal>
  );
}

function ActionRow({
  icon,
  label,
  onPress,
}: {
  icon: string;
  label: string;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      className="flex-row items-center gap-3 px-2 py-3 rounded-lg active:bg-secondary"
    >
      <Text className="text-base text-muted-foreground w-5 text-center">
        {icon}
      </Text>
      <Text className="text-base text-foreground">{label}</Text>
    </Pressable>
  );
}
