import { existsSync } from "node:fs";
import { join } from "node:path";

import { createCodexClientTarget } from "./codex-client.js";
import { createJsonClientTarget } from "./json-client.js";
import type { ClientTarget, McpEntry } from "./types.js";

/** `type: "stdio", command: "node", args: [distPath]` — the shape Claude
 * Code, Kimi CLI, ZCode and the Cursor CLI all share for a local stdio MCP
 * server. */
function stdioEntry(entry: McpEntry): unknown {
  return { type: "stdio", command: "node", args: [entry.distPath] };
}

/**
 * Builds the six client targets against a given home directory. Accepting
 * `homeDir` as a parameter (instead of reading `os.homedir()` internally)
 * keeps every target unit-testable against an isolated fixture directory.
 */
export function createClientTargets(homeDir: string): ClientTarget[] {
  const claudeCodeDetected = () =>
    existsSync(join(homeDir, ".claude.json")) || existsSync(join(homeDir, ".claude"));
  const codexDetected = () => existsSync(join(homeDir, ".codex"));
  const kimiDetected = () => existsSync(join(homeDir, ".kimi-code"));
  const zcodeDetected = () => existsSync(join(homeDir, ".zcode", "cli"));
  const cursorDetected = () => existsSync(join(homeDir, ".cursor"));
  const opencodeDetected = () =>
    existsSync(join(homeDir, ".config", "opencode")) || existsSync(join(homeDir, ".opencode"));

  return [
    createJsonClientTarget({
      id: "claude-code",
      label: "Claude Code",
      configPath: join(homeDir, ".claude.json"),
      detect: claudeCodeDetected,
      serversPath: ["mcpServers"],
      buildEntry: stdioEntry,
    }),
    createCodexClientTarget({
      configPath: join(homeDir, ".codex", "config.toml"),
      detect: codexDetected,
    }),
    createJsonClientTarget({
      id: "kimi",
      label: "Kimi",
      configPath: join(homeDir, ".kimi-code", "mcp.json"),
      detect: kimiDetected,
      serversPath: ["mcpServers"],
      buildEntry: stdioEntry,
    }),
    createJsonClientTarget({
      id: "zcode",
      label: "ZCode",
      configPath: join(homeDir, ".zcode", "cli", "config.json"),
      detect: zcodeDetected,
      serversPath: ["mcp", "servers"],
      buildEntry: stdioEntry,
    }),
    createJsonClientTarget({
      id: "cursor",
      label: "Cursor",
      configPath: join(homeDir, ".cursor", "mcp.json"),
      detect: cursorDetected,
      serversPath: ["mcpServers"],
      buildEntry: stdioEntry,
    }),
    createJsonClientTarget({
      id: "opencode",
      label: "OpenCode",
      configPath: join(homeDir, ".config", "opencode", "opencode.json"),
      detect: opencodeDetected,
      serversPath: ["mcp"],
      buildEntry: (entry) => ({ type: "local", command: ["node", entry.distPath], enabled: true }),
    }),
  ];
}
