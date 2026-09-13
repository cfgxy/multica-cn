#!/usr/bin/env node
/**
 * multica-mcp-install entrypoint, invoked by `make mcp-install`.
 *
 *   node dist/install-cli.js /absolute/path/to/apps/mcp/dist/index.js
 *
 * Configures a `multica` stdio MCP server entry, pointed at the given
 * checkout's built entrypoint, for every locally detected client among
 * Claude Code, Codex, Kimi, ZCode, Cursor and OpenCode. Clients that are not
 * installed are skipped without error; clients whose existing config fails
 * to parse are skipped with the original file left untouched. This process
 * never reads, prints, or persists a Multica PAT — the installed server
 * reuses whatever credentials `multica login` already stored.
 */

import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { isAbsolute } from "node:path";

import { createClientTargets } from "./install/clients.js";
import { formatInstallReport, runInstall } from "./install/installer.js";

function fail(message: string): never {
  process.stderr.write(`multica-mcp-install: ${message}\n`);
  process.exit(1);
}

function main(): void {
  const distPath = process.argv[2];
  if (!distPath) {
    fail("missing required argument: absolute path to the built apps/mcp dist/index.js");
  }
  if (!isAbsolute(distPath)) {
    fail(`entrypoint path must be absolute, got: ${distPath}`);
  }
  if (!existsSync(distPath)) {
    fail(`entrypoint not found — run \`pnpm --filter @multica/mcp build\` first: ${distPath}`);
  }

  const targets = createClientTargets(homedir());
  const results = runInstall(targets, { distPath });
  process.stdout.write(`${formatInstallReport(results)}\n`);
}

main();
