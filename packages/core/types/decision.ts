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
