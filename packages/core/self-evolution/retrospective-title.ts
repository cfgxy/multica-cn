/**
 * Display-title helpers for daily-retrospective runs (RUYI-673).
 *
 * A retrospective run has no issue to title itself with, so its readable
 * title is the localized "每日复盘" label plus a timestamp of the trigger
 * moment (the window end). The stamp part is locale-independent, which is
 * why it lives in core while the surrounding copy stays in the locale files.
 */

/**
 * Formats an instant as `YYYYMMDD_HHmmss` in the viewer's local timezone.
 * Returns "" for unparseable input so callers can fall back to another
 * time source instead of rendering "NaN".
 */
export function formatRetrospectiveStamp(input: string | number | Date): string {
  const d = new Date(input);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return (
    `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}` +
    `_${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`
  );
}
