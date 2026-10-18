import { afterEach, describe, expect, it, vi } from "vitest";
import {
  clearIssueCommentBodyVisibility,
  clearIssueReferenceTailVisibility,
  getIssueCommentBodyHeight,
  hasPotentialIssueReference,
  isIssueCommentBodyVisible,
  replaceIssueCommentBodyVisibleIds,
  isIssueReferenceTailVisible,
  replaceIssueReferenceTailVisibleIds,
  setIssueCommentBodyHeight,
  subscribeIssueCommentBodyVisibility,
  subscribeIssueReferenceTailVisibility,
} from "./issue-reference-visibility";

describe("issue reference tail visibility", () => {
  afterEach(() => {
    clearIssueReferenceTailVisibility("screen-a");
    clearIssueReferenceTailVisibility("screen-b");
    clearIssueCommentBodyVisibility("screen-a");
    clearIssueCommentBodyVisibility("screen-b");
  });

  it("notifies only comments whose visible state changed", () => {
    const a = vi.fn();
    const b = vi.fn();
    const unsubscribeA = subscribeIssueReferenceTailVisibility(
      "screen-a",
      "comment-a",
      a,
    );
    const unsubscribeB = subscribeIssueReferenceTailVisibility(
      "screen-a",
      "comment-b",
      b,
    );

    replaceIssueReferenceTailVisibleIds("screen-a", new Set(["comment-a"]));
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).not.toHaveBeenCalled();
    expect(isIssueReferenceTailVisible("screen-a", "comment-a")).toBe(true);

    replaceIssueReferenceTailVisibleIds("screen-a", new Set(["comment-a"]));
    expect(a).toHaveBeenCalledTimes(1);

    replaceIssueReferenceTailVisibleIds("screen-a", new Set());
    expect(a).toHaveBeenCalledTimes(2);
    expect(isIssueReferenceTailVisible("screen-a", "comment-a")).toBe(false);

    unsubscribeA();
    unsubscribeB();
  });

  it("keeps simultaneous timeline instances isolated", () => {
    const listenerA = vi.fn();
    const listenerB = vi.fn();
    const unsubscribeA = subscribeIssueReferenceTailVisibility(
      "screen-a",
      "comment-a",
      listenerA,
    );
    const unsubscribeB = subscribeIssueReferenceTailVisibility(
      "screen-b",
      "comment-a",
      listenerB,
    );

    replaceIssueReferenceTailVisibleIds("screen-a", new Set(["comment-a"]));

    expect(listenerA).toHaveBeenCalledTimes(1);
    expect(listenerB).not.toHaveBeenCalled();
    expect(isIssueReferenceTailVisible("screen-b", "comment-a")).toBe(false);
    unsubscribeA();
    unsubscribeB();
  });

  it("clears visible state and notifies subscribers on teardown", () => {
    const listener = vi.fn();
    const unsubscribe = subscribeIssueReferenceTailVisibility(
      "screen-a",
      "comment-a",
      listener,
    );
    replaceIssueReferenceTailVisibleIds("screen-a", new Set(["comment-a"]));
    listener.mockClear();

    clearIssueReferenceTailVisibility("screen-a");

    expect(isIssueReferenceTailVisible("screen-a", "comment-a")).toBe(false);
    expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe();
  });

  it("notifies replies entering or leaving the mounted range and retains measured height", () => {
    const listenerA = vi.fn();
    const listenerB = vi.fn();
    const unsubscribeA = subscribeIssueCommentBodyVisibility(
      "screen-a",
      "reply-a",
      listenerA,
    );
    const unsubscribeB = subscribeIssueCommentBodyVisibility(
      "screen-a",
      "reply-b",
      listenerB,
    );

    replaceIssueCommentBodyVisibleIds("screen-a", new Set(["reply-a"]));
    expect(listenerA).not.toHaveBeenCalled();
    expect(listenerB).toHaveBeenCalledTimes(1);
    expect(isIssueCommentBodyVisible("screen-a", "reply-a")).toBe(true);
    expect(isIssueCommentBodyVisible("screen-a", "reply-b")).toBe(false);

    setIssueCommentBodyHeight("screen-a", "reply-b", 84);
    expect(getIssueCommentBodyHeight("screen-a", "reply-b")).toBe(84);

    replaceIssueCommentBodyVisibleIds("screen-a", new Set(["reply-b"]));
    expect(listenerA).toHaveBeenCalledTimes(1);
    expect(listenerB).toHaveBeenCalledTimes(2);
    unsubscribeA();
    unsubscribeB();
  });

  it("recognizes all reference forms without matching ordinary prose", () => {
    for (const content of [
      "See E2ER-9",
      "[issue](mention://issue/00000000-0000-0000-0000-000000000009)",
      "See cfgxy/multica-cn#338",
      "See MR !468",
      "See PR #412",
      "See PR#412",
    ]) {
      expect(hasPotentialIssueReference(content)).toBe(true);
    }
    expect(hasPotentialIssueReference("Ordinary comment text.")).toBe(false);
  });
});
