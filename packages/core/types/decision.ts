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
