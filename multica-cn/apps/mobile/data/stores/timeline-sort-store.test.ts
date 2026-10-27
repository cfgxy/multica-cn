// @vitest-environment node

import { beforeEach, describe, expect, it, vi } from "vitest";
import { useTimelineSortStore } from "./timeline-sort-store";

describe("mobile timeline sort store", () => {
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

  it("keeps a mode choice for the current mobile session", () => {
    useTimelineSortStore.getState().setMode("recent-comment");

    expect(useTimelineSortStore.getState().mode).toBe("recent-comment");
  });

  it("records the one-time sorting hint", () => {
    useTimelineSortStore.getState().setHintSeen();

    expect(useTimelineSortStore.getState().hintSeen).toBe(true);
  });
});
