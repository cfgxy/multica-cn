// Issue suspension (RUYI-690): "waiting" as a dimension orthogonal to the
// lifecycle status. Mirrors handler.IssueSuspensionResponse — one row per
// suspension, retained after release as the audit trail; issue.suspended_at
// is the read-side snapshot the badge and gates key on.

export type SuspensionStatus = "active" | "resumed" | "cancelled";

export type SuspensionWaitingOnType = "external" | "manual" | "issue" | "decision";

/** Mirrors handler.ResumePredicate's closed set, denormalized to JSON text. */
export interface ResumePredicate {
  kind: "issue_done" | "decision_answered";
  issue_id?: string;
  decision_id?: string;
}

export interface IssueSuspension {
  id: string;
  workspace_id: string;
  issue_id: string;
  status: SuspensionStatus;
  reason: string;
  waiting_on_type: SuspensionWaitingOnType;
  waiting_on_issue_id?: string | null;
  waiting_on_decision_id?: string | null;
  /** Server-built human summary of the wait target ("RUYI-123 · title"). */
  waiting_on_summary: string;
  /** Free text from the actor; empty string when absent. */
  resume_condition: string;
  /** Raw JSON of the machine predicate; null for external/manual waits. */
  resume_predicate?: string | null;
  suspended_by_type: string;
  suspended_by_id: string;
  suspended_at: string;
  resumed_by_type?: string | null;
  resumed_by_id?: string | null;
  resumed_at?: string | null;
  resume_note: string;
  cancelled_by_type?: string | null;
  cancelled_by_id?: string | null;
  cancelled_at?: string | null;
  cancel_note: string;
  /** Set when the release was an auto-resume; identifies the predicate. */
  resumed_by_predicate?: string | null;
}

/**
 * Mirrors handler.IssueSuspensionTargetResponse: the wait target's current
 * state, denormalized so the aggregate view renders it with no extra fetch.
 */
export interface IssueSuspensionTarget {
  issue_id?: string;
  identifier?: string;
  title?: string;
  status?: string;
  decision_id?: string;
  question?: string | null;
  card_status?: string;
}

/**
 * Mirrors handler.WorkspaceIssueSuspensionResponse — a suspension row with
 * its issue context riding along (the aggregate view's row shape).
 */
export interface WorkspaceIssueSuspension extends IssueSuspension {
  issue_identifier: string;
  issue_title: string;
  issue_status: string;
  issue_suspended_at: string;
  waiting_on_target?: IssueSuspensionTarget | null;
}

/** Mirrors handler.WorkspaceSuspensionsResponse. */
export interface WorkspaceSuspensionList {
  suspensions: WorkspaceIssueSuspension[];
  total: number;
}

/**
 * Mirrors handler.IssueSuspensionMutationResponse: the release payload —
 * the updated suspension plus the refreshed issue snapshot (suspended_at
 * cleared). Mutations return this shape; suspend returns the bare issue.
 */
export interface IssueSuspensionMutation {
  suspension: IssueSuspension;
  issue: {
    id: string;
    suspended_at?: string | null;
    [key: string]: unknown;
  };
}
