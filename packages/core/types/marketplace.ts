/** The extension kinds the unified marketplace can install. */
export type MarketplaceItemKind = "skill" | "mcp";

/**
 * One value an MCP install must collect before the entry will run.
 *
 * `secret: true` means the value is a credential: it is masked on input and,
 * once installed, it lives in the workspace MCP library's write-only config
 * column, which no read endpoint returns. There is no way to display it again.
 */
export interface MarketplacePlaceholder {
  key: string;
  label: string;
  description: string;
  secret: boolean;
  required: boolean;
}

/**
 * One catalog entry as the marketplace listing shows it.
 *
 * The MCP configuration TEMPLATE is deliberately absent: the server renders it
 * from the values an install submits, so nothing here carries — or can be made
 * to carry — credential material.
 *
 * `installed` reflects whether the workspace already has the skill or MCP
 * server this entry would create, matched on the name the install would take.
 */
export interface MarketplaceItem {
  key: string;
  /**
   * A known MarketplaceItemKind, or any string a newer backend introduces —
   * an unknown kind still parses and the listing renders it as uninstallable
   * rather than dropping the entry.
   */
  kind: string;
  name: string;
  summary: string;
  description: string;
  publisher: string;
  homepage_url: string;
  categories: string[];
  /** Present on skill entries: the source the existing import path fetches. */
  source_url?: string;
  /** Present on MCP entries that need configuration. */
  placeholders?: MarketplacePlaceholder[];
  installed: boolean;
  installed_id?: string;
}

/** Whether a published listing is live or a withdrawn tombstone. */
export type MarketplaceListingState = "published" | "withdrawn";

/**
 * One listing this workspace has published (RUYI-99).
 *
 * Unlike MarketplaceItem this DOES carry `config_template`, because the
 * publisher authored it and needs it back to edit. That is not a hole in the
 * write-only MCP boundary: the template holds `${placeholder}` tokens, never
 * values, and the publish flow never reads workspace_mcp_server.config to build
 * one.
 *
 * `source_workspace_id` is absent by design — the management view is already
 * scoped to the caller's workspace, so the field would only be a way to leak
 * org structure if this type were ever reused on a cross-workspace route.
 */
export interface MarketplaceListing {
  id: string;
  /** The catalog key this listing appears under once merged into the catalog. */
  key: string;
  kind: string;
  name: string;
  publisher_display_name: string;
  summary: string;
  description: string;
  homepage_url: string;
  categories: string[];
  /** Present on skill listings: the public source an install fetches. */
  source_url?: string;
  /** Present on MCP listings: the entry template, placeholders unsubstituted. */
  config_template?: unknown;
  /** The template's transport classification, labelled by the server. */
  transport?: string;
  placeholders?: MarketplacePlaceholder[];
  /** A known MarketplaceListingState, or a state a newer backend introduces. */
  state: string;
  /**
   * Optimistic-concurrency token. An update or withdrawal must send back the
   * revision it read; a stale one is refused with 409 rather than overwriting
   * whoever edited in between.
   */
  revision: number;
  published_at?: string;
  withdrawn_at?: string;
  created_at: string;
  updated_at: string;
}

/**
 * One secret-scan finding from a rejected publish.
 *
 * Location only — category, rule, field, line and a fixed mask. The matched
 * text never leaves the server, so a finding can be rendered in the UI and
 * logged without becoming a way to exfiltrate what was pasted.
 */
export interface MarketplaceScanFinding {
  category: string;
  rule: string;
  field: string;
  line: number;
  mask: string;
}

/** The body of a 422 from publish or update: the scan blocked the content. */
export interface MarketplaceScanError {
  error: string;
  scanner_revision: string;
  findings: MarketplaceScanFinding[];
  truncated: boolean;
}
