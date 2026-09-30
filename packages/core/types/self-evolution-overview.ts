/**
 * Self-evolution workspace overview types (RUYI-284).
 *
 * One read-only aggregate across the six data planes the tabs serve. Every
 * section keeps the honesty rules of the tab it mirrors: a count is a count, a
 * missing reading stays "insufficient" rather than becoming a grade, and a
 * measure keeps the server's state field so the view can never divide a
 * nothing-measured fold into a percentage. The overview adds no writer — the
 * wire type has no mutation fields for the same reason the route has no
 * non-GET method.
 */

import type { PromptQualityMeasures } from "./prompt-quality";

/** One prompt tier's version activity as the API returns it. */
export interface SelfEvolutionOverviewTier {
  /** "workspace" | "project" | "squad" | "agent", server-driven. */
  scope: string;
  version_count: number;
  subject_count: number;
  /**
   * The tier's current version, set only when the tier has a single subject.
   * Across N subjects the highest version belongs to one of them and must not
   * read as a fleet-wide state.
   */
  current_version?: number;
  last_change_at?: string;
  /** The most recent change's author; unattributed snapshots have none. */
  last_actor?: string;
}

/** The folded quality window across every measured agent in the workspace. */
export interface SelfEvolutionOverviewQuality {
  since: string;
  days: number;
  runs: number;
  /** How many agents carry rollup rows in the window; 0 is an empty window. */
  subjects_measured: number;
  measures: PromptQualityMeasures;
  excluded_failed_runs: number;
}

/**
 * The quiz conclusion for the most recently measured scope in the workspace.
 *
 * `verdict` stays a plain string for the same reason the per-scope reading's
 * does: server-driven, and an unknown verdict must parse as the branch that
 * claims nothing.
 */
export interface SelfEvolutionOverviewQuiz {
  scope_id?: string;
  scope_name?: string;
  current_version?: number;
  baseline_version?: number;
  verdict: string;
  /** Whether the current version has any comparable reading at all. */
  measured: boolean;
  required_sample: number;
  required_baseline: number;
  last_measured_at?: string;
}

/** The pool's most recent proposal, as the list endpoint serves it. */
export interface SelfEvolutionOverviewProposal {
  id: string;
  title: string;
  status: string;
  created_at: string;
}

export interface SelfEvolutionOverviewProposals {
  total: number;
  /** draft + needs_revision: in the pool awaiting a decision. */
  pending: number;
  adopted: number;
  /** The full status breakdown, so no status hides behind the headlines. */
  by_status: Record<string, number>;
  latest?: SelfEvolutionOverviewProposal;
}

export interface SelfEvolutionOverviewScan {
  /** "noop" | "changed" | "failed", server-driven. */
  result: string;
  trigger_source: string;
  started_at: string;
}

export interface SelfEvolutionOverviewKnowledge {
  /** Live directories only; removed ones are gone, not hidden. */
  dirs: number;
  entries: number;
  last_scan?: SelfEvolutionOverviewScan;
}

export interface SelfEvolutionOverviewSkills {
  count: number;
  /** Explicit Skill tool invocations only, per the usage reader's rule. */
  invocations: number;
}

export interface SelfEvolutionOverview {
  versions: SelfEvolutionOverviewTier[];
  quality: SelfEvolutionOverviewQuality;
  quiz: SelfEvolutionOverviewQuiz;
  proposals: SelfEvolutionOverviewProposals;
  knowledge: SelfEvolutionOverviewKnowledge;
  skills: SelfEvolutionOverviewSkills;
}
