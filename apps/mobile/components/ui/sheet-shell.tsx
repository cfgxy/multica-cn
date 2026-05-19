/**
 * Reusable iOS pageSheet shell. Wraps RN `<Modal presentationStyle="pageSheet">`
 * with a title + X-button header and a body container that respects the
 * Home Indicator safe area.
 *
 * Use for: any "content view" sheet — long lists, search + list, multi-
 * section filters, form-style modals. See apps/mobile/CLAUDE.md "Lesson
 * #6: Modal container selection" for when this is the right primitive
 * vs. a transparent action sheet vs. Alert.alert.
 *
 * Why a nested SafeAreaProvider inside the Modal: RN's Modal renders to a
 * separate iOS UIPresentationController; the outer SafeAreaProvider
 * context does NOT reliably propagate into Modal-internal trees. The
 * library's documented workaround is to wrap modal contents with a fresh
 * SafeAreaProvider (see AppAndFlow/react-native-safe-area-context). We
 * seed it with `initialWindowMetrics` so `useSafeAreaInsets()` returns
 * non-zero on the first render (no flicker as the sheet slides in).
 *
 * Closing: X button OR iOS drag-down gesture. pageSheet does NOT respond
 * to a "tap exposed top strip" — that strip is the underlying screen,
 * not a backdrop. Always include the X for the discoverable close path.
 *
 * Caveat: pageSheet is iOS-only. On Android RN falls back to a full-screen
 * Modal (no rounded corners, no drag-dismiss). mobile/CLAUDE.md treats iOS
 * as the primary target so this is acceptable for now.
 */
import type { ReactNode } from "react";
import { Modal, View } from "react-native";
import {
  initialWindowMetrics,
  SafeAreaProvider,
  useSafeAreaInsets,
} from "react-native-safe-area-context";
import { Text } from "@/components/ui/text";
import { IconButton } from "@/components/ui/icon-button";

interface Props {
  visible: boolean;
  onClose: () => void;
  /** Header title text — keep it short (e.g. "Filter", "Agent Runs"). */
  title: string;
  /** Body content. Typically a ScrollView / FlatList / SectionList. The
   *  shell already reserves bottom safe-area padding around `children`,
   *  so the body fills the remaining area without colliding with the
   *  Home Indicator. */
  children: ReactNode;
  /** Optional element(s) rendered to the LEFT of the close X button —
   *  e.g. a "Reset" button on a filter sheet. */
  rightAction?: ReactNode;
}

export function SheetShell({
  visible,
  onClose,
  title,
  children,
  rightAction,
}: Props) {
  return (
    <Modal
      visible={visible}
      presentationStyle="pageSheet"
      animationType="slide"
      onRequestClose={onClose}
    >
      <SafeAreaProvider initialMetrics={initialWindowMetrics}>
        <SheetBody title={title} onClose={onClose} rightAction={rightAction}>
          {children}
        </SheetBody>
      </SafeAreaProvider>
    </Modal>
  );
}

function SheetBody({
  title,
  onClose,
  rightAction,
  children,
}: Omit<Props, "visible">) {
  const insets = useSafeAreaInsets();
  return (
    <View className="flex-1 bg-popover">
      <View className="flex-row items-center justify-between px-4 pt-4 pb-3">
        <Text className="text-base font-semibold text-foreground">{title}</Text>
        <View className="flex-row items-center gap-2">
          {rightAction}
          <IconButton
            name="close"
            iconSize={18}
            variant="secondary"
            className="size-7 rounded-full"
            onPress={onClose}
            accessibilityLabel="Close"
          />
        </View>
      </View>
      <View className="flex-1" style={{ paddingBottom: insets.bottom }}>
        {children}
      </View>
    </View>
  );
}
