import { describe, expect, it } from "vitest";
import { partitionReferencePreview } from "./reference-preview";

interface Row {
  id: string;
  kind: "issue" | "pr" | "degraded";
}

const row = (id: string, kind: Row["kind"]): Row => ({ id, kind });

describe("partitionReferencePreview", () => {
  it("shows every row when the list fits the preview budget", () => {
    const rows = [row("a", "issue"), row("b", "pr")];
    expect(partitionReferencePreview(rows, (r) => r.kind, 2)).toEqual({
      previews: rows,
      hidden: [],
      hiddenPrCount: 0,
    });
  });

  it("folds the tail beyond two rows and counts folded PRs", () => {
    const rows = [
      row("i1", "issue"),
      row("p1", "pr"),
      row("i2", "issue"),
      row("p2", "pr"),
      row("i3", "issue"),
    ];
    const partition = partitionReferencePreview(rows, (r) => r.kind, 2);
    expect(partition.previews.map((r) => r.id)).toEqual(["i1", "p1"]);
    expect(partition.hidden.map((r) => r.id)).toEqual(["i2", "p2", "i3"]);
    expect(partition.hiddenPrCount).toBe(1);
  });

  it("counts PRs that appear only inside the folded tail", () => {
    const rows = [row("i1", "issue"), row("i2", "issue"), row("p1", "pr")];
    expect(partitionReferencePreview(rows, (r) => r.kind, 2).hiddenPrCount).toBe(1);
    expect(partitionReferencePreview(rows, (r) => r.kind, 2).hidden).toHaveLength(1);
  });

  it("keeps first-occurrence order in both slices", () => {
    const rows = [
      row("p1", "pr"),
      row("d1", "degraded"),
      row("i1", "issue"),
      row("p2", "pr"),
    ];
    const partition = partitionReferencePreview(rows, (r) => r.kind, 2);
    expect(partition.previews.map((r) => r.id)).toEqual(["p1", "d1"]);
    expect(partition.hidden.map((r) => r.id)).toEqual(["i1", "p2"]);
  });

  it("returns empty slices for an empty list", () => {
    expect(partitionReferencePreview([], (r: Row) => r.kind, 2)).toEqual({
      previews: [],
      hidden: [],
      hiddenPrCount: 0,
    });
  });
});
