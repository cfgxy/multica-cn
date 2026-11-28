export type IssueACStatus =
  | "uncompleted"
  | "pending_verification"
  | "verified"
  | "rejected";

export type IssueTodoStatus =
  | "pending"
  | "in_progress"
  | "pending_verification"
  | "completed"
  | "blocked";

export interface IssueACEvidence {
  type: string; // "run" | "commit" | "pr" | "test" | "screenshot" | "text" | "link"
  url?: string;
  note?: string;
  created_at?: string;
  created_by?: string;
}

export interface IssueAcceptanceCriteria {
  id: string;
  workspace_id: string;
  issue_id: string;
  key: string;
  title: string;
  status: IssueACStatus;
  evidence: IssueACEvidence[];
  rejection_reason: string;
  verified_at?: string;
  verified_by_type?: string;
  verified_by_id?: string;
  position: number;
  created_at: string;
  updated_at: string;
  linked_todo_ids?: string[];
}

export interface IssueTodo {
  id: string;
  workspace_id: string;
  issue_id: string;
  title: string;
  status: IssueTodoStatus;
  next_step: string;
  blocked_reason: string;
  assignee_type?: string;
  assignee_id?: string;
  ac_ids: string[];
  evidence: IssueACEvidence[];
  position: number;
  created_at: string;
  updated_at: string;
}

export interface IssueTaskStateAudit {
  id: string;
  workspace_id: string;
  issue_id: string;
  target_type: "ac" | "todo" | string;
  target_id: string;
  action: string;
  from_status: string;
  to_status: string;
  actor_type: string;
  actor_id: string;
  evidence: IssueACEvidence[];
  note: string;
  created_at: string;
}

export interface IssueLedgerSummary {
  ac_total: number;
  ac_verified: number;
  ac_pending: number;
  ac_rejected: number;
  todo_total: number;
  todo_completed: number;
  todo_in_progress: number;
  todo_pending: number;
  todo_blocked: number;
  current_next_steps: string[];
}

export interface IssueLedger {
  acs: IssueAcceptanceCriteria[];
  todos: IssueTodo[];
  audit: IssueTaskStateAudit[];
  summary: IssueLedgerSummary;
}

export interface CreateIssueACRequest {
  key?: string;
  title: string;
}

export interface UpdateIssueACRequest {
  title?: string;
  position?: number;
}

export interface VerifyIssueACRequest {
  evidence?: string;
}

export interface RejectIssueACRequest {
  reason: string;
  evidence?: string;
}

export interface CreateIssueTodoRequest {
  title: string;
  ac_ids?: string[];
  next_step?: string;
  assignee_type?: string;
  assignee_id?: string;
}

export interface UpdateIssueTodoRequest {
  title?: string;
  status?: IssueTodoStatus;
  ac_ids?: string[];
  next_step?: string;
  blocked_reason?: string;
  evidence_urls?: string[];
  assignee_type?: string;
  assignee_id?: string;
  position?: number;
}

export interface ReopenIssueTodoRequest {
  next_step?: string;
}
