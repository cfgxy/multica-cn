/**
 * Recovers the gateway's precise rejection reason after a failed websocket
 * handshake (RUYI-866 P1-3).
 *
 * Browser/desktop sockets carry the session cookie (cookie mode) or a bearer
 * token (token mode), so the gateway's pre-upgrade rejection is a plain HTTP
 * status — a 409 whose `{"code": "VOICE_UNAVAILABLE:<reason>"}` body the
 * WebSocket API cannot read. The client only sees a connection drop with a
 * close code, and every config-class rejection (D2 unconfigured / missing /
 * disabled provider, runtime gates) collapses into one generic "cannot
 * connect" message.
 *
 * The authenticated preflight route runs the same read-only gate chain without
 * creating a session or dialing the provider. Parsing its rejection code
 * through the shared degrade mapping yields the exact reason instead of the
 * generic copy. Mirrors apps/mobile/lib/voice/handshake-reason.ts,
 * which owns the mobile copy (mobile consumes @multica/core, not this
 * package, per its import rules).
 *
 * Safety rails — the probe only ever refines a message, never gates UX:
 *  - credentials ride cookies (credentials: "include") or an explicit
 *    Authorization header; the token never enters the URL.
 *  - a 3 s budget: on timeout the generic copy stands.
 *  - CORS rejection, unparsable body, codeless body, or any fetch failure
 *    returns null so the UI keeps the generic degrade copy.
 */

import { parseVoiceRejectionCode, type VoiceRejection } from "./degrade";

/** Generous for a LAN round-trip; aborting just keeps the generic copy. */
export const VOICE_HANDSHAKE_PROBE_TIMEOUT_MS = 3000;

type VoiceFetchLike = (url: string, init?: RequestInit) => Promise<Response>;

export interface ProbeVoiceHandshakeOptions {
  /** Injectable fetch for tests; defaults to the global fetch. */
  fetchImpl?: VoiceFetchLike;
  /** Bearer header for header-auth clients; null leaves identity to the cookie. */
  authorizationHeader?: string | null;
  timeoutMs?: number;
}

export async function probeVoiceHandshakeRejection(
  wsUrl: string,
  options: ProbeVoiceHandshakeOptions = {},
): Promise<VoiceRejection | null> {
  const fetchImpl = options.fetchImpl ?? fetch;
  // ws:// → http://, wss:// → https://, then select the side-effect-free gate.
  const httpUrl = wsUrl
    .replace(/^ws/, "http")
    .replace(/(\/api\/agents\/[^/?#]+\/voice-session)(?=\?|$)/, "$1/preflight");
  if (httpUrl === wsUrl.replace(/^ws/, "http")) return null;
  const controller = new AbortController();
  const timer = setTimeout(
    () => controller.abort(),
    options.timeoutMs ?? VOICE_HANDSHAKE_PROBE_TIMEOUT_MS,
  );
  try {
    const init: RequestInit = {
      method: "GET",
      credentials: "include",
      signal: controller.signal,
    };
    if (options.authorizationHeader) {
      init.headers = { Authorization: options.authorizationHeader };
    }
    const response = await fetchImpl(httpUrl, init);
    const body = await response.text();
    let code: unknown;
    try {
      code = JSON.parse(body)?.code;
    } catch {
      return null;
    }
    return parseVoiceRejectionCode(typeof code === "string" ? code : null);
  } catch {
    // CORS rejection (origin not allowlisted), timeout, or a network drop —
    // the generic degrade copy is the floor, never a crash.
    return null;
  } finally {
    clearTimeout(timer);
  }
}
