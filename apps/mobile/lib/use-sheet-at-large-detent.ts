import { useEffect, useState } from "react";
import { useNavigation } from "@react-navigation/native";
import type { NativeStackNavigationProp } from "@react-navigation/native-stack";
import type { ParamListBase } from "@react-navigation/native";

/**
 * RUYI-698 — 当前 formSheet 是否停在最大档（`sheetAllowedDetents` 末位）。
 *
 * 消费 native-stack 的 `sheetDetentChange` navigation 事件（screens 4.23 起
 * 双端原生派发：iOS `UISheetPresentationController` detent delegate、Android
 * `BottomSheetBehavior` 状态回调，payload `{ index, stable }`）。忽略非稳定
 * 事件（Android 拖拽/settling 途中 index 仍是上一个稳定档），只在停靠后
 * 切换避让。
 *
 * `initialAtLargeDetent` 是打开瞬间的形态，也是无事件路由的恒定值：
 *   - 默认 0.6 档打开的 SHEET_OPTIONS 路由传 false（默认值）；
 *   - `sheetInitialDetentIndex: "last"` 的 LONG_CONTENT 路由传 true；
 *   - Android 全屏 modal 路由（modalStackedSheetOptions 派生与 Android-only
 *     modal）传 true——没有 detent 概念就不会有事件，初始值恒定。
 */
type SheetNavProp = NativeStackNavigationProp<ParamListBase>;

export function useSheetAtLargeDetent(initialAtLargeDetent: boolean): boolean {
  const navigation = useNavigation<SheetNavProp>();
  const [atLargeDetent, setAtLargeDetent] = useState(initialAtLargeDetent);

  useEffect(() => {
    // Optional call: component-test navigation stubs routinely omit
    // addListener; a stub environment then just stays on the initial
    // detent — the same constant-inset semantics as the no-event modal
    // routes. Real NavigationProp always carries addListener.
    return navigation.addListener?.("sheetDetentChange", (event) => {
      if (!event.data.stable) return;
      setAtLargeDetent(event.data.index > 0);
    });
  }, [navigation]);

  return atLargeDetent;
}
