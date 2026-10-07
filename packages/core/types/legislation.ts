/**
 * Prompt legislation (RUYI-305 E2) and the daily retrospective (E3).
 *
 * A proposal is a clause-level draft against one of the four prompt carriers.
 * The state machine draft → pending_owner → (gate) → enacted | gate_failed |
 * rejected is enforced server-side; rejected rows stay retrievable and a
 * gate-failed row returns to draft through rework. Approval runs the
 * legislation gate in a sandbox synthesis and only enacts on a clean pass —
 * the client never writes carrier content itself (E5: the carrier write and
 * the prompt_version snapshot stay behind the server boundary).
 */

export type PromptProposalCarrierScope = "workspace" | "project" | "squad" | "agent";

export type PromptProposalChangeKind = "add_clause" | "remove_clause";

/**
 * One gate report line, mirroring the server's legislation.Finding: the
 * carrier-relative line number when known, the severity, and the message.
 */
export interface LegislationGateFinding {
  line?: number;
  level: string;
  message: string;
}

/** Provenance of a pool-merged proposal: which run merged in which issue. */
export interface PromptProposalMergeRef {
  issue_id: string;
  run_id: string;
}

/**
 * The warn-only jev advisory sidecar report (RUYI-347): the local binary
 * engine's risk read over the clause text (submit stage) or the synthesized
 * full carrier (gate stage). Advisory only — it never blocks the state
 * machine. `available: false` carries the skip reason instead of a verdict;
 * `jev_advisory: null` means the layer is off or the row was never checked.
 */
export interface PromptJevAdvisory {
  stage: "submit" | "gate";
  available: boolean;
  engine?: string;
  p_failure?: number;
  threshold?: number;
  warn: boolean;
  decision?: string;
  input_sha256: string;
  skipped_reason?: string;
  checked_at: string;
}

export interface PromptProposal {
  id: string;
  workspace_id: string;
  carrier_scope: PromptProposalCarrierScope;
  carrier_scope_id: string;
  target_section: string;
  change_kind: PromptProposalChangeKind;
  clause_name: string;
  clause_text: string;
  /** The content-gate five answers, all required before submission. */
  gate_answer_layer: string;
  gate_answer_retention: string;
  gate_answer_cost: string;
  gate_answer_conflict: string;
  gate_answer_dedup: string;
  evidence_anchors: { issue_id?: string; quote?: string }[];
  status: string;
  gate_errors: LegislationGateFinding[];
  gate_warnings: LegislationGateFinding[];
  /** Warn-only local-engine advisory; null = layer off / unchecked. */
  jev_advisory?: PromptJevAdvisory | null;
  enacted_version?: number;
  rollback_reason: string;
  merged_from: PromptProposalMergeRef[];
  source: string;
  created_by_type: string;
  created_by_id?: string;
  audit_log: unknown[];
  created_at: string;
  updated_at: string;
}

export interface PromptProposalDraftRequest {
  carrier_scope: PromptProposalCarrierScope;
  carrier_scope_id: string;
  target_section?: string;
  change_kind: PromptProposalChangeKind;
  clause_name: string;
  clause_text?: string;
  gate_answer_layer: string;
  gate_answer_retention: string;
  gate_answer_cost: string;
  gate_answer_conflict: string;
  gate_answer_dedup: string;
  evidence_anchors?: { issue_id?: string; quote?: string }[];
}

/** One line of the sandbox full-carrier diff shown before approval. */
export interface LegislationDiffLine {
  kind: "context" | "add" | "del";
  text: string;
}

export interface PromptProposalPreview {
  proposal: PromptProposal;
  diff: LegislationDiffLine[];
  current_sha256: string;
  baseline_used: boolean;
}

/** Per-id outcome of a batch approve, in input order. status is the HTTP code. */
export interface PromptProposalBatchOutcome {
  id: string;
  status: number;
  body: string;
}

export interface RetrospectiveConfig {
  enabled: boolean;
  include_in_review: boolean;
  window_days: number;
  llm: RetrospectiveLLMConfigView;
}

/**
 * Where a resolved LLM field came from (RUYI-552). "workspace" beats
 * "deployment" beats "default" (a built-in); "none" means nothing anywhere.
 */
export type RetrospectiveLLMSource = "workspace" | "deployment" | "default" | "none";

/** What the workspace itself has saved. The key is only ever a masked hint. */
export interface RetrospectiveLLMStored {
  base_url: string;
  model: string;
  api_key_set: boolean;
  api_key_hint: string;
}

/** The effective resolution after per-field fallback, as the server sees it. */
export interface RetrospectiveLLMEffective {
  source: RetrospectiveLLMSource;
  base_url: string;
  base_url_source: RetrospectiveLLMSource;
  model: string;
  model_source: RetrospectiveLLMSource;
  api_key_source: RetrospectiveLLMSource;
  api_key_hint: string;
  /** Non-empty: the saved config exists but cannot be used, and this says why. */
  issue: string;
}

export interface RetrospectiveLLMConfigView {
  stored: RetrospectiveLLMStored;
  effective: RetrospectiveLLMEffective;
}

/**
 * PATCH body for the retrospective config. Field semantics (RUYI-552):
 * omit = keep the saved value; `api_key: ""` clears the saved key; a
 * non-empty `api_key` replaces it. base_url/model follow the same
 * omit-vs-set rule, with "" meaning "fall back to the deployment default".
 */
export interface RetrospectiveLLMPatch {
  base_url?: string;
  model?: string;
  api_key?: string;
}

export interface RetrospectiveConfigPatch {
  enabled?: boolean;
  include_in_review?: boolean;
  window_days?: number;
  llm?: RetrospectiveLLMPatch;
}

export interface RetrospectiveRunDetail {
  /** False on a failed run that never had an effective LLM (RUYI-552). */
  llm_configured?: boolean;
  [key: string]: unknown;
}

export interface RetrospectiveRun {
  id: string;
  status: string;
  trigger: string;
  window_start: string;
  window_end: string;
  issues_scanned: number;
  issues_analyzed: number;
  proposals_created: number;
  proposals_merged: number;
  duplicates_skipped: number;
  error: string;
  /** Raw server JSONB — its only typed consumer is llm_configured above. */
  detail?: RetrospectiveRunDetail | null;
  created_at: string;
}
