import { describe, expect, it } from "vitest";
import { allowsSubagents, mergeSubagentAllowance } from "./subagent-tools";

describe("allowsSubagents", () => {
  it("uses the provider default for missing, malformed, or non-object configs", () => {
    expect(allowsSubagents(undefined)).toBe(true);
    expect(allowsSubagents(null)).toBe(true);
    expect(allowsSubagents({})).toBe(true);
    expect(allowsSubagents({}, "codex")).toBe(false);
    expect(allowsSubagents("allow_subagents")).toBe(true);
    expect(allowsSubagents("allow_subagents", "codex")).toBe(false);
    expect(allowsSubagents(["allow_subagents"])).toBe(true);
    expect(allowsSubagents({ allow_subagents: "false" })).toBe(true);
  });

  it("only an explicit false denies subagents", () => {
    expect(allowsSubagents({ allow_subagents: true })).toBe(true);
    expect(allowsSubagents({ allow_subagents: false })).toBe(false);
  });

  it("coexists with provider-specific keys like openclaw's", () => {
    expect(
      allowsSubagents({ mode: "gateway", gateway: { host: "gw" }, allow_subagents: true }),
    ).toBe(true);
  });
});

describe("mergeSubagentAllowance", () => {
  it("writes explicit true when allowing without mutating the input", () => {
    const original = { mode: "local", allow_subagents: false };
    const merged = mergeSubagentAllowance(original, true);
    expect(merged).toEqual({ mode: "local", allow_subagents: true });
    expect(original).toEqual({ mode: "local", allow_subagents: false });
    expect(mergeSubagentAllowance(null, true)).toEqual({ allow_subagents: true });
  });

  it("writes explicit false when denying", () => {
    expect(mergeSubagentAllowance({ allow_subagents: true }, false)).toEqual({
      allow_subagents: false,
    });
    expect(mergeSubagentAllowance({ mode: "local", allow_subagents: true }, false)).toEqual({
      mode: "local",
      allow_subagents: false,
    });
  });

  it("denying a config that never had the key adds the override", () => {
    expect(mergeSubagentAllowance({ mode: "local" }, false)).toEqual({
      mode: "local",
      allow_subagents: false,
    });
  });
});
