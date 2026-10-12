// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { IssueReference } from "@multica/core/markdown";
import type { GitHubPullRequest, Issue } from "@multica/core/types";

/**
 * Pure row-building for the reference tail (RUYI-635 + RUYI-731).
 *
 * Mirrors the web footer's row union; the fold cut and the sheet grouping it
 * fed were retired with RUYI-731, so only the row union remains here. RN
 * components can't render in the node vitest lane, so the tail's policy
 * lives here where it CAN be tested for real — the component
 * (issue-reference-list.tsx) only resolves, self-filters, and draws.
 */
import { buildReferenceTailRows } from "./issue-reference-rows";

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
