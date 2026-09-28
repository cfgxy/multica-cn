/**
 * Quiz bank and regression reading types (RUYI-185, self-evolution phase 3).
 *
 * A quiz replays one fixed bank of questions against every prompt version, so
 * two versions can be read against each other. The reading is a DISTRIBUTION —
 * mean, dispersion and N per side — never a single score, and never a gate: no
 * publish path on either side of the wire consults it (Owner Q10).
 *
 * `verdict` stays a plain string on the wire type for the same reason
 * `PromptQualityMeasure.state` does: it is server-driven, and a verdict a newer
 * backend adds must still parse with the view taking its default branch. The
 * narrowing lives in `core/self-evolution/quiz.ts`, and its default branch is
 * "insufficient" — an unreadable verdict is not a clean bill of health.
 */

/** One bank entry as the API returns it. */
export interface PromptQuizItem {
  id: string;
  slug: string;
  title: string;
  /** The question text. Workspace-private: never rendered outside this bank. */
  body: string;
  /** Bumped on every accepted body rewrite; stored with each measurement. */
  revision: number;
  /** "member" | "leader_task", server-driven. */
  runtime_profile: string;
  active: boolean;
  /**
   * Whether this question's readings still spread enough to tell two prompt
   * versions apart: "pending" | "no_signal" | "flat" | "ok", server-driven.
   *
   * Empty on a response that did not compute it, which a reader must treat as
   * "not judged" rather than as "fine" — the narrowing in
   * `core/self-evolution/quiz.ts` maps both empty and unknown to "pending".
   */
  discrimination?: string;
  created_at: string;
  updated_at: string;
}

/**
 * One bank entry including its private half.
 *
 * Only the owner-only single-item read and the writes return this. The list is
 * `PromptQuizItem`, with no `rubric` field at all: the expected answer never
 * travels to the run being measured, and a type without the field cannot carry
 * it to a surface that would.
 */
export interface PromptQuizItemDetail extends PromptQuizItem {
  /** The expected answer and grading points. Empty when none is written yet. */
  rubric: string;
}

/** One side of a comparison: the group's shape, not a score. */
export interface PromptQuizSummary {
  /** Group size. 0 means the version was never measured, never "zero cost". */
  n: number;
  mean: number;
  median: number;
  /**
   * The dispersion measure of record. Long-tail runs are kept in the sample,
   * so a measure the tail cannot move is the only honest one.
   */
  iqr: number;
  /** Reported for operators who ask; no decision reads it. */
  std_dev: number;
  min: number;
  max: number;
}

/** A full, re-derivable account of one reading. */
export interface PromptQuizComparison {
  baseline: PromptQuizSummary;
  current: PromptQuizSummary;
  /** Mann-Whitney U in units of its own sd, signed: positive is more costly. */
  z: number;
  /** The noise line z was compared against. */
  threshold: number;
  /** "insufficient" | "steady" | "improved" | "regressed", server-driven. */
  verdict: string;
}

export interface PromptQuizBaseline {
  scope: string;
  scope_id: string;
  /** 0 when the scope has no version at all. */
  current_version: number;
  /** 0 when the current version is the first one. */
  baseline_version: number;
  /** N the new version's group must reach before a comparison is made. */
  required_sample: number;
  /** The size the baseline group accumulates to. Larger, and free. */
  required_baseline: number;
  /** Absent until there is a previous version to compare against. */
  comparison?: PromptQuizComparison;
  /**
   * The current version's own group, present whether or not a comparison is.
   * A first version has no baseline but still has a distribution.
   */
  current: PromptQuizSummary;
  /** False when the current version has no graded measurement yet. */
  measured: boolean;
  /**
   * Per-outcome counts for the current version: "answered" and "errored", which
   * say whether the run produced an answer at all. NEITHER is a grade, so this
   * map must never be rendered as a pass rate.
   */
  outcomes: Record<string, number>;
  /**
   * Readings excluded from `current` because they were taken with a different
   * instrument — another question wording, or another runtime/model pair.
   * Reported so a group that shrank after a bank edit is distinguishable from a
   * collection failure.
   */
  incomparable: number;
  /** The same count for the baseline group. */
  baseline_incomparable: number;
}

/** Body of a bank create. */
export interface CreatePromptQuizItemRequest {
  slug: string;
  title: string;
  body: string;
  /** The private half. Optional: a question with no answer key yet is normal. */
  rubric?: string;
  runtime_profile?: string;
}

/**
 * Body of a bank update. Retiring a question is `active: false`.
 *
 * `rubric` is a full replacement like `body` is, so an editor that omits it
 * clears the stored answer key. Load the item through the single-item read
 * before editing rather than patching from a list row, which has no rubric.
 */
export interface UpdatePromptQuizItemRequest {
  title: string;
  body: string;
  rubric?: string;
  runtime_profile?: string;
  active?: boolean;
}
