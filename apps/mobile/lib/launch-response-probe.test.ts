// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  LAUNCH_PROBE_FAST_ATTEMPTS,
  LAUNCH_PROBE_FAST_DELAY_MS,
  LAUNCH_PROBE_SLOW_DELAY_MS,
  LAUNCH_PROBE_TOTAL_WINDOW_MS,
  launchProbeDelayMs,
  probeLaunchResponse,
  type LaunchProbeClock,
} from "./launch-response-probe";

function virtualClock(): LaunchProbeClock & { elapsed: () => number } {
  let t = 0;
  return {
    now: () => t,
    sleep: async (ms) => {
      t += ms;
    },
    elapsed: () => t,
  };
}

describe("launch response probe policy", () => {
  it("recovers a response inside the fast net", async () => {
    let calls = 0;
    const clock = virtualClock();
    const result = await probeLaunchResponse(async () => {
      calls += 1;
      return calls >= 2 ? { identifier: "n1" } : null;
    }, { clock });
    expect(result.found).toBe(true);
    expect(result.response).toEqual({ identifier: "n1" });
    expect(result.attempts).toBe(2);
    expect(clock.elapsed()).toBe(LAUNCH_PROBE_FAST_DELAY_MS);
  });

  it("keeps probing into the slow tail and recovers a response past the legacy window", async () => {
    // A degraded device answers null for several seconds after the tree is
    // mounted; the response only shows up on the 10th poll.
    let calls = 0;
    const clock = virtualClock();
    const result = await probeLaunchResponse(async () => {
      calls += 1;
      return calls >= 10 ? { identifier: "late" } : null;
    }, { clock });
    expect(result.found).toBe(true);
    expect(result.attempts).toBe(10);
    expect(calls).toBe(10);
    expect(calls).toBeGreaterThan(LAUNCH_PROBE_FAST_ATTEMPTS);
    expect(clock.elapsed()).toBeLessThan(LAUNCH_PROBE_TOTAL_WINDOW_MS);
  });

  it("terminates bounded on a plain open (response never arrives)", async () => {
    let calls = 0;
    const clock = virtualClock();
    const result = await probeLaunchResponse(async () => {
      calls += 1;
      return null;
    }, { clock });
    expect(result.found).toBe(false);
    expect(result.response).toBeNull();
    expect(calls).toBeGreaterThan(LAUNCH_PROBE_FAST_ATTEMPTS);
    // Probing runs to the window and is bounded by it plus one tail step.
    expect(clock.elapsed()).toBeGreaterThanOrEqual(LAUNCH_PROBE_TOTAL_WINDOW_MS);
    expect(clock.elapsed()).toBeLessThanOrEqual(
      LAUNCH_PROBE_TOTAL_WINDOW_MS + LAUNCH_PROBE_SLOW_DELAY_MS,
    );
  });

  it("stops early once the caller reports cancellation", async () => {
    let calls = 0;
    const clock = virtualClock();
    const result = await probeLaunchResponse(async () => {
      calls += 1;
      return null;
    }, { clock, shouldStop: () => calls >= 3 });
    expect(result.found).toBe(false);
    expect(calls).toBeLessThanOrEqual(4);
  });

  it("treats a transient native-bridge throw as a miss and keeps probing", async () => {
    let calls = 0;
    const clock = virtualClock();
    const result = await probeLaunchResponse(async () => {
      calls += 1;
      if (calls < 3) throw new Error("bridge not ready");
      return { identifier: "n2" };
    }, { clock });
    expect(result.found).toBe(true);
    expect(result.attempts).toBe(3);
  });

  it("runs a fast net before switching to the slow tail", () => {
    expect(launchProbeDelayMs(0)).toBe(LAUNCH_PROBE_FAST_DELAY_MS);
    expect(launchProbeDelayMs(LAUNCH_PROBE_FAST_ATTEMPTS - 1)).toBe(
      LAUNCH_PROBE_FAST_DELAY_MS,
    );
    expect(launchProbeDelayMs(LAUNCH_PROBE_FAST_ATTEMPTS)).toBe(
      LAUNCH_PROBE_SLOW_DELAY_MS,
    );
  });
});
