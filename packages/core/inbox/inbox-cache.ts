import type { InfiniteData } from "@tanstack/react-query";
import type { InboxItem, InboxListPage } from "../types";

/**
 * Shape of the cursor-paginated inbox cache. Modeled on `TimelineCacheData`
 * — same primitives, different domain. Consumers (mutations, WS updaters,
 * tests) all reference this so the cache shape doesn't drift.
 */
export type InboxCacheData = InfiniteData<InboxListPage, string | null>;

/**
 * Map fn over every inbox item across every page. Preserves page reference
 * identity when nothing in that page changed so React.memo on row components
 * isn't defeated by gratuitous reference churn.
 */
export function mapAllItems(
  data: InboxCacheData | undefined,
  fn: (i: InboxItem) => InboxItem,
): InboxCacheData | undefined {
  if (!data) return data;
  let pagesChanged = false;
  const pages = data.pages.map((page) => {
    let entriesChanged = false;
    const entries = page.entries.map((e) => {
      const next = fn(e);
      if (next !== e) entriesChanged = true;
      return next;
    });
    if (!entriesChanged) return page;
    pagesChanged = true;
    return { ...page, entries };
  });
  if (!pagesChanged) return data;
  return { ...data, pages };
}

/**
 * Remove items matching the predicate from every page. NOTE: predicate
 * semantics are *removal* (`true` → drop), opposite of `Array.filter`.
 * Named explicitly to avoid the easy mistake of treating it like
 * `Array.filter` and inverting the boolean.
 */
export function removeMatchingItems(
  data: InboxCacheData | undefined,
  predicate: (i: InboxItem) => boolean,
): InboxCacheData | undefined {
  if (!data) return data;
  let pagesChanged = false;
  const pages = data.pages.map((page) => {
    const entries = page.entries.filter((e) => !predicate(e));
    if (entries.length === page.entries.length) return page;
    pagesChanged = true;
    return { ...page, entries };
  });
  if (!pagesChanged) return data;
  return { ...data, pages };
}

/**
 * Prepend a WS-pushed inbox item to the latest page (pages[0]).
 *
 * Honors the SQL DISTINCT ON dedup invariant: when an item shares an
 * `issue_id` with an existing entry, the existing one is removed first so
 * each issue only appears once in the rendered list.
 *
 * Returns `data` unchanged when:
 *   - the cache is empty / not initialized (the next query refetch will
 *     populate it from the server, including the new item)
 *   - an item with the same `id` is already present (WS replay)
 */
export function prependToLatestPage(
  data: InboxCacheData | undefined,
  item: InboxItem,
): InboxCacheData | undefined {
  if (!data || data.pages.length === 0) return data;
  const first = data.pages[0];
  if (!first) return data;

  // Dedup by id (handles WS replay / setQueryData racing with refetch).
  if (first.entries.some((e) => e.id === item.id)) return data;

  // Per-issue dedup: an existing entry for the same issue must be removed
  // so the new (newer) one takes its slot. Walk every page — a stale entry
  // for the same issue could live anywhere if pagination was already deep.
  const issueId = item.issue_id;
  const cleared = issueId
    ? data.pages.map((page) => {
        const filtered = page.entries.filter((e) => e.issue_id !== issueId);
        if (filtered.length === page.entries.length) return page;
        return { ...page, entries: filtered };
      })
    : data.pages;

  const head = cleared[0]!;
  return {
    ...data,
    pages: [
      { ...head, entries: [item, ...head.entries] },
      ...cleared.slice(1),
    ],
  };
}
