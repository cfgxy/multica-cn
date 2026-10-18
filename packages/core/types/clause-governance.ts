import type { PromptProposal } from "./legislation";

export interface ClauseGovernanceTotals {
  injections: number;
  runs: number;
  degraded_rows: number;
  degraded_runs: number;
  clause_tokens: number;
}

export interface ClauseGovernanceClause {
  id: string;
  slug: string;
  title: string;
  status: string;
  latest_version: number;
  bound_scope?: string;
  bound_scope_id?: string;
  injections: number;
  runs: number;
  injected_tokens: number;
  tokens_pending: boolean;
}

export interface ClauseGovernanceMigration {
  total_carriers: number;
  bound_carriers: number;
  by_scope: Record<string, number>;
  by_scope_total: Record<string, number>;
}

export interface ClauseGovernanceOverview {
  workspace_id: string;
  since: string;
  dynamic_injection_disabled: boolean;
  clauses: ClauseGovernanceClause[];
  status_counts: Record<string, number>;
  totals: ClauseGovernanceTotals;
  migration: ClauseGovernanceMigration;
  rollback_suggestions: PromptProposal[];
}

export interface ClauseGovernanceVersion {
  version: number;
  content: string;
  content_sha256: string;
  source: string;
  carrier_scope: string;
  created_by_type: string;
  created_at: string;
}

export interface ClauseGovernanceConditions {
  task_kinds?: string[];
  [key: string]: unknown;
}

export interface ClauseGovernanceBinding {
  id: string;
  scope: string;
  scope_id: string;
  status: string;
  conditions?: ClauseGovernanceConditions | null;
  since_clause_version: number;
  created_at: string;
}

export interface ClauseGovernanceQualityDay {
  day: string;
  clause_version: number;
  finished_runs: number;
  injected_tokens?: number | null;
  run_tokens_median?: number | null;
  attributable_failed_runs: number;
  excluded_failed_runs: number;
}

export interface ClauseGovernanceDetail {
  id: string;
  slug: string;
  title: string;
  status: string;
  latest_version: number;
  versions: ClauseGovernanceVersion[];
  bindings: ClauseGovernanceBinding[];
  proposals: PromptProposal[];
  quality_daily: ClauseGovernanceQualityDay[];
  injections: number;
  runs: number;
  injected_tokens: number;
}

export interface ClauseLayerSetViolation {
  task_id: string;
  clause_id: string;
  clause_version: number;
  carrier_scope: string;
  degraded: boolean;
  created_at: string;
}

export interface ClauseDriftEntry {
  kind: string;
  slug: string;
  marker_rev?: number;
  expected_rev?: number;
}

export interface ClauseDriftCarrier {
  scope: string;
  scope_id: string;
  entries: ClauseDriftEntry[];
}

export interface ClauseGovernanceReconciliation {
  since: string;
  healthy: boolean;
  layer_set_violations: ClauseLayerSetViolation[];
  totals: ClauseGovernanceTotals;
  drift: ClauseDriftCarrier[];
  carriers_scanned: number;
}
