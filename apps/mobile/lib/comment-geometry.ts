/**
 * 行内评论几何登记表（RUYI-108）。
 *
 * 手机端一个 root 及其全部回复共用同一个 FlashList 行，列表只知道行的位置。
 * 要把「被引用的那条回复」滚进视口，就得知道它在行内的纵向偏移——这只有渲染
 * 它的 `CommentCard` 通过 `onLayout` 才量得到。这里是那份测量结果的登记表：
 * 卡片写入，定位控制器读取。
 *
 * 只存偏移量，不存 React 节点：FlashList 会回收行，登记表必须能在目标行卸载
 * 后安全地报「量不到」（`null`），由控制器按既有的有界重试处理，而不是拿着
 * 一个已卸载节点的旧坐标去滚。
 */
import type { TargetRect } from "./comment-target-visibility";

/** 一条评论在其所属行内的纵向位置。 */
interface RowLocalRect {
  /** 所属 root 行的 id（行查找用）。 */
  rootId: string;
  /** 相对所属行顶边的偏移。 */
  offsetInRow: number;
  height: number;
}

export class CommentGeometryRegistry {
  private readonly rects = new Map<string, RowLocalRect>();

  /** 卡片测得（或重新测得）一条评论的行内位置。 */
  report(commentId: string, rect: RowLocalRect): void {
    this.rects.set(commentId, rect);
  }

  /** 行被回收/卸载时丢弃其测量值，避免拿旧坐标滚动。 */
  forget(commentId: string): void {
    this.rects.delete(commentId);
  }

  /**
   * 把行内偏移换算成内容坐标系里的矩形。`rowTopOf` 由调用方提供（列表的
   * `getLayout`），返回 null 表示该行当前没有布局。
   */
  resolve(
    commentId: string,
    rowTopOf: (rootId: string) => number | null,
  ): TargetRect | null {
    const rect = this.rects.get(commentId);
    if (!rect) return null;
    const rowTop = rowTopOf(rect.rootId);
    if (rowTop == null) return null;
    return { top: rowTop + rect.offsetInRow, height: rect.height };
  }
}
