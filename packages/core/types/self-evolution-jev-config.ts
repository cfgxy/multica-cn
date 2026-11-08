/** Workspace-scoped JEV advisory configuration. */
export type SelfEvolutionJevStatus = "unconfigured" | "offline" | "available";
export type SelfEvolutionJevSource = "unconfigured" | "workspace" | "deployment";

/**
 * Safe JEV status returned by the server. `has_api_key` is intentionally the
 * only credential detail the browser receives.
 */
export interface SelfEvolutionJevConfig {
  status: SelfEvolutionJevStatus;
  source: SelfEvolutionJevSource;
  endpoint: string;
  model: string;
  timeout_ms: number;
  has_api_key: boolean;
  last_checked_at?: string;
  last_check_ok?: boolean;
  last_check_error?: string;
  /** Runtime engine/version returned by the JEV endpoint. */
  engine?: string;
  encryption_ready: boolean;
}

/** An empty API key retains an existing encrypted credential. */
export interface SelfEvolutionJevConfigSave {
  endpoint: string;
  api_key: string;
  model: string;
  timeout_ms: number;
}
