import { useEffect, useLayoutEffect, useRef } from "react";

/**
 * Persist a tab's scroll positions across <Activity> visibility transitions.
 *
 * Tabs render under `<Activity mode="visible|hidden">`, which keeps React
 * state but loses DOM scrollTop — the subtree is taken out of layout while
 * hidden and rejoins with scrollTop=0. This hook records every marked
 * container's `scrollTop` while the tab is visible (continuously, via a
 * capture-phase scroll listener) and restores them in a `useLayoutEffect`
 * the next time the tab becomes visible, before the browser paints.
 *
 * Mark scroll containers in views with `data-tab-scroll-root`. The
 * attribute value is the cache key — defaults to `"main"` for unnamed
 * roots. Most pages have a single scroll container, so a bare attribute
 * is enough; named keys are only needed when a page has multiple
 * independently scrollable regions whose positions must all be restored.
 *
 * When the tab's path changes (intra-tab navigation), the saved offsets
 * are dropped — the new route's container shares the same marker key but
 * is a different page, and restoring the old offset would land the user
 * somewhere arbitrary on the new page.
 */
export function useTabScrollRestore(tabPath: string) {
  const containerRef = useRef<HTMLDivElement>(null);
  const savedRef = useRef<Map<string, number>>(new Map());
  const prevPathRef = useRef(tabPath);

  if (prevPathRef.current !== tabPath) {
    savedRef.current.clear();
    prevPathRef.current = tabPath;
  }

  // <Activity> cleans up effects on hidden and re-mounts them on visible,
  // so an empty-deps useLayoutEffect runs exactly on every hidden → visible
  // transition. Restoring here (before the browser paints) avoids any
  // flash at scrollTop=0.
  useLayoutEffect(() => {
    const root = containerRef.current;
    if (!root) return;
    const els = root.querySelectorAll<HTMLElement>("[data-tab-scroll-root]");
    els.forEach((el) => {
      const key = scrollKey(el);
      const saved = savedRef.current.get(key);
      if (saved !== undefined && el.scrollTop !== saved) {
        el.scrollTop = saved;
      }
    });
  }, []);

  useEffect(() => {
    const root = containerRef.current;
    if (!root) return;
    const onScroll = (e: Event) => {
      const target = e.target;
      if (!(target instanceof HTMLElement)) return;
      if (!target.hasAttribute("data-tab-scroll-root")) return;
      savedRef.current.set(scrollKey(target), target.scrollTop);
    };
    // Scroll events don't bubble, but capture catches them anyway.
    root.addEventListener("scroll", onScroll, { capture: true, passive: true });
    return () => root.removeEventListener("scroll", onScroll, true);
  }, []);

  return containerRef;
}

function scrollKey(el: HTMLElement): string {
  return el.getAttribute("data-tab-scroll-root") || "main";
}
