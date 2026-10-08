// Cache-patch tests for the inbox WS updaters. The updaters run on `issue:*`
// events and must keep BOTH inbox caches coherent once the archived sub-view
// exists (RUYI-532): the archived cache holds rows for the same issues, and
// leaving it stale shows a deleted issue's row (tap → 404) or a stale status
// glyph in the archived list until the next refetch.
import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { InboxItem } from "@multica/core/types";
import { inboxKeys } from "@/data/queries/inbox";
import {
  dropInboxItemsByIssue,
  patchInboxIssueStatus,
} from "./inbox-ws-updaters";

// Vitest lane convention: the native fetch chain must never load here.
vi.mock("@/data/api", () => ({ api: {} }));

function row(overrides: Partial<InboxItem>): InboxItem {
  return {
    id: "inbox-1",
    workspace_id: "workspace-1",
    recipient_type: "member",
    recipient_id: "member-1",
    actor_type: "agent",
    actor_id: "agent-1",
    type: "new_comment",
    severity: "info",
    issue_id: "issue-1",
    title: "Issue title",
    body: null,
    issue_status: null,
    read: false,
    archived: false,
    created_at: "2026-06-15T08:00:00Z",
    details: null,
    ...overrides,
  };
}

function seed(qc: QueryClient) {
  const list: InboxItem[] = [
    row({ id: "list-active", issue_id: "issue-1" }),
    row({ id: "list-other", issue_id: "issue-2" }),
  ];
  const archived: InboxItem[] = [
    row({ id: "archived-1", issue_id: "issue-1", archived: true }),
    row({ id: "archived-other", issue_id: "issue-2", archived: true }),
  ];
  qc.setQueryData(inboxKeys.list("ws-1"), list);
  qc.setQueryData(inboxKeys.archived("ws-1"), archived);
}

describe("patchInboxIssueStatus", () => {
  it("projects the new status into both the list and the archived cache", () => {
    const qc = new QueryClient();
    seed(qc);

    patchInboxIssueStatus(qc, "ws-1", "issue-1", "done");

    const list = qc.getQueryData<InboxItem[]>(inboxKeys.list("ws-1"));
    const archived = qc.getQueryData<InboxItem[]>(inboxKeys.archived("ws-1"));
    expect(list?.find((i) => i.id === "list-active")?.issue_status).toBe("done");
    expect(list?.find((i) => i.id === "list-other")?.issue_status).toBeNull();
    expect(archived?.find((i) => i.id === "archived-1")?.issue_status).toBe(
      "done",
    );
    expect(archived?.find((i) => i.id === "archived-other")?.issue_status).toBe(
      null,
    );
  });
});

describe("dropInboxItemsByIssue", () => {
  it("drops the deleted issue's rows from both caches", () => {
    const qc = new QueryClient();
    seed(qc);

    dropInboxItemsByIssue(qc, "ws-1", "issue-1");

    const list = qc.getQueryData<InboxItem[]>(inboxKeys.list("ws-1"));
    const archived = qc.getQueryData<InboxItem[]>(inboxKeys.archived("ws-1"));
    expect(list?.map((i) => i.id)).toEqual(["list-other"]);
    expect(archived?.map((i) => i.id)).toEqual(["archived-other"]);
  });
});
