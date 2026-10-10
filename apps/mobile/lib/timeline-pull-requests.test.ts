// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { GitHubPullRequest } from "@multica/core/types";
import type { TimelineRow } from "./timeline-thread";
import { DECISION_BATCH_BAR_ID, interleaveDecisions } from "./timeline-decisions";
import {
  PULL_REQUEST_ITEM_ID_PREFIX,
  interleavePullRequests,
  isPullRequestItem,
} from "./timeline-pull-requests";

function row(id: string, createdAt: string): TimelineRow {
  return {
    entry: {
      id,
      type: "comment",
      created_at: createdAt,
      actor_type: "member",
      actor_id: "u-1",
    } as TimelineRow["entry"],
    replies: [],
  };
}

function pr(id: string, prCreatedAt: string): GitHubPullRequest {
  return {
    id,
    workspace_id: "ws-1",
    repo_owner: "cfgxy",
    repo_name: "multica-cn",
    number: 313,
    title: `fix: ${id}`,
    state: "open",
    html_url: `https://github.com/cfgxy/multica-cn/pull/313-${id}`,
    branch: "agent/x",
    author_login: "gu-guyu",
    author_avatar_url: null,
    merged_at: null,
    closed_at: null,
    pr_created_at: prCreatedAt,
    pr_updated_at: prCreatedAt,
  };
}

function ids(items: ReturnType<typeof interleavePullRequests>): string[] {
  return items.map((item) => item.entry.id);
}

describe("interleavePullRequests", () => {
  it("returns the items untouched when there are no pull requests", () => {
    const rows = [row("c-1", "2026-10-02T03:00:00Z"), row("c-2", "2026-10-02T04:00:00Z")];
    expect(interleavePullRequests(rows, [])).toBe(rows);
  });

  it("places each card at its pr_created_at position among the items", () => {
    const items = interleavePullRequests(
      [row("c-1", "2026-10-02T03:00:00Z"), row("c-2", "2026-10-02T05:00:00Z")],
      [pr("pr-1", "2026-10-02T04:00:00Z")],
    );
    expect(ids(items)).toEqual(["c-1", `pull-request-pr-1`, "c-2"]);
  });

  it("breaks timestamp ties in favour of the existing item", () => {
    const at = "2026-10-02T04:00:00Z";
    const items = interleavePullRequests([row("c-1", at)], [pr("pr-1", at)]);
    expect(ids(items)).toEqual(["c-1", "pull-request-pr-1"]);
  });

  it("keeps multiple cards ordered by pr_created_at regardless of input order", () => {
    const items = interleavePullRequests(
      [row("c-1", "2026-10-02T03:00:00Z")],
      [pr("pr-2", "2026-10-02T05:00:00Z"), pr("pr-1", "2026-10-02T02:00:00Z")],
    );
    expect(ids(items)).toEqual([
      "pull-request-pr-1",
      "c-1",
      "pull-request-pr-2",
    ]);
  });

  it("never inserts after the trailing decision batch bar (RUYI-534)", () => {
    // A PR linked AFTER the last open decision is newer than everything,
    // yet the bar must stay the very last row of the feed.
    const rows = [
      row("c-1", "2026-10-02T03:00:00Z"),
      row("c-2", "2026-10-02T06:00:00Z"),
    ];
    const decisions = interleaveDecisions(rows, [
      { ...decision("d-1", "2026-10-02T04:00:00Z") },
      { ...decision("d-2", "2026-10-02T05:00:00Z") },
    ]);
    expect(decisions.at(-1)?.entry.id).toBe(DECISION_BATCH_BAR_ID);

    const merged = interleavePullRequests(decisions, [
      pr("pr-1", "2026-10-02T09:00:00Z"),
    ]);
    expect(ids(merged)).toEqual([
      "c-1",
      "d-1",
      "d-2",
      "c-2",
      "pull-request-pr-1",
      DECISION_BATCH_BAR_ID,
    ]);
  });

  it("emits pull-request items with a minimal ordering surface", () => {
    const source = pr("pr-1", "2026-10-02T04:00:00Z");
    const [item] = interleavePullRequests([], [source]);
    expect(isPullRequestItem(item)).toBe(true);
    expect(item.entry).toEqual({
      id: `${PULL_REQUEST_ITEM_ID_PREFIX}pr-1`,
      created_at: "2026-10-02T04:00:00Z",
    });
    if (isPullRequestItem(item)) expect(item.pullRequest).toBe(source);
    // Comment-only consumers (threading, locate, geometry) discriminate on
    // the `decision` field and entry.type — neither may match a PR item.
    expect("decision" in item).toBe(false);
    expect("type" in item.entry).toBe(false);
  });

  it("composes after interleaveDecisions without re-sorting cards or rows", () => {
    const rows = [row("c-1", "2026-10-02T03:00:00Z"), row("c-2", "2026-10-02T07:00:00Z")];
    const withDecisions = interleaveDecisions(rows, [
      decision("d-1", "2026-10-02T05:00:00Z"),
    ]);
    const merged = interleavePullRequests(withDecisions, [
      pr("pr-1", "2026-10-02T06:00:00Z"),
    ]);
    expect(ids(merged)).toEqual([
      "c-1",
      "d-1",
      "pull-request-pr-1",
      "c-2",
    ]);
  });
});

function decision(id: string, createdAt: string) {
  return {
    id,
    issue_id: "i-1",
    source_comment_id: null,
    question: "q",
    options: [{ label: "A" }, { label: "B" }],
    multi_select: false,
    recommended_indices: [],
    status: "open" as const,
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "agent" as const,
    created_by_id: "a-1",
    created_at: createdAt,
    updated_at: createdAt,
  };
}
