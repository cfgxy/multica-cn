import { afterAll, describe, expect, it } from "vitest";

import type { Logger } from "../src/log.js";
import {
  extractBearerToken,
  startHttpServer,
  wwwAuthenticateChallenge,
} from "../src/http.js";
import type { Server as HttpServer } from "node:http";

const TOKEN = `mul_${"a1b2c3d4e5".repeat(4)}`;
// Shape only — never verified here, so the segments need no real signature.
const OAUTH_TOKEN = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.c2lnbmF0dXJl";

function silentLogger(): Logger {
  return { info: () => undefined, error: () => undefined };
}

const servers: HttpServer[] = [];
afterAll(async () => {
  await Promise.all(
    servers.map(
      (server) =>
        new Promise<void>((resolve) => {
          server.close(() => resolve());
        }),
    ),
  );
});

async function listen(siteRoot?: string): Promise<string> {
  const server = await startHttpServer({
    port: 0,
    host: "127.0.0.1",
    serverUrl: "https://backend.example.com",
    siteRoot,
    logger: silentLogger(),
  });
  servers.push(server);
  const address = server.address();
  if (address === null || typeof address === "string") {
    throw new Error("no ephemeral port");
  }
  return `http://127.0.0.1:${address.port}`;
}

function rpcBody(method: string): string {
  return JSON.stringify({ jsonrpc: "2.0", id: 1, method, params: {} });
}

describe("extractBearerToken", () => {
  it("accepts a well-formed Multica PAT bearer header", () => {
    expect(extractBearerToken(`Bearer ${TOKEN}`)).toBe(TOKEN);
  });

  it("accepts the scheme case-insensitively (RFC 7235)", () => {
    expect(extractBearerToken(`bearer ${TOKEN}`)).toBe(TOKEN);
  });

  it("rejects missing, malformed and non-PAT tokens", () => {
    expect(extractBearerToken(undefined)).toBeNull();
    expect(extractBearerToken("Bearer nope")).toBeNull();
    expect(extractBearerToken("Bearer sk-abcdefghijklmnopqrstuvwxyz")).toBeNull();
    expect(extractBearerToken(TOKEN)).toBeNull();
  });

  // Second branch: the OAuth access token. Accepted on shape alone and
  // forwarded verbatim — the signature is checked by the backend, which is
  // the only place a verifying key exists.
  it("accepts a compact JWS as an OAuth access token", () => {
    expect(extractBearerToken(`Bearer ${OAUTH_TOKEN}`)).toBe(OAUTH_TOKEN);
  });

  it("rejects JWT-like strings that are not three base64url segments", () => {
    expect(extractBearerToken("Bearer header.payload")).toBeNull();
    expect(extractBearerToken("Bearer a.b.c.d")).toBeNull();
    expect(extractBearerToken("Bearer head er.pay.load")).toBeNull();
  });
});

describe("wwwAuthenticateChallenge", () => {
  it("points at the absolute protected-resource document", () => {
    expect(wwwAuthenticateChallenge("https://multica.example.com")).toBe(
      'Bearer resource_metadata="https://multica.example.com/.well-known/oauth-protected-resource"',
    );
  });

  it("normalizes a trailing slash rather than emitting a doubled path", () => {
    expect(wwwAuthenticateChallenge("https://multica.example.com/")).toBe(
      'Bearer resource_metadata="https://multica.example.com/.well-known/oauth-protected-resource"',
    );
  });

  // A relative pointer is worse than none: the client has no base to resolve
  // it against, so the discovery chain would break in a harder-to-read way.
  it("degrades to a bare challenge when no site root is configured", () => {
    expect(wwwAuthenticateChallenge(undefined)).toBe("Bearer");
    expect(wwwAuthenticateChallenge("   ")).toBe("Bearer");
  });
});

describe("http transport gate", () => {
  it("serves /healthz without auth and without data", async () => {
    const base = await listen();
    const response = await fetch(`${base}/healthz`);
    expect(response.status).toBe(200);
    expect(await response.text()).toBe("ok");
  });

  it("answers 401 on /mcp without a bearer token", async () => {
    const base = await listen();
    const response = await fetch(`${base}/mcp`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: rpcBody("tools/list"),
    });
    expect(response.status).toBe(401);
    expect(response.headers.get("WWW-Authenticate")).toBe("Bearer");
    expect(await response.text()).toContain("Unauthorized");
  });

  // The pointer ChatGPT follows. Without it the discovery chain ends at the
  // first 401 and the connector reports the server as unsupported.
  it("points an unauthenticated caller at the protected-resource document", async () => {
    const base = await listen("https://multica.example.com");
    const response = await fetch(`${base}/mcp`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: rpcBody("tools/list"),
    });
    expect(response.status).toBe(401);
    expect(response.headers.get("WWW-Authenticate")).toBe(
      'Bearer resource_metadata="https://multica.example.com/.well-known/oauth-protected-resource"',
    );
  });

  it("does not claim to serve the discovery documents itself", async () => {
    const base = await listen("https://multica.example.com");
    const response = await fetch(
      `${base}/.well-known/oauth-protected-resource`,
    );
    expect(response.status).toBe(404);
    expect(await response.text()).toContain("Multica backend");
  });

  it("answers 401 on a malformed token before touching the backend", async () => {
    const base = await listen();
    const response = await fetch(`${base}/mcp`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: "Bearer not-a-multica-token",
      },
      body: rpcBody("tools/list"),
    });
    expect(response.status).toBe(401);
  });

  it("rejects non-POST methods on /mcp with 405", async () => {
    const base = await listen();
    const response = await fetch(`${base}/mcp`, {
      method: "GET",
      headers: { Authorization: `Bearer ${TOKEN}` },
    });
    expect(response.status).toBe(405);
    expect(response.headers.get("Allow")).toBe("POST");
  });

  it("rejects invalid JSON bodies with 400", async () => {
    const base = await listen();
    const response = await fetch(`${base}/mcp`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${TOKEN}` },
      body: "{not json",
    });
    expect(response.status).toBe(400);
  });

  it("serves the MCP protocol end-to-end over streamable HTTP", async () => {
    const base = await listen();
    const response = await fetch(`${base}/mcp`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        Authorization: `Bearer ${TOKEN}`,
      },
      body: rpcBody("tools/list"),
    });
    expect(response.status).toBe(200);
    const contentType = response.headers.get("Content-Type") ?? "";
    expect(contentType).toContain("application/json");
    const payload = (await response.json()) as {
      result?: { tools?: Array<{ name: string }> };
    };
    const names = (payload.result?.tools ?? []).map((tool) => tool.name);
    expect(names).toContain("list_workspaces");
    expect(names).toContain("dispatch_agent");
  });

  it("keeps requests isolated per bearer token (stateless)", async () => {
    const base = await listen();
    const other = `mul_${"b".repeat(40)}`;
    const first = await fetch(`${base}/mcp`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        Authorization: `Bearer ${TOKEN}`,
      },
      body: rpcBody("tools/list"),
    });
    const second = await fetch(`${base}/mcp`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        Authorization: `Bearer ${other}`,
      },
      body: rpcBody("tools/list"),
    });
    expect(first.status).toBe(200);
    expect(second.status).toBe(200);
  });
});
