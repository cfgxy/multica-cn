// Frontend reader for the host-memory backpressure report (RUYI-393) that the
// daemon attaches to heartbeats and task claims and the server merges into the
// runtime row's metadata bag under `backpressure`. Mirrors
// `protocol.DaemonBackpressureReport` on the server; `recorded_at` is stamped
// by the server's SQL at write time.

export interface RuntimeBackpressure {
  active: boolean;
  /**
   * Why backpressure is active: "mem", "swap", "psi", or a "+"-joined
   * combination such as "mem+swap+psi". Empty when inactive.
   */
  reason: string;
  memAvailablePct: number;
  swapUsedPct: number;
  /** PSI memory some avg10 (stall %, higher is worse). 0 when unread. */
  psiSomeAvg10: number;
  /**
   * Whether the daemon could read /proc/pressure/memory. False on pre-PSI
   * reports and unreadable PSI, in which case the UI stays silent about PSI
   * instead of showing a misleading zero.
   */
  psiReadOK: boolean;
  /** Task-claim polls the daemon skipped while backpressured. */
  deferredClaims: number;
  recordedAt: string;
  /**
   * The gate configuration in force on the daemon (RUYI-729 stage 1) —
   * the card it judged these values against. Null on pre-stage-1 daemons;
   * the UI degrades to the bare numbers then, never inventing thresholds.
   */
  config: DaemonBackpressureConfig | null;
  /**
   * Per-condition readings at report time: value vs high watermark and
   * whether it is past the line. Null alongside a null config.
   */
  conditions: BackpressureCondition[] | null;
}

/** One gate condition's wire shape, mirrored from protocol (RUYI-729). */
export interface BackpressureCondition {
  condition: string;
  value: number;
  threshold: number;
  triggered: boolean;
}

/**
 * The effective gate card: the Runtime page's save body and the detail
 * view's read model. Mirrors protocol.DaemonBackpressureConfig; version and
 * source are server-stamped and never accepted from clients.
 */
export interface DaemonBackpressureConfig {
  enabled: boolean;
  memHighPct: number;
  memRecoveryPct: number;
  swapHighPct: number;
  swapRecoveryPct: number;
  psiHighPct: number;
  psiRecoveryPct: number;
  sampleIntervalSeconds: number;
  windowSize: number;
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
    psiSomeAvg10: num(bp.psi_some_avg10),
    psiReadOK: bp.psi_read_ok === true,
    deferredClaims: num(bp.deferred_claims),
    recordedAt: str(bp.recorded_at),
    config: readBackpressureConfig(bp.config),
    conditions: readBackpressureConditions(bp.conditions),
  };
}

/**
 * Decode the optional config card off a report. Null when absent or
 * malformed — the badge and detail view show bare numbers then, the same
 * fail-open stance as the report itself.
 */
export function readBackpressureConfig(
  raw: unknown,
): DaemonBackpressureConfig | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const c = raw as Record<string, unknown>;
  const num = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : 0);
  return {
    enabled: c.enabled !== false,
    memHighPct: num(c.mem_high_pct),
    memRecoveryPct: num(c.mem_recovery_pct),
    swapHighPct: num(c.swap_high_pct),
    swapRecoveryPct: num(c.swap_recovery_pct),
    psiHighPct: num(c.psi_high_pct),
    psiRecoveryPct: num(c.psi_recovery_pct),
    sampleIntervalSeconds: num(c.sample_interval_seconds),
    windowSize: num(c.window_size),
  };
}

function readBackpressureConditions(raw: unknown): BackpressureCondition[] | null {
  if (!Array.isArray(raw)) return null;
  const num = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : 0);
  const str = (v: unknown) => (typeof v === "string" ? v : "");
  return raw
    .filter((c): c is Record<string, unknown> => !!c && typeof c === "object" && !Array.isArray(c))
    .map((c) => ({
      condition: str(c.condition),
      value: num(c.value),
      threshold: num(c.threshold),
      triggered: c.triggered === true,
    }));
}
