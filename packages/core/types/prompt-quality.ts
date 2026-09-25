/**
 * Prompt quality dashboard types (RUYI-184, self-evolution phase 2).
 *
 * The seven dimensions are read from the platform's own run stream; the server
 * folds each day into counts and never divides, so a dimension arrives here as
 * one of three states rather than as a number that might be a stand-in for
 * "nothing was measured". The UI renders the state — it has no branch that can
 * turn a missing measurement into 0%.
 *
 * `state` stays a plain string on the wire type because it is server-driven: a
 * state a newer backend adds must still parse, with the view taking its default
 * branch. The narrowing to a discriminated union happens in
 * `core/self-evolution/measure.ts`, which is the one place that decides what an
 * unknown state renders as.
 */

/** Which prompt tier a dashboard reads. Matches the server's scope segment. */
export type PromptQualityScope = "workspace" | "project" | "squad" | "agent";

/** The seven cards, keyed as the API returns them. */
export type PromptQualityDimension =
  | "injected_tokens"
  | "run_tokens_median"
  | "discipline"
  | "tool_failure_rate"
  | "retry_rate"
  | "failure_attribution"
  | "first_pass_rate";

/** One dimension as the API returns it. */
export interface PromptQualityMeasure {
  /** "ok" | "no_data" | "insufficient_sample", server-driven. */
  state: string;
  /** The rate or absolute figure. Null unless state is "ok". */
  value: number | null;
  numerator: number | null;
  denominator: number | null;
  /** The dimension's own denominator, which is not always the run count. */
  sample: number;
  /** The declared floor the sample was compared against. */
  threshold: number;
  /** Names why the state is not ok. A key, not a sentence. */
  reason?: string;
  /** D6 only: failures dropped from the attribution denominator. */
  excluded?: number;
}

export interface PromptQualityMeasures {
  injected_tokens: PromptQualityMeasure;
  run_tokens_median: PromptQualityMeasure;
  discipline: PromptQualityMeasure;
  tool_failure_rate: PromptQualityMeasure;
  retry_rate: PromptQualityMeasure;
  failure_attribution: PromptQualityMeasure;
  first_pass_rate: PromptQualityMeasure;
}

/** One version's seven cards plus the span they cover. */
export interface PromptQualityVersionMeasures {
  /** 0 for the window aggregate across versions. */
  version: number;
  /** How many daily buckets went into this group. */
  days: number;
  first_day?: string;
  last_day?: string;
  runs: number;
  measures: PromptQualityMeasures;
  /** D6 breakdown by failure reason. Empty when the blob did not parse. */
  failure_reasons: Record<string, number>;
  excluded_failed_runs: number;
}

/** One evidence entry behind a D3 sub-dimension score. Locations, never text. */
export interface PromptPerplexityEvidence {
  tier: string;
  section: string;
  conflicts_with?: string;
  note?: string;
}

/** One D3 sub-dimension. */
export interface PromptPerplexityItem {
  key: string;
  score: number;
  justification: string;
  evidence?: PromptPerplexityEvidence[];
}

/**
 * One D3 score. There is one per runtime profile per version and they are
 * never merged: the profiles are scored against different assembled documents,
 * so an average would describe a prompt no run ever receives (Owner Q17).
 */
export interface PromptQualityPerplexity {
  version: number;
  /** "member" | "leader_task", server-driven. */
  runtime_profile: string;
  /** "low" | "medium" | "high", server-driven. */
  band: string;
  percent_low: number | null;
  percent_high: number | null;
  evidence: PromptPerplexityItem[];
  model: string;
  scored_at: string;
}

/**
 * One data source's state. Only the platform's own tables are `required`;
 * everything else degrades to a footer label, and no card may depend on it.
 */
export interface PromptQualitySource {
  /** "platform" | "scoring_model" | "langfuse", server-driven. */
  kind: string;
  available: boolean;
  required: boolean;
  degraded: boolean;
}

export interface PromptQualitySources {
  degraded: boolean;
  items: PromptQualitySource[];
}

export interface PromptQualityDashboard {
  scope: string;
  scope_id: string;
  /** First calendar day the window covers, YYYY-MM-DD. */
  since: string;
  /** Every version in the range folded together — the headline row. */
  window: PromptQualityVersionMeasures;
  /** The same data split per version, newest first. */
  versions: PromptQualityVersionMeasures[];
  /** Empty when D3 was never scored for this scope. */
  perplexity: PromptQualityPerplexity[];
  data_sources: PromptQualitySources;
}
