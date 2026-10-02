/**
 * Minimal wire shapes for the Multica REST API subset the MCP server uses.
 *
 * These mirror the Go handler response types (`server/internal/handler/*`)
 * for the fields this server actually surfaces. Fields not listed here are
 * ignored — the client never re-serializes the full payload.
 */

export interface WorkspaceInfo {
  id: string;
  name: string;
  slug: string;
  description?: string;
  issue_prefix?: string;
}

export interface AgentInfo {
  id: string;
  name: string;
  description?: string;
  runtime_bound?: boolean;
}

export interface ProjectInfo {
  id: string;
  workspace_id?: string;
  title: string;
  description?: string;
  /** Project-level prompt text injected into every task brief in the project. */
  instructions?: string;
  icon?: string;
  status?: string;
  priority?: string;
  lead_type?: string;
  lead_id?: string;
  start_date?: string;
  due_date?: string;
  created_at?: string;
  updated_at?: string;
  issue_count?: number;
  done_count?: number;
  resource_count?: number;
  /** Optimistic-lock token: send back as expected_revision on update. */
  revision?: number;
}

export interface CreateProjectBody {
  title: string;
  description?: string;
  instructions?: string;
  icon?: string;
  status?: string;
  priority?: string;
  lead_type?: string;
  lead_id?: string;
  start_date?: string;
  due_date?: string;
}

// PATCH semantics mirror the Go handler's rawFields contract: an omitted key
// keeps the current value, an explicit JSON null clears a nullable field.
// The nulls must survive serialization (same rule as UpdateIssueBody).
export interface UpdateProjectBody {
  expected_revision?: number;
  title?: string;
  description?: string | null;
  instructions?: string | null;
  icon?: string | null;
  status?: string;
  priority?: string;
  lead_type?: string | null;
  lead_id?: string | null;
  start_date?: string | null;
  due_date?: string | null;
}

export interface IssueInfo {
  id: string;
  workspace_id?: string;
  number: number;
  identifier: string;
  title: string;
  description?: string;
  status: string;
  status_category?: string;
  status_name?: string;
  priority?: string;
  assignee_type?: string;
  assignee_id?: string;
  creator_type?: string;
  creator_id?: string;
  parent_issue_id?: string;
  project_id?: string;
  stage?: number;
  start_date?: string;
  due_date?: string;
  created_at?: string;
  updated_at?: string;
  last_activity_at?: string;
  revision?: number;
  /** True while the last run-triggering write was suppressed (RUYI-275). */
  run_suppressed?: boolean;
}

export interface SearchIssueInfo extends IssueInfo {
  match_source?: string;
  matched_snippet?: string;
  matched_comment_snippet?: string;
}

export interface CommentInfo {
  id: string;
  issue_id?: string;
  author_type?: string;
  author_id?: string;
  content: string;
  type?: string;
  parent_id?: string;
  created_at?: string;
  updated_at?: string;
  /** Present on create: per-explicit-@ dispatch outcome for mentioned agents. */
  trigger_outcomes?: Array<{
    target_type?: string;
    target_id?: string;
    status?: string;
    reason_code?: string;
  }>;
}

export interface IssueListParams {
  status?: string;
  statuses?: string;
  status_categories?: string;
  project_id?: string;
  assignee_id?: string;
  open_only?: boolean;
  sort?: string;
  direction?: string;
  limit?: number;
  offset?: number;
}

export interface IssueListResult {
  issues: IssueInfo[];
  total: number;
}

export interface CommentListParams {
  since?: string;
  thread?: string;
  recent?: number;
  tail?: number;
  roots_only?: boolean;
  summary?: boolean;
}

export interface CreateIssueBody {
  title: string;
  description?: string;
  status?: string;
  priority?: string;
  project_id?: string;
  parent_issue_id?: string;
  assignee_type?: string;
  assignee_id?: string;
  start_date?: string;
  due_date?: string;
}

export interface QuickCreateBody {
  agent_id?: string;
  squad_id?: string;
  prompt: string;
  priority?: string;
  due_date?: string;
  project_id?: string;
  parent_issue_id?: string;
}

export interface CreateCommentBody {
  content: string;
  parent_id?: string;
}

export interface UpdateIssueBody {
  status?: string;
  // Core field edit (RUYI-350 update_issue). PATCH semantics: an omitted key
  // keeps the current value. For the four nullable fields an EXPLICIT null
  // clears the value — the null must survive serialization, because the
  // server decides "clear" by rawFields key presence (server/internal/handler/
  // issue.go), exactly like the assignee nulls below. title/description/
  // priority are plain writes: the server models them as *string, so a JSON
  // null decodes to nil and means "keep" — the tool layer never sends null
  // for them.
  title?: string;
  description?: string;
  priority?: string;
  project_id?: string | null;
  parent_issue_id?: string | null;
  start_date?: string | null;
  due_date?: string | null;
  expected_revision?: number;
  suppress_run?: boolean;
  // Assignee change. A string pair assigns/reassigns; explicit nulls clear
  // the assignee. The nulls must survive serialization — the server decides
  // unassign by rawFields ("key present as null"), so omitted keys and empty
  // strings both mean "keep the current assignee" (server/internal/handler/
  // issue.go, refreshUntouchedNullableIssueParams).
  assignee_type?: string | null;
  assignee_id?: string | null;
  // Injected into the triggered run's opening context; dropped when the
  // write starts no run (suppress_run, backlog parking, member/unassign).
  handoff_note?: string;
}

export interface ActiveTaskInfo {
  id?: string;
  status?: string;
  agent_id?: string;
  agent_name?: string;
  queued_at?: string;
  created_at?: string;
  [key: string]: unknown;
}

// RUYI-292 run lifecycle. status is the raw server value: queued, dispatched,
// deferred, waiting_local_directory, running, cancel_requested, completed,
// failed, cancelled. Raw values, not display buckets — callers merge for
// display (queued/dispatched/deferred/waiting_local_directory → "pending").
export interface RunInfo {
  id: string;
  status: string;
  agent_id?: string;
  issue_id?: string;
  created_at?: string;
  started_at?: string;
  completed_at?: string;
  error?: string;
  failure_reason?: string;
  attempt?: number;
  rerun_of_task_id?: string;
  retry_of_task_id?: string;
  cancel_requested_at?: string;
  cancel_requested_by_user_id?: string;
  [key: string]: unknown;
}

// One node of a run's retry chain (get_run detail): manual-rerun and
// system-retry edges both visible, so cancelled→retried→completed history
// reads as one chain even when the edge kinds differ.
export interface RunLineageEntry {
  id: string;
  agent_id: string;
  status: string;
  created_at?: string;
  completed_at?: string;
  attempt: number;
  failure_reason?: string;
  rerun_of_task_id?: string;
  retry_of_task_id?: string;
  cancel_requested_by_user_id?: string;
}

export interface RunDetail {
  task: RunInfo;
  ancestors: RunLineageEntry[];
  descendants: RunLineageEntry[];
}

// Server cancel-matrix answer: code ∈ cancelled | cancel_requested |
// already_cancelling | already_cancelled | not_cancellable (409).
export interface CancelRunResult {
  code: string;
  message?: string;
  task: RunInfo;
}
