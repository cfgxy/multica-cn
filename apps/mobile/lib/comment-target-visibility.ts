/**
 * 「目标评论是否真的在屏幕上」的几何判定（RUYI-108）。
 *
 * 手机端一个 root 及其全部回复渲染在**同一个 FlashList 行**里。行级
 * viewability 只能回答「这一行有没有露出来」，长线程里 root 行露头时被引用
 * 的那条回复完全可能还在屏幕下方几百像素处——这正是本轮 Review 抓到的失效
 * 路径。所以引用回复时必须换成行内几何判定：拿目标回复在内容坐标系里的
 * top/height，和当前滚动视口比。
 *
 * 判定刻意只看**目标的顶边**，不要求整条回复完整入屏：一条长回复可以比视口
 * 还高，要求「完整可见」会让它永远判不成功，进而在超时后退化成失败态。顶边
 * 落在视口内即意味着用户能看到这条回复的开头和它的高亮边框，与 Web 侧
 * `scrollIntoView({ block: "start" })` 的落点语义一致。
 */

/** 目标在内容坐标系里的位置。 */
export interface TargetRect {
  /** 内容坐标系里的顶边（已含所属行的行首偏移）。 */
  top: number;
  height: number;
}

/** 当前滚动视口。 */
export interface Viewport {
  offsetY: number;
  height: number;
}

/** 落点距视口顶部的留白，与 root 行 `scrollToIndex` 的 viewOffset 一致，
 *  避免目标顶边贴着悬浮头部。 */
export const TARGET_VIEW_OFFSET_PX = 8;

/** 顶边判定的容差：滚动落点是估算值，差几个像素不该判成「没到位」。 */
export const TARGET_VISIBLE_EPSILON_PX = 4;

/** 顶边之下至少要露出这么多内容才算「看得见」，否则目标只是贴着视口下沿
 *  露出一条缝，用户实际什么都读不到。 */
export const TARGET_MIN_VISIBLE_PX = 24;

/**
 * 目标顶边是否已经在视口内、且下方露出了足以阅读的一段。
 */
export function isTargetVisible(rect: TargetRect, viewport: Viewport): boolean {
  if (viewport.height <= 0) return false;
  const vpTop = viewport.offsetY;
  const vpBottom = vpTop + viewport.height;
  if (rect.top < vpTop - TARGET_VISIBLE_EPSILON_PX) return false;
  // 目标比视口还矮时，"露出 24px" 应放宽为「露出它自己的全部高度」，
  // 否则一条两行的短回复永远判不成功。
  const needed = Math.min(TARGET_MIN_VISIBLE_PX, Math.max(rect.height, 1));
  return rect.top + needed <= vpBottom + TARGET_VISIBLE_EPSILON_PX;
}

/**
 * 把目标顶边放到视口靠上位置所需的滚动偏移。返回值已夹到非负，
 * 顶部附近的目标不会要求列表滚到负偏移。
 */
export function targetScrollOffset(rect: TargetRect): number {
  return Math.max(0, rect.top - TARGET_VIEW_OFFSET_PX);
}
