#!/usr/bin/env node
/**
 * multica-mcp-status entrypoint, invoked by `make mcp-status`.
 *
 *   node dist/status-cli.js
 *
 * Read-only: reports, for every client Claude Code / Codex / Kimi / ZCode /
 * Cursor / OpenCode, whether it is detected on this machine, whether it has
 * a `multica` entry registered, which `distPath` that entry points at, and
 * whether that path still exists on disk (a stale entry usually means the
 * checkout it was installed from has since moved or been removed — run
 * `make mcp-update` to fix). Never writes to any config file.
 */

import { homedir } from "node:os";

import { createClientTargets } from "./install/clients.js";
import { formatStatusReport, runStatus } from "./install/installer.js";

function main(): void {
  const targets = createClientTargets(homedir());
  const results = runStatus(targets);
  process.stdout.write(`${formatStatusReport(results)}\n`);
}

main();
