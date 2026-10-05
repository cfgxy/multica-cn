/**
 * Cold-start launch-response probe policy (RUYI-415 retest).
 *
 * A tap on a notification that cold-starts the app delivers its response
 * through the native bridge, which answers `getLastNotificationResponseAsync()`
 * with null until that module finishes initializing. On a slow or degraded
 * device the init can lag seconds behind expo-router's tree mount, so a
 * short post-ready probe window burns out and the launch response is
 * dropped permanently — the tap lands on the default screen with zero
 * user feedback (the silent-tap symptom reported by QA on a degrading
 * emulator; the same class of failure as the D3 round-2 fixed window).
 *
 * Policy: a fast 6×500ms net right after readiness (unchanged — covers the
 * common case within ~3s), then a 2s-cadence tail that keeps probing until
 * a 30s total window closes. A plain app open (no launch response) just
 * probes a handful of extra times into the tail — nothing user-visible
 * depends on giving up early, so the longer window is strictly safer.
 * A transient throw from the native bridge (module not yet registered) is
 * treated as a miss, not a probe-loop crash.
 */
export const LAUNCH_PROBE_FAST_ATTEMPTS = 6;
export const LAUNCH_PROBE_FAST_DELAY_MS = 500;
export const LAUNCH_PROBE_SLOW_DELAY_MS = 2_000;
export const LAUNCH_PROBE_TOTAL_WINDOW_MS = 30_000;

export function launchProbeDelayMs(attempt: number): number {
  return attempt < LAUNCH_PROBE_FAST_ATTEMPTS
    ? LAUNCH_PROBE_FAST_DELAY_MS
    : LAUNCH_PROBE_SLOW_DELAY_MS;
}

export type LaunchProbeClock = {
  now: () => number;
  sleep: (ms: number) => Promise<void>;
};

export const realtimeClock: LaunchProbeClock = {
  now: () => Date.now(),
  sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
};

export type LaunchProbeResult<T> = {
  found: boolean;
  response: T | null;
  attempts: number;
};

export async function probeLaunchResponse<T>(
  fetchResponse: () => Promise<T | null>,
  opts: { clock?: LaunchProbeClock; shouldStop?: () => boolean } = {},
): Promise<LaunchProbeResult<T>> {
  const clock = opts.clock ?? realtimeClock;
  const start = clock.now();
  for (let attempt = 0; ; attempt++) {
    if (opts.shouldStop?.()) {
      return { found: false, response: null, attempts: attempt };
    }
    let response: T | null = null;
    try {
      response = await fetchResponse();
    } catch {
      // Native bridge not ready yet — same as a null answer.
    }
    if (response) {
      return { found: true, response, attempts: attempt + 1 };
    }
    if (clock.now() - start >= LAUNCH_PROBE_TOTAL_WINDOW_MS) {
      return { found: false, response: null, attempts: attempt + 1 };
    }
    await clock.sleep(launchProbeDelayMs(attempt));
  }
}
