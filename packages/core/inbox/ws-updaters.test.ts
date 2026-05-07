import { describe, it, expect } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import {
  onInboxIssueDeleted,
  onInboxIssueStatusChanged,
  onInboxNew,
} from "./ws-updaters";
import { inboxKeys } from "./queries";
import type { InboxCacheData } from "./inbox-cache";
import type { InboxItem, InboxListPage } from "../types";

const wsId = "ws-1";

function makeItem(
  id: string,
  issueId: string | null,
  overrides: Partial<InboxItem> = {},
): InboxItem {
  return {
    id,
    workspace_id: wsId,
    recipient_type: "member",
    recipient_id: "user-1",
    actor_type: null,
    actor_id: null,
    type: "mentioned",
    severity: "info",
    issue_id: issueId,
    title: `item ${id}`,
    body: null,
    issue_status: null,
    read: false,
    archived: false,
    created_at: "2025-01-01T00:00:00Z",
    details: null,
    ...overrides,
  };
}

// Helper: build the InfiniteData<InboxListPage> shape that React Query stores
// for a cursor-paginated query. Tests work directly with this so we exercise
// the same cache invariants the production cache enforces.
function makeCache(
  pages: InboxItem[][],
  hasMore = false,
): InboxCacheData {
  const inboxPages: InboxListPage[] = pages.map((entries, idx) => ({
    entries,
    next_cursor: hasMore || idx < pages.length - 1 ? `cursor-${idx}` : null,
    has_more: hasMore || idx < pages.length - 1,
  }));
  return {
    pages: inboxPages,
    pageParams: pages.map((_, idx) => (idx === 0 ? null : `cursor-${idx - 1}`)),
  };
}

describe("onInboxIssueDeleted", () => {
  it("removes all inbox items referencing the deleted issue across pages", () => {
    const qc = new QueryClient();
    qc.setQueryData<InboxCacheData>(
      inboxKeys.list(wsId),
      makeCache([
        [makeItem("i1", "issue-a"), makeItem("i2", "issue-a")],
        [makeItem("i3", "issue-b"), makeItem("i4", null)],
      ]),
    );

    onInboxIssueDeleted(qc, wsId, "issue-a");

    const after = qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId));
    const flat = after?.pages.flatMap((p) => p.entries.map((e) => e.id));
    expect(flat).toEqual(["i3", "i4"]);
  });

  it("is a no-op when the inbox cache is empty", () => {
    const qc = new QueryClient();
    expect(() => onInboxIssueDeleted(qc, wsId, "issue-a")).not.toThrow();
    expect(qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId))).toBeUndefined();
  });
});

describe("onInboxIssueStatusChanged", () => {
  it("updates issue_status only for items referencing the issue", () => {
    const qc = new QueryClient();
    qc.setQueryData<InboxCacheData>(
      inboxKeys.list(wsId),
      makeCache([
        [
          makeItem("i1", "issue-a", { issue_status: "todo" }),
          makeItem("i2", "issue-b", { issue_status: "todo" }),
        ],
      ]),
    );

    onInboxIssueStatusChanged(qc, wsId, "issue-a", "done");

    const after = qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId));
    const flat = after?.pages.flatMap((p) => p.entries) ?? [];
    expect(flat.find((i) => i.id === "i1")?.issue_status).toBe("done");
    expect(flat.find((i) => i.id === "i2")?.issue_status).toBe("todo");
  });
});

describe("onInboxNew", () => {
  it("prepends a brand-new item to pages[0]", () => {
    const qc = new QueryClient();
    qc.setQueryData<InboxCacheData>(
      inboxKeys.list(wsId),
      makeCache([[makeItem("i1", "issue-a")]]),
    );

    onInboxNew(qc, wsId, makeItem("i2", "issue-b"));

    const after = qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId));
    expect(after?.pages[0]?.entries.map((e) => e.id)).toEqual(["i2", "i1"]);
  });

  it("dedups by id when the WS message replays an existing item", () => {
    const qc = new QueryClient();
    qc.setQueryData<InboxCacheData>(
      inboxKeys.list(wsId),
      makeCache([[makeItem("i1", "issue-a")]]),
    );

    onInboxNew(qc, wsId, makeItem("i1", "issue-a", { title: "replay" }));

    const after = qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId));
    const flat = after?.pages.flatMap((p) => p.entries) ?? [];
    expect(flat.map((e) => e.id)).toEqual(["i1"]);
    // The original (cached) row is preserved — we don't trust a duplicate
    // payload to override an item we already have.
    expect(flat[0]?.title).toBe("item i1");
  });

  it("collapses prior entries for the same issue (DISTINCT ON invariant)", () => {
    const qc = new QueryClient();
    qc.setQueryData<InboxCacheData>(
      inboxKeys.list(wsId),
      makeCache([
        [makeItem("old", "issue-a", { title: "old" })],
        [makeItem("older", "issue-a", { title: "older" })],
      ]),
    );

    onInboxNew(qc, wsId, makeItem("new", "issue-a", { title: "new" }));

    const after = qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId));
    const flat = after?.pages.flatMap((p) => p.entries) ?? [];
    expect(flat.map((e) => e.id)).toEqual(["new"]);
  });

  it("leaves the cache untouched when the query is not yet initialized", () => {
    const qc = new QueryClient();
    expect(() =>
      onInboxNew(qc, wsId, makeItem("i1", null)),
    ).not.toThrow();
    expect(qc.getQueryData<InboxCacheData>(inboxKeys.list(wsId))).toBeUndefined();
  });
});
