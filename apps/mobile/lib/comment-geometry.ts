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
 *
 * 坐标系（第三轮返工的关键）：FlashList 的 `getLayout(i).y` 是 **item 区**
 * 坐标——不含 `ListHeaderComponent` 与顶部内边距；而 `onScroll` 的
 * `contentOffset.y`、`scrollToOffset`（默认 `skipFirstItemOffset: true`）走的
 * 是**原生**滚动坐标。两者相差恰好一个 `getFirstItemOffset()`
 * （见 `useRecyclerViewController.tsx`：`getFinalOffset()` 末尾
 * `+ recyclerViewManager.firstItemOffset`）。Issue 头部在真机上是标题、描述、
 * 反应行叠起来的几百像素，少算这一段，二次滚动会永远停在目标上方并把有界
 * 重试烧光。`resolve` 因此要求调用方一并给出 header 偏移，并在拿不到时
 * （列表未挂载）返回 null，而不是当成「没有头部」按 0 算。
 */
import type { TargetRect } from "./comment-target-visibility";

/** 一条评论在其所属行内的纵向位置。 */
export interface RowLocalRect {
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
   * 把行内偏移换算成**原生滚动坐标系**里的矩形。
   *
   * `rowTopOf` 由调用方提供（列表的 `getLayout`，item 区坐标），返回 null
   * 表示该行当前没有布局。`firstItemOffset` 是 `getFirstItemOffset()` 的
   * 结果——item 区与原生滚动坐标之间的位移；列表未挂载时传 null，此时不做
   * 任何猜测，直接报量不到。
   */
  resolve(
    commentId: string,
    rowTopOf: (rootId: string) => number | null,
    firstItemOffset: number | null,
  ): TargetRect | null {
    if (firstItemOffset == null) return null;
    const rect = this.rects.get(commentId);
    if (!rect) return null;
    const rowTop = rowTopOf(rect.rootId);
    if (rowTop == null) return null;
    return {
      top: rowTop + rect.offsetInRow + firstItemOffset,
      height: rect.height,
    };
  }
}

/**
 * 一个 FlashList 行（一个 root + 它的全部回复）的几何上报器。
 *
 * 从 `CommentCard` 的 effect 里抽出来，因为清理语义是这一轮 Review 的阻断点
 * 之一：原实现在 `replyIds` 变化时用旧 effect 的 cleanup 删掉**旧集合的全部**
 * 测量值，而新 effect 并不重新上报。线程新增一条回复后，仍然挂载的老回复不
 * 保证再触发 `onLayout`（RN 只在布局真的变化时派发），它们的坐标就此永久
 * 消失，定位控制器只能空转到有界重试耗尽后判失败。
 *
 * 正确语义是差集：`syncReplies` 只丢弃**真正从集合里消失**的回复，仍在的一律
 * 保留并按当前基准重新登记。整行卸载或折叠走 `release`。
 *
 * 另一件本类负责的事：`onLayout` 给的 `y` 只相对**直接父节点**（回复 wrapper
 * 的父节点是气泡 View），而控制器要的是相对行顶。所以气泡自身的偏移单独量一
 * 次做基准，回复偏移 = 基准 + 自身 y。两者的 layout 事件顺序不确定（RN 里子
 * 节点通常先于父节点），因此原始测量值留在本类里，任一侧到齐都重算一次全部
 * 登记，而不是假设基准先到。
 */
export class RowGeometryReporter {
  private bubbleOffset = 0;
  private readonly raw = new Map<string, { y: number; height: number }>();

  constructor(
    private readonly rootId: string,
    private readonly onReport: (commentId: string, rect: RowLocalRect) => void,
    private readonly onForget: (commentId: string) => void,
  ) {}

  /** 气泡（回复 wrapper 的父节点）相对行顶的偏移。 */
  setBubbleOffset(y: number): void {
    this.bubbleOffset = y;
    this.flush();
  }

  /** 一条回复相对气泡的原始测量值。 */
  measureReply(replyId: string, layout: { y: number; height: number }): void {
    this.raw.set(replyId, { y: layout.y, height: layout.height });
    this.flush();
  }

  /**
   * 当前行实际渲染的回复集合。只丢弃差集里消失的那些，其余重新登记——新增
   * 回复不得连累已挂载回复的坐标。
   */
  syncReplies(replyIds: readonly string[]): void {
    const alive = new Set(replyIds);
    for (const id of [...this.raw.keys()]) {
      if (alive.has(id)) continue;
      this.raw.delete(id);
      this.onForget(id);
    }
    this.flush();
  }

  /** 整行折叠或被 FlashList 回收：丢弃全部测量值。 */
  release(): void {
    for (const id of this.raw.keys()) this.onForget(id);
    this.raw.clear();
  }

  private flush(): void {
    for (const [replyId, raw] of this.raw) {
      this.onReport(replyId, {
        rootId: this.rootId,
        offsetInRow: this.bubbleOffset + raw.y,
        height: raw.height,
      });
    }
  }
}
