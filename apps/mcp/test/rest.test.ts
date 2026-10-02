import { describe, expect, it } from "vitest";

import {
  MulticaApiError,
  MulticaClient,
  MulticaRequestError,
} from "../src/rest.js";
import type { Logger } from "../src/log.js";

const TOKEN = `mul_${"a1b2c3d4e5".repeat(4)}`;

interface CapturedCall {
  url: URL;
  init: RequestInit;
}

function makeFetch(
  status: number,
  body: unknown,
  calls?: CapturedCall[],
): typeof fetch {
  return (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = input instanceof URL ? input : new URL(String(input));
    calls?.push({ url, init: init ?? {} });
    return new Response(typeof body === "string" ? body : JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
}

function silentLogger(): Logger & { lines: string[] } {
  const lines: string[] = [];
  return {
    lines,
    info(message: string): void {
      lines.push(message);
    },
    error(message: string): void {
      lines.push(message);
    },
  };
}

function makeClient(
  fetchImpl: typeof fetch,
  logger: Logger = silentLogger(),
): MulticaClient {
  return new MulticaClient({
    serverUrl: "https://api.example.com",
    token: TOKEN,
    fetchImpl,
    logger,
  });
}

describe("MulticaClient", () => {
  it("sends the PAT as a bearer token", async () => {
    const calls: CapturedCall[] = [];
    const client = makeClient(makeFetch(200, [], calls));
    await client.listWorkspaces();
    const headers = calls[0]?.init.headers as Record<string, string>;
    expect(headers["Authorization"]).toBe(`Bearer ${TOKEN}`);
  });

  it("routes slug workspaces through X-Workspace-Slug", async () => {
    const calls: CapturedCall[] = [];
    const client = makeClient(makeFetch(200, { issues: [], total: 0 }, calls));
    await client.listIssues("my-workspace", { limit: 5 });
    const headers = calls[0]?.init.headers as Record<string, string>;
    expect(headers["X-Workspace-Slug"]).toBe("my-workspace");
    expect(headers["X-Workspace-ID"]).toBeUndefined();
  });

  it("routes UUID workspaces through X-Workspace-ID", async () => {
    const calls: CapturedCall[] = [];
    const ws = "0b7f4c1e-1111-4222-8333-abcdefabcdef";
    const client = makeClient(makeFetch(200, { issues: [], total: 0 }, calls));
    await client.listIssues(ws, {});
    const headers = calls[0]?.init.headers as Record<string, string>;
    expect(headers["X-Workspace-ID"]).toBe(ws);
    expect(headers["X-Workspace-Slug"]).toBeUndefined();
  });

  it("serializes list filters into the query string", async () => {
    const calls: CapturedCall[] = [];
    const client = makeClient(makeFetch(200, { issues: [], total: 0 }, calls));
    await client.listIssues("ws", {
      statuses: "todo,in_progress",
      project_id: "p1",
      limit: 10,
      offset: 20,
      open_only: true,
    });
    const query = calls[0]?.url.searchParams;
    expect(query?.get("statuses")).toBe("todo,in_progress");
    expect(query?.get("project_id")).toBe("p1");
    expect(query?.get("limit")).toBe("10");
    expect(query?.get("offset")).toBe("20");
    expect(query?.get("open_only")).toBe("true");
  });

  it("omits undefined query params", async () => {
    const calls: CapturedCall[] = [];
    const client = makeClient(makeFetch(200, { issues: [], total: 0 }, calls));
    await client.listIssues("ws", { status: "todo" });
    expect([...(calls[0]?.url.searchParams.keys() ?? [])]).toEqual(["status"]);
  });

  it("POSTs JSON bodies with the content type header", async () => {
    const calls: CapturedCall[] = [];
    const client = makeClient(
      makeFetch(201, { id: "i1", identifier: "WS-1", number: 1, title: "t", status: "todo" }, calls),
    );
    await client.createIssue("ws", { title: "t", priority: "high" });
    const headers = calls[0]?.init.headers as Record<string, string>;
    expect(calls[0]?.init.method).toBe("POST");
    expect(headers["Content-Type"]).toBe("application/json");
    expect(JSON.parse(String(calls[0]?.init.body))).toEqual({
      title: "t",
      priority: "high",
    });
  });

  it("serializes unassign as explicit JSON nulls, not omitted keys or empty strings", async () => {
    const calls: CapturedCall[] = [];
    const client = makeClient(
      makeFetch(200, { id: "i1", identifier: "WS-1", number: 1, title: "t", status: "todo" }, calls),
    );
    await client.updateIssue("ws", "WS-1", { assignee_type: null, assignee_id: null });
    expect(calls[0]?.init.method).toBe("PUT");
    const raw = String(calls[0]?.init.body);
    // The server decides unassign by rawFields: keys present as null. An
    // omitted key would keep the current assignee instead.
    expect(raw).toContain('"assignee_type":null');
    expect(raw).toContain('"assignee_id":null');
    const body = JSON.parse(raw) as Record<string, unknown>;
    expect(Object.hasOwn(body, "assignee_type")).toBe(true);
    expect(body.assignee_type).toBeNull();
    expect(body.assignee_id).toBeNull();
    expect(raw).not.toContain('""');
  });

  it("routes project CRUD to the /api/projects surface (RUYI-354)", async () => {
    const calls: CapturedCall[] = [];
    const project = {
      id: "p1",
      workspace_id: "w1",
      title: "Proj",
      status: "planned",
      revision: 1,
    };
    const client = makeClient(makeFetch(200, project, calls));
    await client.getProject("ws", "p1");
    expect(calls[0]?.init.method).toBe("GET");
    expect(calls[0]?.url.pathname).toBe("/api/projects/p1");

    await client.createProject("ws", { title: "Proj", priority: "high" });
    expect(calls[1]?.init.method).toBe("POST");
    expect(calls[1]?.url.pathname).toBe("/api/projects");
    expect(JSON.parse(String(calls[1]?.init.body))).toEqual({
      title: "Proj",
      priority: "high",
    });

    await client.updateProject("ws", "p1", {
      expected_revision: 1,
      description: null,
      title: "v2",
    });
    expect(calls[2]?.init.method).toBe("PUT");
    expect(calls[2]?.url.pathname).toBe("/api/projects/p1");
    const raw = String(calls[2]?.init.body);
    // expected_revision rides along; explicit nulls survive serialization so
    // the server's rawFields contract sees a clear instead of a keep.
    expect(raw).toContain('"expected_revision":1');
    expect(raw).toContain('"description":null');
  });

  it("maps non-2xx to MulticaApiError with the server message", async () => {
    const client = makeClient(
      makeFetch(409, { code: "active_duplicate_issue", error: "duplicate" }),
    );
    const err = await client
      .createIssue("ws", { title: "dup" })
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(MulticaApiError);
    expect((err as MulticaApiError).status).toBe(409);
    expect((err as MulticaApiError).message).toContain("active_duplicate_issue");
  });

  it("keeps a descriptive message for non-JSON error bodies", async () => {
    const fetchImpl = (async () =>
      new Response("<html>oops</html>", { status: 502 })) as typeof fetch;
    const client = makeClient(fetchImpl);
    const err = await client.listWorkspaces().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(MulticaApiError);
    expect((err as MulticaApiError).message).toContain("HTTP 502");
  });

  it("wraps network failures without leaking the token", async () => {
    const fetchImpl = (async () => {
      throw new Error("ECONNREFUSED");
    }) as typeof fetch;
    const client = makeClient(fetchImpl);
    const err = await client.listWorkspaces().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(MulticaRequestError);
    expect((err as MulticaRequestError).message).toContain("network error");
    expect((err as MulticaRequestError).message).not.toContain(TOKEN);
  });

  it("times out long requests", async () => {
    const fetchImpl = (async (_input: RequestInfo | URL, init?: RequestInit) => {
      return await new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => {
          const err = new Error("aborted");
          err.name = "AbortError";
          reject(err);
        });
      });
    }) as typeof fetch;
    const client = new MulticaClient({
      serverUrl: "https://api.example.com",
      token: TOKEN,
      fetchImpl,
      timeoutMs: 20,
    });
    await expect(client.listWorkspaces()).rejects.toThrow(/timed out/);
  });

  it("returns null from getActiveTask on 404", async () => {
    const client = makeClient(makeFetch(404, { error: "not found" }));
    expect(await client.getActiveTask("ws", "issue-1")).toBeNull();
  });

  it("logs method, path template and status — never query or token", async () => {
    const calls: CapturedCall[] = [];
    const logger = silentLogger();
    const client = makeClient(makeFetch(200, { issues: [], total: 0 }, calls), logger);
    await client.searchIssues("ws", "secret voice note content");
    expect(logger.lines).toHaveLength(1);
    expect(logger.lines[0]).toMatch(/^api GET \/api\/issues\/search -> 200 \d+ms$/);
    expect(logger.lines[0]).not.toContain("secret voice note content");
    expect(JSON.stringify(logger.lines)).not.toContain(TOKEN);
  });
});
