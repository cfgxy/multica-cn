/**
 * Prompt governance version lifecycle types (RUYI-285).
 *
 * The server's prompt_version table is the append-only history for the four
 * prompt tiers (RUYI-183); these types describe one row of that line as the
 * governance API returns it. `content` is always present: unlike the
 * marketplace catalog this is a private, workspace-scoped audit surface, and
 * both the editor preload and the client-side comparison read from the list.
 */

/** How a version came into being. Matches the server CHECK constraint. */
export type PromptVersionSource = "import" | "edit" | "revert" | "auto_snapshot" | "snapshot";

export interface PromptGovernanceVersion {
  id: string;
  scope: string;
  scope_id: string;
  version: number;
  content: string;
  content_sha256: string;
  source: string;
  /** Set on source "revert": which historical version's content was copied. */
  source_version?: number;
  change_note: string;
  author_user_id?: string;
  author_note_issue_id?: string;
  /** Which scanner revision cleared this content; "" on import/backfill rows. */
  scanner_revision: string;
  created_at: string;
}

export interface PromptGovernanceVersionList {
  versions: PromptGovernanceVersion[];
  total: number;
}

export interface SavePromptGovernanceVersionRequest {
  content: string;
  change_note: string;
}

/**
 * Body of a manual snapshot (RUYI-285 rework). There is no content field by
 * design: the server versions the entity's currently effective content — the
 * caller only optionally labels the row.
 */
export interface SnapshotPromptGovernanceVersionRequest {
  change_note?: string;
}
