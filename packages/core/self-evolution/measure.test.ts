// @vitest-environment node
import { describe, it, expect } from "vitest";
import {
  PROMPT_QUALITY_DIMENSIONS,
  degradedSourceKinds,
  groupPerplexityByProfile,
  toDimensionCards,
  toMeasureView,
} from "./measure";
import {
  EMPTY_PROMPT_QUALITY_DASHBOARD,
  PromptQualityDashboardSchema,
} from "../api/schemas";
import type { PromptQualityMeasure } from "../types/prompt-quality";

function measure(over: Partial<PromptQualityMeasure>): PromptQualityMeasure {
  return {
    state: "ok",
    value: 0.25,
    numerator: 5,
    denominator: 20,
    sample: 20,
    threshold: 10,
    ...over,
  };
}

describe("toMeasureView", () => {
  it("keeps an ok measure's value", () => {
    const view = toMeasureView(measure({}));
    expect(view.state).toBe("ok");
    if (view.state !== "ok") throw new Error("unreachable");
    expect(view.value).toBe(0.25);
    expect(view.excluded).toBe(0);
  });

  it("carries D6's excluded count through", () => {
    const view = toMeasureView(measure({ excluded: 7 }));
    if (view.state !== "ok") throw new Error("unreachable");
    expect(view.excluded).toBe(7);
  });

  // T5: an un-instrumented run must read as "no data", never as 0%.
  it("reports no_data with its reason rather than a zero", () => {
    const view = toMeasureView(
      measure({ state: "no_data", value: null, numerator: null, denominator: null, reason: "not_instrumented" }),
    );
    expect(view.state).toBe("no_data");
    if (view.state !== "no_data") throw new Error("unreachable");
    expect(view.reason).toBe("not_instrumented");
    expect(view).not.toHaveProperty("value");
  });

  // T7: below the floor, both numbers survive so the UI can say "3 of 10"
  // without ever computing a rate from them.
  it("keeps sample and threshold on insufficient_sample", () => {
    const view = toMeasureView(
      measure({ state: "insufficient_sample", value: null, sample: 3, threshold: 10, reason: "below_sample_floor" }),
    );
    if (view.state !== "insufficient_sample") throw new Error("unreachable");
    expect(view.sample).toBe(3);
    expect(view.threshold).toBe(10);
    expect(view.reason).toBe("below_sample_floor");
  });

  it("treats a state this build does not know as no_data", () => {
    const view = toMeasureView(measure({ state: "partially_sampled_v3" }));
    expect(view.state).toBe("no_data");
  });

  // "ok" plus a null value is a contradiction; resolving it toward 0 would
  // print a measurement nothing backs.
  it("treats ok with a null value as no_data", () => {
    const view = toMeasureView(measure({ value: null }));
    expect(view.state).toBe("no_data");
  });

  it("treats ok with a non-finite value as no_data", () => {
    const view = toMeasureView(measure({ value: Number.NaN }));
    expect(view.state).toBe("no_data");
  });

  it("names an unspecified reason rather than leaving it blank", () => {
    const view = toMeasureView(measure({ state: "no_data", value: null }));
    if (view.state !== "no_data") throw new Error("unreachable");
    expect(view.reason).toBe("unknown");
  });
});

describe("toDimensionCards", () => {
  it("returns all seven dimensions in order", () => {
    const cards = toDimensionCards(EMPTY_PROMPT_QUALITY_DASHBOARD.window.measures);
    expect(cards.map((c) => c.key)).toEqual([...PROMPT_QUALITY_DIMENSIONS]);
  });

  it("renders the empty dashboard as seven no-data cards", () => {
    const cards = toDimensionCards(EMPTY_PROMPT_QUALITY_DASHBOARD.window.measures);
    expect(cards.every((c) => c.view.state === "no_data")).toBe(true);
  });
});

describe("groupPerplexityByProfile", () => {
  const score = (profile: string, version: number) => ({
    version,
    runtime_profile: profile,
    band: "low",
    percent_low: 5,
    percent_high: 15,
    evidence: [],
    model: "m",
    scored_at: "2026-01-01T00:00:00.000Z",
  });

  // Owner Q17: the profiles are scored against different documents, so they
  // stay in separate buckets — no merge, no average.
  it("keeps member and leader_task apart", () => {
    const grouped = groupPerplexityByProfile([
      score("member", 3),
      score("leader_task", 3),
      score("member", 2),
    ]);
    expect(grouped.get("member")?.map((s) => s.version)).toEqual([3, 2]);
    expect(grouped.get("leader_task")?.map((s) => s.version)).toEqual([3]);
  });
});

