/**
 * Voice-session websocket URL building (RUYI-449).
 *
 * The gateway route sits outside the Auth middleware group, so the workspace
 * rides the upgrade URL's query string (the same contract realtime hub
 * clients already follow) and identity rides the session cookie (desktop/web)
 * or the first websocket frame (mobile). The token is never a query
 * parameter — it would leak into proxy and CDN logs.
 */

export interface VoiceSessionUrlOptions {
  workspaceId?: string | null;
  workspaceSlug?: string | null;
}

/** http(s):// → ws(s)://. String munging, no WHATWG URL (Hermes lacks it). */
export function toWebSocketBase(apiUrl: string): string {
  return apiUrl.replace(/\/+$/, "").replace(/^http/, "ws");
}

export function buildVoiceSessionUrl(
  apiUrl: string,
  agentId: string,
  opts: VoiceSessionUrlOptions = {},
): string {
  const base = `${toWebSocketBase(apiUrl)}/api/agents/${encodeURIComponent(agentId)}/voice-session`;
  const workspace = opts.workspaceId
    ? `workspace_id=${encodeURIComponent(opts.workspaceId)}`
    : opts.workspaceSlug
      ? `workspace_slug=${encodeURIComponent(opts.workspaceSlug)}`
      : null;
  return workspace ? `${base}?${workspace}` : base;
}

/**
 * Direct-mode (RUYI-626) provider endpoint: the same v1beta BidiGenerateContent
 * route the gateway dials (demo LiveProtocol.kt). The provider API key never
 * rides this URL — direct transports pass it as the connect's x-goog-api-key
 * header, mirroring the gateway's dial.
 */
export const PROVIDER_VOICE_WS_PATH =
  "/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent";

export const DEFAULT_PROVIDER_VOICE_WS_BASE = "wss://generativelanguage.googleapis.com";

export function buildProviderVoiceSessionUrl(
  base: string = DEFAULT_PROVIDER_VOICE_WS_BASE,
): string {
  return base.replace(/\/+$/, "") + PROVIDER_VOICE_WS_PATH;
}
