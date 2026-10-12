/**
 * Shared form primitives for the agent editor formSheets (RUYI-624) —
 * Field / RadioDot / Chip moved verbatim out of edit-profile.tsx so the
 * split editors (edit-profile / run-config) render identical controls and
 * can't drift apart.
 */
import { Pressable, View } from "react-native";
import { Text } from "@/components/ui/text";

export function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <View className="gap-1.5">
      <Text className="text-xs uppercase tracking-wider text-muted-foreground">
        {label}
      </Text>
      {children}
    </View>
  );
}

export function RadioDot({ selected }: { selected: boolean }) {
  return (
    <View
      className={`size-4 rounded-full border items-center justify-center ${
        selected ? "border-brand" : "border-muted-foreground"
      }`}
    >
      {selected ? <View className="size-2 rounded-full bg-brand" /> : null}
    </View>
  );
}

export function Chip({
  label,
  selected,
  onPress,
}: {
  label: string;
  selected: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      className={`rounded-full border px-3 py-1.5 active:opacity-70 ${
        selected
          ? "border-brand bg-brand/10"
          : "border-border bg-transparent"
      }`}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
    >
      <Text
        className={`text-xs ${selected ? "text-brand font-medium" : "text-muted-foreground"}`}
      >
        {label}
      </Text>
    </Pressable>
  );
}

/** Section header + save action shared by the SHEET_OPTIONS editors
 *  (body-drawn header per the _layout comment on SHEET_OPTIONS). */
export function SheetHeader({
  title,
  saveLabel,
  saveDisabled,
  onSave,
}: {
  title: string;
  saveLabel: string;
  saveDisabled: boolean;
  onSave: () => void;
}) {
  return (
    <View className="flex-row items-center px-4 pb-2 border-b border-border">
      <Text className="flex-1 text-lg font-semibold text-foreground">
        {title}
      </Text>
      <Pressable
        onPress={onSave}
        disabled={saveDisabled}
        className={`px-2 py-1 ${saveDisabled ? "opacity-40" : ""}`}
        accessibilityRole="button"
        accessibilityLabel={saveLabel}
      >
        <Text className="text-base text-brand font-semibold">{saveLabel}</Text>
      </Pressable>
    </View>
  );
}
