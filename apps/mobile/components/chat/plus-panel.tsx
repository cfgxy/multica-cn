/** Composer attachment and mention actions, rendered above the input row. */
import { useCallback, useEffect, useRef } from "react";
import {
  BackHandler,
  Pressable,
  ScrollView,
  View,
  type GestureResponderEvent,
  type StyleProp,
  type ViewStyle,
} from "react-native";
import Animated, { SlideInUp, SlideOutDown } from "react-native-reanimated";
import { Ionicons } from "@expo/vector-icons";
import * as Haptics from "expo-haptics";
import { Text } from "@/components/ui/text";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";
import type { ReactNode } from "react";

interface GridEntry {
  key: string;
  icon: keyof typeof Ionicons.glyphMap;
  label: string;
  onPress?: () => void;
}

const COMPOSER_HORIZONTAL_INSET = 12;

interface Props {
  visible: boolean;
  maxHeight: number;
  onClose: () => void;
  onMention: () => void;
  /** 拍照（直启相机）。 */
  onCamera: () => void;
  /** 相册选图。 */
  onImage: () => void;
  onFiles: () => void;
  /** 技能指令入口；不传不渲染（chat 无该数据源）。 */
  onSkill?: () => void;
  /** 快捷回复入口；不传不渲染。 */
  onQuickReplies?: () => void;
  onProjectContext?: () => void;
  projectContextLabel?: string;
  modelPicker?: ReactNode;
}

export function PlusPanel({
  visible,
  maxHeight,
  onClose,
  onMention,
  onCamera,
  onImage,
  onFiles,
  onSkill,
  onQuickReplies,
  onProjectContext,
  projectContextLabel,
  modelPicker,
}: Props) {
  const { t } = useT("chat");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const dragStartY = useRef<number | null>(null);

  useEffect(() => {
    if (!visible) return;
    const subscription = BackHandler.addEventListener("hardwareBackPress", () => {
      onClose();
      return true;
    });
    return () => subscription.remove();
  }, [onClose, visible]);

  const haptic = useCallback(() => {
    Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
  }, []);

  // Web hides unavailable contextual actions. Mobile keeps the fixed 4x2 grid
  // and marks unavailable actions disabled so the composer shape never shifts.
  const grid: GridEntry[] = [
    {
      key: "camera",
      icon: "camera",
      label: t("mobile.plus.camera", "Camera"),
      onPress: onCamera,
    },
    {
      key: "image",
      icon: "image",
      label: t("mobile.plus.image", "Photos"),
      onPress: onImage,
    },
    {
      key: "files",
      icon: "folder-open",
      label: t("mobile.plus.files", "Files"),
      onPress: onFiles,
    },
    {
      key: "mention",
      icon: "at",
      label: t("mobile.plus.mention", "Mention"),
      onPress: onMention,
    },
    {
      key: "skill",
      icon: "flash",
      label: t("mobile.plus.skill", "Skill"),
      onPress: onSkill,
    },
    {
      key: "quick",
      icon: "chatbox-ellipses",
      label: t("mobile.plus.quick_replies", "Quick replies"),
      onPress: onQuickReplies,
    },
    {
      key: "project",
      icon: "folder-outline",
      label: projectContextLabel ?? t("input.project_context", "Project context"),
      onPress: onProjectContext,
    },
  ];

  // A downward pull of more than 80dp closes the panel.
  const handleStyle: StyleProp<ViewStyle> = {
    width: 40,
    height: 4,
    borderRadius: 2,
    backgroundColor: theme.border,
    alignSelf: "center",
    marginTop: 8,
  };

  if (!visible) return null;

  return (
    <Animated.View
      entering={SlideInUp.duration(220)}
      exiting={SlideOutDown.duration(180)}
      className="self-start rounded-xl border border-border bg-background px-3 pb-3 shadow-lg"
      testID="plus-panel"
      style={{
        position: "absolute",
        bottom: "100%",
        left: COMPOSER_HORIZONTAL_INSET,
        right: COMPOSER_HORIZONTAL_INSET,
        zIndex: 10,
        maxHeight,
        marginBottom: 8,
      }}
    >
      <ScrollView keyboardShouldPersistTaps="handled" bounces={false}>
          <View
            style={handleStyle}
            onTouchStart={(e: GestureResponderEvent) => {
              dragStartY.current = e.nativeEvent.pageY;
            }}
            onTouchEnd={(e: GestureResponderEvent) => {
              const start = dragStartY.current;
              dragStartY.current = null;
              if (start !== null && e.nativeEvent.pageY - start > 80) onClose();
            }}
          />
          <View className="flex-row items-center justify-between px-1 pb-2">
            <Text className="text-sm font-semibold text-foreground">
              {t("mobile.plus.title", "Add to message")}
            </Text>
            <Pressable
              onPress={onClose}
              hitSlop={12}
              accessibilityRole="button"
              accessibilityLabel={t("mobile.plus.close", "Close")}
              className="h-9 w-9 items-center justify-center rounded-full bg-secondary"
            >
              <Ionicons name="close" size={20} color={theme.mutedForeground} />
            </Pressable>
          </View>
          <View className="flex-row flex-wrap">
            {grid.map((entry) => {
              const onPress = entry.onPress;
              return (
                <GridTile
                  key={entry.key}
                  icon={entry.icon}
                  label={entry.label}
                  disabled={!onPress}
                  onPress={onPress ? () => {
                    haptic();
                    onPress();
                    onClose();
                  } : undefined}
                />
              );
            })}
            <View testID="plus-model-cell" style={{ height: 72, width: "25%" }}>
              {modelPicker ?? (
                <UnavailableModelTile
                  label={t("mobile.plus.model_unavailable", "Model unavailable")}
                />
              )}
            </View>
          </View>
      </ScrollView>
    </Animated.View>
  );
}

function GridTile({
  icon,
  label,
  onPress,
  disabled = false,
}: {
  icon: keyof typeof Ionicons.glyphMap;
  label: string;
  onPress?: () => void;
  disabled?: boolean;
}) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      testID="plus-grid-tile"
      accessibilityRole="button"
      accessibilityLabel={label}
      className="items-center justify-center gap-1.5 active:opacity-80 disabled:opacity-50"
      style={{ height: 72, width: "25%" }}
    >
      <Ionicons name={icon} size={22} color={theme.foreground} />
      <Text className="text-xs text-muted-foreground" numberOfLines={1}>
        {label}
      </Text>
    </Pressable>
  );
}

function UnavailableModelTile({ label }: { label: string }) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  return (
    <Pressable
      disabled
      accessibilityRole="button"
      accessibilityLabel={label}
      className="h-full w-full items-center justify-center gap-1.5 opacity-50"
    >
      <Ionicons name="hardware-chip-outline" size={22} color={theme.mutedForeground} />
      <Text className="text-xs text-muted-foreground" numberOfLines={1}>
        {label}
      </Text>
    </Pressable>
  );
}
