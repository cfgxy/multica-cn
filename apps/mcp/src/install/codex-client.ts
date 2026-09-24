import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

import type { ClientTarget, InstallOutcome, McpEntry, StatusOutcome, UninstallOutcome } from "./types.js";

const TABLE_HEADER = "[mcp_servers.multica]";
const ARGS_LINE = /^\s*args\s*=\s*\[\s*"((?:[^"\\]|\\.)*)"\s*\]\s*$/;

function unescapeTomlString(raw: string): string {
  return raw.replace(/\\(.)/g, "$1");
}

/** Locates the `[mcp_servers.multica]` table's line range (header inclusive,
 * end exclusive) in an already-normalized (no trailing blank line) list of
 * lines, or `undefined` when no such table exists. */
function findTable(lines: string[]): { start: number; end: number } | undefined {
  const start = lines.findIndex((line) => line.trim() === TABLE_HEADER);
  if (start === -1) {
    return undefined;
  }
  let end = lines.length;
  for (let i = start + 1; i < lines.length; i += 1) {
    if (/^\s*\[/.test(lines[i]!)) {
      end = i;
      break;
    }
  }
  return { start, end };
}

/** Normalizes away a trailing blank line from the file's own trailing
 * newline so every rebuild ends with exactly one `\n`, keeping repeated
 * runs byte-identical regardless of which branch (append/replace/remove)
 * produced the previous version. */
function normalizeLines(source: string): string[] {
  const lines = source.split("\n");
  if (lines.length > 0 && lines[lines.length - 1] === "") {
    return lines.slice(0, -1);
  }
  return lines;
}

function replaceOrAppendTable(source: string, table: string): string {
  const lines = normalizeLines(source);
  const found = findTable(lines);

  if (found === undefined) {
    const before = lines.length > 0 ? [...lines, ""] : [];
    return [...before, ...table.split("\n")].join("\n") + "\n";
  }

  const before = lines.slice(0, found.start);
  const after = lines.slice(found.end);
  const rebuilt = [...before, ...table.split("\n"), ...after];
  return rebuilt.join("\n") + "\n";
}

/** Extracts the `distPath` from an existing `[mcp_servers.multica]` table's
 * `args = ["..."]` line, or `undefined` if the table (or that line) is not
 * found — mirrors `buildMulticaTable`'s escaping in reverse. */
function extractDistPathFromTable(source: string): string | undefined {
  const lines = normalizeLines(source);
  const found = findTable(lines);
  if (found === undefined) {
    return undefined;
  }
  for (let i = found.start + 1; i < found.end; i += 1) {
    const match = ARGS_LINE.exec(lines[i]!);
    if (match) {
      return unescapeTomlString(match[1]!);
    }
  }
  return undefined;
}

/** Removes the `[mcp_servers.multica]` table (if present), leaving every
 * other table byte-for-byte untouched. Returns `undefined` when no such
 * table exists — callers use that to report "not-installed" without
 * writing. */
function removeTable(source: string): string | undefined {
  const lines = normalizeLines(source);
  const found = findTable(lines);
  if (found === undefined) {
    return undefined;
  }
  const before = lines.slice(0, found.start);
  const after = lines.slice(found.end);
  // Drop one blank separator line left behind between the preceding table
  // and the removed block, if present.
  if (before.length > 0 && before[before.length - 1] === "") {
    before.pop();
  }
  const rebuilt = [...before, ...(before.length > 0 && after.length > 0 ? [""] : []), ...after];
  return rebuilt.length > 0 ? rebuilt.join("\n") + "\n" : "";
}

/**
 * Codex stores MCP servers as TOML tables under `[mcp_servers.<name>]` in
 * `~/.codex/config.toml`. We do not carry a TOML parser dependency, so this
 * performs a line-scoped block replace: locate an existing
 * `[mcp_servers.multica]` table (from its header to the next top-level
 * `[...]` header or EOF) and swap it for ours, or append a fresh table when
 * none exists yet. Every other table in the file — including other
 * `[mcp_servers.*]` entries — is left byte-for-byte untouched.
 */
export function buildMulticaTable(entry: McpEntry): string {
  const escaped = entry.distPath.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
  return [TABLE_HEADER, 'command = "node"', `args = ["${escaped}"]`, 'type = "stdio"'].join("\n");
}

function readSource(configPath: string): { source: string } | { reason: string } | { missing: true } {
  if (!existsSync(configPath)) {
    return { missing: true };
  }
  let source: string;
  try {
    source = readFileSync(configPath, "utf8");
  } catch (error) {
    return { reason: `无法读取配置文件: ${error instanceof Error ? error.message : String(error)}` };
  }
  // Minimal sanity check: bail out rather than risk corrupting a file whose
  // brackets are already unbalanced (e.g. binary/garbled content).
  const opens = (source.match(/\[/g) ?? []).length;
  const closes = (source.match(/\]/g) ?? []).length;
  if (opens !== closes) {
    return { reason: "TOML 文件方括号不匹配，判定为不可安全解析" };
  }
  return { source };
}

export function createCodexClientTarget(options: {
  configPath: string;
  detect: () => boolean;
}): ClientTarget {
  const { configPath, detect } = options;

  function apply(entry: McpEntry): InstallOutcome {
    if (!detect()) {
      return { status: "skipped-not-detected" };
    }

    const parsed = readSource(configPath);
    if ("reason" in parsed) {
      return { status: "skipped-parse-error", path: configPath, reason: parsed.reason };
    }
    const source = "missing" in parsed ? "" : parsed.source;

    const updated = replaceOrAppendTable(source, buildMulticaTable(entry));
    mkdirSync(dirname(configPath), { recursive: true });
    writeFileSync(configPath, updated, "utf8");
    return { status: "installed", path: configPath };
  }

  function read(): StatusOutcome {
    if (!detect()) {
      return { status: "not-detected" };
    }

    const result = readSource(configPath);
    if ("reason" in result) {
      return { status: "parse-error", path: configPath, reason: result.reason };
    }
    if ("missing" in result) {
      return { status: "not-installed" };
    }

    const distPath = extractDistPathFromTable(result.source);
    if (distPath === undefined) {
      return { status: "not-installed" };
    }
    return { status: "registered", path: configPath, distPath, stale: !existsSync(distPath) };
  }

  function remove(): UninstallOutcome {
    if (!detect()) {
      return { status: "skipped-not-detected" };
    }

    const result = readSource(configPath);
    if ("reason" in result) {
      return { status: "skipped-parse-error", path: configPath, reason: result.reason };
    }
    if ("missing" in result) {
      return { status: "not-installed" };
    }

    const updated = removeTable(result.source);
    if (updated === undefined) {
      return { status: "not-installed" };
    }
    writeFileSync(configPath, updated, "utf8");
    return { status: "removed", path: configPath };
  }

  return { id: "codex", label: "Codex", detect, apply, read, remove };
}
