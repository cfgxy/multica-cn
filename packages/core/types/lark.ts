/** A Lark Bot installation bound to a single Multica agent.
 *
 * Wire shape mirrors `LarkInstallationResponse` in
 * `server/internal/handler/lark.go`. New fields the backend adds in the
 * future MUST default to optional so older desktop builds keep parsing
 * the response — see CLAUDE.md → API Response Compatibility. */
export interface LarkInstallation {
  id: string;
  workspace_id: string;
  agent_id: string;
  app_id: string;
  tenant_key?: string | null;
  bot_open_id: string;
  installer_user_id: string;
  status: "active" | "revoked" | string;
  installed_at: string;
  created_at: string;
  updated_at: string;
}

export interface ListLarkInstallationsResponse {
  installations: LarkInstallation[];
  /** Whether the deployment has the at-rest secret key configured. When
   * false the Bind button must be disabled and the panel renders an
   * empty / "ask the operator to enable Lark" state. */
  configured: boolean;
}

export interface StartLarkInstallResponse {
  /** Absolute Lark OAuth authorization URL. Empty when `configured`
   * is false — the UI should render the QR / open-link controls only
   * when this is set. */
  url?: string;
  /** False when MULTICA_LARK_OAUTH_* env vars are not configured.
   * Distinct from `ListLarkInstallationsResponse.configured` (which
   * tracks the at-rest key, MULTICA_LARK_SECRET_KEY) — a deployment
   * can have the latter set for the manual-paste path without
   * configuring OAuth. */
  configured: boolean;
}

export interface RedeemLarkBindingTokenResponse {
  workspace_id: string;
  installation_id: string;
  lark_open_id: string;
}
