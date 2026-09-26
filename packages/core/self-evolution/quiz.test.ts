// @vitest-environment node

import { describe, it, expect } from "vitest";
import { quizOutcomeCount, quizVerdict, toQuizView } from "./quiz";
import type { PromptQuizBaseline, PromptQuizComparison } from "../types/prompt-quiz";

/**
 * The canonical matrix for the quiz reading's state fold (RUYI-185).
 *
 * The rule under test is one-directional: every path that is not a complete
 * comparison must land on a state that claims nothing. A regression here would
 * not crash anything — it would print "steady" over a version nobody measured,
 * which is the single worst failure this feature can have.
 *
 * `packages/views/self-evolution/components/quiz-reading-card.tsx` renders these
 * states and keeps only the wiring assertions.
 */

function summary(over: Partial<PromptQuizComparison["current"]> = {}) {
  return { n: 12, mean: 100, median: 95, iqr: 20, std_dev: 30, min: 60, max: 200, ...over };
}

function baseline(over: Partial<PromptQuizBaseline> = {}): PromptQuizBaseline {
  return {
    scope: "agent",
    scope_id: "agent-1",
    current_version: 3,
    baseline_version: 2,
    required_sample: 12,
    required_baseline: 30,
    measured: true,
    current: summary(),
    outcomes: { passed: 10, failed: 2 },
    comparison: {
      baseline: summary({ n: 30 }),
      current: summary(),
      z: -2.4,
      threshold: 1.96,
      verdict: "improved",
    },
    ...over,
  };
}

describe("quizVerdict", () => {
  it("keeps the four verdicts this build renders", () => {
    for (const v of ["insufficient", "steady", "improved", "regressed"] as const) {
      expect(quizVerdict(v)).toBe(v);
    }
  });

  it("narrows anything else to insufficient, never to steady", () => {
    // A newer backend adding a verdict must degrade to "no reading", because
    // this build cannot know whether the new label is good news.
    for (const v of ["", "STEADY", "flaky", "degraded_slightly", "unknown"]) {
      expect(quizVerdict(v)).toBe("insufficient");
    }
  });
});

describe("toQuizView", () => {
  it("reports no version when the scope has none", () => {
    const view = toQuizView(baseline({ current_version: 0 }));
    expect(view.state).toBe("no_version");
  });

  it("compares when both groups are complete", () => {
    const view = toQuizView(baseline());
    expect(view.state).toBe("compared");
    if (view.state !== "compared") return;
    expect(view.verdict).toBe("improved");
    expect(view.baselineVersion).toBe(2);
    expect(view.comparison.z).toBe(-2.4);
    expect(view.comparison.threshold).toBe(1.96);
  });

  it("accumulates while the sample is short of N", () => {
    const view = toQuizView(
      baseline({
        current: summary({ n: 7 }),
        comparison: {
          baseline: summary({ n: 30 }),
          current: summary({ n: 7 }),
          z: 0,
          threshold: 1.96,
          verdict: "insufficient",
        },
      }),
    );
    expect(view.state).toBe("accumulating");
    if (view.state !== "accumulating") return;
    // Two counts, so the view can print "7 of 12" instead of a share of an
    // unknown denominator.
    expect(view.have).toBe(7);
    expect(view.need).toBe(12);
  });

  it("accumulates when the server has measured nothing yet", () => {
    const view = toQuizView(
      baseline({ measured: false, current: summary({ n: 0 }), comparison: undefined }),
    );
    expect(view.state).toBe("accumulating");
    if (view.state !== "accumulating") return;
    expect(view.have).toBe(0);
  });

  it("accumulates when the server itself calls the comparison insufficient", () => {
    // The group sizes look complete, so only the server's own verdict says the
    // reading is not usable. Trusting the sizes over the verdict would print a
    // z-score the server refused to stand behind.
    const view = toQuizView(
      baseline({
        comparison: {
          baseline: summary({ n: 30 }),
          current: summary({ n: 12 }),
          z: 0,
          threshold: 1.96,
          verdict: "insufficient",
        },
      }),
    );
    expect(view.state).toBe("accumulating");
  });

  it("accumulates on a verdict this build does not know", () => {
    const view = toQuizView(
      baseline({
        comparison: {
          baseline: summary({ n: 30 }),
          current: summary({ n: 12 }),
          z: 3,
          threshold: 1.96,
          verdict: "catastrophic",
        },
      }),
    );
    expect(view.state).toBe("accumulating");
  });

  it("reports no baseline for a first version, carrying its own group", () => {
    // The first version of a scope has nothing to regress against, but its
    // distribution is exactly what the next version will be read against, so
    // the group must still be readable — this is why the server sends
    // `current` outside the comparison.
    const view = toQuizView(baseline({ baseline_version: 0, comparison: undefined }));
    expect(view.state).toBe("no_baseline");
    if (view.state !== "no_baseline") return;
    expect(view.version).toBe(3);
    expect(view.current.n).toBe(12);
    expect(view.current.median).toBe(95);
  });

  it("reports no baseline when a baseline version exists but carries no comparison", () => {
    const view = toQuizView(baseline({ comparison: undefined }));
    expect(view.state).toBe("no_baseline");
  });

  it("carries outcome counts into every measured state", () => {
    for (const b of [
      baseline(),
      baseline({ measured: false, current: summary({ n: 0 }), comparison: undefined }),
      baseline({ baseline_version: 0, comparison: undefined }),
    ]) {
      const view = toQuizView(b);
      if (view.state === "no_version") throw new Error("unexpected state");
      expect(view.outcomes.passed).toBe(10);
    }
  });

  it("treats a missing outcome map as empty rather than throwing", () => {
    const view = toQuizView(baseline({ outcomes: undefined as unknown as Record<string, number> }));
    if (view.state === "no_version") throw new Error("unexpected state");
    expect(quizOutcomeCount(view.outcomes, "errored")).toBe(0);
  });
});

describe("quizOutcomeCount", () => {
  it("returns 0 for an outcome the server did not report", () => {
    expect(quizOutcomeCount({ passed: 4 }, "errored")).toBe(0);
    expect(quizOutcomeCount({ errored: 3 }, "errored")).toBe(3);
  });
});
