/** Composer attachment and mention actions, rendered above the input row. */
import { useCallback, useEffect, useRef } from "react";
import {
  BackHandler,
  Dimensions,
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
import { withAlphaColor } from "@/lib/color-alpha";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

const PANEL_MAX_HEIGHT = Math.round(Dimensions.get("window").height * 0.42);

interface GridEntry {
  key: string;
  icon: keyof typeof Ionicons.glyphMap;
  label: string;
  onPress: () => void;
}

interface Props {
  visible: boolean;
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
}

export function PlusPanel({
  visible,
  onClose,
  onMention,
  onCamera,
  onImage,
  onFiles,
  onSkill,
  onQuickReplies,
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

  const grid: GridEntry[] = [
    {
      key: "mention",
      icon: "at",
      label: t("mobile.plus.mention", "Mention"),
      onPress: onMention,
    },
    ...(onSkill
      ? [
          {
            key: "skill",
            icon: "flash" as const,
            label: t("mobile.plus.skill", "Skill"),
            onPress: onSkill,
          },
        ]
      : []),
    ...(onQuickReplies
      ? [
          {
            key: "quick",
            icon: "chatbox-ellipses" as const,
            label: t("mobile.plus.quick_replies", "Quick replies"),
            onPress: onQuickReplies,
          },
        ]
      : []),
    {
      key: "files",
      icon: "folder-open",
      label: t("mobile.plus.files", "Files"),
      onPress: onFiles,
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
      className="mb-2 self-start rounded-xl border border-border bg-background px-3 pb-4 shadow-lg"
      style={{ maxHeight: PANEL_MAX_HEIGHT, width: "92%" }}
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
          <View className="flex-row items-center justify-between px-1 pt-2 pb-3">
            <Text className="text-base font-semibold text-foreground">
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
          <View className="flex-row gap-2">
            <BigTile
              icon="camera"
              label={t("mobile.plus.camera", "Camera")}
              onPress={() => {
                haptic();
                onCamera();
              }}
              accent
            />
            <BigTile
              icon="image"
              label={t("mobile.plus.image", "Photos")}
              onPress={() => {
                haptic();
                onImage();
              }}
              accent
            />
          </View>
          <View className="mt-2 flex-row flex-wrap gap-2">
            {grid.map((entry) => (
              <GridTile
                key={entry.key}
                icon={entry.icon}
                label={entry.label}
                onPress={() => {
                  haptic();
                  entry.onPress();
                  onClose();
                }}
              />
            ))}
          </View>
      </ScrollView>
    </Animated.View>
  );
}

function BigTile({
  icon,
  label,
  onPress,
  accent,
}: {
  icon: keyof typeof Ionicons.glyphMap;
  label: string;
  onPress: () => void;
  accent: boolean;
}) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={label}
      className="flex-1 flex-row items-center justify-center gap-2 rounded-[18px] active:opacity-80"
      style={{
        height: 86,
        backgroundColor: accent
          ? // 品牌色 10% 底：THEME token 是 hsl 串，hex alpha 不能直接
            // 拼接（Android 渲染非法色为黑），须经 withAlphaColor 生成
            // 合法 rgba（RUYI-741）。
            withAlphaColor(theme.brand, 0.1)
          : theme.secondary,
      }}
    >
      <Ionicons name={icon} size={22} color={theme.brand} />
      <Text className="text-sm font-medium text-foreground">{label}</Text>
    </Pressable>
  );
}

function GridTile({
  icon,
  label,
  onPress,
}: {
  icon: keyof typeof Ionicons.glyphMap;
  label: string;
  onPress: () => void;
}) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={label}
      className="items-center justify-center gap-1.5 rounded-2xl bg-secondary active:opacity-80"
      style={{ height: 76, width: "23%" }}
    >
      <Ionicons name={icon} size={22} color={theme.foreground} />
      <Text className="text-xs text-muted-foreground" numberOfLines={1}>
        {label}
      </Text>
    </Pressable>
  );
}
