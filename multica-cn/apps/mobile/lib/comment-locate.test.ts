// @vitest-environment node
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import {
  CommentLocateController,
  LOCATE_EXPAND_SETTLE_MS,
  LOCATE_LAYOUT_RETRY_MS,
  LOCATE_MAX_REFINE_ATTEMPTS,
  LOCATE_REFINE_SETTLE_MS,
  LOCATE_TIMEOUT_MS,
  type CommentLocateAdapter,
  type LocateResult,
} from "./comment-locate";
import type { TargetRect } from "./comment-target-visibility";
import { buildTimelineRows } from "./timeline-thread";
import type { TimelineEntry } from "@multica/core/types";
import {
  resolveExpandRootId,
  resolvePublishedRootIds,
} from "./comment-locate";

/**
 * Virtual-clock harness: `schedule` returns handles backed by vi timers.
 * Advance with `advance` so both the settle delay and the probe backoff
 * run deterministically — real-clock tests would only cover the happy
 * path and time out the rest.
 */
interface Harness {
  adapter: CommentLocateAdapter & {
    setIndex: (i: number) => void;
    setLayoutReady: (b: boolean) => void;
    pendingScroll: () => { resolve: () => void; reject: (e: unknown) => void };
    viewable: Set<string>;
    scrollCalls: number;
    findCalls: number;
    /** 行内校正的三件套：目标矩形（null = 还没量到）、视口、二次滚动记录。 */
    setTargetRect: (rect: TargetRect | null) => void;
    viewport: { offsetY: number; height: number };
    offsetScrolls: number[];
    measureCalls: number;
  };
  results: LocateResult[];
}

function makeHarness(): Harness {
  const results: LocateResult[] = [];
  const state = {
    index: 3,
    layoutReady: true,
    targetRect: null as TargetRect | null,
  };
  // Deferred created by the LAST scrollToIndex call — the controller holds
  // its promise; the test settles it through `pendingScroll()`.
  let deferred: {
    resolve: () => void;
    reject: (e: unknown) => void;
  } | null = null;
  const adapter: Harness["adapter"] = {
    findCalls: 0,
    scrollCalls: 0,
    viewable: new Set<string>(),
    // 默认视口 800 高、停在顶部；目标矩形默认落在视口内，需要「屏外」时由
    // 用例显式改。
    viewport: { offsetY: 0, height: 800 },
    offsetScrolls: [],
    measureCalls: 0,
    setIndex: (i) => (state.index = i),
    setLayoutReady: (b) => (state.layoutReady = b),
    setTargetRect: (rect) => (state.targetRect = rect),
    measureTarget: () => {
      adapter.measureCalls += 1;
      return state.targetRect;
    },
    getViewport: () => adapter.viewport,
    scrollToOffset: (offset) => {
      adapter.offsetScrolls.push(offset);
    },
    pendingScroll: () => {
      if (!deferred) throw new Error("no pending scrollToIndex promise");
      return deferred;
    },
    findIndex: () => {
      adapter.findCalls += 1;
      return state.index;
    },
    getLayout: () => (state.layoutReady ? { y: 120 } : null),
    scrollToIndex: () => {
      adapter.scrollCalls += 1;
      return new Promise<void>((res, rej) => {
        deferred = { resolve: res, reject: rej };
      });
    },
    isViewable: (rootId) => adapter.viewable.has(rootId),
    schedule: (fn, ms) => {
      const id = setTimeout(fn, ms);
      return { cancel: () => clearTimeout(id) };
    },
  };
  return { adapter, results };
}

function makeController(h: Harness) {
  return new CommentLocateController(h.adapter, (r) => h.results.push(r));
}

