import type { useT } from "../../i18n";
import type { RetrospectiveRun, RetrospectiveRunDetail } from "@multica/core/types";

type SelfEvolutionT = ReturnType<typeof useT<"self-evolution">>["t"];

// Locale keys for the run row's structured failure verdict (RUYI-561). The
// server persists a stable reason code at detail.reason.code
// (server/internal/retrospective/reason.go is the enum's home); this table
// mirrors it and retrospective-run-error-i18n-parity.test.ts locks every
// code to a four-locale key under retrospective.run_errors. The wire value
// stays an open string: a newer server can send a code this client does not
// know yet, and runErrorLine degrades to the generic fallback — never the
// raw value, which for this surface means legacy internal copy.
export const RUN_ERROR_I18N_KEYS = {
  // Agent gate — actionable: the user fixes the configuration and retries.
  agent_not_configured: "agent_not_configured",
  agent_missing: "agent_missing",
  agent_archived: "agent_archived",
  agent_runtime_missing: "agent_runtime_missing",

  // Trigger-side failures — the next run retries automatically.
  window_scan_failed: "window_scan_failed",
  watermark_check_failed: "watermark_check_failed",
  scan_scope_record_failed: "scan_scope_record_failed",
  task_enqueue_failed: "task_enqueue_failed",

  // Completion-side failures — nothing was recorded; the next run re-scans.
  agent_report_invalid: "agent_report_invalid",
  agent_no_report: "agent_no_report",
  agent_run_failed: "agent_run_failed",

  // Membership violations — the whole report was rejected.
  report_out_of_scope: "report_out_of_scope",
  draft_out_of_scope: "draft_out_of_scope",

  // Reconciler backstops — the next run retries automatically.
  task_not_completed: "task_not_completed",
  task_never_enqueued: "task_never_enqueued",
} as const;

// Generic localized fallback for anything the table does not map: an
// unknown code, or a legacy row whose raw server text sits in `error`.
export const RUN_ERROR_FALLBACK_KEY = "retrospective.run_error_fallback";

type KnownRunError = keyof typeof RUN_ERROR_I18N_KEYS;

interface RunReason {
  code: string;
  issue_id?: string;
}

// Narrow parse of the raw JSONB passthrough — the run detail is untyped on
// the wire, so every field is validated before use.
function readRunReason(detail: RetrospectiveRunDetail | null | undefined): RunReason | null {
  const reason = (detail as { reason?: unknown } | null | undefined)?.reason;
  if (!reason || typeof reason !== "object") return null;
  const code = (reason as { code?: unknown }).code;
  if (typeof code !== "string" || code.length === 0) return null;
  const issueId = (reason as { issue_id?: unknown }).issue_id;
  return { code, issue_id: typeof issueId === "string" ? issueId : undefined };
}

/**
 * Localized sentence for a failed run, or `null` when there is nothing to
 * show. Known codes localize from the four-locale bundle, interpolating the
 * one whitelisted param (issue_id) where the code carries it. Anything else
 * — an unknown code from a newer server, or a legacy row with raw server
 * text in `error` — falls back to generic localized copy, so internal paths,
 * error chains and hardcoded legacy sentences never reach the screen
 * (RUYI-561).
 */
export function runErrorLine(run: RetrospectiveRun, t: SelfEvolutionT): string | null {
  const reason = readRunReason(run.detail);
  if (reason && Object.prototype.hasOwnProperty.call(RUN_ERROR_I18N_KEYS, reason.code)) {
    const key = RUN_ERROR_I18N_KEYS[reason.code as KnownRunError];
    return t(($) => $.retrospective.run_errors[key], { issue_id: reason.issue_id ?? "" });
  }
  if (reason || run.error) {
    return t(($) => $.retrospective.run_error_fallback);
  }
  return null;
}
