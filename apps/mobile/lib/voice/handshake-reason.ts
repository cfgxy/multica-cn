/**
 * Recovers the gateway's precise rejection reason after a failed websocket
 * handshake (RUYI-449 P2). A logged-in RN socket carries the session cookie,
 * so the gateway takes the header-auth path and fails the upgrade with a
 * plain HTTP status whose body the WebSocket API cannot read — the client
 * only sees a connection drop. The gate chain runs before the upgrade, so a
 * plain GET with the same credentials on the same URL gets the identical
 * rejection JSON; parse it with the shared degrade mapping. Cookies ride
 * automatically (no CORS in RN), the token never enters the URL, and every
 * probe failure returns null so the UI keeps the generic degrade copy.
 */
import {
  parseVoiceRejectionCode,
  type VoiceRejection,
} from "@multica/core/voice";

/** Generous for a LAN round-trip; aborting just keeps the generic copy. */
const PROBE_TIMEOUT_MS = 3000;

type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

export type { FetchLike };

export async function probeHandshakeRejection(
  wsUrl: string,
  fetchImpl: FetchLike = fetch,
): Promise<VoiceRejection | null> {
  // ws:// → http://, wss:// → https:// (the gateway serves both on one port).
  const httpUrl = wsUrl.replace(/^ws/, "http");
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS);
  try {
    const response = await fetchImpl(httpUrl, {
      method: "GET",
      credentials: "include",
      signal: controller.signal,
    });
    const body = await response.text();
    let code: unknown;
    try {
      code = JSON.parse(body)?.code;
    } catch {
      return null;
    }
    return parseVoiceRejectionCode(typeof code === "string" ? code : null);
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}
