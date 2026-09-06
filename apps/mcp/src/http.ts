/**
 * Streamable HTTP transport for the MCP server.
 *
 * Stateless mode: no MCP sessions, no server-side client registry. Each POST
 * carries its own `Authorization: Bearer mul_…` PAT and gets a throwaway
 * server+transport pair bound to exactly that credential, so concurrent
 * callers with different tokens never share state. GET (server-initiated
 * streams) and DELETE (session termination) have nothing to do here in
 * stateless mode and are rejected.
 *
 * Bind host defaults to loopback: exposing this beyond localhost is a
 * deployment decision (put TLS in front); it is not the default.
 */

import { createServer, type IncomingMessage, type Server as HttpServer } from "node:http";
import type { ServerResponse } from "node:http";

import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";

import { stderrLogger, type Logger } from "./log.js";
import { MulticaClient } from "./rest.js";
import { createMcpServer } from "./server.js";

const MAX_BODY_BYTES = 5 * 1024 * 1024;

const BEARER_PATTERN = /^Bearer (mul_[0-9a-f]{40})$/i;

export function extractBearerToken(header: string | undefined): string | null {
  if (header === undefined) {
    return null;
  }
  const match = BEARER_PATTERN.exec(header.trim());
  return match?.[1] ?? null;
}

export interface HttpServerOptions {
  port: number;
  host: string;
  /** Base URL of the Multica backend API. */
  serverUrl: string;
  logger?: Logger;
}

export async function startHttpServer(options: HttpServerOptions): Promise<HttpServer> {
  const logger = options.logger ?? stderrLogger;
  const httpServer = createServer((request, response) => {
    void handleRequest(request, response, options.serverUrl, logger);
  });

  await new Promise<void>((resolve, reject) => {
    httpServer.once("error", reject);
    httpServer.listen(options.port, options.host, () => resolve());
  });
  logger.info(
    `multica-mcp streamable HTTP listening on http://${options.host}:${options.port}/mcp (auth: Multica PAT bearer)`,
  );
  return httpServer;
}

async function handleRequest(
  request: IncomingMessage,
  response: ServerResponse,
  backendUrl: string,
  logger: Logger,
): Promise<void> {
  const url = request.url ?? "/";
  try {
    if (url === "/healthz") {
      // Liveness only — deliberately unauthenticated, carries no data.
      response.writeHead(200, { "Content-Type": "text/plain" });
      response.end("ok");
      return;
    }
    if (url !== "/mcp" && !url.startsWith("/mcp?")) {
      sendJsonError(response, 404, "Not found. MCP endpoint: POST /mcp");
      return;
    }
    if (request.method !== "POST") {
      // Stateless mode: no SSE listening (GET) and no sessions (DELETE).
      response.setHeader("Allow", "POST");
      sendJsonError(response, 405, "Method not allowed. This server is stateless: POST /mcp only.");
      return;
    }

    const token = extractBearerToken(request.headers.authorization);
    if (token === null) {
      response.setHeader("WWW-Authenticate", "Bearer");
      sendJsonError(
        response,
        401,
        "Unauthorized: provide a Multica personal access token as 'Authorization: Bearer mul_…'.",
      );
      logger.info(`http ${request.method} ${url} -> 401`);
      return;
    }

    const parsed = await readJsonBody(request);
    if (!parsed.ok) {
      sendJsonError(response, parsed.status, parsed.message);
      return;
    }

    const client = new MulticaClient({ serverUrl: backendUrl, token, logger });
    const mcpServer = createMcpServer(client);
    const transport = new StreamableHTTPServerTransport({
      sessionIdGenerator: undefined,
      enableJsonResponse: true,
    });
    response.on("close", () => {
      void transport.close();
      void mcpServer.close();
    });
    await mcpServer.connect(transport);
    await transport.handleRequest(request, response, parsed.body);
    logger.info(`http POST /mcp -> handled`);
  } catch (error) {
    logger.error(`http request failed: ${error instanceof Error ? error.message : String(error)}`);
    if (!response.headersSent) {
      sendJsonError(response, 500, "Internal server error");
    } else {
      response.end();
    }
  }
}

interface BodyReadResult {
  ok: true;
  body: unknown;
}

interface BodyReadFailure {
  ok: false;
  status: number;
  message: string;
}

async function readJsonBody(
  request: IncomingMessage,
): Promise<BodyReadResult | BodyReadFailure> {
  const chunks: Buffer[] = [];
  let total = 0;
  for await (const chunk of request) {
    const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk as string);
    total += buffer.length;
    if (total > MAX_BODY_BYTES) {
      return { ok: false, status: 413, message: "Request body too large" };
    }
    chunks.push(buffer);
  }
  const raw = Buffer.concat(chunks).toString("utf8");
  if (raw.length === 0) {
    return { ok: false, status: 400, message: "Request body required" };
  }
  try {
    return { ok: true, body: JSON.parse(raw) };
  } catch {
    return { ok: false, status: 400, message: "Request body is not valid JSON" };
  }
}

function sendJsonError(response: ServerResponse, status: number, message: string): void {
  response.writeHead(status, { "Content-Type": "application/json" });
  response.end(JSON.stringify({ error: message }));
}
