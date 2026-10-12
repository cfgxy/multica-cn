/**
 * 「+」面板（RUYI-694 板2）：把散在工具栏的附件入口收进一个面板。
 *
 *   - 顶部大按钮行：拍照 / 图片（h86，品牌色 10% 底、圆角 18）。
 *   - 网格 4 列：@提及 / 技能指令 / 快捷回复 / 文件（h76，中性底）。
 *     技能指令、快捷回复仅在调用方传入对应入口时渲染——chat 现状
 *     没有这两个数据源（MessageComposer 的 skillPickerPath /
 *     quickReplies 都是 optional），不渲染空入口。
 *
 * 动效：面板 slide-up 260ms、遮罩 fade 200ms rgba(12,15,24,0.44)；
 * 关闭 = 点遮罩 / 下拉把手位移 > 80dp（× 按钮由调用方头部提供）。
 */
import { useCallback, useRef } from "react";
import {
  Modal,
  Pressable,
  View,
  type GestureResponderEvent,
  type StyleProp,
  type ViewStyle,
} from "react-native";
import Animated, {
  FadeIn,
  FadeOut,
  SlideInDown,
  SlideOutDown,
} from "react-native-reanimated";
import { Ionicons } from "@expo/vector-icons";
import * as Haptics from "expo-haptics";
import { Text } from "@/components/ui/text";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

const PANEL_MIN_HEIGHT = 300;

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

  // 下拉把手：位移超过 80dp 视为关闭手势。grant 记起点，release 比对。
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
    <Modal transparent visible={visible} animationType="none" onRequestClose={onClose}>
      <View className="flex-1 justify-end">
        <Animated.View
          entering={FadeIn.duration(200)}
          exiting={FadeOut.duration(200)}
          style={{ ...({ backgroundColor: "rgba(12,15,24,0.44)" } as ViewStyle) }}
          className="absolute inset-0"
        >
          <Pressable className="flex-1" onPress={onClose} accessibilityLabel={t("mobile.plus.close", "Close")} />
        </Animated.View>
        <Animated.View
          entering={SlideInDown.duration(260)}
          exiting={SlideOutDown.duration(260)}
          className="rounded-t-3xl bg-background px-3 pb-6"
          style={{ minHeight: PANEL_MIN_HEIGHT }}
        >
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
        </Animated.View>
      </View>
    </Modal>
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
          ? // 品牌色 10% 底：双主题都用 primary 透明度近似。
            ((theme.primary ?? "#2563eb") + "1A") as string
          : theme.secondary,
      }}
    >
      <Ionicons name={icon} size={22} color={theme.primary} />
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
