import type { AgentWebhook } from "../types";

/**
 * Compose a usable absolute URL for an agent webhook.
 *
 * Same resolution order as the autopilot variant (core/autopilots/webhook.ts):
 *  1. webhook.webhook_url — present only when MULTICA_PUBLIC_URL is set on
 *     the server. Authoritative when available.
 *  2. apiBaseUrl + webhook_path — desktop apps and self-host setups where the
 *     server didn't mint an absolute URL but the client knows its API origin.
 *  3. currentOrigin + webhook_path — browser fallback.
 *
 * Returns null when the webhook carries no token / path (non-manager view).
 */
export function buildAgentWebhookUrl(params: {
  webhook: Pick<AgentWebhook, "webhook_token" | "webhook_path" | "webhook_url">;
  apiBaseUrl?: string;
  currentOrigin?: string;
}): string | null {
  const { webhook, apiBaseUrl, currentOrigin } = params;

  if (typeof webhook.webhook_url === "string" && webhook.webhook_url) {
    return webhook.webhook_url;
  }

  const path =
    (typeof webhook.webhook_path === "string" && webhook.webhook_path) ||
    (webhook.webhook_token ? `/api/webhooks/agents/${webhook.webhook_token}` : null);
  if (!path) return null;

  const base = stripTrailingSlash(apiBaseUrl) || stripTrailingSlash(currentOrigin);
  if (!base) return path; // last resort — relative path still works in-browser
  return base + path;
}

/**
 * A recognizable masked URL for viewers who hold no credential fields (the
 * server strips webhook_token / webhook_path / webhook_url for non-managers).
 * The path prefix carries no secret, so it stays readable; the token is the
 * fixed-width mask — identical in spirit to maskAgentWebhookUrl below.
 */
export function maskedAgentWebhookUrlPreview(params: {
  apiBaseUrl?: string;
  currentOrigin?: string;
}): string {
  const base = stripTrailingSlash(params.apiBaseUrl) || stripTrailingSlash(params.currentOrigin);
  return `${base}/api/webhooks/agents/${AGENT_WEBHOOK_URL_MASK}`;
}

/** Fixed-width run — never derived from the token, so the mask leaks no length. */
export const AGENT_WEBHOOK_URL_MASK = "••••••••••••";

/**
 * Mask the secret part of an agent webhook URL for display.
 *
 * Only the trailing token segment is a credential: anyone holding it can fire
 * the agent. The origin and the `/api/webhooks/agents/` prefix carry no
 * secret, so they stay readable. Falls back to the bare mask when the URL has
 * no separable last segment. Intentionally parallel to
 * `maskAutopilotWebhookUrl` — same contract, agent ingress path.
 */
export function maskAgentWebhookUrl(url: string): string {
  const cut = url.lastIndexOf("/");
  if (cut < 0 || cut === url.length - 1) return AGENT_WEBHOOK_URL_MASK;
  return url.slice(0, cut + 1) + AGENT_WEBHOOK_URL_MASK;
}

function stripTrailingSlash(s: string | undefined): string {
  if (!s) return "";
  return s.endsWith("/") ? s.slice(0, -1) : s;
}
