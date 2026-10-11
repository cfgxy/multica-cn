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
 */
export function sheetHeaderTopInset(
  insetsTop: number,
  atLargeDetent: boolean,
  gap: SheetHeaderGap,
): number {
  return (atLargeDetent ? insetsTop : 0) + gap;
}
