// Decision cards (RUYI-345): a run raises a structured question with 2-4
// options; a human member answers by picking options in the web UI; the
// platform echoes the answer as a comment that mentions the creating agent.
// Mirrors the server's handler.IssueDecisionResponse.

export interface DecisionOption {
  label: string;
}

export type IssueDecisionStatus = "open" | "answered" | "cancelled";

export interface IssueDecision {
  id: string;
  issue_id: string;
  source_comment_id: string | null;
  question: string;
  options: DecisionOption[];
  multi_select: boolean;
  recommended_indices: number[];
  status: IssueDecisionStatus;
  selected_indices: number[];
  answered_by_type: string | null;
  answered_by_id: string | null;
  answered_at: string | null;
  answer_comment_id: string | null;
  created_by_type: string;
  created_by_id: string;
  created_at: string;
  updated_at: string;
  /** Present on the answer response only — outcomes of the echo comment's trigger pipeline. */
  trigger_outcomes?: Array<Record<string, unknown>>;

  // RUYI-630 authorization-card face — present only when decision_kind is
  // "authorization" (plain question cards keep the legacy shape).
  decision_kind?: "question" | "authorization";
  visible_tier?: string;
  operator_tier?: string;
  named_approver_ids?: string[];
  approve_label?: string | null;
  deny_label?: string | null;
  expires_at?: string | null;
  answer_source?: DecisionAnswerSource | null;
  /** pending → approved/denied → executed/execute_failed; mirrors the group. */
  auth_state?: string | null;
  request_group_id?: string | null;
  action_type?: string | null;
  executed_at?: string | null;
  execution_result?: unknown;
  execution_error?: string | null;
}

/** Per-card outcome of the batch answer endpoint (RUYI-471). Mirrors handler.BatchDecisionAnswerOutcome. */
export type BatchDecisionAnswerStatus = "answered" | "conflict" | "invalid" | "not_found";

export interface BatchDecisionAnswerOutcome {
  decision_id: string;
  status: BatchDecisionAnswerStatus;
  error?: string;
  decision?: IssueDecision;
}

/** Mirrors handler.BatchAnswerIssueDecisionsResponse. Per-card failures never roll the batch back. */
export interface BatchDecisionAnswerResult {
  results: BatchDecisionAnswerOutcome[];
  echo_comment_id?: string;
  trigger_outcomes?: Array<Record<string, unknown>>;
}

export interface BatchIssueDecisionAnswer {
  decision_id: string;
  selected_indices: number[];
}

/**
 * Workspace decision inbox (RUYI-494): the cross-issue aggregation behind the
 * Decision Center. One row PER CARD — an issue with three cards yields three
 * items so an older still-open card is never hidden by a newer answered one.
 * Mirrors handler.WorkspaceDecisionInboxItem.
 */
export interface WorkspaceDecisionInboxItem extends IssueDecision {
  workspace_id: string;
  issue_number: number;
  /** Human-readable identifier, e.g. "RUYI-494" (empty when the workspace has no issue_prefix). */
  issue_identifier: string;
  issue_title: string;
}

/** Workspace totals, independent of any status filter on the list window. */
export interface DecisionInboxCounts {
  open: number;
  answered: number;
  cancelled: number;
}

/** Mirrors handler.WorkspaceDecisionInboxResponse. */
export interface WorkspaceDecisionInbox {
  items: WorkspaceDecisionInboxItem[];
  counts: DecisionInboxCounts;
}

// ── Agent authorization requests (RUYI-630) ─────────────────────────────────
// A first-class, Issue-independent carrier for agent-initiated sensitive
// operations: one row PER INVOLVED SPACE (方案甲), grouped by request_group_id.
// The target-space row is the operable second confirmation (operator tier
// forced to the target Owner); the origin-space row is a read-only state
// projection — the origin step happens on the issue thread's authorization
// link card when an issue reference exists, and on the origin row itself
// only when it does not. Mirrors handler.DecisionRequestResponse.

export type DecisionRequestStatus =
  | "pending"
  | "approved"
  | "denied"
  | "expired"
  | "revoked"
  | "executed"
  | "execute_failed";

export type DecisionRequestRole = "origin" | "target";

/** answer_source discriminates HOW a human acted (server-authoritative). */
export type DecisionAnswerSource = "card_click" | "text_token" | "batch";

export interface DecisionRequest {
  id: string;
  request_group_id: string;
  workspace_id: string;
  role: DecisionRequestRole;
  /** Only operable rows accept answers; projections mirror state. */
  operable: boolean;
  status: DecisionRequestStatus;
  action_type: string;
  action_params?: unknown;
  /** Registry-derived risk tier ("read" | "write_low"); server-authoritative. */
  risk_tier: string;
  title: string;
  detail: string;
  origin_workspace_id: string;
  origin_agent_id: string;
  origin_task_id: string | null;
  /** Title-level reference only — the summary never carries issue content. */
  origin_issue_id: string | null;
  origin_issue_title: string | null;
  operator_tier: string;
  named_approver_ids: string[];
  approve_label: string | null;
  deny_label: string | null;
  expires_at: string;
  answered_by_type: string | null;
  answered_by_id: string | null;
  answered_at: string | null;
  answer_source: DecisionAnswerSource | null;
  executed_at: string | null;
  execution_result?: unknown;
  execution_error: string | null;
  created_at: string;
  updated_at: string;
}

/** The decision-center detail: this space's row plus every space's step. */
export interface DecisionRequestDetail {
  request: DecisionRequest;
  steps: DecisionRequest[];
  /** Present on same-space requests that reference an issue. */
  card?: IssueDecision;
}

export interface DecisionRequestCounts {
  pending: number;
  closed: number;
  executed: number;
}

/** Mirrors handler.ListDecisionRequests' {items, counts} envelope. */
export interface DecisionRequestsList {
  items: DecisionRequest[];
  counts: DecisionRequestCounts;
}
