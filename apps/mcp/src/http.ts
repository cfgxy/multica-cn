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
import { createHash, randomUUID } from "node:crypto";
import type { ServerResponse } from "node:http";
import { createRequire } from "node:module";

import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";

import { stderrLogger, type Logger } from "./log.js";
import { MulticaClient } from "./rest.js";
import { createMcpServer } from "./server.js";
import { TOOL_DEFINITIONS } from "./tools.js";

const MAX_BODY_BYTES = 5 * 1024 * 1024;
const CLIENT_INFO_TTL_MS = 30 * 60 * 1000;
const CLIENT_INFO_LIMIT = 1024;
const reportedClients = new Map<string, { name: string; version: string; expiresAt: number }>();

// Two credential shapes, one transport. `mul_…` is the Multica PAT this
// server has always taken. The second branch is a compact JWS — the OAuth
// access token the Go authorization server mints for ChatGPT.
//
// Neither is verified here. The token is forwarded verbatim to the backend
// (src/rest.ts), whose auth middleware already dispatches on the same two
// shapes, so signature checking exists in exactly one place. Widening this
// pattern is therefore a routing decision, not an authentication one: a
// forged JWT gets past this line and is rejected by the backend, the same way
// a well-formed but unknown PAT always has been.
const BEARER_PATTERN = /^Bearer (mul_[0-9a-f]{40}|[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)$/i;

export function extractBearerToken(header: string | undefined): string | null {
  if (header === undefined) {
    return null;
  }
  const match = BEARER_PATTERN.exec(header.trim());
  return match?.[1] ?? null;
}

/**
 * Static diagnostic payload for GET /diag: the tool catalogue
 * and this process's version, nothing else. Computed once at startup —
 * the registry is compile-time static, so answering the admin status page
 * never constructs a client, touches the backend, or can trigger a run.
 * Unauthenticated on purpose: the listener binds loopback by default and
 * the payload is marketing-grade metadata (names and descriptions only).
 */
export function diagnosticPayload(): { version: string; tools: Array<{ name: string; description: string }> } {
  let version = "unknown";
  try {
    const require = createRequire(import.meta.url);
    const pkg = require("../package.json") as { version?: string };
    if (typeof pkg.version === "string") {
      version = pkg.version;
    }
  } catch {
    // Packaged without a readable package.json — report "unknown" rather
    // than failing the diagnostic.
  }
  return {
    version,
    tools: TOOL_DEFINITIONS.map((tool) => ({ name: tool.name, description: tool.description })),
  };
}

/**
 * Builds the `WWW-Authenticate` value for an unauthenticated MCP request.
 *
 * The `resource_metadata` pointer is the first hop of the OAuth discovery
 * chain (RFC 9728 §5.1): a client that gets a bare `Bearer` challenge has
 * nowhere to go and gives up, which is what stopped ChatGPT from connecting.
 * The URL must be absolute — the challenge is consumed by a client that has
 * no base to resolve against — so it is omitted entirely when no site root is
 * configured rather than emitted as a relative path a client would misread.
 */
export function wwwAuthenticateChallenge(siteRoot: string | undefined): string {
  const root = siteRoot?.trim().replace(/\/+$/, "");
  if (!root) {
    return "Bearer";
  }
  return `Bearer resource_metadata="${root}/.well-known/oauth-protected-resource"`;
}

export interface HttpServerOptions {
  port: number;
  host: string;
  /** Base URL of the Multica backend API. */
  serverUrl: string;
  /**
   * Public origin this MCP endpoint is reached at — the Next.js front door,
   * not this process's own listener. Used only to build the absolute
   * `resource_metadata` pointer in 401 challenges. Undefined degrades the
   * challenge to a bare `Bearer`, which is the pre-OAuth behaviour.
   */
  siteRoot?: string | undefined;
  /** Private credential used only for the backend's existing invocation audit endpoints. */
  auditServiceCredential?: string | undefined;
  mcpInstanceId?: string | undefined;
  fileAccessInstanceCredential?: string | undefined;
  fetchImpl?: typeof fetch | undefined;
  logger?: Logger;
}

export async function startHttpServer(options: HttpServerOptions): Promise<HttpServer> {
  const logger = options.logger ?? stderrLogger;
  const challenge = wwwAuthenticateChallenge(options.siteRoot);
  const httpServer = createServer((request, response) => {
    void handleRequest(request, response, options.serverUrl, challenge, options.auditServiceCredential, options.mcpInstanceId, options.fileAccessInstanceCredential, options.fetchImpl, logger);
  });

  await new Promise<void>((resolve, reject) => {
    httpServer.once("error", reject);
    httpServer.listen(options.port, options.host, () => resolve());
  });
  logger.info(
    `multica-mcp streamable HTTP listening on http://${options.host}:${options.port}/mcp (auth: Multica PAT or OAuth bearer)`,
  );
  return httpServer;
}

async function handleRequest(
  request: IncomingMessage,
  response: ServerResponse,
  backendUrl: string,
  challenge: string,
  auditServiceCredential: string | undefined,
  mcpInstanceId: string | undefined,
  fileAccessInstanceCredential: string | undefined,
  fetchImpl: typeof fetch | undefined,
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
    if (url === "/diag" || url.startsWith("/diag?")) {
      // Static read-only diagnostic: version + tool names,
      // computed at startup, no credential, no backend call.
      response.writeHead(200, { "Content-Type": "application/json" });
      response.end(JSON.stringify(diagnosticPayload()));
      return;
    }
    if (url.startsWith("/.well-known/")) {
      // The discovery documents are served by the Go backend at the public
      // origin, not here (ADR §3.3). Answering 404 with that wording keeps a
      // direct probe of this process from reading as "this deployment has no
      // authorization server" — the one misreading that would send an
      // operator looking for a missing route instead of a missing proxy rule.
      sendJsonError(
        response,
        404,
        "Not served here. OAuth discovery documents are published by the Multica backend at the public site origin, not by the MCP process.",
      );
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
      await recordHttpPreAuthRejection(
        backendUrl,
        "",
        auditServiceCredential,
        mcpInstanceId,
        fileAccessInstanceCredential,
        fetchImpl,
        "unauthenticated_actor",
        logger,
      );
      response.setHeader("WWW-Authenticate", challenge);
      sendJsonError(
        response,
        401,
        "Unauthorized: provide a Multica personal access token ('Authorization: Bearer mul_…') or an OAuth access token.",
      );
      logger.info(`http ${request.method} ${url} -> 401`);
      return;
    }

    const parsed = await readJsonBody(request);
    if (!parsed.ok) {
      await recordHttpPreAuthRejection(
        backendUrl,
        token,
        auditServiceCredential,
        mcpInstanceId,
        fileAccessInstanceCredential,
        fetchImpl,
        "entry_rejected",
        logger,
      );
      sendJsonError(response, parsed.status, parsed.message);
      return;
    }

    const clientKey = createHash("sha256").update(token).digest("hex");
    const reportedClient = getReportedClient(clientKey);
    if (isInitializeRequest(parsed.body)) {
      const clientInfo = readReportedClient(parsed.body);
      if (clientInfo !== undefined) rememberReportedClient(clientKey, clientInfo);
    }

    const client = new MulticaClient({
      serverUrl: backendUrl,
      token,
      logger,
      auditServiceCredential,
      mcpInstanceId,
      fileAccessInstanceCredential,
      fetchImpl,
    });
    const mcpServer = createMcpServer(client, reportedClient);
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

function getReportedClient(key: string): { name: string; version: string } | undefined {
  const value = reportedClients.get(key);
  if (value === undefined) return undefined;
  if (value.expiresAt <= Date.now()) {
    reportedClients.delete(key);
    return undefined;
  }
  return { name: value.name, version: value.version };
}

function rememberReportedClient(key: string, value: { name: string; version: string }): void {
  const now = Date.now();
  for (const [entryKey, entry] of reportedClients) {
    if (entry.expiresAt <= now) reportedClients.delete(entryKey);
  }
  if (!reportedClients.has(key) && reportedClients.size >= CLIENT_INFO_LIMIT) {
    const oldestKey = reportedClients.keys().next().value;
    if (oldestKey !== undefined) reportedClients.delete(oldestKey);
  }
  reportedClients.delete(key);
  reportedClients.set(key, { ...value, expiresAt: now + CLIENT_INFO_TTL_MS });
}

function isInitializeRequest(value: unknown): boolean {
  return typeof value === "object" && value !== null &&
    (value as Record<string, unknown>).method === "initialize";
}

function readReportedClient(value: unknown): { name: string; version: string } | undefined {
  if (typeof value !== "object" || value === null) return undefined;
  const params = (value as Record<string, unknown>).params;
  if (typeof params !== "object" || params === null) return undefined;
  const info = (params as Record<string, unknown>).clientInfo;
  if (typeof info !== "object" || info === null) return undefined;
  const { name, version } = info as Record<string, unknown>;
  if (typeof name !== "string" || typeof version !== "string" || !name.trim() || !version.trim()) return undefined;
  return { name: name.trim().slice(0, 128), version: version.trim().slice(0, 128) };
}

async function recordHttpPreAuthRejection(
  backendUrl: string,
  token: string,
  auditServiceCredential: string | undefined,
  mcpInstanceId: string | undefined,
  fileAccessInstanceCredential: string | undefined,
  fetchImpl: typeof fetch | undefined,
  reason: "unauthenticated_actor" | "entry_rejected",
  logger: Logger,
): Promise<void> {
  if (!auditServiceCredential || !mcpInstanceId || !fileAccessInstanceCredential) return;
  const client = new MulticaClient({
    serverUrl: backendUrl,
    token,
    logger,
    auditServiceCredential,
    mcpInstanceId,
    fileAccessInstanceCredential,
    fetchImpl,
  });
  try {
    await client.recordMcpPreAuthRejection({
      invocation_id: randomUUID(),
      reason_code: reason,
    });
  } catch {
    // Early protocol rejection remains unchanged if its audit write is unavailable.
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
