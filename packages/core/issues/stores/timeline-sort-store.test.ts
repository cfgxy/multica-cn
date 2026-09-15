// @vitest-environment node

import { beforeEach, describe, expect, it, vi } from "vitest";
import { useTimelineSortStore } from "./timeline-sort-store";

describe("timeline sort store", () => {
  beforeEach(() => {
    useTimelineSortStore.setState({ mode: "created", hintSeen: false });
  });

  it("defaults to created mode on a fresh session", async () => {
    vi.resetModules();
    const { useTimelineSortStore: freshStore } = await import(
      "./timeline-sort-store"
    );

    expect(freshStore.getState().mode).toBe("created");
  });

  it("starts in created mode and switches for the current session", () => {
    const { setMode } = useTimelineSortStore.getState();

    expect(useTimelineSortStore.getState().mode).toBe("created");
    setMode("recent-comment");

    expect(useTimelineSortStore.getState().mode).toBe("recent-comment");
  });

  it("marks the explanatory hint once without replacing unchanged state", () => {
    const { setHintSeen } = useTimelineSortStore.getState();
    const before = useTimelineSortStore.getState();

    setHintSeen();
    expect(useTimelineSortStore.getState().hintSeen).toBe(true);

    const afterFirstMark = useTimelineSortStore.getState();
    setHintSeen();
    expect(useTimelineSortStore.getState()).toBe(afterFirstMark);
    expect(before.hintSeen).toBe(false);
  });
});
