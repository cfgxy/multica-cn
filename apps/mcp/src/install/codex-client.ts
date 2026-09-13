import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

import type { ClientTarget, InstallOutcome, McpEntry } from "./types.js";

const TABLE_HEADER = "[mcp_servers.multica]";

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

function replaceOrAppendTable(source: string, table: string): string {
  // Normalize away a trailing blank line from the file's own trailing
  // newline so the rebuilt output always ends with exactly one `\n`,
  // regardless of whether we hit the append or replace branch below —
  // otherwise the two branches disagree on trailing whitespace and a
  // second run (which always takes the replace branch) is not
  // byte-identical to the first (which takes the append branch).
  let lines = source.split("\n");
  if (lines.length > 0 && lines[lines.length - 1] === "") {
    lines = lines.slice(0, -1);
  }

  const headerIndex = lines.findIndex((line) => line.trim() === TABLE_HEADER);

  if (headerIndex === -1) {
    const before = lines.length > 0 ? [...lines, ""] : [];
    return [...before, ...table.split("\n")].join("\n") + "\n";
  }

  let endIndex = lines.length;
  for (let i = headerIndex + 1; i < lines.length; i += 1) {
    if (/^\s*\[/.test(lines[i]!)) {
      endIndex = i;
      break;
    }
  }

  const before = lines.slice(0, headerIndex);
  const after = lines.slice(endIndex);
  const rebuilt = [...before, ...table.split("\n"), ...after];
  return rebuilt.join("\n") + "\n";
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

    let source = "";
    if (existsSync(configPath)) {
      try {
        source = readFileSync(configPath, "utf8");
      } catch (error) {
        return {
          status: "skipped-parse-error",
          path: configPath,
          reason: `无法读取配置文件: ${error instanceof Error ? error.message : String(error)}`,
        };
      }
      // Minimal sanity check: bail out rather than risk corrupting a file
      // whose brackets are already unbalanced (e.g. binary/garbled content).
      const opens = (source.match(/\[/g) ?? []).length;
      const closes = (source.match(/\]/g) ?? []).length;
      if (opens !== closes) {
        return {
          status: "skipped-parse-error",
          path: configPath,
          reason: "TOML 文件方括号不匹配，判定为不可安全解析",
        };
      }
    }

    const updated = replaceOrAppendTable(source, buildMulticaTable(entry));
    mkdirSync(dirname(configPath), { recursive: true });
    writeFileSync(configPath, updated, "utf8");
    return { status: "installed", path: configPath };
  }

  return { id: "codex", label: "Codex", detect, apply };
}
