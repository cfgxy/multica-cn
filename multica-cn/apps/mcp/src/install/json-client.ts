import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

import type { ClientTarget, InstallOutcome, McpEntry, StatusOutcome, UninstallOutcome } from "./types.js";

/**
 * Reads and JSON-parses `configPath`. Returns `{ root: {} }` when the file
 * does not exist, and a `reason` when it exists but fails to read/parse or
 * its root is not a plain object — callers must leave the file untouched in
 * that case.
 */
function readConfig(configPath: string): { root: Record<string, unknown> } | { reason: string } {
  if (!existsSync(configPath)) {
    return { root: {} };
  }
  let raw: string;
  try {
    raw = readFileSync(configPath, "utf8");
  } catch (error) {
    return { reason: `无法读取配置文件: ${error instanceof Error ? error.message : String(error)}` };
  }
  try {
    const parsed: unknown = raw.trim().length === 0 ? {} : JSON.parse(raw);
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      return { reason: "配置文件根节点不是 JSON 对象" };
    }
    return { root: parsed as Record<string, unknown> };
  } catch (error) {
    return { reason: `JSON 解析失败: ${error instanceof Error ? error.message : String(error)}` };
  }
}

/** Walks `serversPath` inside `root`, returning the object holding `multica`
 * (the last-but-one segment) or `undefined` if any intermediate segment is
 * missing or not an object. */
function resolveServers(root: Record<string, unknown>, serversPath: string[]): Record<string, unknown> | undefined {
  let cursor: Record<string, unknown> = root;
  for (const key of serversPath) {
    const next = cursor[key];
    if (next === null || typeof next !== "object" || Array.isArray(next)) {
      return undefined;
    }
    cursor = next as Record<string, unknown>;
  }
  return cursor;
}

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
  /** Inverse of `buildEntry`: pulls the registered `distPath` back out of a
   * previously-written entry, or `undefined` if the shape is unrecognized. */
  extractDistPath: (rawEntry: unknown) => string | undefined;
}): ClientTarget {
  const { id, label, configPath, detect, serversPath, buildEntry, extractDistPath } = options;

  function apply(entry: McpEntry): InstallOutcome {
    if (!detect()) {
      return { status: "skipped-not-detected" };
    }

    const config = readConfig(configPath);
    if ("reason" in config) {
      return { status: "skipped-parse-error", path: configPath, reason: config.reason };
    }
    const root = config.root;

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

  function read(): StatusOutcome {
    if (!detect()) {
      return { status: "not-detected" };
    }

    const config = readConfig(configPath);
    if ("reason" in config) {
      return { status: "parse-error", path: configPath, reason: config.reason };
    }

    const servers = resolveServers(config.root, serversPath);
    const rawEntry = servers?.multica;
    if (rawEntry === undefined) {
      return { status: "not-installed" };
    }
    const distPath = extractDistPath(rawEntry);
    if (distPath === undefined) {
      return { status: "not-installed" };
    }
    return { status: "registered", path: configPath, distPath, stale: !existsSync(distPath) };
  }

  function remove(): UninstallOutcome {
    if (!detect()) {
      return { status: "skipped-not-detected" };
    }

    const config = readConfig(configPath);
    if ("reason" in config) {
      return { status: "skipped-parse-error", path: configPath, reason: config.reason };
    }
    const root = config.root;

    const servers = resolveServers(root, serversPath);
    if (servers === undefined || !("multica" in servers)) {
      return { status: "not-installed" };
    }

    delete servers.multica;
    writeFileSync(configPath, `${JSON.stringify(root, null, 2)}\n`, "utf8");
    return { status: "removed", path: configPath };
  }

  return { id, label, detect, apply, read, remove };
}
