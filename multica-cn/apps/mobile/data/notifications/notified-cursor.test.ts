import { describe, expect, it } from "vitest";
import {
  NOTIFIED_CURSOR_CAPACITY,
  cursorHas,
  cursorRecord,
  cursorStorageKey,
  emptyNotifiedCursor,
  parseNotifiedCursor,
  serializeNotifiedCursor,
} from "./notified-cursor";

function ids(n: number, prefix = "item"): string[] {
  return Array.from({ length: n }, (_, i) => `${prefix}-${i}`);
}

describe("emptyNotifiedCursor / cursorHas", () => {
  it("an empty cursor knows nothing", () => {
    expect(cursorHas(emptyNotifiedCursor(), "item-1")).toBe(false);
  });
});

describe("cursorRecord", () => {
  it("records ids so a second arrival is recognized as seen", () => {
    let cursor = emptyNotifiedCursor();
    expect(cursorHas(cursor, "item-1")).toBe(false);
    cursor = cursorRecord(cursor, ["item-1"]);
    expect(cursorHas(cursor, "item-1")).toBe(true);
  });

  it("does not duplicate an already-recorded id", () => {
    let cursor = cursorRecord(emptyNotifiedCursor(), ["item-1"]);
    cursor = cursorRecord(cursor, ["item-1", "item-2"]);
    expect(cursor.ids.filter((id) => id === "item-1")).toHaveLength(1);
    expect(cursor.ids).toHaveLength(2);
  });

  it("caps growth, evicting the oldest ids first", () => {
    let cursor = emptyNotifiedCursor();
    cursor = cursorRecord(cursor, ids(NOTIFIED_CURSOR_CAPACITY));
    cursor = cursorRecord(cursor, ["fresh-1"]);
    expect(cursor.ids).toHaveLength(NOTIFIED_CURSOR_CAPACITY);
    // Oldest (lowest index, recorded first) evicted…
    expect(cursorHas(cursor, "item-0")).toBe(false);
    expect(cursorHas(cursor, "item-1")).toBe(true);
    // …newest retained.
    expect(cursorHas(cursor, "fresh-1")).toBe(true);
  });
});

describe("cursorStorageKey", () => {
  it("scopes by server and user", () => {
    expect(cursorStorageKey("srv-1", "user-1")).toBe(
      "multica_notified_inbox_ids:srv-1:user-1",
    );
  });
});

describe("parseNotifiedCursor / serializeNotifiedCursor", () => {
  it("round-trips through the serialized form", () => {
    const cursor = cursorRecord(emptyNotifiedCursor(), ["a", "b"]);
    expect(parseNotifiedCursor(serializeNotifiedCursor(cursor))).toEqual(cursor);
  });

  it("tolerates null / undefined / garbage storage values", () => {
    for (const raw of [null, undefined, "", "not json", "42", "[1,2]"]) {
      expect(parseNotifiedCursor(raw as string | null)).toEqual(
        emptyNotifiedCursor(),
      );
    }
  });

  it("drops non-string entries instead of failing the whole cursor", () => {
    const raw = JSON.stringify({ v: 1, ids: ["ok", 5, null, "also-ok"] });
    const cursor = parseNotifiedCursor(raw);
    expect(cursor.ids).toEqual(["ok", "also-ok"]);
  });

  it("rejects oversized stored cursors (defensive against corrupt state)", () => {
    const raw = JSON.stringify({ v: 1, ids: ids(NOTIFIED_CURSOR_CAPACITY + 50) });
    const cursor = parseNotifiedCursor(raw);
    expect(cursor.ids).toHaveLength(NOTIFIED_CURSOR_CAPACITY);
  });
});