const REQ = { issueId: "i1", rootId: "r1", nonce: 1 };

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("CommentLocateController — happy path", () => {
  it("pending → scroll → viewability → located", async () => {
    const h = makeHarness();
    const c = makeController(h);
    c.start(REQ);
    // Nothing before the expand-settle delay.
    expect(h.results).toEqual([]);
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    expect(h.adapter.scrollCalls).toBe(1);
    const d = h.adapter.pendingScroll();
    d.resolve();
    await vi.advanceTimersByTimeAsync(0);
    // Scroll resolved but the row is NOT viewable yet — must not locate.
    expect(h.results).toEqual([]);
    h.adapter.viewable.add("r1");
    c.confirmViewable();
    expect(h.results).toEqual([
      { ...REQ, status: "located" },
    ]);
  });

  it("already-viewable row locates without a second viewability event", async () => {
    const h = makeHarness();
    h.adapter.viewable.add("r1");
    const c = makeController(h);
    c.start(REQ);
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    h.adapter.pendingScroll().resolve();
    await vi.advanceTimersByTimeAsync(0);
    expect(h.results).toEqual([{ ...REQ, status: "located" }]);
  });
});

describe("CommentLocateController — viewability gate", () => {
  it("viewability of a DIFFERENT root does not close the run", async () => {
    const h = makeHarness();
    const c = makeController(h);
    c.start(REQ);
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    h.adapter.pendingScroll().resolve();
    await vi.advanceTimersByTimeAsync(0);
    h.adapter.viewable.add("some-other-root");
    c.confirmViewable();
    expect(h.results).toEqual([]);
    // Still waiting — the timeout eventually fails it, not a false close.
    vi.advanceTimersByTime(LOCATE_TIMEOUT_MS);
    expect(h.results).toEqual([{ ...REQ, status: "failed", reason: "timeout" }]);
  });
});

/**
 * RUYI-108 行内校正。
 *
 * 这组用例存在的唯一理由：把「root 行可见」和「被引用的那条回复可见」分开。
 * 手机端 root 与它全部回复渲染在同一个 FlashList 行里，行级 viewability 对
 * 两者给出同一个答案；长线程里 root 行刚露头时目标回复可能还在屏幕下方几百
 * 像素处。所以下面每个用例都让 `isViewable("r1")` 为真——行是可见的——再单独
 * 摆布目标矩形，检查控制器是否还会误判为已定位。
 */
