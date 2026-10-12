// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { IssueReference } from "@multica/core/markdown";
import type { GitHubPullRequest, Issue } from "@multica/core/types";

/**
 * Pure row-building + folding + grouping for the reference tail (RUYI-635
 * 增量 + RUYI-731).
 *
 * Mirrors the web footer's row union; RUYI-731 (Owner correction) kept the
 * fold as mobile-only policy, so the preview cut and the sheet grouping live
 * here too. RN components can't render in the node vitest lane, so the
 * tail's policy is tested for real here — the component
 * (issue-reference-list.tsx) only resolves and draws.
 */
import {
  buildReferenceTailRows,
  groupReferenceTailRows,
  partitionReferencePreview,
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

describe("buildReferenceTailRows self-filter (RUYI-731)", () => {
  it("drops a row that resolves to the current issue", () => {
    const rows = buildReferenceTailRows(
      [mention(UUID, "MUL-7")],
      [issue(UUID, "MUL-7")],
      UUID,
    );

    expect(rows).toHaveLength(0);
  });

  it("drops only the self row and keeps the rest", () => {
    const rows = buildReferenceTailRows(
      [mention(UUID, "MUL-7"), bare("MUL-9")],
      [issue(UUID, "MUL-7"), issue(UUID2, "MUL-9")],
      UUID,
    );

    expect(rows.map((r) => r.key)).toEqual(["identifier:MUL-9"]);
  });

  it("keeps every row when there is no current-issue context", () => {
    const rows = buildReferenceTailRows(
      [mention(UUID, "MUL-7")],
      [issue(UUID, "MUL-7")],
      null,
    );

    expect(rows).toHaveLength(1);
  });
});

describe("partitionReferencePreview", () => {
  it("shows every row when the list fits the preview budget", () => {
    const rows = buildReferenceTailRows(
      [bare("MUL-1"), prRef("cfgxy/multica-cn#338")],
      [issue(UUID, "MUL-1"), pr(338)],
    );
    expect(partitionReferencePreview(rows)).toEqual({
      previews: rows,
      hidden: [],
      hiddenPrCount: 0,
    });
  });

  it("folds the tail beyond two rows and counts folded PRs", () => {
    const rows = buildReferenceTailRows(
      [
        bare("MUL-1"),
        prRef("cfgxy/multica-cn#338"),
        bare("MUL-2"),
        prRef("cfgxy/multica-cn#341"),
        bare("MUL-3"),
      ],
      [issue(UUID, "MUL-1"), pr(338), issue(UUID2, "MUL-2"), pr(341), null],
    );
    const partition = partitionReferencePreview(rows);
    expect(partition.previews.map((r) => r.kind)).toEqual(["issue", "pr"]);
    expect(partition.hidden.map((r) => r.kind)).toEqual([
      "issue",
      "pr",
      "degraded",
    ]);
    expect(partition.hiddenPrCount).toBe(1);
  });

  it("counts PRs that appear only inside the folded tail", () => {
    const rows = buildReferenceTailRows(
      [bare("MUL-1"), bare("MUL-2"), prRef("cfgxy/multica-cn#338")],
      [issue(UUID, "MUL-1"), issue(UUID2, "MUL-2"), pr(338)],
    );
    const partition = partitionReferencePreview(rows);
    expect(partition.hidden).toHaveLength(1);
    expect(partition.hiddenPrCount).toBe(1);
  });

  it("returns empty slices for an empty list", () => {
    expect(partitionReferencePreview([])).toEqual({
      previews: [],
      hidden: [],
      hiddenPrCount: 0,
    });
  });

  it("never surfaces a self-filtered row: the fold runs on post-filter rows", () => {
    const rows = buildReferenceTailRows(
      [
        mention(UUID, "MUL-7"),
        bare("MUL-9"),
        bare("MUL-11"),
        bare("MUL-12"),
      ],
      [issue(UUID, "MUL-7"), issue(UUID2, "MUL-9"), issue("u3", "MUL-11"), issue("u4", "MUL-12")],
      UUID,
    );
    const partition = partitionReferencePreview(rows);
    // The self row is gone BEFORE the cut: the two other issues fill the
    // preview and only the third folds — the self row consumed no slot.
    expect(partition.previews.map((r) => r.kind)).toEqual(["issue", "issue"]);
    expect(partition.hidden).toHaveLength(1);
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
