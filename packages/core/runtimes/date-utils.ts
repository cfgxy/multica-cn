// ---------------------------------------------------------------------------
// Calendar helpers — all date math runs on YYYY-MM-DD strings in a caller's
// IANA timezone. The backend groups daily usage by `start-of-day in runtime
// tz`, so the entire frontend aggregation stays on the same axis (Daily /
// Weekly) to avoid one-day drift when the browser and runtime sit in
// different time zones. Moved here from `packages/views/runtimes/utils.ts`
// (RUYI-638) so the mobile stats screen computes on the exact same
// functions — views re-exports these symbols.
// ---------------------------------------------------------------------------

// Today's calendar date (YYYY-MM-DD) in the given IANA timezone. `en-CA`
// gives ISO-shaped output without us having to assemble Intl parts by hand.
export function todayIso(tz: string): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: tz,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date());
}

// Pure date arithmetic on a YYYY-MM-DD string. Uses UTC under the hood so
// DST transitions never shift the result by an hour and round to a
// neighbouring day.
export function addDaysIso(iso: string, days: number): string {
  const [y, m, d] = iso.split("-").map(Number);
  const dt = new Date(Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1));
  dt.setUTCDate(dt.getUTCDate() + days);
  return dt.toISOString().slice(0, 10);
}

// Monday-of-week as YYYY-MM-DD. ISO 8601 week-start, matching the heatmap
// and the team's day-to-day "this week" mental model. Pure string math —
// no `new Date()` reads — so it's stable under any host timezone.
export function weekStartIso(iso: string): string {
  const [y, m, d] = iso.split("-").map(Number);
  const dt = new Date(Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1));
  const day = dt.getUTCDay(); // 0 = Sun, 1 = Mon, ..., 6 = Sat
  const offset = (day + 6) % 7; // distance back to Monday
  dt.setUTCDate(dt.getUTCDate() - offset);
  return dt.toISOString().slice(0, 10);
}

// "May 12" — short, locale-aware month/day for a YYYY-MM-DD string. Parsing
// via UTC keeps the displayed day stable regardless of the browser's tz.
export function formatShortDate(iso: string): string {
  const [y, m, d] = iso.split("-").map(Number);
  const dt = new Date(Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1));
  return dt.toLocaleString("en", {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

// Inclusive-exclusive day distance between two YYYY-MM-DD strings. Used by
// the weekly folds to report how many days a partial week covers. Was
// duplicated as a private helper in both views/runtimes/utils.ts and
// views/dashboard/utils.ts; consolidated here.
export function diffDaysIso(from: string, to: string): number {
  const [y1, m1, d1] = from.split("-").map(Number);
  const [y2, m2, d2] = to.split("-").map(Number);
  const a = Date.UTC(y1 ?? 1970, (m1 ?? 1) - 1, d1 ?? 1);
  const b = Date.UTC(y2 ?? 1970, (m2 ?? 1) - 1, d2 ?? 1);
  return Math.round((b - a) / 86_400_000);
}
