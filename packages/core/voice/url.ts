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
