import { afterAll, describe, expect, it } from "vitest";

import type { Logger } from "../src/log.js";
import { extractBearerToken, startHttpServer } from "../src/http.js";
import type { Server as HttpServer } from "node:http";

const TOKEN = `mul_${"a1b2c3d4e5".repeat(4)}`;

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

async function listen(): Promise<string> {
  const server = await startHttpServer({
    port: 0,
    host: "127.0.0.1",
    serverUrl: "https://backend.example.com",
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
