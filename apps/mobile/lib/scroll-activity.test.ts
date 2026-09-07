// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  SCROLL_IDLE_HIDE_MS,
  ScrollActivityTracker,
} from "./scroll-activity";

describe("ScrollActivityTracker (RUYI-101)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("idle window is 3 秒（Owner 验收口径）", () => {
    expect(SCROLL_IDLE_HIDE_MS).toBe(3000);
  });

  it("starts inactive and emits nothing before the first scroll", () => {
    const onChange = vi.fn();
    const tracker = new ScrollActivityTracker(onChange);
    expect(tracker.active).toBe(false);
    vi.advanceTimersByTime(10_000);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("goes active on scroll and hides exactly at the 3s boundary", () => {
    const onChange = vi.fn();
    const tracker = new ScrollActivityTracker(onChange);

    tracker.notifyScroll();
    expect(tracker.active).toBe(true);
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenLastCalledWith(true);

    // 2999ms 仍在窗口内 —— 边界是 3000ms，不是「差不多 3 秒」。
    vi.advanceTimersByTime(SCROLL_IDLE_HIDE_MS - 1);
    expect(tracker.active).toBe(true);
    expect(onChange).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(1);
    expect(tracker.active).toBe(false);
    expect(onChange).toHaveBeenCalledTimes(2);
    expect(onChange).toHaveBeenLastCalledWith(false);
  });

  it("keeps scrolling refreshes the deadline instead of hiding mid-gesture", () => {
    const onChange = vi.fn();
    const tracker = new ScrollActivityTracker(onChange);

    tracker.notifyScroll();
    // 连续滚动帧：每 1s 一次，总时长远超 3s，但按钮不得中途消失。
    for (let i = 0; i < 5; i++) {
      vi.advanceTimersByTime(1000);
      tracker.notifyScroll();
      expect(tracker.active).toBe(true);
    }
    // 已经 active 时重复 notifyScroll 不重复通知订阅方。
    expect(onChange).toHaveBeenCalledTimes(1);

    // 最后一次滚动后满 3s 才隐藏。
    vi.advanceTimersByTime(SCROLL_IDLE_HIDE_MS);
    expect(tracker.active).toBe(false);
    expect(onChange).toHaveBeenLastCalledWith(false);
  });

  it("re-arms after hiding: a later scroll shows the chips again", () => {
    const onChange = vi.fn();
    const tracker = new ScrollActivityTracker(onChange);

    tracker.notifyScroll();
    vi.advanceTimersByTime(SCROLL_IDLE_HIDE_MS);
    expect(tracker.active).toBe(false);

    tracker.notifyScroll();
    expect(tracker.active).toBe(true);
    expect(onChange).toHaveBeenLastCalledWith(true);
  });

  it("dispose clears the pending timer and emits nothing (unmount path)", () => {
    const onChange = vi.fn();
    const tracker = new ScrollActivityTracker(onChange);

    tracker.notifyScroll();
    onChange.mockClear();
    tracker.dispose();

    vi.advanceTimersByTime(10 * SCROLL_IDLE_HIDE_MS);
    // 卸载后不得再 setState —— React 里这就是「组件已卸载仍更新状态」的告警源。
    expect(onChange).not.toHaveBeenCalled();
    expect(tracker.active).toBe(false);
  });

  it("notifyScroll after dispose is inert", () => {
    const onChange = vi.fn();
    const tracker = new ScrollActivityTracker(onChange);
    tracker.dispose();

    tracker.notifyScroll();
    expect(tracker.active).toBe(false);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("accepts a custom idle window", () => {
    const onChange = vi.fn();
    const tracker = new ScrollActivityTracker(onChange, 500);
    tracker.notifyScroll();
    vi.advanceTimersByTime(499);
    expect(tracker.active).toBe(true);
    vi.advanceTimersByTime(1);
    expect(tracker.active).toBe(false);
  });
});
