#!/usr/bin/env node
/**
 * multica-mcp entrypoint.
 *
 *   multica-mcp                    # stdio transport (default) — for claude code / codex
 *   multica-mcp --transport http --port 8080 --host 127.0.0.1
 *
 * Credentials resolve exactly like the multica CLI: --token / --server-url
 * flags, then MULTICA_TOKEN / MULTICA_SERVER_URL env, then the CLI config
 * file (~/.multica/config.json, or a named profile via --profile /
 * MULTICA_PROFILE, or MULTICA_TASK_CONFIG_ROOT under daemon task sandboxes).
 */

import { readFileSync } from "node:fs";
import { parseArgs } from "node:util";

import { resolveCredentials, type CredentialOverrides } from "./config.js";
import { stderrLogger } from "./log.js";
import { MulticaClient } from "./rest.js";
import { runStdio } from "./stdio.js";
import { startHttpServer } from "./http.js";

interface ParsedFlags {
  transport: string;
  port?: string;
  host?: string;
  token?: string;
  serverUrl?: string;
  profile?: string;
}

function fail(message: string): never {
  stderrLogger.error(`multica-mcp: ${message}`);
  process.exit(1);
}

function parseFlags(): ParsedFlags {
  try {
    const { values } = parseArgs({
      options: {
        transport: { type: "string", default: "stdio" },
        port: { type: "string" },
        host: { type: "string" },
        token: { type: "string" },
        "server-url": { type: "string" },
        profile: { type: "string" },
      },
    });
    return {
      transport: values.transport ?? "stdio",
      port: values.port,
      host: values.host,
      token: values.token,
      serverUrl: values["server-url"],
      profile: values.profile,
    };
  } catch (error) {
    fail(`invalid arguments: ${error instanceof Error ? error.message : String(error)}`);
  }
}

async function main(): Promise<void> {
  const flags = parseFlags();
  const overrides: CredentialOverrides = {
    token: flags.token,
    serverUrl: flags.serverUrl,
    profile: flags.profile,
  };

  const credentials = resolveCredentials(
    process.env,
    (path) => readFileSync(path, "utf8"),
    overrides,
  );
  const client = new MulticaClient({
    serverUrl: credentials.serverUrl,
    token: credentials.token,
  });

  if (flags.transport === "stdio") {
    await runStdio(client, stderrLogger);
    return;
  }
  if (flags.transport === "http") {
    const port = flags.port ?? process.env["MULTICA_MCP_PORT"] ?? "8080";
    const portNumber = Number.parseInt(port, 10);
    if (!Number.isInteger(portNumber) || portNumber < 1 || portNumber > 65535) {
      fail(`invalid port: '${port}'`);
    }
    const host = flags.host ?? process.env["MULTICA_MCP_HOST"] ?? "127.0.0.1";
    const httpServer = await startHttpServer({
      port: portNumber,
      host,
      serverUrl: credentials.serverUrl,
    });
    const shutdown = (): void => {
      httpServer.close(() => process.exit(0));
      setTimeout(() => process.exit(0), 2000).unref();
    };
    process.on("SIGINT", shutdown);
    process.on("SIGTERM", shutdown);
    return;
  }
  fail(`unknown transport '${flags.transport}' (expected 'stdio' or 'http')`);
}

main().catch((error: unknown) => {
  stderrLogger.error(`multica-mcp: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
});
