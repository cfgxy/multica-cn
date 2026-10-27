/**
 * Truncated preview for long prompt fields (RUYI-541) — squad instructions
 * on the squad detail page, agent instructions on the agent overview.
 *
 * Detail pages keep only this preview; full viewing and editing live in a
 * dedicated window the caller opens via `onTap`. `canEdit` controls the
 * pencil glyph independently of `onTap`: a reader may open a read-only view
 * (squad) without ever seeing an edit affordance — the squad permission rule
 * is hide, not disable (RUYI-346).
 */
import { Pressable, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { Text } from "@/components/ui/text";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";

interface InstructionsPreviewProps {
  text: string;
  emptyHint?: string;
  numberOfLines?: number;
  onTap?: () => void;
  canEdit?: boolean;
  accessibilityLabel?: string;
}

const DEFAULT_LINES = 6;

export function InstructionsPreview({
  text,
  emptyHint,
  numberOfLines = DEFAULT_LINES,
  onTap,
  canEdit = false,
  accessibilityLabel,
}: InstructionsPreviewProps) {
  const { colorScheme } = useColorScheme();
  const body = text ? (
    <Text
      numberOfLines={numberOfLines}
      className="flex-1 text-sm leading-5 text-foreground"
    >
      {text}
    </Text>
  ) : emptyHint ? (
    <Text
      numberOfLines={numberOfLines}
      className="flex-1 text-sm italic text-muted-foreground/60"
    >
      {emptyHint}
    </Text>
  ) : null;

  if (!onTap || !body) {
    return body ?? null;
  }

  return (
    <View className="flex-row">
      <Pressable
        onPress={onTap}
        className="flex-1 flex-row items-start gap-1.5 active:opacity-70"
        accessibilityRole="button"
        accessibilityLabel={accessibilityLabel}
      >
        {body}
        {canEdit ? (
          <Ionicons
            name="pencil"
            size={14}
            color={THEME[colorScheme].mutedForeground}
          />
        ) : null}
      </Pressable>
    </View>
  );
}
