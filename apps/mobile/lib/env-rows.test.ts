import { describe, expect, it } from "vitest";

import {
  envMapToRows,
  envRowsToMap,
  hasDuplicateKeys,
  type EnvRow,
} from "./env-rows";

function row(key: string, value: string, rowId = 0): EnvRow {
  return { rowId, key, value };
}

describe("envMapToRows", () => {
  it("maps each entry to a row with a unique incrementing rowId", () => {
    const rows = envMapToRows({ A: "1", B: "2" });
    expect(rows.map((r) => [r.key, r.value])).toEqual([
      ["A", "1"],
      ["B", "2"],
    ]);
    expect(new Set(rows.map((r) => r.rowId)).size).toBe(rows.length);
  });

  it("rowIds keep increasing across calls (no React key reuse)", () => {
    const first = envMapToRows({ A: "1" });
    const second = envMapToRows({ B: "2" });
    expect(second[0].rowId).toBeGreaterThan(first[0].rowId);
  });

  it("skips blank keys from the payload", () => {
    expect(envMapToRows({ "": "orphan", A: "1" })).toEqual([
      { rowId: expect.any(Number), key: "A", value: "1" },
    ]);
  });

  it("returns empty rows for an empty map", () => {
    expect(envMapToRows({})).toEqual([]);
  });
});

describe("envRowsToMap", () => {
  it("round-trips rows back to the wire map", () => {
    const rows = envMapToRows({ A: "1", B: "2" });
    expect(envRowsToMap(rows)).toEqual({ A: "1", B: "2" });
  });

  it("drops blank-key rows and trims keys (entriesToEnvMap parity)", () => {
    expect(
      envRowsToMap([row("  LOG_LEVEL  ", "debug"), row("   ", "dropped")]),
    ).toEqual({ LOG_LEVEL: "debug" });
  });

  it("keeps empty values — only blank KEYS are dropped", () => {
    expect(envRowsToMap([row("EMPTY", "")])).toEqual({ EMPTY: "" });
  });
});

describe("hasDuplicateKeys", () => {
  it("is false for distinct keys", () => {
    expect(hasDuplicateKeys([row("A", "1"), row("B", "2")])).toBe(false);
  });

  it("flags duplicates after trimming", () => {
    expect(hasDuplicateKeys([row("A", "1"), row(" A ", "2")])).toBe(true);
  });

  it("ignores blank keys when checking (they get dropped on save)", () => {
    expect(hasDuplicateKeys([row("", "1"), row("  ", "2")])).toBe(false);
  });

  it("is false for empty input", () => {
    expect(hasDuplicateKeys([])).toBe(false);
  });
});
