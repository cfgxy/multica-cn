import { describe, expect, it } from "vitest";

import { readRuntimeBackpressure } from "./backpressure";

describe("readRuntimeBackpressure", () => {
  it("returns null when metadata is missing or has no backpressure key", () => {
    expect(readRuntimeBackpressure(undefined)).toBeNull();
    expect(readRuntimeBackpressure({})).toBeNull();
    expect(readRuntimeBackpressure({ cli_version: "0.4.10" })).toBeNull();
  });

  it("parses a full report", () => {
    const bp = readRuntimeBackpressure({
      backpressure: {
        active: true,
        reason: "mem+swap",
        mem_available_pct: 8.4,
        swap_used_pct: 82.1,
        psi_some_avg10: 62.5,
        psi_read_ok: true,
        deferred_claims: 12,
        recorded_at: "2026-10-03T06:00:00Z",
      },
    });
    expect(bp).toEqual({
      active: true,
      reason: "mem+swap",
      memAvailablePct: 8.4,
      swapUsedPct: 82.1,
      psiSomeAvg10: 62.5,
      psiReadOK: true,
      deferredClaims: 12,
      recordedAt: "2026-10-03T06:00:00Z",
    });
  });

  it("returns null for malformed shapes", () => {
    expect(readRuntimeBackpressure({ backpressure: "active" })).toBeNull();
    expect(readRuntimeBackpressure({ backpressure: null })).toBeNull();
    expect(readRuntimeBackpressure({ backpressure: { reason: "mem" } })).toBeNull();
    expect(
      readRuntimeBackpressure({ backpressure: { active: "yes" } }),
    ).toBeNull();
  });

  it("tolerates optional fields with defaults", () => {
    const bp = readRuntimeBackpressure({ backpressure: { active: false } });
    expect(bp).toEqual({
      active: false,
      reason: "",
      memAvailablePct: 0,
      swapUsedPct: 0,
      psiSomeAvg10: 0,
      psiReadOK: false,
      deferredClaims: 0,
      recordedAt: "",
    });
  });

  it("ignores non-numeric watermark fields instead of trusting them", () => {
    const bp = readRuntimeBackpressure({
      backpressure: {
        active: true,
        mem_available_pct: "8.4",
        swap_used_pct: null,
        psi_some_avg10: "62.5",
        psi_read_ok: "yes",
        deferred_claims: "many",
      },
    });
    expect(bp?.memAvailablePct).toBe(0);
    expect(bp?.swapUsedPct).toBe(0);
    expect(bp?.psiSomeAvg10).toBe(0);
    expect(bp?.psiReadOK).toBe(false);
    expect(bp?.deferredClaims).toBe(0);
  });
});
