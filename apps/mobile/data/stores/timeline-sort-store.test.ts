// @vitest-environment node

import { beforeEach, describe, expect, it } from "vitest";
import { useTimelineSortStore } from "./timeline-sort-store";

describe("mobile timeline sort store", () => {
  beforeEach(() => {
    useTimelineSortStore.setState({ mode: "recent-comment", hintSeen: false });
  });

  it("keeps a mode choice for the current mobile session", () => {
    useTimelineSortStore.getState().setMode("created");

    expect(useTimelineSortStore.getState().mode).toBe("created");
  });

  it("records the one-time sorting hint", () => {
    useTimelineSortStore.getState().setHintSeen();

    expect(useTimelineSortStore.getState().hintSeen).toBe(true);
  });
});
