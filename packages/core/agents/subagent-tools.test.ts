import { describe, expect, it } from "vitest";
import { allowsSubagents, mergeSubagentAllowance } from "./subagent-tools";

describe("allowsSubagents", () => {
  it("is fail-closed for missing, malformed, or non-object configs", () => {
    expect(allowsSubagents(undefined)).toBe(false);
    expect(allowsSubagents(null)).toBe(false);
    expect(allowsSubagents({})).toBe(false);
    expect(allowsSubagents("allow_subagents")).toBe(false);
    expect(allowsSubagents(["allow_subagents"])).toBe(false);
    expect(allowsSubagents({ allow_subagents: "true" })).toBe(false);
  });

  it("only an explicit true allows subagents", () => {
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
  it("sets the key on a non-object config without mutating the input", () => {
    const original = { mode: "local" };
    const merged = mergeSubagentAllowance(original, true);
    expect(merged).toEqual({ mode: "local", allow_subagents: true });
    expect(original).toEqual({ mode: "local" });
    expect(mergeSubagentAllowance(null, true)).toEqual({ allow_subagents: true });
  });

  it("removes the key when denying so absent equals the default", () => {
    expect(mergeSubagentAllowance({ allow_subagents: true }, false)).toEqual({});
    expect(mergeSubagentAllowance({ mode: "local", allow_subagents: true }, false)).toEqual({
      mode: "local",
    });
  });

  it("denying a config that never had the key is a no-op", () => {
    expect(mergeSubagentAllowance({ mode: "local" }, false)).toEqual({ mode: "local" });
  });
});
