import { describe, expect, it } from "vitest";

import { redactTokens, redactUnknown } from "../src/redact.js";

const MUL = `mul_${"a1b2c3d4e5".repeat(4)}`;
const MAT = `mat_${"0123456789abcdef".repeat(2) + "0123abcd"}`;
const MCN = `mcn_${"f0e1d2c3b4".repeat(4)}`;

describe("redactTokens", () => {
  it("masks a personal access token inside arbitrary text", () => {
    const out = redactTokens(`Bearer ${MUL} rejected`);
    expect(out).not.toContain(MUL);
    expect(out).toBe("Bearer mul_***redacted*** rejected");
  });

  it("masks task and cloud node tokens too", () => {
    expect(redactTokens(MAT)).not.toContain(MAT);
    expect(redactTokens(MCN)).not.toContain(MCN);
  });

  it("masks uppercase variants", () => {
    const upper = MUL.toUpperCase();
    expect(redactTokens(upper)).not.toContain(upper);
  });

  it("leaves normal text untouched", () => {
    const text = "issue RUYI-82 updated to in_progress";
    expect(redactTokens(text)).toBe(text);
  });

  it("does not mask a wrong-length suffix", () => {
    const short = "mul_12345";
    expect(redactTokens(short)).toBe(short);
  });
});

describe("redactUnknown", () => {
  it("redacts Error messages", () => {
    const err = new Error(`request failed for token ${MUL}`);
    const out = redactUnknown(err);
    expect(out).not.toContain(MUL);
    expect(out.startsWith("request failed for token")).toBe(true);
  });

  it("stringifies non-string values", () => {
    expect(redactUnknown(42)).toBe("42");
  });
});
