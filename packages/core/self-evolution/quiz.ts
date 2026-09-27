import type {
  PromptQuizBaseline,
  PromptQuizComparison,
  PromptQuizSummary,
} from "../types/prompt-quiz";

/**
 * The one place that decides what a quiz reading renders as (RUYI-185).
 *
 * Two rules drive everything here, and both exist to stop an unmeasured version
 * from reading as a healthy one:
 *
 * 1. A verdict this build does not recognise narrows to "insufficient", not to
 *    "steady". The wire type keeps `verdict` a plain string so a verdict a newer
 *    backend adds still parses; the default branch must be the one that claims
 *    nothing.
 * 2. A group below its required size has no reading at all. `n` and the required
 *    size are both carried so the view can print "7 of 12 runs" — never a
 *    percentage of an unknown denominator, and never a 0.
 */

/** The discrimination marks this build knows how to render (A4). */
export type QuizDiscrimination = "pending" | "no_signal" | "flat" | "ok";

const KNOWN_DISCRIMINATIONS: readonly string[] = ["pending", "no_signal", "flat", "ok"];

/**
 * Narrows the server-driven discrimination mark.
 *
 * Absent and unknown both become "pending": a question this build cannot judge
 * has not been judged. "ok" would claim the question still separates versions,
 * which is exactly the claim the mark exists to withhold.
 */
export function quizDiscrimination(mark: string | undefined): QuizDiscrimination {
  return mark !== undefined && KNOWN_DISCRIMINATIONS.includes(mark)
    ? (mark as QuizDiscrimination)
    : "pending";
}

/** The verdicts this build knows how to render. */
export type QuizVerdict = "insufficient" | "steady" | "improved" | "regressed";

const KNOWN_VERDICTS: readonly string[] = ["insufficient", "steady", "improved", "regressed"];

/**
 * Narrows the server-driven verdict string.
 *
 * Unknown values become "insufficient": a reading this build cannot interpret
 * is a reading it does not have.
 */
export function quizVerdict(verdict: string): QuizVerdict {
  return KNOWN_VERDICTS.includes(verdict) ? (verdict as QuizVerdict) : "insufficient";
}

/** What the quiz panel renders, with every "not measured" case made explicit. */
export type QuizView =
  | {
      /** The scope has no prompt version, so there is nothing to measure. */
      state: "no_version";
    }
  | {
      /** A version exists; its sample has not reached N yet. */
      state: "accumulating";
      version: number;
      /** Graded measurements so far. May be 0. */
      have: number;
      /** N, as the server declares it. */
      need: number;
      outcomes: Record<string, number>;
      incomparable: Incomparable;
    }
  | {
      /**
       * The current version is measured, but there is no comparable previous
       * version — the first version of a scope has nothing to regress against.
       */
      state: "no_baseline";
      version: number;
      current: PromptQuizSummary;
      outcomes: Record<string, number>;
      incomparable: Incomparable;
    }
  | {
      /** Both groups are present; `verdict` is the reading. */
      state: "compared";
      version: number;
      baselineVersion: number;
      verdict: QuizVerdict;
      comparison: PromptQuizComparison;
      outcomes: Record<string, number>;
      incomparable: Incomparable;
    };

/**
 * Readings that were taken but could not be read against the current group,
 * because the question was reworded or the runtime/model changed under them.
 *
 * Carried into every measured state so the view can say why a group is smaller
 * than the number of runs that were made: without it a bank edit and a
 * collection outage look identical from the outside.
 */
export interface Incomparable {
  current: number;
  baseline: number;
}

/**
 * Folds a baseline response into the one state the panel renders.
 *
 * The "accumulating" branch is deliberately reachable in two ways — no
 * comparison at all, and a comparison the server itself called insufficient.
 * Both mean the same thing to a reader, and collapsing them here keeps the view
 * from having to decide which "not enough data" it is looking at.
 */
export function toQuizView(baseline: PromptQuizBaseline): QuizView {
  if (baseline.current_version <= 0) return { state: "no_version" };

  const outcomes = baseline.outcomes ?? {};
  const incomparable: Incomparable = {
    current: baseline.incomparable ?? 0,
    baseline: baseline.baseline_incomparable ?? 0,
  };
  const cmp = baseline.comparison;
  const version = baseline.current_version;
  // The server sends `current` whether or not a comparison exists, so a first
  // version's group size is readable without one.
  const have = baseline.current?.n ?? cmp?.current.n ?? 0;
  const need = baseline.required_sample;

  if (!baseline.measured || (need > 0 && have < need)) {
    return { state: "accumulating", version, have, need, outcomes, incomparable };
  }
  if (cmp === undefined || baseline.baseline_version <= 0) {
    return {
      state: "no_baseline",
      version,
      // The current group's own shape is still a reading worth showing: it is
      // what the NEXT version will be compared against.
      current: baseline.current,
      outcomes,
      incomparable,
    };
  }
  const verdict = quizVerdict(cmp.verdict);
  if (verdict === "insufficient") {
    return { state: "accumulating", version, have, need, outcomes, incomparable };
  }
  return {
    state: "compared",
    version,
    baselineVersion: baseline.baseline_version,
    verdict,
    comparison: cmp,
    outcomes,
    incomparable,
  };
}

/**
 * How many of a version's stored runs carry the given outcome.
 *
 * The vocabulary is "answered" / "errored" and says whether the run produced an
 * answer at all — NEITHER is a grade (migration 933). A caller that renders
 * answered/total as a pass rate is reporting something the data does not say.
 * Errored runs are kept out of the sample, so they are reported next to the
 * reading rather than folded into it: an outage is not a cost improvement.
 */
export function quizOutcomeCount(outcomes: Record<string, number>, outcome: string): number {
  return outcomes[outcome] ?? 0;
}
