/**
 * Bottom emoji picker. RN `Modal` with a centered card showing 8 quick
 * emojis. Mirrors the visual identity of web's
 * `packages/ui/components/common/quick-emoji-picker.tsx:11` — the
 * QUICK_EMOJIS list is duplicated verbatim so the same set of one-tap
 * options shows on both clients.
 *
 * V1 has no "More emojis..." button (web has one that lazy-loads
 * emoji-mart). Mobile users who need a wider set can fall back to the
 * iOS keyboard's emoji input from any text composer; for reactions,
 * the curated 8 cover ~95% of usage in practice.
 */
import { Modal, Pressable, View } from "react-native";
import { Text } from "@/components/ui/text";

export const QUICK_EMOJIS = ["👍", "👌", "❤️", "✅", "🎉", "😕", "🚀", "👀"];

interface Props {
  visible: boolean;
  onSelect: (emoji: string) => void;
  onClose: () => void;
}

export function EmojiPickerSheet({ visible, onSelect, onClose }: Props) {
  return (
    <Modal
      visible={visible}
      transparent
      animationType="fade"
      onRequestClose={onClose}
    >
      <Pressable className="flex-1 bg-black/40" onPress={onClose}>
        <View className="flex-1 items-center justify-center px-8">
          {/* Inner Pressable with onPress noop swallows taps so backdrop
              doesn't dismiss when the user taps inside the card. */}
          <Pressable onPress={() => {}} className="w-full">
            <View className="bg-popover rounded-2xl p-3 flex-row flex-wrap justify-center gap-2">
              {QUICK_EMOJIS.map((emoji) => (
                <Pressable
                  key={emoji}
                  onPress={() => {
                    onSelect(emoji);
                    onClose();
                  }}
                  className="h-12 w-12 items-center justify-center rounded-md active:bg-secondary"
                >
                  <Text className="text-2xl">{emoji}</Text>
                </Pressable>
              ))}
            </View>
          </Pressable>
        </View>
      </Pressable>
    </Modal>
  );
}
