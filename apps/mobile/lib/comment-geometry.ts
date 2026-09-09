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
  private rootId: string;
  private readonly raw = new Map<string, { y: number; height: number }>();

  constructor(
    rootId: string,
    private readonly onReport: (commentId: string, rect: RowLocalRect) => void,
    private readonly onForget: (commentId: string) => void,
  ) {
    this.rootId = rootId;
  }

  /**
   * 把上报器绑到当前渲染的 root 行。
   *
   * FlashList v2 用的是 **view recycling**：行滚出渲染窗口时组件实例并不卸载，
   * 而是带着新 item 复用（`RenderStackManager.sync` 把回收的 key 分配给新的
   * stableId）。上报器存在卡片的 ref 里，构造时定死的 `rootId` 因此会跟着同
   * 一个实例活过多次身份切换：不重绑，新 root 的回复会登记到旧 root 名下，
   * `resolve` 拿旧行的行顶去换算，目标滚进错误线程；旧 root 的测量值也没人
   * 清，行不在渲染窗口时 `getLayout` 返回 null，定位只能空转到重试耗尽。
   *
   * 身份真的变了才动手：`release` 掉旧行全部测量并作废基准（新行的气泡偏移
   * 必须重新量）。身份未变时是空操作——同一行的普通重渲染远多于回收，不能
   * 顺手把仍然有效的几何删掉。
   */
  rebindRoot(rootId: string): void {
    if (rootId === this.rootId) return;
    this.release();
    // 基准属于旧行的气泡，新行会派发自己的 onLayout；留着它会让新 root 的
    // 第一批回复按旧行的气泡高度偏移。（`release` 不重置基准是刻意的——同一
    // 行折叠再展开时气泡位置没变，见「release 之后重新测量仍然工作」。）
    this.bubbleOffset = 0;
    this.rootId = rootId;
  }

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
