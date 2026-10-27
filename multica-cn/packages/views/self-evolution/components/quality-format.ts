/**
 * Printing one measured value (RUYI-184).
 *
 * The unit arrives with the measure — the server sets it next to the arithmetic
 * that produced the number (`promptquality.Unit`). Nothing here maps a dimension
 * to a unit: that mapping used to live here as a hand-kept set of dimension
 * names, and because D2's 0..100 deduction score and D6's failure count sat in
 * the set meant for 0..1 ratios, 90 printed as 9,000% and 2 as 200%. A set of
 * names cannot be checked against the numbers the server emits; a field on the
 * measure can.
 *
 * Nothing here derives a value either — formatting only decides how an
 * already-measured number is printed, so a missing measurement can never
 * acquire one on the way to the screen.
 */

/** Formats one measured value according to the unit the server declared. */
export function formatMeasureValue(
  unit: string,
  value: number,
  locale: string,
  scoreMax: number,
): string {
  if (unit === "ratio") {
    return new Intl.NumberFormat(locale, {
      style: "percent",
      maximumFractionDigits: 1,
    }).format(value);
  }
  const points = new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(value);
  // A score is points out of a declared maximum. Without a maximum it is still
  // points — a bare number, never a percentage of an unknown scale.
  if (unit === "score" && scoreMax > 0) {
    const max = new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(scoreMax);
    return `${points} / ${max}`;
  }
  // "count", and any unit this build does not know: a plain number. Reading an
  // unrecognised unit as a ratio would multiply it by a hundred, which is the
  // larger error.
  return points;
}

/**
 * Formats the gap between two versions of the same dimension.
 *
 * Returns null when either side is unmeasured — the comparison dialog prints a
 * dash there rather than treating a missing side as zero, which is the same
 * rule the cards follow.
 */
export function formatMeasureDelta(
  unit: string,
  a: number | null,
  b: number | null,
  locale: string,
): string | null {
  if (a === null || b === null) return null;
  const isRatio = unit === "ratio";
  const delta = b - a;
  return new Intl.NumberFormat(locale, {
    style: isRatio ? "percent" : "decimal",
    maximumFractionDigits: isRatio ? 1 : 0,
    signDisplay: "exceptZero",
  }).format(delta);
}
