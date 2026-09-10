/**
 * Bounded comment-locate state machine for the comments-directory focus
 * intent (RUYI-28).
 *
 * Extracted from timeline-list.tsx as a framework-free controller so the
 * failure paths the review demanded are unit-testable in the plain Node
 * vitest lane (apps/mobile/vitest.config.ts intentionally ships no RN
 * component renderer). The component injects every side effect — timers,
 * layout probes, the scroll call, and the viewability set — through
 * `CommentLocateAdapter`; the controller owns ONLY sequencing and
 * state consistency.
 *
 * Why a controller instead of the previous `useEffect` + `setTimeout(64)`:
 * the effect cleanup cleared the pending locate whenever the effect
 * re-ran (dep churn), while the "stamp consumed" ref kept the effect from
 * re-arming — the intent was marked consumed and the locate silently never
 * happened. Here a run survives effect re-runs; `start()` is idempotent
 * per (issueId, nonce) and only a NEW nonce (the store's per-tap counter)
 * or an explicit `cancel()` can interrupt an in-flight run. Every async
 * edge (timer fire, scroll promise settlement, viewability callback)
 * re-checks run identity, so stale callbacks are no-ops.
 *
 * Lifecycle of one run:
 *
 *   pending ──(expand settle delay)──▶ probe ──layout ready──▶ scrolling
 *                                        │                       │
 *                                        │ absent ×N             │ scrollToIndex
 *                                        ▼                       │ resolves
 *                                      failed ◀──reject──── scrolling   ▼
 *                                        ▲                  awaitingView
 *                                        │                       │
 *                                        └──timeout──┬──viewability confirmed
 *                                                    ▼
 *                                                 located
 *
 * `located` REQUIRES a viewability confirmation (the scroll promise
 * resolving does not prove the row is on screen — FlashList's scroll is
 * estimate-based and can land short). The confirmation is either the
 * post-scroll check against the last viewability snapshot (covers a
 * target that was already visible, where no new viewability event fires)
 * or a later `confirmViewable()` call from the list's viewability
 * callback.
 *
 * RUYI-108 行内校正（`refining` 阶段）：引用的目标可能是**回复**，而一个
 * root 及其全部回复共用同一个 FlashList 行。行可见 ≠ 目标可见——长线程里
 * root 行露头时被引用的回复可能还在屏幕外。所以目标不是 root 自身时，行级
 * viewability 只作为「行已挂载并落位」的前置，随后必须按目标在内容坐标系里
 * 的实际位置判定并按需二次滚动，直到目标本身入屏才算 `located`。校正是
 * 有界的（`LOCATE_MAX_REFINE_ATTEMPTS`），耗尽仍不可见按 `layout` 失败走既有
 * 降级路径，不新增失败语义、不发任何请求。
 */
import {
  isTargetVisible,
  targetScrollOffset,
  type TargetRect,
  type Viewport,
} from "./comment-target-visibility";
import type { TimelineRow } from "./timeline-thread";

/** Grace before the first layout probe: expanding the root changes its
 *  row height, so probing in the same tick would read the pre-expansion
 *  layout. Same 64ms the previous implementation waited. */
export const LOCATE_EXPAND_SETTLE_MS = 64;

/** Backoff between layout-probe retries (row not laid out yet). */
export const LOCATE_LAYOUT_RETRY_MS = 150;

/** Total probe attempts before the run is declared failed. */
export const LOCATE_MAX_LAYOUT_ATTEMPTS = 4;

/** Hard ceiling for the whole run — settle + probes + animated scroll +
 *  viewability dispatch. Anything slower is a failure the user can retry,
 *  not an invisible hang. */
export const LOCATE_TIMEOUT_MS = 4000;

/** 行内校正每次二次滚动后的沉降等待：动画滚动落位、目标行内的 markdown
 *  异步排版都需要一帧以上才稳定，紧接着量会读到旧几何。 */
export const LOCATE_REFINE_SETTLE_MS = 120;

/** 行内校正的次数上限。一次滚动通常就够（按实测位置直接落到目标顶边），留出
 *  这么多次是因为两种慢路径都要在这个预算里走完：折叠线程刚展开时回复还没
 *  测到尺寸（每次只等、不滚），以及行内图片/表格在滚动过程中才排完版而位移。
 *  预算 5 × 120ms = 600ms，仍远小于整体 4s 超时。 */
export const LOCATE_MAX_REFINE_ATTEMPTS = 5;

export type LocateFailureReason =
  | "not-found"
  | "layout"
  | "scroll"
  | "timeout";

