import type { PromptQualityDimension } from "@multica/core/types";

/**
 * How each dimension's value should be read (RUYI-184).
 *
 * D1's two cards are absolute token counts; the other five are fractions in
 * 0..1 that the server produced by dividing counts it holds. Nothing here
 * derives a value — formatting only decides how an already-measured number is
 * printed, so a missing measurement can never acquire one on the way to the
 * screen.
 */
const RATE_DIMENSIONS: ReadonlySet<PromptQualityDimension> = new Set<PromptQualityDimension>([
  "discipline",
  "tool_failure_rate",
  "retry_rate",
  "failure_attribution",
  "first_pass_rate",
]);

export function isRateDimension(key: PromptQualityDimension): boolean {
  return RATE_DIMENSIONS.has(key);
}

/** Formats one measured value for its dimension. */
export function formatMeasureValue(
  key: PromptQualityDimension,
  value: number,
  locale: string,
): string {
  if (isRateDimension(key)) {
    return new Intl.NumberFormat(locale, {
      style: "percent",
      maximumFractionDigits: 1,
    }).format(value);
  }
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(value);
}

/**
 * Formats the gap between two versions of the same dimension.
 *
 * Returns null when either side is unmeasured — the comparison dialog prints a
 * dash there rather than treating a missing side as zero, which is the same
 * rule the cards follow.
 */
export function formatMeasureDelta(
  key: PromptQualityDimension,
  a: number | null,
  b: number | null,
  locale: string,
): string | null {
  if (a === null || b === null) return null;
  const delta = b - a;
  return new Intl.NumberFormat(locale, {
    style: isRateDimension(key) ? "percent" : "decimal",
    maximumFractionDigits: isRateDimension(key) ? 1 : 0,
    signDisplay: "exceptZero",
  }).format(delta);
}