describe("CommentLocateController — 行内校正（回复目标）", () => {
  /** 目标是回复，行可见但目标在屏外。 */
  const REPLY_REQ = { ...REQ, targetId: "reply-9" };

  async function landRow(
    h: Harness,
    c: CommentLocateController,
    req: { issueId: string; rootId: string; targetId?: string; nonce: number } =
      REPLY_REQ,
  ) {
    c.start(req);
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    h.adapter.viewable.add("r1");
    h.adapter.pendingScroll().resolve();
    await vi.advanceTimersByTimeAsync(0);
  }

  it("root 行已可见但目标回复在屏外时，不判定为已定位", async () => {
    const h = makeHarness();
    // 视口 0–800，目标顶边在 1500 —— 行露头了，被引用的回复没有。
    h.adapter.setTargetRect({ top: 1500, height: 200 });
    const c = makeController(h);
    await landRow(h, c);
    expect(h.results).toEqual([]);
  });

  it("目标在屏外时按其顶边二次滚动，入屏后才判 located", async () => {
    const h = makeHarness();
    h.adapter.setTargetRect({ top: 1500, height: 200 });
    const c = makeController(h);
    await landRow(h, c);
    // 第一次校正：读到屏外 → 按顶边减去落点留白滚动。
    expect(h.adapter.offsetScrolls).toEqual([1500 - 8]);
    expect(h.results).toEqual([]);
    // 滚动落位后目标进入视口。
    h.adapter.viewport.offsetY = 1492;
    await vi.advanceTimersByTimeAsync(LOCATE_REFINE_SETTLE_MS);
    expect(h.results).toEqual([{ ...REPLY_REQ, status: "located" }]);
  });

  it("目标已经在视口内时不做多余的二次滚动", async () => {
    const h = makeHarness();
    h.adapter.setTargetRect({ top: 120, height: 200 });
    const c = makeController(h);
    await landRow(h, c);
    expect(h.adapter.offsetScrolls).toEqual([]);
    expect(h.results).toEqual([{ ...REPLY_REQ, status: "located" }]);
  });

  it("折叠线程刚展开、回复还没量到时只等待重试，不按空几何乱滚", async () => {
    const h = makeHarness();
    // 展开后行内 markdown 尚未排完版：连续两轮都量不到。
    h.adapter.setTargetRect(null);
    const c = makeController(h);
    await landRow(h, c);
    await vi.advanceTimersByTimeAsync(LOCATE_REFINE_SETTLE_MS);
    expect(h.adapter.offsetScrolls).toEqual([]);
    expect(h.results).toEqual([]);
    // 排版完成后量到且已在屏内 → 定位成功。
    h.adapter.setTargetRect({ top: 200, height: 100 });
    await vi.advanceTimersByTimeAsync(LOCATE_REFINE_SETTLE_MS);
    expect(h.results).toEqual([{ ...REPLY_REQ, status: "located" }]);
  });

  it("校正次数耗尽仍不可见 → failed(layout)，走既有降级", async () => {
    const h = makeHarness();
    h.adapter.setTargetRect({ top: 5000, height: 100 }); // 永远滚不到
    const c = makeController(h);
    await landRow(h, c);
    await vi.advanceTimersByTimeAsync(
      LOCATE_REFINE_SETTLE_MS * (LOCATE_MAX_REFINE_ATTEMPTS + 1),
    );
    expect(h.results).toEqual([
      { ...REPLY_REQ, status: "failed", reason: "layout" },
    ]);
    expect(h.adapter.measureCalls).toBe(LOCATE_MAX_REFINE_ATTEMPTS);
  });

  it("目标就是 root 自身时保持行级判定，不做行内校正", async () => {
    const h = makeHarness();
    // 即使几何显示「屏外」，root 目标也只看行级 viewability。
    h.adapter.setTargetRect({ top: 9999, height: 100 });
    const c = makeController(h);
    await landRow(h, c, { ...REQ, targetId: "r1" });
    expect(h.adapter.measureCalls).toBe(0);
    expect(h.results).toEqual([
      { ...REQ, targetId: "r1", status: "located" },
    ]);
  });

  it("缺省 targetId（评论目录/深链）等同于定位 root 本身", async () => {
    const h = makeHarness();
    h.adapter.setTargetRect({ top: 9999, height: 100 });
    const c = makeController(h);
    await landRow(h, c, REQ);
    expect(h.adapter.measureCalls).toBe(0);
    expect(h.results).toEqual([{ ...REQ, status: "located" }]);
  });

  it("重复点击同一条回复（新 nonce）会重放整条落地链路", async () => {
    const h = makeHarness();
    h.adapter.setTargetRect({ top: 120, height: 200 });
    const c = makeController(h);
    await landRow(h, c, { ...REPLY_REQ, nonce: 1 });
    expect(h.results).toEqual([
      { ...REPLY_REQ, nonce: 1, status: "located" },
    ]);
    await landRow(h, c, { ...REPLY_REQ, nonce: 2 });
    expect(h.adapter.scrollCalls).toBe(2);
    expect(h.results[1]).toEqual({
      ...REPLY_REQ,
      nonce: 2,
      status: "located",
    });
  });

  it("整体超时期间的校正不会把失败盖成成功", async () => {
    const h = makeHarness();
    h.adapter.setTargetRect(null); // 一直量不到
    const c = makeController(h);
    c.start(REPLY_REQ);
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    h.adapter.viewable.add("r1");
    h.adapter.pendingScroll().resolve();
    await vi.advanceTimersByTimeAsync(LOCATE_TIMEOUT_MS);
    expect(h.results).toHaveLength(1);
    expect(h.results[0].status).toBe("failed");
  });
});

