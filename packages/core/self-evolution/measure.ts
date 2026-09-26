import type {
  PromptQualityDimension,
  PromptQualityMeasure,
  PromptQualityMeasures,
  PromptQualityPerplexity,
  PromptQualitySources,
} from "../types/prompt-quality";

/**
 * The one place that decides what a dimension's server-driven `state` renders
 * as (RUYI-184).
 *
 * The wire type keeps `state` a plain string so a state a newer backend adds
 * still parses. Narrowing it here means every view shares the same default
 * branch, and that branch is `no_data` — not `ok`. A state this build does not
 * understand is a measurement it cannot explain, and rendering it as a number
 * would put a figure on a card that nothing backs.
 */
export type MeasureView =
  | {
      state: "ok";
      /** How `value` must be read. See UNKNOWN_UNIT for the absent case. */
      unit: string;
      /** Non-null exactly when the state is "ok". */
      value: number;
      /** Top of the scale for unit "score". 0 for every other unit. */
      scoreMax: number;
      numerator: number | null;
      denominator: number | null;
      sample: number;
      threshold: number;
      excluded: number;
    }
  | {
      state: "no_data";
      unit: string;
      /** Why nothing was measured. A key the views translate, never a sentence. */
      reason: string;
      sample: number;
      threshold: number;
    }
  | {
      state: "insufficient_sample";
      unit: string;
      reason: string;
      /** How many runs the dimension actually had. */
      sample: number;
      /** The floor it was compared against. Both are shown, never the ratio. */
      threshold: number;
    };

/** The reason key used when the server named none. */
export const UNKNOWN_REASON = "unknown";

/**
 * The unit assumed when the server named none.
 *
 * "count" rather than "ratio" on purpose: a plain number is at worst mislabelled,
 * while a count read as a ratio is multiplied by a hundred — the failure that a
 * hand-kept set of "these dimensions are rates" dimension names produced for D2
 * and D6 before the unit travelled with the value.
 */
export const UNKNOWN_UNIT = "count";

/**
 * Narrows one measure.
 *
 * `ok` with a null value is treated as `no_data`: the two fields disagree, and
 * the safe reading of that disagreement is that nothing was measured. The
 * alternative — substituting 0 — is the exact failure T5 and T7 exist to
 * prevent.
 */
export function toMeasureView(measure: PromptQualityMeasure): MeasureView {
  const sample = Number.isFinite(measure.sample) ? measure.sample : 0;
  const threshold = Number.isFinite(measure.threshold) ? measure.threshold : 0;
  const unit = measure.unit ?? UNKNOWN_UNIT;
  const scoreMax =
    measure.score_max !== undefined && Number.isFinite(measure.score_max) ? measure.score_max : 0;

  switch (measure.state) {
    case "ok":
      if (measure.value === null || !Number.isFinite(measure.value)) {
        return { state: "no_data", unit, reason: UNKNOWN_REASON, sample, threshold };
      }
      return {
        state: "ok",
        unit,
        value: measure.value,
        scoreMax,
        numerator: measure.numerator,
        denominator: measure.denominator,
        sample,
        threshold,
        excluded: measure.excluded ?? 0,
      };
    case "insufficient_sample":
      return {
        state: "insufficient_sample",
        unit,
        reason: measure.reason ?? UNKNOWN_REASON,
        sample,
        threshold,
      };
    case "no_data":
      return {
        state: "no_data",
        unit,
        reason: measure.reason ?? UNKNOWN_REASON,
        sample,
        threshold,
      };
    default:
      return { state: "no_data", unit, reason: UNKNOWN_REASON, sample, threshold };
  }
}

/** The seven cards in the order the dashboard lays them out. */
export const PROMPT_QUALITY_DIMENSIONS: readonly PromptQualityDimension[] = [
  "injected_tokens",
  "run_tokens_median",
  "discipline",
  "tool_failure_rate",
  "retry_rate",
  "failure_attribution",
  "first_pass_rate",
] as const;

/** One card, ready to render. */
export interface DimensionCard {
  key: PromptQualityDimension;
  view: MeasureView;
}

export function toDimensionCards(measures: PromptQualityMeasures): DimensionCard[] {
  return PROMPT_QUALITY_DIMENSIONS.map((key) => ({
    key,
    view: toMeasureView(measures[key]),
  }));
}

/**
 * Groups D3 scores by runtime profile.
 *
 * The profiles are never merged and never averaged: each is scored against a
 * different assembled document, so a combined band would describe a prompt no
 * run receives (Owner Q17). The dashboard switches between them; it does not
 * summarise across them.
 */
export function groupPerplexityByProfile(
  scores: PromptQualityPerplexity[],
): Map<string, PromptQualityPerplexity[]> {
  const byProfile = new Map<string, PromptQualityPerplexity[]>();
  for (const score of scores) {
    const bucket = byProfile.get(score.runtime_profile);
    if (bucket === undefined) {
      byProfile.set(score.runtime_profile, [score]);
    } else {
      bucket.push(score);
    }
  }
  return byProfile;
}

/**
 * Which optional sources are off, for the footer label.
 *
 * A required source that is unavailable is NOT listed here: that is a broken
 * dashboard, not a degradation, and it surfaces as the dimensions themselves
 * reporting no data. The footer only ever names optional exports, so no card
 * can be gated on one (T3).
 */
export function degradedSourceKinds(sources: PromptQualitySources): string[] {
  return sources.items
    .filter((item) => item.required !== true && item.degraded === true)
    .map((item) => item.kind);
}