export interface LocateRequest {
  issueId: string;
  rootId: string;
  /** 真正要看到的那条评论。等于 `rootId` 时是 root 引用，只需行级
   *  viewability；不等时是回复引用，必须额外做行内校正才算到位。缺省
   *  （评论目录 / 深链等只按 root 定位的旧调用方）等同于 `rootId`。 */
  targetId?: string;
  /** Store-minted per-tap counter. A retry of the same root carries a NEW
   *  nonce — the controller never reuses a consumed nonce. */
  nonce: number;
}

export interface LocateResult extends LocateRequest {
  status: "located" | "failed";
  reason?: LocateFailureReason;
}

export interface LocateTimerHandle {
  cancel(): void;
}

/** All side effects the controller needs, injected by the timeline. */
export interface CommentLocateAdapter {
  /** Index of the target root's row in the list data, or -1. */
  findIndex(rootId: string): number;
  /** FlashList `getLayout` — falsy when the row has no layout yet. */
  getLayout(index: number): unknown;
  /** FlashList `scrollToIndex`. May reject (estimate-based). */
  scrollToIndex(index: number): Promise<void>;
  /** Whether the row id is in the list's last viewability snapshot. */
  isViewable(rootId: string): boolean;
  /** 目标评论在内容坐标系里的矩形（回复引用的行内校正用）。尚未测得时返回
   *  null——目标所在行刚展开、行内 markdown 还没排完版时就是这种状态。 */
  measureTarget(targetId: string): TargetRect | null;
  /** 当前滚动视口（offset + 高度）。 */
  getViewport(): Viewport;
  /** 直接滚到内容坐标系的某个偏移（行内校正用；FlashList 的
   *  `scrollToOffset` 是同步无返回的）。 */
  scrollToOffset(offset: number): void;
  /** One-shot cancellable timer (component injects setTimeout). */
  schedule(fn: () => void, ms: number): LocateTimerHandle;
}

type Phase =
  | "settling"
  | "probing"
  | "scrolling"
  | "awaitingView"
  | "refining"
  | "done";

interface Run {
  req: LocateRequest;
  phase: Phase;
  timers: Set<LocateTimerHandle>;
}

export class CommentLocateController {
  private run: Run | null = null;
  /** Last (issueId:nonce) handed to `start` — makes repeated `start`
   *  calls for the SAME intent no-ops regardless of run state. */
  private lastKey: string | null = null;

  constructor(
    private readonly adapter: CommentLocateAdapter,
    private readonly onDone: (result: LocateResult) => void,
  ) {}

  /**
   * Begin (or re-begin) a locate run. Idempotent per intent: re-running
   * the consuming effect with an unchanged store intent must not restart
   * the scroll. A new nonce always wins and cancels any in-flight run.
   */
  start(req: LocateRequest): void {
    const key = `${req.issueId}:${req.nonce}`;
    if (this.lastKey === key) return;
    this.lastKey = key;

    this.cancelRun();
    const run: Run = { req, phase: "settling", timers: new Set() };
    this.run = run;
    this.schedule(run, () => this.probe(run, 0), LOCATE_EXPAND_SETTLE_MS);
    this.schedule(run, () => this.finish(run, "failed", "timeout"), LOCATE_TIMEOUT_MS);
  }

  /**
   * Viewability edge (list's onViewableItemsChanged). Locates only when a
   * run is actively waiting for on-screen confirmation of ITS target.
   *
   * root 行可见只是**前置**：目标是回复时还要交给行内校正，因为回复与 root
   * 共用同一行。目标就是 root 自身时行可见即到位。
   */
  confirmViewable(): void {
    const run = this.run;
    if (!run || run.phase === "done") return;
    if (run.phase !== "scrolling" && run.phase !== "awaitingView") return;
    if (!this.adapter.isViewable(run.req.rootId)) return;
    const targetId = run.req.targetId ?? run.req.rootId;
    if (targetId === run.req.rootId) {
      this.finish(run, "located");
      return;
    }
    this.refine(run, 0);
  }

  /**
   * 行内校正：按目标回复的实测位置判定是否真的入屏，未入屏就按其顶边二次
   * 滚动，沉降后重量。有界重试耗尽仍不可见判 `layout` 失败——与「行没排好
   * 版」同源，走同一条既有降级。
   */
  private refine(run: Run, attempt: number): void {
    run.phase = "refining";
    const targetId = run.req.targetId ?? run.req.rootId;
    const rect = this.adapter.measureTarget(targetId);
    if (rect && isTargetVisible(rect, this.adapter.getViewport())) {
      this.finish(run, "located");
      return;
    }
    if (attempt + 1 >= LOCATE_MAX_REFINE_ATTEMPTS) {
      this.finish(run, "failed", "layout");
      return;
    }
    // 量不到就纯等下一轮（行内还在排版，此时滚动等于按旧几何乱跳）。
    if (rect) this.adapter.scrollToOffset(targetScrollOffset(rect));
    this.schedule(
      run,
      () => this.refine(run, attempt + 1),
      LOCATE_REFINE_SETTLE_MS,
    );
  }

