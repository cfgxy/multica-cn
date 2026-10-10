/**
 * Maps a failed REST voice-session start onto the degrade vocabulary
 * (RUYI-626). A 409 from the §4.4 gate carries the stable
 * `VOICE_UNAVAILABLE:<reason>` code in the response body's `code` field and
 * maps to the specific degrade copy; anything else (network down, 401,
 * 5xx) maps to null → the generic connection-failure copy. Auth internals
 * are never surfaced. Pure — the lib vitest lane pins the mapping without
 * loading the data layer (body narrowed instead of an ApiError instanceof,
 * same decoupling as instance-delete/task-retry).
 */
import { parseVoiceRejectionCode, type VoiceRejection } from "@multica/core/voice";

export function voiceRejectionFromApiError(err: unknown): VoiceRejection | null {
  const body = (err as { body?: { code?: unknown } } | null)?.body;
  const code = body && typeof body === "object" ? body.code : undefined;
  if (typeof code === "string") {
    const rejection = parseVoiceRejectionCode(code);
    if (rejection) return rejection;
  }
  return null;
}
