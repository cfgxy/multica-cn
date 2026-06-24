import { describe, expect, it } from "vitest";
import { bucketFor, inPercent } from "./hash";

describe("feature-flags hash", () => {
  it("bucketFor returns a value in [0, 100)", () => {
    for (const id of ["a", "b", "user-1", "user-2", "", "🦄"]) {
      const b = bucketFor("flag", id);
      expect(b).toBeGreaterThanOrEqual(0);
      expect(b).toBeLessThan(100);
    }
  });

  it("bucketFor is deterministic for the same (key, id)", () => {
    const first = bucketFor("billing_new_invoice", "user-42");
    for (let i = 0; i < 100; i++) {
      expect(bucketFor("billing_new_invoice", "user-42")).toBe(first);
    }
  });

  it("separator prevents key/id boundary collisions", () => {
    // ("ab","c") and ("a","bc") must not hash to the same bucket.
    expect(bucketFor("ab", "c")).not.toBe(bucketFor("a", "bc"));
  });

  // Pinned (key, identifier) -> bucket values that MUST agree with the
  // Go-side server/pkg/featureflag/hash_test.go::TestPercentBucketCrossLanguageGolden.
  // The shared golden table is the single source of truth for "same user,
  // same bucket" across backend and frontend; if either side drifts, both
  // tests fail and one must be brought back in sync.
  it("cross-language golden: bucket values match the Go side exactly", () => {
    const cases: ReadonlyArray<[string, string, number]> = [
      ["billing_new_invoice", "user-42", 97],
      ["feature_a", "user-1", 50],
      ["checkout_algo", "u-7f8a", 11],
      ["ws_rollout", "workspace-1", 62],
      ["empty_id_flag", "", 83],
    ];
    for (const [key, id, want] of cases) {
      expect(bucketFor(key, id)).toBe(want);
    }
  });

  it("inPercent clamps boundary values", () => {
    expect(inPercent("any", "any", 0)).toBe(false);
    expect(inPercent("any", "any", -10)).toBe(false);
    expect(inPercent("any", "any", 100)).toBe(true);
    expect(inPercent("any", "any", 999)).toBe(true);
  });

  it("inPercent splits a 50% rollout roughly in half across 1000 users", () => {
    // 50% over 1000 distinct users should land near 500; we allow a
    // generous +/- 100 window so the test isn't flaky.
    let enabled = 0;
    for (let i = 0; i < 1000; i++) {
      if (inPercent("split", `user-${i.toString(36)}`, 50)) enabled++;
    }
    expect(enabled).toBeGreaterThan(400);
    expect(enabled).toBeLessThan(600);
  });
});
