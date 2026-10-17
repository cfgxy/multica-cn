import { QueryClient } from "@tanstack/react-query";
import type { Issue, IssueReaction, TimelineEntry } from "@multica/core/types";
import {
  effectiveTruncatedKinds,
  type TimelineQueryData,
  type TimelineTruncationKind,
} from "@multica/core/issues/timeline-query";
import { describe, expect, it, vi } from "vitest";

import { issueKeys } from "@/data/queries/issue-keys";
import {
  addCommentReaction,
  addIssueReaction,
  appendTimelineEntry,
  reconcileCreatedComment,
  onIssueAuxiliaryRevision,
  invalidateIssueAfterReconnect,
  patchIssueDetail,
  patchIssueLabels,
  removeIssueReaction,
  replaceCommentTimelineEntry,
} from "./issue-ws-updaters";

function timelineData(
  entries: TimelineEntry[],
  truncatedKinds: TimelineTruncationKind[] = [],
): TimelineQueryData {
  return { entries, truncatedKinds };
}

describe("invalidateIssueAfterReconnect", () => {
  it("invalidates attachments together with the issue and task caches", () => {
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    const wsId = "workspace-1";
    const issueId = "issue-1";

    invalidateIssueAfterReconnect(qc, wsId, issueId);

    expect(invalidate.mock.calls.map(([filters]) => filters?.queryKey)).toEqual([
      issueKeys.detail(wsId, issueId),
      issueKeys.timeline(wsId, issueId),
      issueKeys.attachments(wsId, issueId),
      issueKeys.activeTasks(wsId, issueId),
      issueKeys.tasks(wsId, issueId),
    ]);
  });
});

describe("appendTimelineEntry", () => {
  const wsId = "workspace-1";
  const issueId = "issue-1";

  function timelineEntry(id: string, type: "activity" | "comment" = "activity"): TimelineEntry {
    return {
      type,
      id,
      actor_type: "member",
      actor_id: "member-1",
      created_at: "2026-09-05T09:00:00Z",
    };
  }

  it.each(["activity", "comment"] as const)(
    "keeps the fetch snapshot and invalidates once after a %s cap crossing",
    (type) => {
      const qc = new QueryClient();
      const key = issueKeys.timeline(wsId, issueId);
      const invalidate = vi.spyOn(qc, "invalidateQueries");
      qc.setQueryData<TimelineQueryData>(key, {
        entries: Array.from({ length: 2000 }, (_, index) => timelineEntry(`${type}-${index}`, type)),
        truncatedKinds: [],
      });

      appendTimelineEntry(qc, wsId, issueId, timelineEntry(`${type}-2000`, type));

      expect(qc.getQueryData<TimelineQueryData>(key)).toMatchObject({
        truncatedKinds: [],
      });
      expect(qc.getQueryData<TimelineQueryData>(key)?.entries).toHaveLength(2001);
      expect(
        effectiveTruncatedKinds(qc.getQueryData<TimelineQueryData>(key)),
      ).toEqual([type]);
      expect(invalidate).toHaveBeenCalledTimes(1);

      appendTimelineEntry(qc, wsId, issueId, timelineEntry(`${type}-2000`, type));
      appendTimelineEntry(qc, wsId, issueId, timelineEntry(`${type}-2001`, type));

      expect(invalidate).toHaveBeenCalledTimes(1);
      expect(
        effectiveTruncatedKinds(qc.getQueryData<TimelineQueryData>(key)),
      ).toEqual([type]);
    },
  );

  it.each(["activity", "comment"] as const)(
    "does not invalidate when a %s append reaches exactly 2000 entries",
    (type) => {
      const qc = new QueryClient();
      const key = issueKeys.timeline(wsId, issueId);
      const invalidate = vi.spyOn(qc, "invalidateQueries");
      qc.setQueryData<TimelineQueryData>(
        key,
        timelineData(
          Array.from({ length: 1999 }, (_, index) =>
            timelineEntry(`${type}-${index}`, type),
          ),
        ),
      );

      appendTimelineEntry(qc, wsId, issueId, timelineEntry(`${type}-1999`, type));

      expect(invalidate).not.toHaveBeenCalled();
      expect(
        effectiveTruncatedKinds(qc.getQueryData<TimelineQueryData>(key)),
      ).toEqual([]);
    },
  );

  it.each(["activity", "comment"] as const)(
    "preserves an existing %s truncation header without another invalidate",
    (type) => {
      const qc = new QueryClient();
      const key = issueKeys.timeline(wsId, issueId);
      const invalidate = vi.spyOn(qc, "invalidateQueries");
      qc.setQueryData<TimelineQueryData>(
        key,
        timelineData(
          Array.from({ length: 2000 }, (_, index) =>
            timelineEntry(`${type}-${index}`, type),
          ),
          [type],
        ),
      );

      appendTimelineEntry(qc, wsId, issueId, timelineEntry(`${type}-2000`, type));

      expect(invalidate).not.toHaveBeenCalled();
      expect(qc.getQueryData<TimelineQueryData>(key)?.truncatedKinds).toEqual([type]);
      expect(
        effectiveTruncatedKinds(qc.getQueryData<TimelineQueryData>(key)),
      ).toEqual([type]);
    },
  );

  it("does not materialize a timeline cache from a realtime entry", () => {
    const qc = new QueryClient();
    const key = issueKeys.timeline(wsId, issueId);

    appendTimelineEntry(qc, wsId, issueId, timelineEntry("activity-1"));

    expect(qc.getQueryData<TimelineQueryData>(key)).toBeUndefined();
  });
});

