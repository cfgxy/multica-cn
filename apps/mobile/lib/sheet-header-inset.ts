/**
 * RUYI-698 — formSheet 自绘标题头的条件化状态栏避让。
 *
 * SHEET_OPTIONS_BASE 双档位 `[0.6, 0.95]` 下，标题头只有停在最大档（0.95，
 * 顶边距屏幕顶部约 5%）时才与系统状态栏重叠，避让 padding 只在该档位有意义；
 * 0.6 小档顶边在屏幕高度约 40% 处，`insets.top` 的避让留白是纯空白（Owner
 * 截图实证，RUYI-698）。Android 全屏 modal（modalStackedSheetOptions 派生，
 * RUYI-623）与 Android-only modal 路由（runs/[taskId]）顶边贴状态栏，恒等价
 * 于「最大档」——调用点以 `initialAtLargeDetent` 打开即表达该形态，这类路由
 * 没有 detent 事件，初始值就是恒定值。
 */

/** 标题头内容间距（原各调用点 `insets.top + N` 表达式里的 N）。 */
export type SheetHeaderGap = number;

/**
 * 标题头顶部 padding：大档/全屏形态保留 `insetsTop` 避让，小档回落为常规
 * 内容间距 `gap`。
 *
 * "worklet" 指令必须保留：本函数在 `components/ui/sheet-header.tsx` 的
 * `useAnimatedStyle`（UI 线程 worklet）内被调用，无指令的普通函数进入
 * worklet 闭包后 release 包无法解析为可调用体，UI 线程抛 TypeError 直接
 * 杀进程（RUYI-698 P0 根因）。指令不影响 JS 线程调用——单测按普通纯函数
 * 覆盖同一实现。
 */
export function sheetHeaderTopInset(
  insetsTop: number,
  atLargeDetent: boolean,
  gap: SheetHeaderGap,
): number {
  "worklet";
  return (atLargeDetent ? insetsTop : 0) + gap;
}
