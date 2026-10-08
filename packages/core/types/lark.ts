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
  /** Which Lark cloud the bot lives on: "feishu" (mainland) or "lark"
   * (international). Auto-detected at install time. Optional so an older
   * desktop build parsing a newer server — or a newer build hitting a
   * server that predates the field — defaults to Feishu in the UI
   * (see CLAUDE.md → API Response Compatibility). */
  region?: "feishu" | "lark" | string;
  installed_at: string;
  created_at: string;
  updated_at: string;
  /** Per-capability permission verdicts from the latest probe (RUYI-400).
   * Empty array = the bot has never been probed (installed before this
   * feature). Null = the stored verdicts could not be read right now —
   * the UI shows a visible retry hint, never a silent blank (RUYI-545).
   * Undefined = the server predates the field — the UI renders nothing
   * rather than a fake "all good". Optional per the compat rule above. */
  capabilities?: LarkCapabilityState[] | null;
}

/** One capability's probe verdict for a Lark bot installation (RUYI-400).
 * `granted` means the synthetic API call succeeded RIGHT NOW; `missing`
 * means Feishu rejected it with a permission error (covers both "fix in
 * console" and "awaiting enterprise-admin approval"); `unknown` means the
 * probe itself could not run (token/transport) and must never be shown
 * as granted. Wire shape mirrors `lark.CapabilityStateView`. */
export interface LarkCapabilityState {
  capability: string;
  status: "granted" | "missing" | "unknown" | string;
  detail?: string;
  /** The scopes this capability needs (AND-of-OR groups, flattened by
   * the server into the sorted union when missing). Optional for older
   * servers. */
  required_scopes?: string[];
  checked_at?: string;
}

/** One row of the static capability→scope catalog served by
 * GET /lark/permission-catalog (RUYI-400). `scopes` keeps the AND-of-OR
 * shape: every inner group needs at least one of its members granted in
 * the Feishu console. */
export interface LarkPermissionCatalogEntry {
  id: string;
  /** Whether the backend can probe this capability over REST. Event-
   * subscription capabilities (receive_messages) are judged by live
   * message arrival instead, so their UI copy differs. */
  probeable: boolean;
  scopes: string[][];
}

export interface LarkPermissionCatalogResponse {
  capabilities: LarkPermissionCatalogEntry[];
}

/** Response of POST /lark/installations/{id}/recheck-permissions:
 * fresh verdicts for every capability, display-ordered. */
export interface RecheckLarkPermissionsResponse {
  capabilities: LarkCapabilityState[];
}

export interface ListLarkInstallationsResponse {
  installations: LarkInstallation[];
  /** Whether the deployment has the at-rest secret key configured. When
   * false the Bind button must be disabled and the panel renders an
   * empty / "ask the operator to enable Lark" state. */
  configured: boolean;
  /** Whether new installs via the device-flow scan-to-bind path can
   * complete end-to-end — i.e. the device-flow RegistrationService is
   * wired AND the real Lark HTTP APIClient (not the no-op stub) is in
   * place. When false the install entry points are hidden and the
   * panel surfaces a "coming soon" notice. Optional so older desktop
   * builds receiving a server that does not yet emit the field
   * default to `undefined`, treated as not supported. */
  install_supported?: boolean;
}

/** First half of the device-flow install: the server has opened a
 * registration session against accounts.feishu.cn and returned the QR
 * URL. The frontend renders `qr_code_url` as a QR (and as a clickable
 * link fallback) and starts polling `/install/{session_id}/status` at
 * the supplied cadence until success or terminal failure. */
export interface BeginLarkInstallResponse {
  session_id: string;
  qr_code_url: string;
  expires_in_seconds: number;
  poll_interval_seconds: number;
}

/** Status polling result. `status` is the discriminator. */
export interface LarkInstallStatusResponse {
  status: "pending" | "success" | "error" | string;
  /** Populated when status === "success". The frontend invalidates the
   * installations cache so the new row appears in the Settings tab. */
  installation_id?: string;
  /** Stable code on error — switch on this (NOT error_message) to pick
   * the right copy. Common values: "expired", "access_denied",
   * "lark_protocol_error", "bot_info_failed", "installation_conflict",
   * "installer_bind_failed", "internal_error". */
  error_reason?: string;
  /** Human-readable error tail for debugging; the production UI should
   * surface the copy keyed off error_reason and use this only as a
   * diagnostic tooltip. */
  error_message?: string;
}

export interface RedeemLarkBindingTokenResponse {
  workspace_id: string;
  installation_id: string;
  lark_open_id: string;
}
