import { afterAll, describe, expect, it } from "vitest";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import type { Logger } from "../src/log.js";
import {
  extractBearerToken,
  startHttpServer,
  wwwAuthenticateChallenge,
} from "../src/http.js";
import type { HttpServerOptions } from "../src/http.js";
import type { Server as HttpServer } from "node:http";

const TOKEN = `mul_${"a1b2c3d4e5".repeat(4)}`;
const NON_MEMBER_TOKEN = `mul_${"b1".repeat(20)}`;
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

async function listen(siteRoot?: string, overrides: Partial<HttpServerOptions> = {}): Promise<string> {
  const server = await startHttpServer({
    port: 0,
    host: "127.0.0.1",
    serverUrl: "https://backend.example.com",
    siteRoot,
    logger: silentLogger(),
    ...overrides,
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

describe("HTTP systemd credential wiring", () => {
  it("uses systemd LoadCredential and passes only its runtime file path to MCP", () => {
    const template = readFileSync(
      join(process.cwd(), "..", "..", "deploy", "multica-mcp-http.service.template"),
      "utf8",
    );
    expect(template).toContain("LoadCredential=multica-mcp-audit:@AUDIT_CREDENTIAL_FILE@");
    expect(template).toContain('--audit-credential-file "$CREDENTIALS_DIRECTORY/multica-mcp-audit"');
    expect(template).not.toContain("SetCredential=");
    expect(template).not.toContain("MULTICA_MCP_AUDIT_SERVICE_CREDENTIAL=");
  });

  it("renders custom credential paths with sed replacement characters literally", () => {
    const directory = mkdtempSync(join(tmpdir(), "mcp-audit-"));
    const credentialPath = join(directory, "audit&key|file");
    const credential = "secret-audit-credential";
    writeFileSync(credentialPath, credential);

    try {
      const unit = execFileSync(
        "make",
        ["--silent", "mcp-http-render", `MCP_AUDIT_CREDENTIAL_FILE=${credentialPath}`],
        { cwd: join(process.cwd(), "..", ".."), encoding: "utf8" },
      );
      expect(unit).toContain(`LoadCredential=multica-mcp-audit:${credentialPath}`);
      expect(unit).not.toContain(credential);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });
});

describe("http transport gate", () => {
  it("serves /healthz without auth and without data", async () => {
    const base = await listen();
    const response = await fetch(`${base}/healthz`);
    expect(response.status).toBe(200);
    expect(await response.text()).toBe("ok");
  });

  it("records only bounded categories for missing authentication and invalid JSON", async () => {
    const auditBodies: string[] = [];
    const base = await listen(undefined, {
      auditServiceCredential: "private-audit-credential",
      mcpInstanceId: "managed-instance",
      fileAccessInstanceCredential: "private-instance-credential",
      fetchImpl: (async (_input, init) => {
        auditBodies.push(String(init?.body ?? ""));
        return new Response('{"status":"recorded"}', { status: 200 });
      }) as typeof fetch,
    });

    const unauthenticated = await fetch(`${base}/mcp`, { method: "POST", body: rpcBody("initialize") });
    expect(unauthenticated.status).toBe(401);
    const invalidJson = await fetch(`${base}/mcp`, {
      method: "POST",
      headers: { Authorization: `Bearer ${TOKEN}` },
      body: "{not-json",
    });
    expect(invalidJson.status).toBe(400);

    const bodies = auditBodies.map((body) => JSON.parse(body) as Record<string, unknown>);
    expect(bodies).toHaveLength(2);
    expect(bodies.map((body) => body.reason_code)).toEqual(["unauthenticated_actor", "entry_rejected"]);
    expect(bodies.every((body) => typeof body.invocation_id === "string")).toBe(true);
    expect(bodies.every((body) => Object.keys(body).sort().join(",") === "invocation_id,reason_code")).toBe(true);
    expect(JSON.stringify(bodies)).not.toContain("not-json");
    expect(JSON.stringify(bodies)).not.toContain(TOKEN);
  });

  it("carries clientInfo from HTTP initialize to later file invocation audit", async () => {
    const auditBodies: Array<Record<string, unknown>> = [];
    const base = await listen(undefined, {
      auditServiceCredential: "private-audit-credential",
      mcpInstanceId: "managed-instance",
      fileAccessInstanceCredential: "private-instance-credential",
      fetchImpl: (async (input, init) => {
        const path = new URL(String(input)).pathname;
        const body = String(init?.body ?? "");
        if (path === "/api/mcp/audit-start") auditBodies.push(JSON.parse(body) as Record<string, unknown>);
        if (path === "/api/file-access/list") return new Response('{"operation_id":"op-http","status":"pending"}');
        if (path === "/api/file-access/operations/op-http") {
          return new Response('{"operation_id":"op-http","kind":"list","status":"succeeded","result":{"entries":[]},"error":""}');
        }
        return new Response('{"status":"recorded"}');
      }) as typeof fetch,
    });
    const headers = {
      Authorization: `Bearer ${TOKEN}`,
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
    };
    const initialize = await fetch(`${base}/mcp`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "initialize",
        params: { protocolVersion: "2025-03-26", capabilities: {}, clientInfo: { name: "http-vitest", version: "2.4.1" } },
      }),
    });
    expect(initialize.status).toBe(200);
    const call = await fetch(`${base}/mcp`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 2,
        method: "tools/call",
        params: { name: "list_files", arguments: { workspace: "0b7f4c1e-1111-4222-8333-abcdefabcdef", path: "/srv/data" } },
      }),
    });
    expect(call.status).toBe(200);
    expect(auditBodies).toHaveLength(1);
    expect(auditBodies[0]).toMatchObject({ reported_client_name: "http-vitest", reported_client_version: "2.4.1" });
  });

  // The admin status page polls this. It must stay credential-free
  // and static — exactly version plus a name/description catalogue, so the
  // key-shape assertions below are the leak guard, not just shape docs.
  it("serves /diag without auth as a static tool catalogue only", async () => {
    const base = await listen();
    const response = await fetch(`${base}/diag`);
    expect(response.status).toBe(200);
    expect(response.headers.get("Content-Type")).toContain("application/json");
    const payload = (await response.json()) as {
      version: unknown;
      tools: Array<Record<string, unknown>>;
    };
    expect(Object.keys(payload).sort()).toEqual(["tools", "version"]);
    expect(typeof payload.version).toBe("string");
    expect(payload.tools.length).toBeGreaterThan(0);
    for (const tool of payload.tools) {
      expect(Object.keys(tool).sort()).toEqual(["description", "name"]);
    }
    expect(JSON.stringify(payload)).not.toMatch(/mul_[a-z0-9]{20,}/);
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
      result?: { tools?: Array<{ name: string; annotations?: Record<string, unknown> }> };
    };
    const tools = payload.result?.tools ?? [];
    const names = tools.map((tool) => tool.name);
    expect(names).toContain("list_workspaces");
    expect(names).toContain("dispatch_agent");
    expect(tools.find((tool) => tool.name === "list_workspaces")?.annotations).toEqual({
      readOnlyHint: true,
      destructiveHint: false,
      idempotentHint: true,
      openWorldHint: false,
    });
    expect(tools.find((tool) => tool.name === "get_runtime_models")?.annotations).toEqual({
      readOnlyHint: false,
      destructiveHint: false,
      idempotentHint: false,
      openWorldHint: true,
    });
  });

  it("executes workspace and project tools through the authenticated HTTP transport", async () => {
    const backendRequests: Array<{ path: string; authorization: string; workspaceSlug: string | null }> = [];
    const base = await listen(undefined, {
      fetchImpl: (async (input, init) => {
        const url = new URL(String(input));
        const requestHeaders = new Headers(init?.headers);
        const authorization = requestHeaders.get("Authorization") ?? "";
        const workspaceSlug = requestHeaders.get("X-Workspace-Slug");
        backendRequests.push({ path: url.pathname, authorization, workspaceSlug });
        if (url.pathname === "/api/workspaces") {
          if (authorization === `Bearer ${TOKEN}`) {
            return new Response(JSON.stringify([{ id: "ws-1", slug: "member", name: "Member workspace" }]), { status: 200 });
          }
          return new Response(JSON.stringify([]), { status: 200 });
        }
        if (url.pathname === "/api/projects") {
          if (authorization === `Bearer ${TOKEN}` && workspaceSlug === "member") {
            return new Response(JSON.stringify({ total: 1, projects: [{ id: "p-1", title: "Member project", status: "active", issue_count: 2 }] }), { status: 200 });
          }
          return new Response(JSON.stringify({ error: "membership required" }), { status: 403 });
        }
        return new Response(JSON.stringify({ status: "recorded" }), { status: 200 });
      }) as typeof fetch,
    });
    const headers = {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
      Authorization: `Bearer ${TOKEN}`,
    };
    const call = async (id: number, name: string, args: Record<string, unknown>, authorization = headers.Authorization) => {
      const response = await fetch(`${base}/mcp`, {
        method: "POST",
        headers: { ...headers, Authorization: authorization },
        body: JSON.stringify({ jsonrpc: "2.0", id, method: "tools/call", params: { name, arguments: args } }),
      });
      expect(response.status).toBe(200);
      return (await response.json()) as { result?: { content?: Array<{ text?: string }>; isError?: boolean } };
    };

    const tools = await fetch(`${base}/mcp`, { method: "POST", headers, body: rpcBody("tools/list") });
    const toolPayload = (await tools.json()) as { result?: { tools?: Array<{ name: string }> } };
    expect(toolPayload.result?.tools?.map((tool) => tool.name)).toEqual(expect.arrayContaining(["list_workspaces", "list_projects"]));

    const workspaces = await call(2, "list_workspaces", {});
    expect(JSON.parse(workspaces.result?.content?.[0]?.text ?? "{}")).toMatchObject({ total: 1, workspaces: [{ slug: "member" }] });
    const projects = await call(3, "list_projects", { workspace: "member" });
    expect(JSON.parse(projects.result?.content?.[0]?.text ?? "{}")).toMatchObject({ total: 1, projects: [{ title: "Member project" }] });

    const denied = await call(4, "list_projects", { workspace: "member" }, `Bearer ${NON_MEMBER_TOKEN}`);
    expect(denied.result?.isError).toBe(true);
    expect(JSON.stringify(denied)).toContain("membership required");
    expect(backendRequests).toEqual(expect.arrayContaining([
      { path: "/api/workspaces", authorization: `Bearer ${TOKEN}`, workspaceSlug: null },
      { path: "/api/projects", authorization: `Bearer ${TOKEN}`, workspaceSlug: "member" },
      { path: "/api/projects", authorization: `Bearer ${NON_MEMBER_TOKEN}`, workspaceSlug: "member" },
    ]));
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
