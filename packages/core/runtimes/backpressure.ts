// Frontend reader for the host-memory backpressure report (RUYI-393) that the
// daemon attaches to heartbeats and task claims and the server merges into the
// runtime row's metadata bag under `backpressure`. Mirrors
// `protocol.DaemonBackpressureReport` on the server; `recorded_at` is stamped
// by the server's SQL at write time.

export interface RuntimeBackpressure {
  active: boolean;
  /** Why backpressure is active: "mem", "swap", or "mem+swap". Empty when inactive. */
  reason: string;
  memAvailablePct: number;
  swapUsedPct: number;
  /** Task-claim polls the daemon skipped while backpressured. */
  deferredClaims: number;
  recordedAt: string;
}

/**
 * Pull the backpressure report off a runtime row's loosely-typed metadata bag.
 * Returns `null` when absent or malformed — the UI then renders nothing, the
 * same fail-open stance the daemon takes when its sampler can't read /proc.
 * `active` is the only load-bearing field: the badge keys off it, so a report
 * without a boolean `active` is treated as no report at all.
 */
export function readRuntimeBackpressure(
  metadata: Record<string, unknown> | undefined,
): RuntimeBackpressure | null {
  const raw = metadata?.backpressure;
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const bp = raw as Record<string, unknown>;
  if (typeof bp.active !== "boolean") return null;

  const num = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : 0);
  const str = (v: unknown) => (typeof v === "string" ? v : "");

  return {
    active: bp.active,
    reason: str(bp.reason),
    memAvailablePct: num(bp.mem_available_pct),
    swapUsedPct: num(bp.swap_used_pct),
    deferredClaims: num(bp.deferred_claims),
    recordedAt: str(bp.recorded_at),
  };
}
