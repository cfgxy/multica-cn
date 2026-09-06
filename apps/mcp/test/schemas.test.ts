import { describe, expect, it } from "vitest";

import {
  optionalBoolean,
  optionalEnum,
  optionalInt,
  optionalString,
  requireString,
  ToolInputError,
} from "../src/schemas.js";

describe("requireString", () => {
  it("returns trimmed non-empty strings", () => {
    expect(requireString({ a: "  x " }, "a")).toBe("x");
  });

  it("rejects missing, empty and non-string values", () => {
    expect(() => requireString({}, "a")).toThrow(ToolInputError);
    expect(() => requireString({ a: "  " }, "a")).toThrow(ToolInputError);
    expect(() => requireString({ a: 5 }, "a")).toThrow(ToolInputError);
  });

  it("enforces max length", () => {
    expect(() => requireString({ a: "x".repeat(11) }, "a", { maxLength: 10 }))
      .toThrow(/maximum length/);
  });
});

describe("optionalString", () => {
  it("treats null, undefined and empty as absent", () => {
    expect(optionalString({}, "a")).toBeUndefined();
    expect(optionalString({ a: null }, "a")).toBeUndefined();
    expect(optionalString({ a: "" }, "a")).toBeUndefined();
  });

  it("rejects non-string values", () => {
    expect(() => optionalString({ a: 7 }, "a")).toThrow(ToolInputError);
  });
});

describe("optionalInt", () => {
  it("accepts integers within bounds", () => {
    expect(optionalInt({ n: 5 }, "n", { min: 1, max: 10 })).toBe(5);
    expect(optionalInt({ n: undefined }, "n")).toBeUndefined();
  });

  it("rejects floats and out-of-bounds values", () => {
    expect(() => optionalInt({ n: 1.5 }, "n")).toThrow(ToolInputError);
    expect(() => optionalInt({ n: 0 }, "n", { min: 1 })).toThrow(ToolInputError);
    expect(() => optionalInt({ n: 11 }, "n", { max: 10 })).toThrow(ToolInputError);
  });
});

describe("optionalBoolean", () => {
  it("passes booleans through and rejects strings", () => {
    expect(optionalBoolean({ b: true }, "b")).toBe(true);
    expect(() => optionalBoolean({ b: "true" }, "b")).toThrow(ToolInputError);
  });
});

describe("optionalEnum", () => {
  const STATUSES = ["todo", "in_progress", "done"] as const;

  it("accepts listed values", () => {
    expect(optionalEnum({ s: "todo" }, "s", STATUSES)).toBe("todo");
    expect(optionalEnum({}, "s", STATUSES)).toBeUndefined();
  });

  it("rejects unlisted values with the allowed list", () => {
    expect(() => optionalEnum({ s: "archived" }, "s", STATUSES)).toThrow(
      /must be one of: todo, in_progress, done/,
    );
  });
});
