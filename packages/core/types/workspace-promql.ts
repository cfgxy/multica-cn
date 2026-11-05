export type PromQLRuleMetric = "failures" | "failure_rate";
export type PromQLRuleGrouping = "workspace" | "agent";
export type PromQLRuleCondition = "threshold" | "present";
export type PromQLRuleComparison = ">" | ">=" | "<" | "<=";
export type PromQLRulePolicy = "keep" | "trigger" | "resolve";

export interface PromQLRuleSample {
  labels?: Record<string, string>;
  value: number;
}

export interface PromQLRuleState {
  firing: boolean;
  pending_since?: string;
  last_evaluated_at?: string;
  last_error?: string;
  values?: PromQLRuleSample[];
}

export interface WorkspacePromQLRule {
  id: string;
  name: string;
  enabled: boolean;
  metric: PromQLRuleMetric;
  grouping: PromQLRuleGrouping;
  condition: PromQLRuleCondition;
  comparison?: PromQLRuleComparison;
  threshold?: number;
  recovery_threshold?: number;
  lookback_seconds: number;
  evaluation_seconds: number;
  for_seconds: number;
  no_data_policy: PromQLRulePolicy;
  query_error_policy: PromQLRulePolicy;
  state: PromQLRuleState;
}

export type WorkspacePromQLRuleSave = Omit<WorkspacePromQLRule, "state">;

export interface WorkspacePromQLRules {
  rules: WorkspacePromQLRule[];
  prometheus_configured: boolean;
}

export interface WorkspacePromQLRulePreview {
  query: string;
  samples: PromQLRuleSample[];
  matches: boolean;
}
