#!/usr/bin/env node
/**
 * multica-mcp-update entrypoint, invoked by `make mcp-update`.
 *
 *   node dist/update-cli.js /absolute/path/to/apps/mcp/dist/index.js
 *
 * Refreshes the `multica` entry only for clients that already have one
 * registered — fixes a stale `distPath` after the checkout was moved or
 * rebuilt, without opting a newly-detected client into registration (that
 * is what `mcp-install` is for). Never reads, prints, or persists a
 * Multica PAT.
 */

import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { isAbsolute } from "node:path";

import { createClientTargets } from "./install/clients.js";
import { formatInstallReport, runUpdate } from "./install/installer.js";

function fail(message: string): never {
  process.stderr.write(`multica-mcp-update: ${message}\n`);
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
  const results = runUpdate(targets, { distPath });
  process.stdout.write(`${formatInstallReport(results)}\n`);
}

main();
