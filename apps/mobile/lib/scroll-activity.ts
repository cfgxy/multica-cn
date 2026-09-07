/**
 * Transient "the user is scrolling right now" signal (RUYI-101).
 *
 * The timeline's "to top" / "to bottom" chips used to be driven by scroll
 * GEOMETRY alone: once the viewport was more than 48px away from an edge
 * the matching chip appeared and then stayed there for the rest of the
 * read session, covering the text underneath. Owner's requirement is the
 * common mobile idiom (Telegram / 微信 read view): the chips are a
 * transient affordance — they appear on scroll and fade out once the user
 * has been still for 3 seconds.
 *
 * Extracted as a plain class rather than a hook so the timing rules —
 * activation, deadline refresh on continued scrolling, the exact 3s
 * boundary and unmount cleanup — are testable in the mobile vitest lane
 * (node environment, no RN renderer). timeline-list.tsx owns one instance
 * per mount and forwards `notifyScroll()` from `onScroll`.
 *
 * Deliberately NOT a debounce over a shared state atom: the two chips must
 * observe exactly the same activity window, and a per-chip debounce would
 * let them drift apart by a frame under load.
 */

/** Idle time after the LAST scroll event before the jump chips hide.
 *  3 秒 = Owner 在 RUYI-101 需求原文给定的口径。 */
export const SCROLL_IDLE_HIDE_MS = 3000;

export class ScrollActivityTracker {
  private timer: ReturnType<typeof setTimeout> | null = null;
  private disposed = false;
  private isActive = false;

  constructor(
    private readonly onChange: (active: boolean) => void,
    private readonly idleMs: number = SCROLL_IDLE_HIDE_MS,
  ) {}

  /** Current activity state. Starts false — a freshly opened issue shows
   *  no jump chips until the user actually scrolls. */
  get active(): boolean {
    return this.isActive;
  }

  /**
   * Called on every scroll event. Turns the signal on (notifying once per
   * off→on edge, not per frame) and pushes the hide deadline out by the
   * full idle window, so a continuous gesture can never hide the chips
   * mid-scroll.
   */
  notifyScroll(): void {
    if (this.disposed) return;
    this.clearTimer();
    if (!this.isActive) {
      this.isActive = true;
      this.onChange(true);
    }
    this.timer = setTimeout(() => {
      this.timer = null;
      if (this.disposed || !this.isActive) return;
      this.isActive = false;
      this.onChange(false);
    }, this.idleMs);
  }

  /**
   * Unmount path. Drops the pending timer WITHOUT emitting, so the owning
   * component never gets a state update after it has gone away, and makes
   * every later `notifyScroll()` inert.
   */
  dispose(): void {
    this.disposed = true;
    this.clearTimer();
    this.isActive = false;
  }

  private clearTimer(): void {
    if (this.timer === null) return;
    clearTimeout(this.timer);
    this.timer = null;
  }
}