describe("CommentLocateController — failure paths", () => {
  it("scroll reject → failed(scroll)", async () => {
    const h = makeHarness();
    const c = makeController(h);
    c.start(REQ);
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    h.adapter.pendingScroll().reject(new Error("estimate miss"));
    await vi.advanceTimersByTimeAsync(0);
    expect(h.results).toEqual([{ ...REQ, status: "failed", reason: "scroll" }]);
  });

  it("row never lays out after retries → failed(layout)", () => {
    const h = makeHarness();
    h.adapter.setLayoutReady(false);
    const c = makeController(h);
    c.start(REQ);
    vi.advanceTimersByTime(
      LOCATE_EXPAND_SETTLE_MS + LOCATE_LAYOUT_RETRY_MS * 10,
    );
    expect(h.results).toEqual([{ ...REQ, status: "failed", reason: "layout" }]);
    expect(h.adapter.findCalls).toBe(4);
  });

  it("row missing from data → failed(not-found)", () => {
    const h = makeHarness();
    h.adapter.setIndex(-1);
    const c = makeController(h);
    c.start(REQ);
    vi.advanceTimersByTime(
      LOCATE_EXPAND_SETTLE_MS + LOCATE_LAYOUT_RETRY_MS * 10,
    );
    expect(h.results).toEqual([{ ...REQ, status: "failed", reason: "not-found" }]);
  });

  it("whole-run timeout fires while awaiting scroll → failed(timeout)", () => {
    const h = makeHarness();
    const c = makeController(h);
    c.start(REQ);
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    // Scroll promise never settles.
    vi.advanceTimersByTime(LOCATE_TIMEOUT_MS);
    expect(h.results).toEqual([{ ...REQ, status: "failed", reason: "timeout" }]);
  });

  it("timeout fires while awaiting viewability → failed(timeout)", async () => {
    const h = makeHarness();
    const c = makeController(h);
    c.start(REQ);
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    h.adapter.pendingScroll().resolve();
    await vi.advanceTimersByTimeAsync(LOCATE_TIMEOUT_MS);
    expect(h.results).toEqual([{ ...REQ, status: "failed", reason: "timeout" }]);
  });
});

describe("CommentLocateController — retry / cancel semantics", () => {
  it("retrying the SAME root with a NEW nonce runs a fresh scroll", async () => {
    const h = makeHarness();
    h.adapter.viewable.add("r1");
    const c = makeController(h);
    // First attempt fails on scroll.
    c.start({ ...REQ, nonce: 1 });
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    expect(h.adapter.scrollCalls).toBe(1);
    h.adapter.pendingScroll().reject(new Error("miss"));
    await vi.advanceTimersByTimeAsync(0);
    expect(h.results).toEqual([
      { ...REQ, nonce: 1, status: "failed", reason: "scroll" },
    ]);
    // Same root, new nonce — must scroll again and succeed.
    c.start({ ...REQ, nonce: 2 });
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    expect(h.adapter.scrollCalls).toBe(2);
    h.adapter.pendingScroll().resolve();
    await vi.advanceTimersByTimeAsync(0);
    expect(h.results[1]).toEqual({ ...REQ, nonce: 2, status: "located" });
  });

  it("start with an unchanged nonce is a no-op (no double scroll)", () => {
    const h = makeHarness();
    const c = makeController(h);
    c.start(REQ);
    c.start(REQ); // effect re-ran with the same intent
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    expect(h.adapter.scrollCalls).toBe(1);
  });

  it("a new nonce cancels the in-flight run without emitting its result", async () => {
    const h = makeHarness();
    h.adapter.viewable.add("r1");
    const c = makeController(h);
    c.start({ ...REQ, nonce: 1 });
    vi.advanceTimersByTime(LOCATE_EXPAND_SETTLE_MS);
    const first = h.adapter.pendingScroll();
    // User re-taps before the first scroll settles.
    c.start({ ...REQ, nonce: 2 });
    first.resolve(); // stale settlement — must be ignored
    await vi.advanceTimersByTimeAsync(LOCATE_EXPAND_SETTLE_MS);
    expect(h.adapter.scrollCalls).toBe(2);
    expect(h.results).toEqual([]);
    h.adapter.pendingScroll().resolve();
    await vi.advanceTimersByTimeAsync(0);
    expect(h.results).toEqual([{ ...REQ, nonce: 2, status: "located" }]);
  });

  it("cancel() drops the run and all timers without emitting", () => {
    const h = makeHarness();
    const c = makeController(h);
    c.start(REQ);
    c.cancel();
    vi.advanceTimersByTime(LOCATE_TIMEOUT_MS * 2);
    expect(h.results).toEqual([]);
  });
});

