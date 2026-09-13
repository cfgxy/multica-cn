/** Contracts shared by every client-specific MCP config writer. */

export interface McpEntry {
  /** Absolute path to the built `apps/mcp` entrypoint (`dist/index.js`). */
  distPath: string;
}

export type InstallOutcome =
  | { status: "installed"; path: string }
  | { status: "skipped-not-detected" }
  | { status: "skipped-parse-error"; path: string; reason: string };

export interface ClientTarget {
  /** Stable id used in reports and tests, e.g. "claude-code". */
  id: string;
  /** Human-readable name, e.g. "Claude Code". */
  label: string;
  /** Returns true when this client appears to be installed on the machine. */
  detect(): boolean;
  /**
   * Reads the existing config (if any), merges in the `multica` entry, and
   * writes the result. Must never touch the file when detect() is false or
   * when the existing file fails to parse.
   */
  apply(entry: McpEntry): InstallOutcome;
}
