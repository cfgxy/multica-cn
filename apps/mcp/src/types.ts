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
  /**
   * Optimistic-lock counter, bumped on every content edit. revision > 1 (or
   * updated_at > created_at) marks an edited comment — the recognizable
   * audit trail; there is no per-edit content history beyond this.
   */
  revision?: number;
  /** roots_only listings only: orientation stats for the thread under this root. */
  reply_count?: number;
  last_activity_at?: string;
  /** Present on create/edit: per-explicit-@ dispatch outcome for mentioned agents. */
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
  fold?: boolean;
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

export interface UpdateCommentBody {
  content: string;
  /**
   * Optimistic lock: the edit lands only when the stored revision still
   * equals this value; otherwise the server answers 409 revision_conflict
   * carrying the current revision.
   */
  expected_revision?: number;
  /**
   * Agent/squad ids whose dispatch is suppressed when the edit re-runs the
   * comment's trigger computation (content-changing edits recompute the
   * trigger surface of the new body).
   */
  suppress_agent_ids?: string[];
}

export interface UpdateIssueBody {
  status?: string;
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
  // Parent change (RUYI-351). A string re-parents the issue (server walks
  // the ancestor chain for cycles); explicit null clears the parent. The
  // null must survive serialization — same rawFields rule as the assignee.
  parent_issue_id?: string | null;
}

// RUYI-351 structured issue relations. The five caller-facing types; the
// server stores one canonical row per edge, so blocked_by / superseded_by
// writes land as their forward counterpart and relates_to is symmetric.
export type IssueRelationType =
  | "blocks"
  | "blocked_by"
  | "relates_to"
  | "supersedes"
  | "superseded_by";

export interface IssueRelationRef {
  id: string;
  identifier?: string;
  title?: string;
  status?: string;
}

export interface IssueRelationsInfo {
  issue_id: string;
  identifier?: string;
  revision?: number;
  parent?: IssueRelationRef | null;
  blocks: IssueRelationRef[];
  blocked_by: IssueRelationRef[];
  relates_to: IssueRelationRef[];
  supersedes: IssueRelationRef[];
  superseded_by: IssueRelationRef[];
}

export interface AddIssueRelationBody {
  type: IssueRelationType;
  target_issue_id: string;
  expected_revision?: number;
}

export interface AddIssueRelationResult {
  added: true;
  relation: {
    id?: string;
    type: IssueRelationType;
    source_issue_id: string;
    target_issue_id: string;
  };
  issue: { id: string; revision: number };
}

export interface RemoveIssueRelationResult {
  removed: true;
  relation: {
    type: IssueRelationType;
    source_issue_id: string;
    target_issue_id: string;
  };
  issue: { id: string; revision: number };
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
