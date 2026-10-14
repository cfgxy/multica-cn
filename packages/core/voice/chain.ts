/**
 * Pure voice-chain helpers (RUYI-749) — no api-client imports, so the
 * mobile app can share them without dragging the web client into its
 * bundle ("mirror, don't import" applies to the client, not pure logic).
 */

/**
 * The slice of workspace.settings the voice chain reads, decoded
 * defensively: settings is a shared bag written by many features, and a
 * foreign malformed "voice" key must not take the settings tab down —
 * same tolerance as the server's read path.
 */
export function parseWorkspaceVoiceSettings(
  settings: Record<string, unknown> | null | undefined,
): { default: null | { provider_id: string; model?: string; advanced?: Record<string, unknown> }; asr: null | { provider_id: string; model?: string; advanced?: Record<string, unknown> } } {
  const voice = settings?.["voice"];
  if (!voice || typeof voice !== "object") {
    return { default: null, asr: null };
  }
  const readRef = (ref: unknown) => {
    if (!ref || typeof ref !== "object") return null;
    const providerId = (ref as { provider_id?: unknown }).provider_id;
    if (typeof providerId !== "string" || !providerId) return null;
    const model = (ref as { model?: unknown }).model;
    const advanced = (ref as { advanced?: unknown }).advanced;
    return {
      provider_id: providerId,
      model: typeof model === "string" && model ? model : undefined,
      advanced:
        advanced && typeof advanced === "object" && !Array.isArray(advanced)
          ? (advanced as Record<string, unknown>)
          : undefined,
    };
  };
  return {
    default: readRef((voice as { default?: unknown }).default),
    asr: readRef((voice as { asr?: unknown }).asr),
  };
}

/**
 * Whether a pooled provider can serve the resolution chain today (workspace
 * default, ASR, or an agent override picker): on, speaking a protocol the
 * gateway serves (gemini), carrying a usable key, and not probe-rejected.
 * Shared by the settings tab and the agent inspector so every picker offers
 * the same set the server's chain gate would actually accept. Takes a
 * minimal structural slice — both the core type and mobile's mirrored
 * VoiceProvider satisfy it, so the rule itself stays single-sourced.
 */
export function voiceProviderChainEligible(p: {
  provider_type: string;
  enabled: boolean;
  credential_status: string;
  credential_probe?: { status?: string | null } | null;
}): boolean {
  return (
    p.provider_type === "gemini" &&
    p.enabled &&
    p.credential_status === "configured" &&
    p.credential_probe?.status !== "invalid"
  );
}

/**
 * Decodes the free-text "advanced parameters" textarea of a voice chain
 * editor: empty → null (no value), a JSON object → the object, anything
 * else → "invalid" (caller blocks the save and surfaces the error). Shared
 * by the workspace settings tab and the agent inspector.
 */
export function parseVoiceAdvancedDraft(
  raw: string,
): Record<string, unknown> | null | "invalid" {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  try {
    const parsed: unknown = JSON.parse(trimmed);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return "invalid";
    return parsed as Record<string, unknown>;
  } catch {
    return "invalid";
  }
}
