"use client";

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { IssueReferenceTail } from "./issue-reference-footer";
import { useRichContentScrollRoot } from "./scroll-root";

type ScrollAdjustment = { manual: number; claimed: number };

const scrollAdjustments = new WeakMap<object, ScrollAdjustment>();

function getScrollAdjustment(target: object): ScrollAdjustment {
  let adjustment = scrollAdjustments.get(target);
  if (!adjustment) {
    adjustment = { manual: 0, claimed: 0 };
    scrollAdjustments.set(target, adjustment);
  }
  return adjustment;
}

export function LazyIssueReferenceTail({ content }: { content: string }) {
  const root = useRichContentScrollRoot();
  const ref = useRef<HTMLDivElement>(null);
  const [nearViewport, setNearViewport] = useState(false);
  const mountedHeight = useRef(0);
  const pendingScroll = useRef<{
    top: number;
    windowRoot: boolean;
    manualAdjustment: number;
    claimedAdjustment: number;
    selectionRange: Range | null;
    selectionText: string;
    selectionTop: number | null;
  } | null>(null);

  useLayoutEffect(() => {
    const element = ref.current;
    if (!element) return;

    const heightDelta = element.scrollHeight - mountedHeight.current;
    mountedHeight.current = element.scrollHeight;
    const pending = pendingScroll.current;
    pendingScroll.current = null;
    if (!pending || heightDelta === 0) return;

    // Preserve the native find range itself when several tail updates race
    // browser anchoring; scroll deltas cannot identify which sibling moved it.
    const selection = window.getSelection();
    if (
      pending.selectionRange &&
      pending.selectionTop !== null &&
      selection &&
      !selection.isCollapsed &&
      selection.toString() === pending.selectionText &&
      pending.selectionRange.startContainer.isConnected
    ) {
      const selectionTop = pending.selectionRange.getBoundingClientRect().top;
      const correction = selectionTop - pending.selectionTop;
      if (correction !== 0) {
        if (pending.windowRoot) {
          window.scrollBy(0, correction);
        } else if (root) {
          root.scrollTop += correction;
        }
      }
      return;
    }

    if (element.getBoundingClientRect().top > 0) return;

    // Native find can scroll past this shell before its footer mounts. Keep the
    // found content stationary, accounting for native anchoring and sibling
    // tails that share this scroll baseline.
    const currentTop = pending.windowRoot ? window.scrollY : root?.scrollTop ?? 0;
    const scrollTarget = pending.windowRoot ? window : root;
    const adjustment = scrollTarget ? getScrollAdjustment(scrollTarget) : null;
    const manualSinceSnapshot = adjustment
      ? adjustment.manual - pending.manualAdjustment
      : 0;
    const claimedSinceSnapshot = adjustment
      ? adjustment.claimed - pending.claimedAdjustment
      : 0;
    const alreadyAdjusted =
      currentTop - pending.top - manualSinceSnapshot - claimedSinceSnapshot;
    if (adjustment) adjustment.claimed += alreadyAdjusted;
    const correction = heightDelta - alreadyAdjusted;
    if (correction === 0) return;
    if (pending.windowRoot) {
      window.scrollBy(0, correction);
    } else if (root) {
      root.scrollTop += correction;
    }
    if (adjustment) adjustment.manual += correction;
  }, [nearViewport, root]);

  useEffect(() => {
    if (typeof window === "undefined" || !window.IntersectionObserver) {
      setNearViewport(true);
      return;
    }

    const element = ref.current;
    if (!element) return;
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries.find((candidate) => candidate.target === element);
        if (entry) {
          const adjustment = getScrollAdjustment(root ?? window);
          const selection = window.getSelection();
          const selectionRange = selection && !selection.isCollapsed
            ? selection.getRangeAt(0).cloneRange()
            : null;
          pendingScroll.current = {
            top: root ? root.scrollTop : window.scrollY,
            windowRoot: !root,
            manualAdjustment: adjustment.manual,
            claimedAdjustment: adjustment.claimed,
            selectionRange,
            selectionText: selectionRange?.toString() ?? "",
            selectionTop: selectionRange?.getBoundingClientRect().top ?? null,
          };
          setNearViewport(entry.isIntersecting);
        }
      },
      { root, rootMargin: "600px 0px" },
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, [root]);

  return (
    <div ref={ref} data-issue-reference-tail-shell="">
      {nearViewport ? <IssueReferenceTail content={content} /> : null}
    </div>
  );
}
