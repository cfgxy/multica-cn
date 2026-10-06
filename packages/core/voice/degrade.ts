/**
 * Voice-session rejection parsing and degrade-message mapping (RUYI-449).
 *
 * The gateway rejects session starts with the stable code
 * `VOICE_UNAVAILABLE:<reason>` — as an HTTP 409 `{"code"}` pre-upgrade
 * (cookie path) or as a websocket error frame post-upgrade (first-frame
 * path). Both shapes funnel through here so every surface shows the same
 * comprehensible degrade message and text sending is never affected.
 */

export type VoiceDegradeReason =
  | "no_voice_runtime"
  | "instance_disabled"
  | "instance_not_active"
  | "capability_mismatch"
  | "credential_missing"
  | "credential_invalid";

export const VOICE_UNAVAILABLE_PREFIX = "VOICE_UNAVAILABLE:";

export type VoiceRejection =
  | { kind: "voice_unavailable"; reason: VoiceDegradeReason }
  | { kind: "auth_failed" }
  | { kind: "provider_unreachable" }
  | { kind: "rejected" };

/**
 * Parses the gateway's stable rejection code. Unknown or empty codes return
 * null — the caller then maps a bare connection drop to the generic
 * "voice is unavailable right now" message.
 */
export function parseVoiceRejectionCode(
  code: string | null | undefined,
): VoiceRejection | null {
  if (!code) return null;
  if (code.startsWith(VOICE_UNAVAILABLE_PREFIX)) {
    const reason = code.slice(VOICE_UNAVAILABLE_PREFIX.length);
    if (isVoiceDegradeReason(reason)) {
      return { kind: "voice_unavailable", reason };
    }
    return { kind: "rejected" };
  }
  if (code === "VOICE_AUTH_FAILED") return { kind: "auth_failed" };
  if (code === "VOICE_PROVIDER_UNREACHABLE") return { kind: "provider_unreachable" };
  if (code === "VOICE_SESSION_REJECTED") return { kind: "rejected" };
  return null;
}

function isVoiceDegradeReason(value: string): value is VoiceDegradeReason {
  return (
    value === "no_voice_runtime" ||
    value === "instance_disabled" ||
    value === "instance_not_active" ||
    value === "capability_mismatch" ||
    value === "credential_missing" ||
    value === "credential_invalid"
  );
}

/**
 * A connect() failure whose rejection the transport already knows. The
 * header-auth upgrade path fails as a plain HTTP status whose body the
 * WebSocket API cannot read; a transport that recovers the code over plain
 * HTTP (RUYI-449 mobile) throws this so the controller degrades with the
 * precise reason instead of the generic message. Null means the failure
 * stays generic.
 */
export class VoiceRejectionError extends Error {
  readonly rejection: VoiceRejection | null;

  constructor(rejection: VoiceRejection | null) {
    super("voice session start rejected");
    this.name = "VoiceRejectionError";
    this.rejection = rejection;
  }
}

/** Unwraps a typed rejection; every other thrown value maps to null. */
export function rejectionFromError(error: unknown): VoiceRejection | null {
  return error instanceof VoiceRejectionError ? error.rejection : null;
}

/**
 * The i18n key for a session-start failure. Auth internals never surface as
 * copy — a bad token reads the same as any other "cannot start" so the UI
 * never leaks authentication detail.
 */
export function voiceFailureMessageKey(rejection: VoiceRejection | null): string {
  if (!rejection) return "voice.failure.connection_failed";
  switch (rejection.kind) {
    case "voice_unavailable":
      return `voice.failure.unavailable.${rejection.reason}`;
    default:
      return "voice.failure.connection_failed";
  }
}