describe("reconcileCreatedComment", () => {
  const wsId = "workspace-1";
  const issueId = "issue-1";

  function commentEntry(id: string, overrides: Partial<TimelineEntry> = {}): TimelineEntry {
    return {
      type: "comment",
      id,
      actor_type: "member",
      actor_id: "member-1",
      content: "same text",
      parent_id: null,
      created_at: "2026-09-05T09:00:00Z",
      ...overrides,
    };
  }

  it("replaces the pending row when the realtime event arrives before the response", () => {
    const qc = new QueryClient();
    const key = issueKeys.timeline(wsId, issueId);
    qc.setQueryData<TimelineQueryData>(key, timelineData([
      commentEntry("optimistic-1"),
    ]));

    reconcileCreatedComment(qc, wsId, issueId, commentEntry("comment-1"));
    reconcileCreatedComment(qc, wsId, issueId, commentEntry("comment-1"), "optimistic-1");

    expect(qc.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-1",
    ]);
    qc.clear();
  });

  it("keeps the response row when the realtime event arrives after the response", () => {
    const qc = new QueryClient();
    const key = issueKeys.timeline(wsId, issueId);
    qc.setQueryData<TimelineQueryData>(key, timelineData([
      commentEntry("optimistic-1"),
    ]));

    reconcileCreatedComment(qc, wsId, issueId, commentEntry("comment-1"), "optimistic-1");
    reconcileCreatedComment(qc, wsId, issueId, commentEntry("comment-1"));

    expect(qc.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-1",
    ]);
    qc.clear();
  });

  it("removes only the matching optimistic row when the server row already exists", () => {
    const qc = new QueryClient();
    const key = issueKeys.timeline(wsId, issueId);
    qc.setQueryData<TimelineQueryData>(key, timelineData([
      commentEntry("comment-1"),
      commentEntry("optimistic-1"),
      commentEntry("optimistic-2"),
    ]));

    reconcileCreatedComment(qc, wsId, issueId, commentEntry("comment-1"), "optimistic-1");

    expect(qc.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-1",
      "optimistic-2",
    ]);
    qc.clear();
  });

  it("reconciles identical responses by optimistic id and preserves failed rows", () => {
    const qc = new QueryClient();
    const key = issueKeys.timeline(wsId, issueId);
    qc.setQueryData<TimelineQueryData>(key, timelineData([
      commentEntry("optimistic-failed"),
      commentEntry("optimistic-1"),
      commentEntry("optimistic-2"),
    ]));

    const failedIds = new Set(["optimistic-failed"]);
    reconcileCreatedComment(
      qc,
      wsId,
      issueId,
      commentEntry("comment-1"),
      "optimistic-1",
      failedIds,
    );
    reconcileCreatedComment(
      qc,
      wsId,
      issueId,
      commentEntry("comment-2"),
      "optimistic-2",
      failedIds,
    );

    const ids = qc.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id);
    expect(ids).toHaveLength(3);
    expect(ids).toEqual(expect.arrayContaining([
      "optimistic-failed",
      "comment-1",
      "comment-2",
    ]));
    qc.clear();
  });

  it("keeps an ambiguous realtime success while identical sends are pending", () => {
    const qc = new QueryClient();
    const key = issueKeys.timeline(wsId, issueId);
    qc.setQueryData<TimelineQueryData>(key, timelineData([
      commentEntry("optimistic-a"),
      commentEntry("optimistic-b"),
    ]));

    reconcileCreatedComment(qc, wsId, issueId, commentEntry("comment-b"));
    expect(qc.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "optimistic-a",
      "optimistic-b",
    ]);
    reconcileCreatedComment(qc, wsId, issueId, commentEntry("comment-a"), "optimistic-a");
    reconcileCreatedComment(qc, wsId, issueId, commentEntry("comment-b"), "optimistic-b");

    expect(qc.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-a",
      "comment-b",
    ]);
    qc.clear();
  });

  it("reconciles an optimistic id even when the server normalizes its content", () => {
    const qc = new QueryClient();
    const key = issueKeys.timeline(wsId, issueId);
    qc.setQueryData<TimelineQueryData>(key, timelineData([
      commentEntry("optimistic-1", { content: "hello\u0000" }),
    ]));

    reconcileCreatedComment(
      qc,
      wsId,
      issueId,
      commentEntry("comment-1", { content: "hello" }),
      "optimistic-1",
    );

    expect(qc.getQueryData<TimelineQueryData>(key)?.entries).toEqual([
      commentEntry("comment-1", { content: "hello" }),
    ]);
    qc.clear();
  });

  it("matches a normalized realtime comment to its pending entry", () => {
    const qc = new QueryClient();
    const key = issueKeys.timeline(wsId, issueId);
    qc.setQueryData<TimelineQueryData>(key, timelineData([
      commentEntry("optimistic-1", { content: "hello\u0000" }),
    ]));

    reconcileCreatedComment(
      qc,
      wsId,
      issueId,
      commentEntry("comment-1", { content: "hello" }),
    );

    expect(qc.getQueryData<TimelineQueryData>(key)?.entries).toEqual([
      commentEntry("comment-1", { content: "hello" }),
    ]);
    qc.clear();
  });

  it("does not reconcile comments from another author or reply thread", () => {
    const qc = new QueryClient();
    const key = issueKeys.timeline(wsId, issueId);
    qc.setQueryData<TimelineQueryData>(key, timelineData([
      commentEntry("optimistic-author"),
      commentEntry("optimistic-reply", { parent_id: "parent-1" }),
    ]));

    reconcileCreatedComment(
      qc,
      wsId,
      issueId,
      commentEntry("comment-other-author", { actor_id: "member-2" }),
    );
    reconcileCreatedComment(
      qc,
      wsId,
      issueId,
      commentEntry("comment-other-parent", { parent_id: "parent-2" }),
    );

    expect(qc.getQueryData<TimelineQueryData>(key)?.entries.map(({ id }) => id)).toEqual([
      "comment-other-author",
      "comment-other-parent",
      "optimistic-author",
      "optimistic-reply",
    ]);
    qc.clear();
  });
});

