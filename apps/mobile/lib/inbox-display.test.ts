import { beforeAll, describe, expect, it } from "vitest";
import i18n from "i18next";
import { RESOURCES } from "@multica/views/locales";
import type { InboxItem } from "@multica/core/types";
import {
  deduplicateArchivedInboxItems,
  deduplicateInboxItems,
  getAutopilotQuotaBody,
  getInboxDisplayTitle,
  getInboxNavigationTarget,
} from "./inbox-display";

// getInboxDisplayTitle / getAutopilotQuotaBody resolve user-visible copy
// through `i18n.t`, and an uninitialized i18next returns undefined — not even
// the fallback argument. Production initializes it at app startup; tests do
// the equivalent here with the real RESOURCES so the assertions cover the
// strings users actually read (same pattern as lib/dispatch-reason.test.ts).
beforeAll(async () => {
  await i18n.init({
    resources: RESOURCES as never,
    lng: "en",
    fallbackLng: "en",
    defaultNS: "common",
    interpolation: { escapeValue: false },
  });
});

function item(overrides: Partial<InboxItem>): InboxItem {
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

describe("deduplicateInboxItems", () => {
  it("keeps the newest issue row while preserving an older comment anchor", () => {
    const merged = deduplicateInboxItems([
      item({
        id: "comment-notification",
        created_at: "2026-06-15T08:00:00Z",
        details: { comment_id: "comment-1" },
      }),
      item({
        id: "status-notification",
        type: "status_changed",
        created_at: "2026-06-15T08:01:00Z",
        details: { from: "in_progress", to: "in_review" },
      }),
    ]);

    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({
      id: "status-notification",
      type: "status_changed",
      details: {
        from: "in_progress",
        to: "in_review",
        comment_id: "comment-1",
      },
    });
  });
});

describe("deduplicateArchivedInboxItems", () => {
  it("keeps only archived rows and drops active ones", () => {
    const merged = deduplicateArchivedInboxItems([
      item({ id: "active-1", archived: false }),
      item({ id: "archived-1", archived: true, created_at: "2026-06-15T08:01:00Z" }),
    ]);

    expect(merged).toHaveLength(1);
    expect(merged[0]?.id).toBe("archived-1");
  });

  it("keeps the newest archived row per issue while preserving an older comment anchor", () => {
    const merged = deduplicateArchivedInboxItems([
      item({
        id: "comment-notification",
        archived: true,
        created_at: "2026-06-15T08:00:00Z",
        details: { comment_id: "comment-1" },
      }),
      item({
        id: "status-notification",
        type: "status_changed",
        archived: true,
        created_at: "2026-06-15T08:01:00Z",
        details: { from: "in_progress", to: "in_review" },
      }),
      // Same issue, but still in the main inbox — must not leak into the
      // archived list (the two lists are mutually exclusive server-side).
      item({
        id: "active-sibling",
        archived: false,
        created_at: "2026-06-15T08:02:00Z",
      }),
    ]);

    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({
      id: "status-notification",
      details: {
        from: "in_progress",
        to: "in_review",
        comment_id: "comment-1",
      },
    });
  });

  it("sorts issues newest-first and groups issue-less items by their own id", () => {
    const merged = deduplicateArchivedInboxItems([
      item({
        id: "quick-create-1",
        type: "quick_create_failed",
        issue_id: null,
        archived: true,
        created_at: "2026-06-15T08:00:00Z",
      }),
      item({
        id: "older-issue",
        archived: true,
        created_at: "2026-06-15T07:00:00Z",
      }),
      item({
        id: "quick-create-2",
        type: "quick_create_failed",
        issue_id: null,
        archived: true,
        created_at: "2026-06-15T09:00:00Z",
      }),
    ]);

    expect(merged.map((i) => i.id)).toEqual([
      "quick-create-2",
      "quick-create-1",
      "older-issue",
    ]);
  });

  // useUnarchiveInbox's optimistic patch flips `archived` to false on the
  // tapped row AND its issue siblings inside the archived cache; this filter
  // is what makes them leave the rendered list at once (mirrors how the main
  // dedup drops optimistically archived rows).
  it("drops rows the optimistic unarchive flipped back to active", () => {
    const cached: InboxItem[] = [
      item({ id: "target", archived: false }),
      item({ id: "sibling", archived: false }),
      item({ id: "other-issue", archived: true }),
    ];

    const merged = deduplicateArchivedInboxItems(cached);

    expect(merged.map((i) => i.id)).toEqual(["other-issue"]);
  });
});

describe("getInboxDisplayTitle", () => {
  it("uses the same stable quota title as web instead of backend fallback copy", () => {
    expect(
      getInboxDisplayTitle(
        item({
          issue_id: null,
          type: "autopilot_quota_exceeded",
          title: "Autopilot quota exceeded (100/100)",
        }),
      ),
    ).toBe("Autopilot run limit reached");
  });

  it("keeps paused autopilot copy on the server fallback path", () => {
    expect(
      getInboxDisplayTitle(
        item({
          issue_id: null,
          type: "autopilot_paused",
          title: "Paused after repeated failures",
        }),
      ),
    ).toBe("Paused after repeated failures");
  });
});

describe("getInboxNavigationTarget", () => {
  it("opens issue-less quota notices in their workspace sheet", () => {
    expect(
      getInboxNavigationTarget(
        item({ issue_id: null, type: "autopilot_quota_exceeded" }),
        "acme",
        "history-1",
      ),
    ).toEqual({
      pathname: "/[workspace]/inbox/[id]",
      params: { workspace: "acme", id: "inbox-1" },
    });
  });

  // RUYI-134: an issue-less quick-create outcome used to return null because
  // only autopilot notices were allowed through. Web renders either outcome in
  // its notification detail pane, so mobile must route both instead of
  // swallowing the tap.
  it("opens issue-less quick-create outcomes in the notification sheet", () => {
    for (const type of [
      "quick_create_failed",
      "quick_create_unconfirmed",
    ] as const) {
      expect(
        getInboxNavigationTarget(
          item({ issue_id: null, type }),
          "acme",
          "history-1",
        ),
      ).toEqual({
        pathname: "/[workspace]/inbox/[id]",
        params: { workspace: "acme", id: "inbox-1" },
      });
    }
  });

  it("preserves canonical issue navigation for an issue-linked failed task", () => {
    expect(
      getInboxNavigationTarget(
        item({ type: "task_failed" }),
        "acme",
        "history-1",
      ),
    ).toMatchObject({
      pathname: "/[workspace]/issue/[id]",
      params: { workspace: "acme", id: "issue-1", h: "history-1" },
    });
  });

  it("opens paused notices in the notification sheet", () => {
    expect(
      getInboxNavigationTarget(
        item({ issue_id: null, type: "autopilot_paused" }),
        "acme",
        "history-1",
      ),
    ).toEqual({
      pathname: "/[workspace]/inbox/[id]",
      params: { workspace: "acme", id: "inbox-1" },
    });
  });
});

describe("getAutopilotQuotaBody", () => {
  it("formats the machine reset timestamp for the device locale", () => {
    const body = getAutopilotQuotaBody(
      item({
        issue_id: null,
        type: "autopilot_quota_exceeded",
        body: "Raw fallback 2026-09-01T00:00:00Z",
        details: {
          limit: "100",
          reset_at: "2026-09-01T00:00:00Z",
          autopilot_title: "Daily triage",
        },
      }),
    );

    expect(body).toContain("Daily triage");
    expect(body).toContain("limit of 100 runs");
    expect(body).not.toContain("2026-09-01T00:00:00Z");
  });

  it("keeps the server fallback when structured facts are incomplete", () => {
    expect(
      getAutopilotQuotaBody(
        item({
          issue_id: null,
          type: "autopilot_quota_exceeded",
          body: "Readable server fallback",
          details: { limit: "100" },
        }),
      ),
    ).toBe("Readable server fallback");
  });
});
