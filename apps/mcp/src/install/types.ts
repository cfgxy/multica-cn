/** Contracts shared by every client-specific MCP config writer. */

export interface McpEntry {
  /** Absolute path to the built `apps/mcp` entrypoint (`dist/index.js`). */
  distPath: string;
}

export type InstallOutcome =
  | { status: "installed"; path: string }
  | { status: "skipped-not-detected" }
  | { status: "skipped-not-registered" }
  | { status: "skipped-parse-error"; path: string; reason: string };

/** Result of a read-only scan of a client's current registration state. */
export type StatusOutcome =
  | { status: "not-detected" }
  | { status: "not-installed" }
  | { status: "registered"; path: string; distPath: string; stale: boolean }
  | { status: "parse-error"; path: string; reason: string };

export type UninstallOutcome =
  | { status: "removed"; path: string }
  | { status: "not-installed" }
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
  /**
   * Read-only: reports whether this client currently has a `multica` entry,
   * and the `distPath` it points at. Never writes to the config file.
   */
  read(): StatusOutcome;
  /**
   * Removes the `multica` entry from the config, leaving every other entry
   * and the file's formatting untouched. Must never touch the file when
   * detect() is false, no `multica` entry exists, or the file fails to
   * parse.
   */
  remove(): UninstallOutcome;
}
