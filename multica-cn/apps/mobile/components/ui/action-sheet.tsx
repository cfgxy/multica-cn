import { useCallback, useRef, useState } from "react";
import {
  ActionSheetIOS,
  Modal,
  Platform,
  Pressable,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";

export interface ActionSheetOptions {
  options: string[];
  cancelButtonIndex?: number;
  destructiveButtonIndex?: number;
  title?: string;
  onSelect?: (index: number) => void;
}

/**
 * 跨平台 ActionSheet hook。
 * iOS：原生 ActionSheetIOS；Android：Modal 底部弹出列表。
 *
 * 返回的 modalProps 直接传给 <ActionSheetModal>。
 */
export function useActionSheet() {
  const [visible, setVisible] = useState(false);
  const [sheet, setSheet] = useState<ActionSheetOptions | null>(null);
  const onSelectRef = useRef<((index: number) => void) | undefined>(undefined);

  const show = useCallback((props: ActionSheetOptions) => {
    if (Platform.OS === "ios") {
      const { options, cancelButtonIndex, destructiveButtonIndex, title, onSelect } = props;
      ActionSheetIOS.showActionSheetWithOptions(
        { options, cancelButtonIndex, destructiveButtonIndex, title },
        (index: number) => { onSelect?.(index); },
      );
    } else {
      onSelectRef.current = props.onSelect;
      setSheet(props);
      setVisible(true);
    }
  }, []);

  const handleSelect = useCallback((index: number) => {
    setVisible(false);
    const cb = onSelectRef.current;
    setTimeout(() => {
      setSheet(null);
      cb?.(index);
    }, 50);
  }, []);

  return { show, modalProps: { visible, sheet, onSelect: handleSelect } };
}

/**
 * Android 侧的 ActionSheet 底部 Modal。
 * 在 iOS 上渲染为 null（iOS 用原生 ActionSheet，不需要 DOM）。
 */
export function ActionSheetModal({
  visible,
  sheet,
  onSelect,
}: {
  visible: boolean;
  sheet: ActionSheetOptions | null;
  onSelect: (index: number) => void;
}) {
  const insets = useSafeAreaInsets();
  const { colorScheme } = useColorScheme();
  const t = THEME[colorScheme];

  if (Platform.OS === "ios") return null;
  if (!sheet) return null;

  const { options, cancelButtonIndex, destructiveButtonIndex, title } = sheet;
  const cancelIdx = cancelButtonIndex ?? options.length - 1;

  // 按组拆分：cancel 项单独成组（上方有间距）
  const mainItems = options.filter((_, i) => i !== cancelIdx);
  const cancelItem = options[cancelIdx];

  const renderItem = (label: string, index: number) => {
    const isDestructive = index === destructiveButtonIndex;
    return (
      <Pressable
        key={index}
        className={"px-4 py-3.5 active:bg-secondary"}
        onPress={() => onSelect(index)}
      >
        <Text
          className={
            isDestructive
              ? "text-base text-center text-destructive font-semibold"
              : "text-base text-center text-foreground"
          }
        >
          {label}
        </Text>
      </Pressable>
    );
  };

  return (
    <Modal
      visible={visible}
      transparent
      animationType="fade"
      onRequestClose={() => onSelect(cancelIdx)}
    >
      <Pressable
        className="flex-1 bg-black/40"
        onPress={() => onSelect(cancelIdx)}
      />
      <View style={{ backgroundColor: t.background }}>
        {title ? (
          <View className="rounded-t-2xl overflow-hidden">
            <Text className="px-4 py-3 text-xs font-medium text-muted-foreground text-center">
              {title}
            </Text>
          </View>
        ) : null}
        {/* 主选项组 */}
        <View className="rounded-t-2xl overflow-hidden" style={{ backgroundColor: t.background }}>
          {mainItems.map((label, arrIdx) => {
            // mainItems 过滤了 cancel，需要映射回原始 index
            const origIndex = arrIdx < cancelIdx ? arrIdx : arrIdx + 1;
            return (
              <View key={origIndex}>
                {arrIdx > 0 ? (
                  <View className="h-px" style={{ backgroundColor: t.border }} />
                ) : null}
                {renderItem(label, origIndex)}
              </View>
            );
          })}
        </View>
        {/* cancel 组（上方有 8px 间距） */}
        <View className="mt-2 rounded-b-2xl overflow-hidden" style={{ backgroundColor: t.muted }}>
          <Pressable
            className="px-4 py-3.5 active:bg-secondary rounded-b-2xl"
            onPress={() => onSelect(cancelIdx)}
          >
            <Text className="text-base text-center text-primary font-semibold">
              {cancelItem}
            </Text>
          </Pressable>
        </View>
        <View style={{ height: insets.bottom, backgroundColor: t.background }} />
      </View>
    </Modal>
  );
}
