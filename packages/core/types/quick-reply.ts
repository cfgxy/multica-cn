/**
 * A workspace's quick-reply catalog (RUYI-435).
 *
 * Workspace-level reply templates the issue comment composer offers behind a
 * single "快速回复" entry. Managed by workspace owner/admin (Web settings tab
 * or the MCP quick-reply tools); read by every member. Every client surface
 * renders these rows dynamically — the defaults live only in the server seed,
 * never hardcoded here.
 */

export interface QuickReply {
  id: string;
  workspace_id: string;
  /** Menu label. Unique within the workspace (server enforces, 409 otherwise). */
  name: string;
  /** Template body inserted into the composer on selection — never auto-sent. */
  content: string;
  /** Display order, ascending. */
  position: number;
  created_at: string;
  updated_at: string;
}

export interface ListQuickRepliesResponse {
  quick_replies: QuickReply[];
  total: number;
}

export interface CreateQuickReplyRequest {
  name: string;
  content: string;
  /** Omit to append after everything currently in the catalog. */
  position?: number;
}

/** PATCH semantics: every field optional, absent leaves the stored value. */
export interface UpdateQuickReplyRequest {
  name?: string;
  content?: string;
  position?: number;
}
