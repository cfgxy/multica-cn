/**
 * Self-evolution model-service config types (RUYI-551).
 *
 * One workspace-scoped LLM endpoint (base URL + key + model) behind the
 * module's scoring and retrospective consumers. The wire type obeys the
 * module's honesty rules: the stored API key never leaves the server — reads
 * carry only `has_api_key` — and `resolved` names what a run would use right
 * now (module config over deploy default) rather than what was saved last.
 */

/** Where the effective configuration comes from; "" when unconfigured. */
export type SelfEvolutionConfigSource = "module_config" | "deploy_default" | "";

/**
 * The four configured states a consumer can be in: configured-and-validated,
 * nothing found, validation failed (module-config source only), or switched
 * off by the workspace.
 */
export type SelfEvolutionConfigStatus =
  | "ok"
  | "unconfigured"
  | "error"
  | "disabled";

/** The workspace's own stored override; the API key is never echoed. */
export interface SelfEvolutionModelConfigOverride {
  base_url: string;
  model: string;
  has_api_key: boolean;
  scoring_enabled: boolean;
  /** Set only after an explicit validation ran against this override. */
  last_validated_at?: string;
  last_validation_ok?: boolean;
  last_validation_error?: string;
}

/** What a scoring or retrospective run would resolve to right now. */
export interface SelfEvolutionModelConfigResolved {
  status: SelfEvolutionConfigStatus;
  source: SelfEvolutionConfigSource;
  model?: string;
}

export interface SelfEvolutionModelConfig {
  override: SelfEvolutionModelConfigOverride | null;
  resolved: SelfEvolutionModelConfigResolved;
  scoring_enabled: boolean;
  /**
   * Whether the deployment configured the per-workspace encryption key.
   * `false` means saves are refused (503) and every workspace falls back to
   * the deploy-wide default — the config card says so instead of failing.
   */
  encryption_ready: boolean;
}

/** Classified validation failure, masked server-side (never a raw error). */
export interface SelfEvolutionConfigValidation {
  ok: boolean;
  error_kind: string;
  message: string;
  validated_at?: string;
}

/** Save payload; an empty `api_key` keeps the stored key. */
export interface SelfEvolutionModelConfigSave {
  base_url: string;
  api_key: string;
  model: string;
  scoring_enabled?: boolean;
}
