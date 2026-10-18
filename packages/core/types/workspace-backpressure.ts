/**
 * Workspace host-backpressure settings types (RUYI-618).
 *
 * The watermarks pause new task claiming on every daemon host in the
 * workspace. The card is saved per workspace from the settings UI and
 * delivered to running daemons on every heartbeat ack, so a save lands
 * within one beat without a daemon restart. Field semantics (hysteresis
 * bands, sentinel disables) are validated server-side against the same
 * rulebook the daemon enforces; the shape here mirrors that wire type.
 */

export interface WorkspaceBackpressureSettings {
  enabled: boolean;
  mem_high_pct: number;
  mem_recovery_pct: number;
  swap_high_pct: number;
  swap_recovery_pct: number;
  psi_high_pct: number;
  psi_recovery_pct: number;
  sample_interval_seconds: number;
  window_size: number;
  /** False when no owner ever saved the form: the values are the defaults. */
  custom: boolean;
}

export type WorkspaceBackpressureSettingsSave = Omit<
  WorkspaceBackpressureSettings,
  "custom"
>;