describe("mobile issue revision gates", () => {
  const wsId = "workspace-1";
  const issueId = "issue-1";

  it("rejects stale issue snapshots and accepts a newer one", () => {
    const qc = new QueryClient();
    qc.setQueryData<Issue>(issueKeys.detail(wsId, issueId), {
      id: issueId,
      title: "revision 5",
      revision: 5,
    } as Issue);

    patchIssueDetail(qc, wsId, { id: issueId, title: "stale", revision: 4 });
    expect(qc.getQueryData<Issue>(issueKeys.detail(wsId, issueId))?.title).toBe("revision 5");

    patchIssueDetail(qc, wsId, { id: issueId, title: "revision 6", revision: 6 });
    expect(qc.getQueryData<Issue>(issueKeys.detail(wsId, issueId))).toMatchObject({
      title: "revision 6",
      revision: 6,
    });
  });

  it("invalidates instead of blind-merging an unversioned event over versioned state", () => {
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    qc.setQueryData<Issue>(issueKeys.detail(wsId, issueId), {
      id: issueId,
      title: "known",
      revision: 5,
    } as Issue);

    patchIssueDetail(qc, wsId, { id: issueId, title: "unknown order" });

    expect(qc.getQueryData<Issue>(issueKeys.detail(wsId, issueId))?.title).toBe("known");
    expect(invalidate).toHaveBeenCalledWith({ queryKey: issueKeys.detail(wsId, issueId) });
  });

  it("keeps comment content and reactions monotonic", () => {
    const qc = new QueryClient();
    const key = issueKeys.timeline(wsId, issueId);
    const current = {
      type: "comment",
      id: "comment-1",
      actor_type: "member",
      actor_id: "member-1",
      created_at: "2026-01-01T00:00:00Z",
      content: "revision 5",
      revision: 5,
      reactions: [],
    } as TimelineEntry;
    qc.setQueryData<TimelineQueryData>(key, timelineData([current]));

    replaceCommentTimelineEntry(qc, wsId, issueId, {
      ...current,
      content: "stale",
      revision: 4,
    });
    addCommentReaction(qc, wsId, issueId, "comment-1", {
      id: "reaction-1",
      comment_id: "comment-1",
      actor_type: "member",
      actor_id: "member-1",
      emoji: "👍",
      created_at: "2026-01-01T00:00:00Z",
    }, 4);
    expect(qc.getQueryData<TimelineQueryData>(key)?.entries[0]).toMatchObject({
      content: "revision 5",
      revision: 5,
      reactions: [],
    });

    replaceCommentTimelineEntry(qc, wsId, issueId, {
      ...current,
      content: "revision 6",
      revision: 6,
    });
    expect(qc.getQueryData<TimelineQueryData>(key)?.entries[0]).toMatchObject({
      content: "revision 6",
      revision: 6,
    });
  });

  it("preserves hydrated actor identity when replacing a comment snapshot", () => {
    const qc = new QueryClient();
    const key = issueKeys.timeline(wsId, issueId);
    qc.setQueryData<TimelineQueryData>(key, timelineData([
      {
        type: "comment",
        id: "comment-1",
        actor_type: "member",
        actor_id: "departed-user",
        actor_name: "Former Member",
        actor_avatar_url: "https://profiles.example.com/former.png",
        created_at: "2026-01-01T00:00:00Z",
        content: "before",
        revision: 1,
      },
    ]));

    replaceCommentTimelineEntry(qc, wsId, issueId, {
      type: "comment",
      id: "comment-1",
      actor_type: "member",
      actor_id: "departed-user",
      created_at: "2026-01-01T00:00:00Z",
      content: "after",
      revision: 2,
    });

    expect(qc.getQueryData<TimelineQueryData>(key)?.entries[0]).toMatchObject({
      content: "after",
      actor_name: "Former Member",
      actor_avatar_url: "https://profiles.example.com/former.png",
    });
  });

  it("invalidates after a partial owner event without replacing the full snapshot revision", () => {
    const qc = new QueryClient();
    qc.setQueryData<Issue>(issueKeys.detail(wsId, issueId), {
      id: issueId,
      revision: 2,
    } as Issue);
    onIssueAuxiliaryRevision(qc, wsId, issueId, 7);
    expect(qc.getQueryData<Issue>(issueKeys.detail(wsId, issueId))?.revision).toBe(2);
    expect(qc.getQueryState(issueKeys.detail(wsId, issueId))?.isInvalidated).toBe(true);
  });

  it("accepts a full r2 snapshot after a revision-only r3 event", () => {
    const qc = new QueryClient();
    const key = issueKeys.detail(wsId, issueId);
    qc.setQueryData<Issue>(key, {
      id: issueId,
      title: "A",
      revision: 1,
    } as Issue);

    onIssueAuxiliaryRevision(qc, wsId, issueId, 3);
    patchIssueDetail(qc, wsId, { id: issueId, title: "B", revision: 2 });

    expect(qc.getQueryData<Issue>(key)).toMatchObject({
      title: "B",
      revision: 2,
    });
    expect(qc.getQueryState(key)?.isInvalidated).toBe(true);
  });

  it("applies auxiliary labels without claiming they are a full issue snapshot", () => {
    const qc = new QueryClient();
    const key = issueKeys.detail(wsId, issueId);
    qc.setQueryData<Issue>(key, {
      id: issueId,
      labels: [],
      revision: 1,
    } as unknown as Issue);

    patchIssueLabels(qc, wsId, issueId, [{
      id: "label-1",
      workspace_id: wsId,
      name: "Latest",
      color: "#000000",
      created_at: "2026-08-17T00:00:00Z",
      updated_at: "2026-08-17T00:00:00Z",
    }], 3);

    expect(qc.getQueryData<Issue>(key)).toMatchObject({
      revision: 1,
      labels: [{ id: "label-1" }],
    });
    expect(qc.getQueryState(key)?.isInvalidated).toBe(true);
  });

  it("rejects a delayed labels event after a newer labels projection", () => {
    const qc = new QueryClient();
    const key = issueKeys.detail(wsId, issueId);
    qc.setQueryData<Issue>(key, {
      id: issueId,
      labels: [],
      revision: 1,
    } as unknown as Issue);

    patchIssueLabels(qc, wsId, issueId, [{
      id: "label-latest",
      workspace_id: wsId,
      name: "Latest",
      color: "#000000",
      created_at: "2026-08-18T00:00:00Z",
      updated_at: "2026-08-18T00:00:00Z",
    }], 3);
    patchIssueLabels(qc, wsId, issueId, [{
      id: "label-stale",
      workspace_id: wsId,
      name: "Stale",
      color: "#000000",
      created_at: "2026-08-17T00:00:00Z",
      updated_at: "2026-08-17T00:00:00Z",
    }], 2);

    expect(qc.getQueryData<Issue>(key)?.labels).toMatchObject([
      { id: "label-latest" },
    ]);
  });

  it("does not let a newer generic event block the first labels projection", () => {
    const qc = new QueryClient();
    const key = issueKeys.detail(wsId, issueId);
    qc.setQueryData<Issue>(key, {
      id: issueId,
      labels: [],
      revision: 1,
    } as unknown as Issue);

    onIssueAuxiliaryRevision(qc, wsId, issueId, 4);
    patchIssueLabels(
      qc,
      wsId,
      issueId,
      [
        {
          id: "label-3",
          workspace_id: wsId,
          name: "Independent",
          color: "#000000",
          created_at: "2026-08-18T00:00:00Z",
          updated_at: "2026-08-18T00:00:00Z",
        },
      ],
      3,
    );

    expect(qc.getQueryData<Issue>(key)?.labels).toMatchObject([
      { id: "label-3" },
    ]);
  });

  it("rejects a delayed issue-reaction add after a newer removal", () => {
    const qc = new QueryClient();
    const key = issueKeys.detail(wsId, issueId);
    const reaction: IssueReaction = {
      id: "reaction-1",
      issue_id: issueId,
      actor_type: "member",
      actor_id: "member-1",
      emoji: "👍",
      created_at: "2026-08-17T00:00:00Z",
    };
    qc.setQueryData<Issue>(key, {
      id: issueId,
      reactions: [reaction],
      revision: 1,
    } as unknown as Issue);

    removeIssueReaction(qc, wsId, issueId, reaction.emoji, reaction.actor_id, 3);
    addIssueReaction(qc, wsId, issueId, reaction, 2);

    expect(qc.getQueryData<Issue>(key)?.reactions).toEqual([]);
  });
});
