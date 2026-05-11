/**
 * Cancel button rendered in the modal Stack header. iOS pattern: a text
 * "Cancel" affordance on the leading edge that dismisses the modal.
 *
 * Used by `[workspace]/_layout.tsx` for the new-issue and search modals.
 */
import { Pressable } from "react-native";
import { router } from "expo-router";
import { Text } from "@/components/ui/text";

export function ModalCloseButton() {
  return (
    <Pressable
      onPress={() => router.back()}
      hitSlop={8}
      className="active:opacity-60"
    >
      <Text className="text-base text-brand">Cancel</Text>
    </Pressable>
  );
}
