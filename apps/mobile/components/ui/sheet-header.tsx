import { useEffect, type ReactNode } from "react";
import Animated, {
  useAnimatedStyle,
  useSharedValue,
  withTiming,
} from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import {
  sheetHeaderTopInset,
  type SheetHeaderGap,
} from "@/lib/sheet-header-inset";
import { useSheetAtLargeDetent } from "@/lib/use-sheet-at-large-detent";

/**
 * formSheet 自绘标题头容器（SHEET_OPTIONS `headerShown: false` 的配对件）。
 *
 * RUYI-698：状态栏避让只在 sheet 停在最大档（0.95）时存在——0.6 小档顶边
 * 远离状态栏，避让留白是纯空白。detent 切换时 padding 以 180ms 过渡，与
 * 原生档位切换动画并行，避免标题行瞬跳。无事件路由（Android 全屏 modal、
 * fitToContents）停在 `initialAtLargeDetent` 初始值上，行为恒定。
 *
 * 替代旧模式 `<View className={X} style={{ paddingTop: insets.top + N }}>`
 * （RUYI-563 引入的无条件避让）；`className` 与 `children` 原样透传，仅
 * paddingTop 由本组件管理。
 */
interface SheetHeaderProps {
  className?: string;
  /** 标题头内容间距（旧表达式 `insets.top + N` 的 N）。 */
  gap?: SheetHeaderGap;
  /**
   * 打开瞬间 sheet 顶边是否贴屏幕顶部（LONG_CONTENT 初始 0.95 档、Android
   * 全屏 modal 路由传 true；见 useSheetAtLargeDetent）。
   */
  initialAtLargeDetent?: boolean;
  children?: ReactNode;
}

const INSET_TRANSITION_MS = 180;

export function SheetHeader({
  className,
  gap = 12,
  initialAtLargeDetent = false,
  children,
}: SheetHeaderProps) {
  const insets = useSafeAreaInsets();
  const atLargeDetent = useSheetAtLargeDetent(initialAtLargeDetent);
  const statusBarInset = useSharedValue(
    initialAtLargeDetent ? insets.top : 0,
  );

  useEffect(() => {
    statusBarInset.value = withTiming(atLargeDetent ? insets.top : 0, {
      duration: INSET_TRANSITION_MS,
    });
  }, [atLargeDetent, insets.top, statusBarInset]);

  const headerStyle = useAnimatedStyle(() => ({
    paddingTop: sheetHeaderTopInset(statusBarInset.value, atLargeDetent, gap),
  }));

  return (
    <Animated.View style={headerStyle} className={className}>
      {children}
    </Animated.View>
  );
}