  /** Drop the in-flight run WITHOUT emitting a result (unmount). */
  cancel(): void {
    this.cancelRun();
    this.run = null;
  }

  private cancelRun(): void {
    const run = this.run;
    if (!run) return;
    for (const t of run.timers) t.cancel();
    run.timers.clear();
    run.phase = "done";
  }

  /** Schedule `fn` on this run; firing after the run ended is a no-op. */
  private schedule(run: Run, fn: () => void, ms: number): void {
    const handle = this.adapter.schedule(() => {
      run.timers.delete(handle);
      if (this.run !== run || run.phase === "done") return;
      fn();
    }, ms);
    run.timers.add(handle);
  }

  private probe(run: Run, attempt: number): void {
    run.phase = "probing";
    const idx = this.adapter.findIndex(run.req.rootId);
    if (idx >= 0 && this.adapter.getLayout(idx)) {
      run.phase = "scrolling";
      this.adapter.scrollToIndex(idx).then(
        () => {
          if (this.run !== run || run.phase === "done") return;
          // Scroll settled — still need viewability proof. Check the
          // existing snapshot first so an already-visible target doesn't
          // wait for an event that will never fire.
          run.phase = "awaitingView";
          this.confirmViewable();
        },
        () => {
          if (this.run !== run || run.phase === "done") return;
          this.finish(run, "failed", "scroll");
        },
      );
      return;
    }
    const reason: LocateFailureReason = idx < 0 ? "not-found" : "layout";
    if (attempt + 1 < LOCATE_MAX_LAYOUT_ATTEMPTS) {
      this.schedule(run, () => this.probe(run, attempt + 1), LOCATE_LAYOUT_RETRY_MS);
    } else {
      this.finish(run, "failed", reason);
    }
  }

  private finish(
    run: Run,
    status: "located" | "failed",
    reason?: LocateFailureReason,
  ): void {
    if (this.run !== run) return;
    this.cancelRun();
    this.run = null;
    this.onDone({ ...run.req, status, reason });
  }
}

/**
 * Resolve the root row a comment belongs to: the comment id itself when it
 * IS a root, otherwise the root of the row whose flattened replies contain
 * it. Used by the just-published path — the composer only knows the server
 * comment id (and a reply's parent may itself be a nested reply), while the
 * timeline rows already encode the full root→replies bundling.
 *
 * Returns null when the comment isn't in the given rows yet (optimistic
 * cache still refetching) so the caller can retry on the next data change.
 */
export function resolveExpandRootId(
  rows: TimelineRow[],
  commentId: string,
): string | null {
  for (const row of rows) {
    if (row.entry.type !== "comment") continue;
    if (row.entry.id === commentId) return row.entry.id;
    if (row.replies.some((r) => r.id === commentId)) return row.entry.id;
  }
  return null;
}

/**
 * Batched form of `resolveExpandRootId` for the just-published queue: two
 * or three comments can land (composer + Retry, or a quick burst) before
 * the timeline refetch replaces their optimistic ids, and resolving each
 * id against the rows separately re-walked the full list per id. One pass
 * here answers the whole queue.
 *
 * Returns a Map keyed by the PUBLISHED comment id with its resolved root
 * id as the value; published ids that aren't in `rows` yet (optimistic
 * cache still refetching) are omitted so the caller can retry them on the
 * next data change. Insertion order follows the ROW order of the resolved
 * roots, giving callers a deterministic expansion order.
 */
export function resolvePublishedRootIds(
  rows: TimelineRow[],
  publishedIds: readonly string[],
): Map<string, string> {
  const wanted = new Set(publishedIds);
  const out = new Map<string, string>();
  if (wanted.size === 0) return out;
  for (const row of rows) {
    if (row.entry.type !== "comment") continue;
    if (wanted.has(row.entry.id)) {
      out.set(row.entry.id, row.entry.id);
    }
    for (const reply of row.replies) {
      if (wanted.has(reply.id)) {
        out.set(reply.id, row.entry.id);
      }
    }
  }
  return out;
}
