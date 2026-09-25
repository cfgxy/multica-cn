#!/usr/bin/env node
/**
 * multica-mcp-uninstall entrypoint, invoked by `make mcp-uninstall`.
 *
 *   node dist/uninstall-cli.js
 *
 * Removes the `multica` entry from every detected client's config —
 * Claude Code / Codex / Kimi / ZCode / Cursor / OpenCode — leaving every
 * other server entry and the file's formatting untouched. Clients that are
 * not installed, have no `multica` entry, or whose config fails to parse
 * are skipped with the original file left exactly as-is. Never reads,
 * prints, or persists a Multica PAT.
 */

import { homedir } from "node:os";

import { createClientTargets } from "./install/clients.js";
import { formatUninstallReport, runUninstall } from "./install/installer.js";

function main(): void {
  const targets = createClientTargets(homedir());
  const results = runUninstall(targets);
  process.stdout.write(`${formatUninstallReport(results)}\n`);
}

main();
