import type { AgentRuntime, RuntimeProfile } from "@multica/core/types";

/**
 * RUYI-425 §4.3 mobile voice-instance helpers — a semantic re-implementation
 * of the desktop functions in packages/views/runtimes/components/
 * (voice-instance-settings.tsx + voice-instance-create-dialog.tsx). views is
 * outside the mobile import whitelist (apps/mobile/CLAUDE.md), so the logic
 * is mirrored here and pinned by lib/voice-runtime.test.ts against the
 * desktop suites. Any behavior change on desktop must land here too.
 */

/** The credential key this form manages (mirrors the Go handler tests). */
export const VOICE_INSTANCE_CREDENTIAL_KEY = "api_key";

export interface VoiceInstanceSettings {
  model: string;
  advanced: Record<string, unknown> | null;
  disabled: boolean;
}

/** Reads the §4.3 settings out of the instance metadata bag. */
export function readVoiceInstanceSettings(
  metadata: Record<string, unknown> | null | undefined,
): VoiceInstanceSettings {
  const bag = metadata ?? {};
  return {
    model: typeof bag.model === "string" ? bag.model : "",
    advanced:
      bag.advanced && typeof bag.advanced === "object" && !Array.isArray(bag.advanced)
        ? (bag.advanced as Record<string, unknown>)
        : null,
    disabled: bag.disabled === true,
  };
}

export type AdvancedParamsResult =
  | { ok: true; value: Record<string, unknown> }
  | { ok: false; reason: "invalid_json" | "not_object" };

/**
 * Advanced params: JSON-object text → submit value. Empty text clears the
 * stored object; anything that is not a JSON object fails closed — the
 * server would store it, but a scalar/array here is always a user mistake.
 */
export function parseAdvancedParams(text: string): AdvancedParamsResult {
  const trimmed = text.trim();
  if (trimmed === "") return { ok: true, value: {} };
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return { ok: false, reason: "invalid_json" };
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    return { ok: false, reason: "not_object" };
  }
  return { ok: true, value: parsed as Record<string, unknown> };
}

/**
 * Whether the runtime is a voice-protocol instance (§4.2): the server derives
 * `capabilities` from the protocol-family baseline; a missing block (older
 * backend / CLI instance) means "not voice".
 */
export function isVoiceProtocolRuntime(runtime: AgentRuntime): boolean {
  return runtime.capabilities?.realtime_voice === true;
}

/** The profiles the create form offers, derived from Type capabilities. */
export function isVoiceProfile(profile: RuntimeProfile): boolean {
  return profile.capabilities?.realtime_voice === true;
}
