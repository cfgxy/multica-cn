// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { IssueReference } from "@multica/core/markdown";
import type { GitHubPullRequest, Issue } from "@multica/core/types";

/**
 * Pure row-building + grouping for the reference tail (RUYI-635 增量).
 *
 * Mirrors the web footer's row union and popover grouping; the fold cut
 * itself comes from `partitionReferencePreview` in core, so both ends fold
 * at the same row with the same counts. RN components can't render in the
 * node vitest lane, so the tail's policy lives here where it CAN be tested
 * for real — the component (issue-reference-list.tsx) only resolves,
 * partitions, and draws.
 */
import {
  buildReferenceTailRows,
  groupReferenceTailRows,
  shouldCloseSheetOnDragRelease,
} from "./issue-reference-rows";

const UUID = "1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed";
const UUID2 = "8f14e45f-ceea-4670-9b1c-0d3e4f5a6b7c";

function issue(id: string, identifier: string): Issue {
  return { id, identifier, title: `Issue ${identifier}` } as unknown as Issue;
}

function pr(number: number, state = "open"): GitHubPullRequest {
  return {
    id: `pr-${number}`,
    repo_owner: "cfgxy",
    repo_name: "multica-cn",
    number,
    state,
    title: `Pull request #${number}`,
    html_url: `https://github.com/cfgxy/multica-cn/pull/${number}`,
  } as unknown as GitHubPullRequest;
}

function mention(ref: string, label: string): IssueReference {
  return { ref, label, form: "mention" };
}

function bare(ref: string): IssueReference {
  return { ref, label: null, form: "identifier" };
}

function prRef(token: string): IssueReference {
  return { ref: token, label: null, form: "pull_request" };
}

describe("buildReferenceTailRows", () => {
  it("zips resolutions positionally into issue / pr / degraded rows", () => {
    const rows = buildReferenceTailRows(
      [
        bare("MUL-1"),
        prRef("cfgxy/multica-cn#338"),
        bare("MUL-404"),
      ],
      [issue(UUID, "MUL-1"), pr(338, "merged"), null],
    );

    expect(rows.map((r) => r.kind)).toEqual(["issue", "pr", "degraded"]);
    expect(rows[2]).toEqual({
      kind: "degraded",
      key: "identifier:MUL-404",
      fallback: "MUL-404",
    });
  });

  it("collapses a UUID mention and a bare identifier of the same issue into one row", () => {
    const mul7 = issue(UUID, "MUL-7");
    const rows = buildReferenceTailRows(
      [bare("MUL-7"), mention(UUID, "MUL-7")],
      [mul7, mul7],
    );

    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ kind: "issue", key: "identifier:MUL-7" });
  });

  it("collapses two tokens resolving to the same PR", () => {
    const same = pr(338, "open");
    const rows = buildReferenceTailRows(
      [prRef("cfgxy/multica-cn#338"), prRef("cfgxy/multica-cn#338")],
      [same, same],
    );

    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ kind: "pr", key: "pull_request:cfgxy/multica-cn#338" });
  });

  it("keeps first-appearance order across kinds", () => {
    const rows = buildReferenceTailRows(
      [
        prRef("cfgxy/multica-cn#341"),
        bare("MUL-2"),
        prRef("cfgxy/multica-cn#338"),
      ],
      [pr(341), null, pr(338)],
    );

    expect(rows.map((r) => r.key)).toEqual([
      "pull_request:cfgxy/multica-cn#341",
      "identifier:MUL-2",
      "pull_request:cfgxy/multica-cn#338",
    ]);
  });
});

describe("groupReferenceTailRows", () => {
  it("groups into tasks and pull requests only when both kinds mix", () => {
    const rows = buildReferenceTailRows(
      [bare("MUL-1"), prRef("cfgxy/multica-cn#338"), bare("MUL-404")],
      [issue(UUID, "MUL-1"), pr(338, "merged"), null],
    );
    const groups = groupReferenceTailRows(rows);

    expect(groups.grouped).toBe(true);
    expect(groups.tasks.map((r) => r.kind)).toEqual(["issue"]);
    expect(groups.prs.map((r) => r.kind)).toEqual(["pr"]);
    // Degraded rows never join a group — plain-text slots after them.
    expect(groups.degraded.map((r) => r.kind)).toEqual(["degraded"]);
  });

  it("keeps a single-kind list ungrouped", () => {
    const rows = buildReferenceTailRows(
      [bare("MUL-1"), bare("MUL-2")],
      [issue(UUID, "MUL-1"), issue(UUID2, "MUL-2")],
    );
    const groups = groupReferenceTailRows(rows);

    expect(groups.grouped).toBe(false);
    expect(groups.tasks).toHaveLength(2);
    expect(groups.prs).toHaveLength(0);
  });
});

describe("shouldCloseSheetOnDragRelease", () => {
  it("closes when the release point travels past the distance threshold", () => {
    expect(shouldCloseSheetOnDragRelease(80, 0)).toBe(true);
  });

  it("closes on a downward fling even from a short drag", () => {
    expect(shouldCloseSheetOnDragRelease(12, 0.8)).toBe(true);
  });

  it("stays open below both thresholds", () => {
    expect(shouldCloseSheetOnDragRelease(40, 0.1)).toBe(false);
  });

  it("never closes on an upward drag or upward fling", () => {
    expect(shouldCloseSheetOnDragRelease(-120, 0)).toBe(false);
    expect(shouldCloseSheetOnDragRelease(8, -2)).toBe(false);
  });
});
