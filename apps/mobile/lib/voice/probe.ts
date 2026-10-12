/**
 * Device-side voice credential connectivity probe (RUYI-626). The direct
 * path dials Google from the user's own network, so the connectivity
 * verdict that matters is the device's — the server-side probe's result
 * describes the server's egress, which is exactly what a deployment may not
 * have (RUYI-603). The outcome is reported to
 * POST /api/runtimes/{id}/credential-probe and feeds the same
 * credential_probe badge the server probe writes.
 *
 * Status semantics extend the server enum with "unreachable" (RUYI-619's
 * boundary: a network verdict is not a credential verdict — the device may
 * simply have no route to Google, e.g. mainland networks). 4xx means the
 * provider saw and rejected the key; a network failure or 5xx says nothing
 * about the key and stays "unreachable", which never gates a session start.
 */
import type { FetchLike } from "./handshake-reason";

export type VoiceProbeStatus = "ok" | "invalid" | "unreachable";

export interface VoiceProbeOutcome {
  status: VoiceProbeStatus;
  httpStatus?: number;
}

/** Public Gemini endpoint the server probe also defaults to (§4.3). */
export const PROVIDER_PROBE_BASE_URL = "https://generativelanguage.googleapis.com";

/** Generous for a cold radio round-trip; aborting means unreachable. */
const PROBE_TIMEOUT_MS = 5000;

export async function probeVoiceCredential(
  apiKey: string,
  fetchImpl: FetchLike = fetch,
  baseUrl: string = PROVIDER_PROBE_BASE_URL,
): Promise<VoiceProbeOutcome> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS);
  try {
    const response = await fetchImpl(`${baseUrl}/v1beta/models`, {
      method: "GET",
      headers: { "x-goog-api-key": apiKey },
      signal: controller.signal,
    });
    if (response.status >= 200 && response.status < 300) {
      return { status: "ok", httpStatus: response.status };
    }
    if (response.status < 500) {
      // 4xx: the provider answered and rejected the request — the key (or
      // its model access) is genuinely bad from this network.
      return { status: "invalid", httpStatus: response.status };
    }
    return { status: "unreachable", httpStatus: response.status };
  } catch {
    return { status: "unreachable" };
  } finally {
    clearTimeout(timer);
  }
}
