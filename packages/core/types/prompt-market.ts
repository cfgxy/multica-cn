/**
 * Types for the prompt marketplace (RUYI-100): publishing an agent or squad
 * prompt as a versioned asset, and installing and applying one.
 *
 * Two things are deliberately absent from every shape here, because they are
 * absent from the API:
 *  - the source workspace of a published version. The catalog shows the
 *    publisher's display name and nothing else (Owner decision D4), so there is
 *    no field for a client to accidentally surface.
 *  - any raw secret value from the publish-time scan. A finding carries a
 *    category, a rule and a line, never the matched text.
 */

/** Which kind of prompt an asset is. The two are not interchangeable. */
export type PromptAssetKind = "agent_prompt" | "squad_prompt";

/** The object a prompt is published from, or applied to. */
export type PromptTargetType = "agent" | "squad";

/**
 * A published version's lifecycle.
 *
 * `withdrawn` is not a delete: the version stays readable for workspaces that
 * already installed it, and only new installs and applies are refused.
 */
export type PromptVersionState = "draft" | "published" | "withdrawn";

/**
 * The licences a publisher can choose (Owner decision D2). Required at draft
 * time so no asset reaches the catalog without reuse terms.
 */
export type PromptLicenseCode =
  | "cc0"
  | "cc-by-4.0"
  | "internal-only"
  | "all-rights-reserved";

/**
 * What the apply should do when the target already has prompt text.
 *
 * `preserve` is the default everywhere, including when the field is omitted:
 * an overwrite is only ever the result of an explicit choice.
 */
export type PromptApplyStrategy = "preserve" | "replace";

/** One draft or immutable published version. */
export interface PromptVersion {
  id: string;
  /** Groups a version line. v1 and v2 of one asset share a series. */
  series_id: string;
  kind: string;
  /** Null on a draft: a version number is assigned at publish time. */
  version: number | null;

  name: string;
  summary: string;
  audience: string;
  categories: string[];
  license_code: string;
  usage_notes: string;
  companions: string;

  /** The publisher's display name, snapshotted at publish time. */
  publisher_display_name: string;

  /**
   * The prompt body. Empty in a discovery listing — the catalog omits it, so a
   * listing render cannot leak the full text of a paid or restricted asset
   * before install.
   */
  content: string;
  content_sha256: string;

  state: string;
  visibility: string;

  /** Only populated for a reader who manages the source object. */
  source_type?: string;
  source_id?: string;

  scanner_revision: string;
  published_at: string | null;
  withdrawn_at: string | null;
  created_at: string;
  updated_at: string;
}

/** A catalog entry: a version plus this workspace's relationship to it. */
export interface PromptMarketItem extends PromptVersion {
  installed: boolean;
  install_id?: string;
  installed_version?: number;
  /** True when the catalog's latest version is ahead of what is installed. */
  update_available: boolean;
}

/**
 * One row of the workspace's install library.
 *
 * Holding this changes no prompt. Applying it to an agent or squad is a
 * separate, confirmed step (Owner decision: two-phase install/apply).
 */
export interface PromptInstall {
  id: string;
  series_id: string;
  kind: string;
  installed_version_id: string;
  installed_version: number;
  installed_content_sha256: string;
  name: string;
  summary: string;
  publisher_display_name: string;
  license_code: string;
  installed_at: string;
  updated_at: string;
}

/**
 * What the confirmation dialog renders before an apply.
 *
 * Both sides of the diff come from the server; rendering it is the client's
 * job. `preview_token` must be echoed back on apply — it is the proof that a
 * human saw THIS diff, and it stops matching if the target changes underneath.
 */
export interface PromptApplyPreview {
  target_type: string;
  target_id: string;

  current_content: string;
  current_sha256: string;
  incoming_content: string;
  incoming_sha256: string;

  /** Nothing to lose: the UI can offer a one-click apply. */
  target_empty: boolean;
  /** Applying would change nothing. */
  identical: boolean;
  /** Applying would overwrite existing text; `replace` must be chosen. */
  requires_confirmation: boolean;

  preview_token: string;
  preview_expires_at: string;
}

/** The result of an apply. */
export interface PromptApplyResult {
  target_type: string;
  target_id: string;
  applied_version: number;
  content_sha256: string;
  /** Whether the one-step undo is available. */
  can_restore: boolean;
}

/** The result of a restore. */
export interface PromptRestoreResult {
  target_type: string;
  target_id: string;
  content_sha256: string;
  restored: boolean;
}

/**
 * What the status strip on an agent or squad reads.
 *
 * `applied_content_intact` is false once the prompt has been hand-edited since
 * the apply. That is exactly what makes a restore refuse, so the UI can explain
 * it before the click rather than after the error.
 */
export interface PromptTargetState {
  target_type: string;
  target_id: string;
  series_id: string;
  version_id: string;
  applied_version: number | null;
  applied_at: string;
  applied_content_intact: boolean;
  can_restore: boolean;
  current_sha256: string;
}

/**
 * The answer to a scan that found nothing.
 *
 * "Passed" is narrower than "safe": it means no rule of `scanner_revision`
 * matched. The revision travels with the result so a publisher can tell which
 * detector set cleared their prompt, and so a later revision finding something
 * this one missed is a legible change rather than a contradiction.
 */
export interface PromptScanResult {
  scanner_revision: string;
  passed: boolean;
}

/**
 * One secret-scan hit that blocked a publish.
 *
 * There is no override anywhere in the flow, and no field here carries the
 * matched value or any prefix of it — `mask` is a fixed-width mask, not a
 * partial reveal. The publisher edits the prompt and republishes.
 */
export interface PromptSecretFinding {
  category: string;
  rule: string;
  line: number;
  mask: string;
}

/** The 422 body a blocked publish returns. */
export interface PromptSecretScanBlocked {
  code: string;
  error: string;
  scanner_revision: string;
  findings: PromptSecretFinding[];
  /** The prompt was longer than the scanner reads; findings may be partial. */
  truncated: boolean;
}
