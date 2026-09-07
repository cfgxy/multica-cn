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
