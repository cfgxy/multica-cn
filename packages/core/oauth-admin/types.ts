// OAuth management types (RUYI-420). These mirror the /api/admin/oauth
// and /api/oauth/grants responses; parsing happens in api/schemas.ts, so
// the types here stay pure data shapes.

export interface AdminOAuthClient {
  id: string;
  client_id: string;
  name: string;
  redirect_uris: string[];
  created_by: string | null;
  created_at: string;
  /** When the current secret hash was written; null = original secret. */
  secret_updated_at: string | null;
  /** Soft-disable timestamp; null = enabled. */
  disabled_at: string | null;
  /** Users who ever authorized this client. */
  grant_count: number;
  /** Authorizations still live. */
  active_grants: number;
  /** Most recent gate-confirmed use across the client's grants. */
  last_used_at: string | null;
}

export interface AdminOAuthClientList {
  clients: AdminOAuthClient[];
}

export interface AdminOAuthGrant {
  id: string;
  client_id: string;
  client_name: string | null;
  user_id: string;
  user_name: string | null;
  user_email: string | null;
  scope: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
}

export interface AdminOAuthGrantList {
  grants: AdminOAuthGrant[];
}

/** One row of the Settings "my authorizations" list. */
export interface MyOAuthGrant {
  id: string;
  client_id: string;
  client_name: string | null;
  scope: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
}

export interface MyOAuthGrantList {
  grants: MyOAuthGrant[];
}

/** The one-shot create/rotate responses — the only places a secret exists. */
export interface OAuthClientSecretReveal {
  secret: string;
  /** rotate only: when the new hash was written. */
  secret_updated_at?: string;
  /** create only: the client row as the list renders it. */
  client?: AdminOAuthClient;
}

export interface AdminMCPStatus {
  oauth: {
    enabled: boolean;
    issuer: string;
    key_id: string;
    authorization_endpoint: string;
    token_endpoint: string;
    jwks_url: string;
    protected_resource_url: string;
  };
  mcp: {
    url_configured: boolean;
    reachable: boolean;
    version: string;
    tool_count: number;
    tools: Array<{ name: string; description: string }>;
  };
  clients: { total: number; active: number };
  grants: { total: number; active: number };
}

/** GET /auth/oauth/consent/{id} — what the consent confirmation page renders. */
export interface OAuthConsentInfo {
  client_id: string;
  client_name: string;
  scopes: string[];
}

/** approve/deny consent — where to send the browser next. */
export interface OAuthRedirectResponse {
  redirect: string;
}
