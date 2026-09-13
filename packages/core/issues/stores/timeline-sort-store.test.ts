// @vitest-environment node

import { beforeEach, describe, expect, it } from "vitest";
import { useTimelineSortStore } from "./timeline-sort-store";

describe("timeline sort store", () => {
  beforeEach(() => {
    useTimelineSortStore.setState({ mode: "recent-comment", hintSeen: false });
  });

  it("starts in recent-comment mode and switches for the current session", () => {
    const { setMode } = useTimelineSortStore.getState();

    expect(useTimelineSortStore.getState().mode).toBe("recent-comment");
    setMode("created");

    expect(useTimelineSortStore.getState().mode).toBe("created");
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