describe("resolveExpandRootId", () => {
  const root = (id: string, parentId: string | null = null): TimelineEntry =>
    ({
      type: "comment",
      id,
      actor_type: "member",
      actor_id: `u-${id}`,
      content: `c-${id}`,
      parent_id: parentId,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      comment_type: "comment",
      reactions: [],
      attachments: [],
    }) as TimelineEntry;

  it("maps a root id to itself", () => {
    const rows = buildTimelineRows([root("a"), root("b")]);
    expect(resolveExpandRootId(rows, "a")).toBe("a");
  });

  it("maps a nested reply to its owning root", () => {
    const rows = buildTimelineRows([
      root("a"),
      root("r1", "a"),
      root("r2", "r1"), // reply-to-reply still bundles under the root
    ]);
    expect(resolveExpandRootId(rows, "r2")).toBe("a");
  });

  it("returns null when the comment is not in the rows yet", () => {
    const rows = buildTimelineRows([root("a")]);
    expect(resolveExpandRootId(rows, "zz")).toBeNull();
  });

  it("ignores activity rows", () => {
    const activity = {
      type: "activity",
      id: "act",
      actor_type: "member",
      actor_id: "u",
      created_at: "2026-01-01T00:00:00Z",
    } as unknown as TimelineEntry;
    const rows = buildTimelineRows([activity, root("a")]);
    expect(resolveExpandRootId(rows, "a")).toBe("a");
  });
});

describe("resolvePublishedRootIds", () => {
  const root = (id: string, parentId: string | null = null): TimelineEntry =>
    ({
      type: "comment",
      id,
      actor_type: "member",
      actor_id: `u-${id}`,
      content: `c-${id}`,
      parent_id: parentId,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      comment_type: "comment",
      reactions: [],
      attachments: [],
    }) as TimelineEntry;

  it("resolves each queued publish to its owning root, in row order", () => {
    const rows = buildTimelineRows([
      root("a"),
      root("b"),
      root("b-reply", "b"),
    ]);
    const resolved = resolvePublishedRootIds(rows, ["b-reply", "a"]);
    // Map keys follow the ROW order of the owning roots (a before b),
    // not the queue order — callers get a deterministic expansion order.
    expect([...resolved.keys()]).toEqual(["a", "b-reply"]);
    expect(resolved.get("b-reply")).toBe("b");
    expect(resolved.get("a")).toBe("a");
  });

  it("keeps unresolved ids out of the result — the caller retries them later", () => {
    const rows = buildTimelineRows([root("a")]);
    const resolved = resolvePublishedRootIds(rows, ["a", "still-optimistic"]);
    expect([...resolved.keys()]).toEqual(["a"]);
    expect(resolved.has("still-optimistic")).toBe(false);
  });

  it("returns an empty map for an empty queue", () => {
    const rows = buildTimelineRows([root("a")]);
    expect(resolvePublishedRootIds(rows, []).size).toBe(0);
  });

  it("collapses multiple publishes in the same thread to one root entry", () => {
    // Root + its reply published back-to-back → one expansion of root "a"
    // (Map keyed by published id keeps both entries, but the root VALUE is
    // shared; callers expanding per entry stay idempotent — and the
    // root-only view is a single Set entry).
    const rows = buildTimelineRows([
      root("a"),
      root("a-r", "a"),
    ]);
    const resolved = resolvePublishedRootIds(rows, ["a", "a-r"]);
    expect(new Set(resolved.values())).toEqual(new Set(["a"]));
  });
});
