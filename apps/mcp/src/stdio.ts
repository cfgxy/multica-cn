/**
 * stdio transport: the MCP client (claude code, codex, …) spawns this process
 * and speaks JSON-RPC over stdin/stdout. Protocol frames use stdout, so every
 * diagnostic goes to stderr via the logger.
 */

import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";

import type { Logger } from "./log.js";
import type { MulticaClient } from "./rest.js";
import { createMcpServer } from "./server.js";

export async function runStdio(client: MulticaClient, logger: Logger): Promise<void> {
  const server = createMcpServer(client);
  const transport = new StdioServerTransport();
  await server.connect(transport);
  logger.info("multica-mcp stdio transport connected (credentials resolved at startup)");
}