describe("degradedSourceKinds", () => {
  // T3: an optional export being off is a footer label and nothing else.
  it("names optional sources that are degraded", () => {
    const kinds = degradedSourceKinds({
      degraded: true,
      items: [
        { kind: "platform", available: true, required: true, degraded: false },
        { kind: "langfuse", available: false, required: false, degraded: true },
      ],
    });
    expect(kinds).toEqual(["langfuse"]);
  });

  it("does not list a required source as a degradation", () => {
    const kinds = degradedSourceKinds({
      degraded: true,
      items: [{ kind: "platform", available: false, required: true, degraded: true }],
    });
    expect(kinds).toEqual([]);
  });
});

describe("PromptQualityDashboardSchema", () => {
  it("parses a full response", () => {
    const parsed = PromptQualityDashboardSchema.parse({
      scope: "agent",
      scope_id: "a1",
      since: "2026-01-01",
      window: {
        version: 0,
        days: 30,
        first_day: "2026-01-01",
        last_day: "2026-01-30",
        runs: 40,
        measures: {
          injected_tokens: { state: "ok", value: 1200, numerator: null, denominator: null, sample: 40, threshold: 10 },
          run_tokens_median: { state: "ok", value: 900, numerator: null, denominator: null, sample: 40, threshold: 10 },
          discipline: { state: "ok", value: 0.9, numerator: 36, denominator: 40, sample: 40, threshold: 10 },
          tool_failure_rate: { state: "no_data", value: null, numerator: null, denominator: null, sample: 0, threshold: 10, reason: "not_instrumented" },
          retry_rate: { state: "ok", value: 0.1, numerator: 4, denominator: 40, sample: 40, threshold: 10 },
          failure_attribution: { state: "ok", value: 0.5, numerator: 2, denominator: 4, sample: 4, threshold: 0, excluded: 3 },
          first_pass_rate: { state: "insufficient_sample", value: null, numerator: null, denominator: null, sample: 2, threshold: 5, reason: "below_sample_floor" },
        },
        failure_reasons: { runtime_offline: 3 },
        excluded_failed_runs: 3,
      },
      versions: [],
      perplexity: [
        {
          version: 4,
          runtime_profile: "member",
          band: "medium",
          percent_low: 20,
          percent_high: 40,
          evidence: [
            {
              key: "cross_tier_conflict",
              score: 0.6,
              justification: "two tiers disagree on escalation",
              evidence: [{ tier: "workspace", section: "STOP / NEVER", conflicts_with: "agent:STOP" }],
            },
          ],
          model: "claude",
          scored_at: "2026-01-30T10:00:00.000Z",
        },
      ],
      data_sources: {
        degraded: true,
        items: [{ kind: "langfuse", available: false, required: false, degraded: true }],
      },
    });
    expect(parsed.window.measures.tool_failure_rate.state).toBe("no_data");
    expect(parsed.window.measures.failure_attribution.excluded).toBe(3);
    expect(parsed.perplexity[0]?.evidence[0]?.evidence?.[0]?.tier).toBe("workspace");
  });

  // A dropped field must not turn into a measured zero.
  it("defaults a missing dimension to no_data, not to zero", () => {
    const parsed = PromptQualityDashboardSchema.parse({
      scope: "agent",
      scope_id: "a1",
      since: "2026-01-01",
      window: { version: 0, days: 1, runs: 0 },
      versions: [],
      perplexity: [],
      data_sources: {},
    });
    expect(parsed.window.measures.first_pass_rate.state).toBe("no_data");
    expect(parsed.window.measures.first_pass_rate.value).toBeNull();
  });

  it("rejects a malformed response so the caller falls back", () => {
    expect(PromptQualityDashboardSchema.safeParse({ scope: 7 }).success).toBe(false);
    expect(PromptQualityDashboardSchema.safeParse("nope").success).toBe(false);
    expect(
      PromptQualityDashboardSchema.safeParse({
        scope: "agent",
        scope_id: "a1",
        since: "2026-01-01",
        window: { runs: "many" },
        versions: [],
        perplexity: [],
        data_sources: {},
      }).success,
    ).toBe(false);
  });

  // The fallback is what a desktop build on a drifted backend renders.
  it("has no measured value anywhere in the fallback", () => {
    const cards = toDimensionCards(EMPTY_PROMPT_QUALITY_DASHBOARD.window.measures);
    expect(cards.map((c) => c.view.state)).toEqual(Array(7).fill("no_data"));
    expect(EMPTY_PROMPT_QUALITY_DASHBOARD.data_sources.items).toEqual([]);
    expect(EMPTY_PROMPT_QUALITY_DASHBOARD.perplexity).toEqual([]);
  });
});
