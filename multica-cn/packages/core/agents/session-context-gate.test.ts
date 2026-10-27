// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  AGENT_SESSION_COMPACT_MIN_EFFECTIVE_TOKENS,
  AGENT_SESSION_COMPACT_PCT_DEFAULT,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN,
  agentSessionEffectiveCompactThreshold,
} from "./constants";

// Canonical matrix for the session context gate's threshold arithmetic
// (RUYI-107). The component suite only checks that the inspector renders this
// number; the cases live here.
describe("agentSessionEffectiveCompactThreshold", () => {
  it.each([
    ["the default configuration", 400_000, 80, 320_000],
    // The frozen interlock: 100,000 at 10% works out to 10,000 — under one
    // turn's prompt — so the switch is floored instead.
    ["floored when the arithmetic lands under 50K", 100_000, 10, 50_000],
    ["exact for a ceiling that is not a multiple of 100", 700_001, 75, 525_000],
    ["the maximum ceiling at the default percentage", 2_000_000, 80, 1_600_000],
    // An out-of-range percentage falls back to the default rather than being
    // clamped: clamping a stored 1% up to 10% would still compact almost every
    // turn, whereas the default is the behavior the product specified.
    ["an invalid percentage uses the default", 400_000, 0, 320_000],
    // Only reachable from a row written before the range existed. The floor
    // must not push the soft switch past the hard ceiling, which would leave
    // that agent with no soft stage at all.
    ["the floor never exceeds the ceiling it protects", 20_000, 50, 20_000],
  ])("%s", (_name, ceiling, pct, expected) => {
    expect(agentSessionEffectiveCompactThreshold(ceiling, pct)).toBe(expected);
  });

  // Pins the numbers the product froze rather than whatever the constants
  // happen to say, so widening the range is a deliberate edit here too.
  it("matches the frozen spec", () => {
    expect(AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN).toBe(100_000);
    expect(AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX).toBe(2_000_000);
    expect(AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT).toBe(400_000);
    expect(AGENT_SESSION_COMPACT_PCT_DEFAULT).toBe(80);
    expect(AGENT_SESSION_COMPACT_MIN_EFFECTIVE_TOKENS).toBe(50_000);
  });
});
