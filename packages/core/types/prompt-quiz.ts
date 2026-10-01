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
  /** Presentation metadata (RUYI-286). Tags carry the benchmark type; never a private half. */
  tags?: string[];
  /** "easy" | "medium" | "hard"; absent on a backend predating RUYI-286. */
  difficulty?: string;
  created_at: string;
  updated_at: string;
}

/** One item's graded aggregate inside a score summary (RUYI-286). */
export interface PromptQuizItemScore {
  item_id: string;
  /** Graded readings of this item in the group; 0 renders as "not graded". */
  graded: number;
  /** Mean weighted pass ratio over those readings, 0..1. */
  mean: number;
}

/**
 * The graded side of a sample group: counts and means over explicitly graded
 * rows. `graded: 0` means nothing was graded — it renders as "not graded",
 * never as a 0% score.
 */
export interface PromptQuizScoreSummary {
  graded: number;
  mean: number;
  items: PromptQuizItemScore[];
}

/** One graded sample on the wire — a run's verdict, evidence included. */
export interface PromptQuizSampleRow {
  task_id: string;
  scope: string;
  scope_id: string;
  version: number;
  item_id: string;
  item_revision: number;
  item_slug?: string;
  item_title?: string;
  /** "answered" | "errored" — whether the run answered, never a grade. */
  outcome: string;
  /** Null when not graded (errored run, check-less item). Never read as 0. */
  score: number | null;
  /** Per-assertion verdicts; shape mirrors pkg/promptquiz.CheckVerdict. */
  score_detail?: unknown;
  graded_at?: string;
  measured_at?: string;
  run_tokens?: number;
  task_status?: string;
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
  /**
   * The structured answer key, passed through for the editor round-trip.
   * Like `rubric`, owner-only and never rendered on any list surface.
   */
  rubric_checks?: unknown;
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
  /** Graded side of the current group (RUYI-286); absent on older backends. */
  scores?: PromptQuizScoreSummary;
  /** Graded side of the baseline group, same rules. */
  baseline_scores?: PromptQuizScoreSummary;
}

/** Body of POST /api/prompt-quiz/batches. */
export interface CreatePromptQuizBatchRequest {
  agent_ids: string[];
  /** Empty = every active member-profile item. */
  item_ids?: string[];
}

/** What a batch order actually did. */
export interface PromptQuizBatchCreateResponse {
  batch_id: string;
  ordered: number;
  refused_agents?: { agent_id: string; reason: string }[];
}

/** One batch's read-back: per-run rows, outcome counts, graded summary. */
export interface PromptQuizBatchResponse {
  batch_id: string;
  rows: PromptQuizSampleRow[];
  /** Per-outcome counts ("answered"/"errored") — never a pass rate. */
  counts: Record<string, number>;
  scores?: PromptQuizScoreSummary;
}

/** Body of POST /api/prompt-quiz/bank/import. */
export interface PromptQuizBankImportResponse {
  imported: number;
  slugs: string[];
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
