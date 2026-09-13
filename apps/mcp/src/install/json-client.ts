import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

import type { ClientTarget, InstallOutcome, McpEntry } from "./types.js";

/**
 * Reads a `serversPath` (dot path, e.g. "mcpServers") inside a JSON config
 * file, sets `serversPath.multica` to `buildEntry(entry)`, and writes the
 * file back with the rest of the document untouched. Creates the file (and
 * its parent directory) when it does not exist yet — but only when the
 * caller's `detect()` already confirmed the client is installed.
 */
export function createJsonClientTarget(options: {
  id: string;
  label: string;
  configPath: string;
  detect: () => boolean;
  serversPath: string[];
  buildEntry: (entry: McpEntry) => unknown;
}): ClientTarget {
  const { id, label, configPath, detect, serversPath, buildEntry } = options;

  function apply(entry: McpEntry): InstallOutcome {
    if (!detect()) {
      return { status: "skipped-not-detected" };
    }

    let root: Record<string, unknown> = {};
    if (existsSync(configPath)) {
      let raw: string;
      try {
        raw = readFileSync(configPath, "utf8");
      } catch (error) {
        return {
          status: "skipped-parse-error",
          path: configPath,
          reason: `无法读取配置文件: ${error instanceof Error ? error.message : String(error)}`,
        };
      }
      try {
        const parsed: unknown = raw.trim().length === 0 ? {} : JSON.parse(raw);
        if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
          return {
            status: "skipped-parse-error",
            path: configPath,
            reason: "配置文件根节点不是 JSON 对象",
          };
        }
        root = parsed as Record<string, unknown>;
      } catch (error) {
        return {
          status: "skipped-parse-error",
          path: configPath,
          reason: `JSON 解析失败: ${error instanceof Error ? error.message : String(error)}`,
        };
      }
    }

    let cursor: Record<string, unknown> = root;
    for (const [index, key] of serversPath.entries()) {
      const isLast = index === serversPath.length - 1;
      const existing = cursor[key];
      if (isLast) {
        const servers =
          existing !== null && typeof existing === "object" && !Array.isArray(existing)
            ? (existing as Record<string, unknown>)
            : {};
        servers.multica = buildEntry(entry);
        cursor[key] = servers;
      } else {
        const next =
          existing !== null && typeof existing === "object" && !Array.isArray(existing)
            ? (existing as Record<string, unknown>)
            : {};
        cursor[key] = next;
        cursor = next;
      }
    }

    mkdirSync(dirname(configPath), { recursive: true });
    writeFileSync(configPath, `${JSON.stringify(root, null, 2)}\n`, "utf8");
    return { status: "installed", path: configPath };
  }

  return { id, label, detect, apply };
}
