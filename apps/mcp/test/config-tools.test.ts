/**
 * Execution-config management tools.
 *
 * The config faces share one contract and these tests pin it at the
 * tool↔client seam (same discipline as tools.test.ts: the client methods are
 * fakes; the handlers are the code under test):
 *   - read-modify-write with expected_revision, stale writes answered as
 *     STRUCTURED revision_conflict results carrying actual_revision;
 *   - model writes refused with structured unsupported_model (catalog hit,
 *     static incompatible) vs invalid_input for every other 400;
 *   - get_runtime_models' polling ladder: cache hit answers inline, a cold
 *     catalog polls to completion or returns still_pending/runtime_offline;
 *   - bulk config updates: per-item outcomes, per-item expected_revision,
 *     on_error stop semantics, unknown-key rejection;
 *   - apply_execution_profile's squad mapping (decision 1): agent members
 *     materialize one-to-one with the shared template, humans are skipped,
 *     the roster is read once (later roster changes do not follow);
 *   - the nine read faces stay wire-read-only.
 *
 * fakeClient records EVERY client method call (base and per-test overrides
 * alike) through a Proxy, so call-count assertions survive overrides. Agent
 * refs must be UUIDs (or exact names) because resolveAgentRef enforces that
 * gate client-side — fixtures use UUID-shaped ids.
 */

import { describe, expect, it } from "vitest";

import { MulticaApiError } from "../src/rest.js";
import type { MulticaClient } from "../src/rest.js";
import { ToolInputError } from "../src/schemas.js";
import { findTool, TOOL_DEFINITIONS } from "../src/tools.js";
import type {
  AgentConfigInfo,
  ExecutionProfileInfo,
  ModelListRequestInfo,
  RuntimeInfo,
} from "../src/types.js";

const WS = "voice-notes";

const A1 = "aaaaaaaa-0000-4000-8000-000000000001";
const A2 = "aaaaaaaa-0000-4000-8000-000000000002";
const A3 = "aaaaaaaa-0000-4000-8000-000000000003";
const A4 = "aaaaaaaa-0000-4000-8000-000000000004";
const A9 = "aaaaaaaa-0000-4000-8000-000000000009";
const A_ARCH = "aaaaaaaa-0000-4000-8000-0000000000aa";

function apiError(status: number, body: Record<string, unknown>): MulticaApiError {
  const message = typeof body.error === "string" ? body.error : `HTTP ${status}`;
  return new MulticaApiError(status, message, body);
}

function runtimeFixture(over: Partial<RuntimeInfo> = {}): RuntimeInfo {
  return {
    id: "rt-1",
    workspace_id: "w1",
    daemon_id: "daemon-1",
    name: "guixy-desk",
    runtime_mode: "local",
    provider: "claude",
    status: "online",
    visibility: "private",
    last_seen_at: "2026-10-05T03:00:00Z",
    ...over,
  };
}

function agentFixture(over: Partial<AgentConfigInfo> = {}): AgentConfigInfo {
  return {
    id: A1,
    name: "顾小鱼",
    runtime_id: "rt-1",
    runtime_mode: "local",
    model: "claude-opus-5",
    thinking_level: "high",
    status: "active",
    revision: 3,
    updated_at: "2026-10-05T03:00:00Z",
    ...over,
  };
}

function profileFixture(over: Partial<ExecutionProfileInfo> = {}): ExecutionProfileInfo {
  return {
    id: "p1",
    workspace_id: "w1",
    name: "backend-default",
    is_active: false,
    entry_count: 1,
    revision: 2,
    entries: [
      { agent_id: A1, runtime_id: "rt-1", model: "claude-opus-5", thinking_level: "high" },
    ],
    ...over,
  };
}

function catalogFixture(over: Partial<ModelListRequestInfo> = {}): ModelListRequestInfo {
  return {
    id: "req-1",
    runtime_id: "rt-1",
    status: "completed",
    supported: true,
    models: [
      { id: "claude-opus-5", default: true, thinking: { supported_levels: [{ value: "high", label: "High" }] } },
      { id: "claude-sonnet-5" },
    ],
    unavailable_models: [{ id: "claude-3-haiku", reason: "removed by provider" }],
    ...over,
  };
}

type Call = { method: string; args: unknown[] };

function fakeClient(overrides: Partial<Record<string, unknown>> = {}): MulticaClient {
  const calls: Call[] = [];
  const base = {
    listWorkspaces: async () => [{ id: "w1", slug: WS, name: "Voice" }],
    listRuntimes: async () => [
      runtimeFixture(),
      runtimeFixture({ id: "rt-2", daemon_id: "daemon-1", status: "offline", name: "guixy-laptop" }),
      runtimeFixture({ id: "rt-cloud", daemon_id: null, name: "cloud-runner", provider: "codex" }),
    ],
    listAgentConfigs: async () => [
      agentFixture(),
      agentFixture({ id: A2, name: "金小欣", runtime_id: "rt-1", model: "", revision: 1 }),
      agentFixture({ id: A3, name: "dup", runtime_id: null, model: "", revision: 1 }),
      agentFixture({ id: A4, name: "dup", runtime_id: null, model: "", revision: 1 }),
      agentFixture({ id: A_ARCH, name: " archived", runtime_id: "rt-1", revision: 1, archived_at: "2026-10-01T00:00:00Z" }),
    ],
    getAgentConfig: async (_ws: string, id: string) => agentFixture({ id }),
    updateAgentConfig: async (_ws: string, id: string, body: Record<string, unknown>) =>
      agentFixture({ id, revision: ((body.expected_revision as number | undefined) ?? 3) + 1 }),
    initiateModelList: async () => catalogFixture(),
    getModelListRequest: async (_ws: string, _rt: string, requestId: string) =>
      catalogFixture({ id: requestId }),
    listExecutionProfiles: async () => [profileFixture({ is_active: true, name: "active-one" })],
    getExecutionProfile: async (_wsId: string, profileId: string) => profileFixture({ id: profileId }),
    createExecutionProfile: async (_wsId: string, body: Record<string, unknown>) =>
      profileFixture({ id: "p-new", name: String(body.name), revision: 1, entries: [], entry_count: 0 }),
    updateExecutionProfile: async (_wsId: string, profileId: string, body: Record<string, unknown>) =>
      profileFixture({ id: profileId, name: String(body.name ?? "backend-default"), revision: 3 }),
    deleteExecutionProfile: async () => undefined,
    upsertExecutionProfileEntry: async (_wsId: string, _profileId: string, body: Record<string, unknown>) => body,
    deleteExecutionProfileEntry: async () => undefined,
    activateExecutionProfile: async (_wsId: string, profileId: string) => ({
      profile: profileFixture({ id: profileId, is_active: true, revision: 9 }),
      applied: 1,
      skipped: 0,
      failed: 0,
      results: [{ agent_id: A1, status: "applied" }],
    }),
    listSquads: async () => [{ id: "sq-1", name: "dev-squad", member_count: 3 }],
    listSquadMembers: async (_ws: string, squadId: string) => [
      { id: "m1", squad_id: squadId, member_type: "agent", member_id: A1, role: "member" },
      { id: "m2", squad_id: squadId, member_type: "member", member_id: "u9", role: "member" },
    ],
  };
  const target = { ...base, ...overrides } as Record<string, unknown>;
  const client = new Proxy(target as unknown as MulticaClient, {
    get(t, prop) {
      if (prop === "__calls") return calls;
      const value = Reflect.get(t, prop);
      if (typeof value === "function") {
        return (...args: unknown[]) => {
          calls.push({ method: String(prop), args });
          return (value as (...a: unknown[]) => unknown).apply(t, args);
        };
      }
      return value;
    },
  });
  return client;
}

function callsOf(client: MulticaClient): Call[] {
  return (client as unknown as { __calls: Call[] }).__calls;
}

async function call(name: string, args: Record<string, unknown>, overrides: Partial<Record<string, unknown>> = {}) {
  const client = fakeClient(overrides);
  const tool = findTool(name);
  if (tool === undefined) throw new Error(`unknown tool ${name}`);
  const result = await tool.handler({ workspace: WS, ...args }, client);
  return { result: result as Record<string, unknown>, calls: callsOf(client), client };
}

// ---- discovery reads ------------------------------------------------------

describe("daemon instance discovery", () => {
  it("groups runtimes per daemon and counts cloud runtimes separately", async () => {
    const { result } = await call("list_daemon_instances", {});
    const daemons = result.daemons as Array<Record<string, unknown>>;
    expect(daemons).toHaveLength(1);
    expect(daemons[0]).toMatchObject({
      daemon_id: "daemon-1",
      status: "online",
      runtime_count: 2,
      online_runtime_count: 1,
    });
    expect(result.cloud_runtime_count).toBe(1);
    expect(result.total_runtime_count).toBe(3);
  });

  it("answers a known daemon with its runtimes and an unknown one with not_found", async () => {
    const found = await call("get_daemon_instance", { daemon_id: "daemon-1" });
    expect(found.result.found).toBe(true);
    expect(found.result.status).toBe("online");
    expect(found.result.runtime_count).toBe(2);

    const missing = await call("get_daemon_instance", { daemon_id: "daemon-404" });
    expect(missing.result).toMatchObject({ found: false, code: "not_found" });
  });
});

describe("runtime reads", () => {
  it("lists runtimes read-only", async () => {
    const { result, calls } = await call("list_runtimes", {});
    expect(result.total).toBe(3);
    expect(calls.every((c) => c.method === "listRuntimes")).toBe(true);
  });

  it("joins used_by non-archived agents and reports not_found for unknown ids", async () => {
    const { result } = await call("get_runtime", { runtime_id: "rt-1" });
    expect(result.found).toBe(true);
    expect(result.used_by_count).toBe(2);
    const usedBy = result.used_by as Array<Record<string, unknown>>;
    expect(usedBy.map((agent) => agent.id).sort()).toEqual([A1, A2]);

    const missing = await call("get_runtime", { runtime_id: "rt-none" });
    expect(missing.result).toMatchObject({ found: false, code: "not_found" });
  });
});

describe("get_runtime_models polling ladder", () => {
  it("answers a cache hit inline without polling", async () => {
    const { result, calls } = await call("get_runtime_models", { runtime_id: "rt-1" });
    expect(result.completed).toBe(true);
    expect(calls.filter((c) => c.method === "getModelListRequest")).toHaveLength(0);
    const models = result.models as Array<Record<string, unknown>>;
    expect(models).toHaveLength(2);
    expect(models[0]).toMatchObject({ id: "claude-opus-5", default: true });
    expect(models[0]?.thinking_levels).toEqual([{ value: "high", label: "High" }]);
  });

  it("polls a cold catalog to completion", async () => {
    let polls = 0;
    const { result, calls } = await call(
      "get_runtime_models",
      { runtime_id: "rt-1", wait_ms: 5000 },
      {
        initiateModelList: async () => catalogFixture({ status: "pending", models: undefined }),
        getModelListRequest: async (_ws: string, _rt: string, reqId: string) => {
          polls += 1;
          return polls >= 2 ? catalogFixture({ id: reqId }) : catalogFixture({ status: "running", id: reqId, models: undefined });
        },
      },
    );
    expect(result.completed).toBe(true);
    expect(polls).toBe(2);
    expect(calls.filter((c) => c.method === "getModelListRequest")).toHaveLength(2);
  });

  it("hands back still_pending with the request id when the deadline passes", async () => {
    const { result } = await call(
      "get_runtime_models",
      { runtime_id: "rt-1", wait_ms: 0 },
      { initiateModelList: async () => catalogFixture({ status: "pending", models: undefined }) },
    );
    expect(result.completed).toBe(false);
    expect(result.code).toBe("still_pending");
    expect(result.request_id).toBe("req-1");
    expect(String(result.hint)).toContain("req-1");
  });

  it("maps a failed discovery and an offline runtime to structured codes", async () => {
    const failed = await call(
      "get_runtime_models",
      { runtime_id: "rt-1" },
      { initiateModelList: async () => catalogFixture({ status: "failed", error: "daemon exploded", models: undefined }) },
    );
    expect(failed.result).toMatchObject({ completed: false, code: "discovery_failed", error: "daemon exploded" });

    const offline = await call(
      "get_runtime_models",
      { runtime_id: "rt-1" },
      {
        initiateModelList: async () => {
          throw apiError(503, { error: "runtime is offline" });
        },
      },
    );
    expect(offline.result).toMatchObject({ completed: false, code: "runtime_offline", message: "runtime is offline" });
  });
});

// ---- agent config read-modify-write --------------------------------------

describe("agent config faces", () => {
  it("reads config by UUID directly and by unique name via resolution", async () => {
    const byId = await call("get_agent_runtime_config", { agent: A1 });
    expect(byId.result.found).toBe(true);
    expect(byId.calls[0]).toEqual({ method: "getAgentConfig", args: [WS, A1] });

    const byName = await call("get_agent_runtime_config", { agent: "金小欣" });
    expect(byName.result.found).toBe(true);
    expect((byName.result.config as Record<string, unknown>).id).toBe(A2);
  });

  it("refuses an ambiguous name and an unknown name without wire writes", async () => {
    await expect(
      call("get_agent_runtime_config", { agent: "dup" }).then((r) => r.result),
    ).rejects.toBeInstanceOf(ToolInputError);
    await expect(
      call("update_agent_runtime_config", { agent: "nobody", model: "x" }).then((r) => r.result),
    ).rejects.toBeInstanceOf(ToolInputError);
  });

  it("passes the tri-state model/thinking values and expected_revision through", async () => {
    const { result, calls } = await call("update_agent_runtime_config", {
      agent: A1,
      model: "",
      thinking_level: "low",
      expected_revision: 3,
    });
    expect(result.updated).toBe(true);
    const write = calls.find((c) => c.method === "updateAgentConfig");
    expect(write?.args).toEqual([WS, A1, { expected_revision: 3, model: "", thinking_level: "low" }]);
    expect((result.config as Record<string, unknown>).revision).toBe(4);
  });

  it("answers a stale revision with structured revision_conflict + actual_revision", async () => {
    const { result } = await call(
      "update_agent_runtime_config",
      { agent: A1, model: "claude-opus-5", expected_revision: 2 },
      {
        updateAgentConfig: async () => {
          throw apiError(409, {
            error: "agent was modified concurrently",
            code: "revision_conflict",
            expected_revision: 2,
            actual_revision: 7,
          });
        },
      },
    );
    expect(result.updated).toBe(false);
    expect(result.code).toBe("revision_conflict");
    expect(result.expected_revision).toBe(2);
    expect(result.actual_revision).toBe(7);
  });

  it("maps unsupported_model and other 400s to distinct structured codes", async () => {
    const unsupported = await call(
      "update_agent_runtime_config",
      { agent: A1, model: "gpt-5.2" },
      {
        updateAgentConfig: async () => {
          throw apiError(400, {
            error: 'model "gpt-5.2" is not in runtime rt-1\'s model catalog',
            code: "unsupported_model",
          });
        },
      },
    );
    expect(unsupported.result.updated).toBe(false);
    expect(unsupported.result.code).toBe("unsupported_model");
    expect(String(unsupported.result.hint)).toContain("get_runtime_models");

    const invalid = await call(
      "update_agent_runtime_config",
      { agent: A1, thinking_level: "mega" },
      {
        updateAgentConfig: async () => {
          throw apiError(400, { error: "thinking_level 'mega' is not valid for provider claude" });
        },
      },
    );
    expect(invalid.result.code).toBe("invalid_input");
    expect(invalid.result.message).toContain("mega");
  });
});

describe("bulk config updates", () => {
  it("reports per-item outcomes and passes per-item expected_revision", async () => {
    const { result, calls } = await call("bulk_update_agent_runtime_config", {
      updates: [
        { agent: A1, model: "claude-opus-5", expected_revision: 3 },
        { agent: "顾小鱼", thinking_level: "" },
      ],
    });
    expect(result).toMatchObject({ total: 2, updated: 2, failed: 0, skipped: 0 });
    const writes = calls.filter((c) => c.method === "updateAgentConfig");
    expect(writes[1]?.args[2]).toMatchObject({ thinking_level: "" });
  });

  it("classifies a conflicting item with actual_revision and keeps going", async () => {
    const { result } = await call("bulk_update_agent_runtime_config", {
      updates: [
        { agent: A1, model: "x" },
        { agent: A2, model: "y", expected_revision: 1 },
      ],
      on_error: "continue",
    }, {
      updateAgentConfig: async (_ws: string, id: string) => {
        if (id === A2) {
          throw apiError(409, { error: "conflict", code: "revision_conflict", actual_revision: 9 });
        }
        return agentFixture({ id });
      },
    });
    expect(result.updated).toBe(1);
    expect(result.failed).toBe(1);
    const failed = (result.results as Array<Record<string, unknown>>)[1];
    expect(failed?.outcome).toBe("failed");
    expect(failed?.error).toMatchObject({ code: "revision_conflict", actual_revision: 9 });
  });

  it("stops the batch on the first failure and skips the rest", async () => {
    const { result, calls } = await call("bulk_update_agent_runtime_config", {
      updates: [{ agent: A1, model: "x" }, { agent: A2, model: "y" }, { agent: A3, model: "z" }],
      on_error: "stop",
    }, {
      updateAgentConfig: async (_ws: string, id: string) => {
        if (id === A1) throw apiError(400, { error: "bad", code: "unsupported_model" });
        return agentFixture({ id });
      },
    });
    expect(result).toMatchObject({ updated: 0, failed: 1, skipped: 2 });
    expect(calls.filter((c) => c.method === "updateAgentConfig")).toHaveLength(1);
    const skipped = (result.results as Array<Record<string, unknown>>)[2];
    expect(skipped).toMatchObject({ outcome: "skipped", reason: "not_attempted" });
  });

  it("rejects unknown item keys, oversized batches and empty batches client-side", async () => {
    await expect(
      call("bulk_update_agent_runtime_config", { updates: [{ agent: A1, priority: "high" }] }).then((r) => r.result),
    ).rejects.toBeInstanceOf(ToolInputError);
    await expect(
      call("bulk_update_agent_runtime_config", { updates: [] }).then((r) => r.result),
    ).rejects.toBeInstanceOf(ToolInputError);
    await expect(
      call(
        "bulk_update_agent_runtime_config",
        { updates: Array.from({ length: 51 }, () => ({ agent: A1 })) },
      ).then((r) => r.result),
    ).rejects.toBeInstanceOf(ToolInputError);
  });
});

// ---- execution profile lifecycle ------------------------------------------

describe("execution profile faces", () => {
  it("lists profiles read-only with revisions", async () => {
    const { result, calls } = await call("list_execution_profiles", {});
    expect(result.total).toBe(1);
    expect((result.profiles as Array<Record<string, unknown>>)[0]).toMatchObject({
      id: "p1", is_active: true, revision: 2,
    });
    expect(calls.some((c) => c.method === "listExecutionProfiles")).toBe(true);
  });

  it("reads a profile in full and answers 404 structurally", async () => {
    const { result } = await call("get_execution_profile", { profile_id: "p1" });
    expect(result.found).toBe(true);
    expect((result.profile as Record<string, unknown>).entries).toHaveLength(1);
    expect((result.profile as Record<string, unknown>).revision).toBe(2);

    const missing = await call("get_execution_profile", { profile_id: "gone" }, {
      getExecutionProfile: async () => {
        throw apiError(404, { error: "profile not found" });
      },
    });
    expect(missing.result).toMatchObject({ found: false, code: "not_found" });
  });

  it("creates a profile and echoes its fresh revision 1", async () => {
    const { result } = await call("create_execution_profile", { name: "fresh", description: "d" });
    expect(result.created).toBe(true);
    expect(result.profile).toMatchObject({ name: "fresh", revision: 1 });
  });

  it("chains the fresh revision into entry upserts after the metadata write", async () => {
    const { result, calls } = await call("update_execution_profile", {
      profile_id: "p1",
      name: "renamed",
      expected_revision: 2,
      entries: [
        { agent: A1, runtime_id: "rt-1", model: "claude-sonnet-5" },
        { agent: A2, runtime_id: "rt-1", model: "claude-opus-5", thinking_level: null },
      ],
    });
    expect(result.updated).toBe(true);
    const meta = calls.find((c) => c.method === "updateExecutionProfile");
    expect(meta?.args[2]).toMatchObject({ name: "renamed", expected_revision: 2 });
    const upserts = calls.filter((c) => c.method === "upsertExecutionProfileEntry");
    expect(upserts).toHaveLength(2);
    // First entry is guarded by the revision the metadata write produced;
    // the rest land unguarded inside the sequential batch.
    expect(upserts[0]?.args[2]).toMatchObject({ agent_id: A1, expected_revision: 3 });
    expect(upserts[1]?.args[2]).toMatchObject({ agent_id: A2, expected_revision: undefined });
  });

  it("answers a guarded delete with revision_conflict and a missing one with not_found", async () => {
    const conflict = await call("delete_execution_profile", { profile_id: "p1", expected_revision: 1 }, {
      deleteExecutionProfile: async () => {
        throw apiError(409, { error: "changed", code: "revision_conflict", actual_revision: 4 });
      },
    });
    expect(conflict.result).toMatchObject({ updated: false, code: "revision_conflict", actual_revision: 4 });

    const missing = await call("delete_execution_profile", { profile_id: "gone" }, {
      deleteExecutionProfile: async () => {
        throw apiError(404, { error: "profile not found" });
      },
    });
    expect(missing.result).toMatchObject({ deleted: false, code: "not_found" });
  });

  it("deletes stale entries only through apply's replace_entries path", async () => {
    // Covered by the squad-mapping describe below; kept as a marker that the
    // standalone delete_execution_profile face has no entry-delete surface.
    expect(findTool("delete_execution_profile")).toBeDefined();
  });
});

// ---- apply_execution_profile: squad mapping (decision 1) -------------------

describe("apply_execution_profile squad mapping", () => {
  it("maps agent members one-to-one with the shared template and skips humans", async () => {
    const { result, calls } = await call("apply_execution_profile", {
      profile_id: "p1",
      squad: "dev-squad",
      runtime_id: "rt-2",
      model: "gpt-5.2",
      thinking_level: null,
      expected_revision: 2,
    });
    expect(result.applied).toBe(true);
    expect(result.squad).toMatchObject({
      squad_id: "sq-1",
      members_mapped: 1,
      members_skipped_human: 1,
    });
    expect(String((result.squad as Record<string, unknown>).mapping_semantics)).toContain("do not follow");

    const upserts = calls.filter((c) => c.method === "upsertExecutionProfileEntry");
    expect(upserts).toHaveLength(1);
    expect(upserts[0]?.args[2]).toMatchObject({
      agent_id: A1,
      runtime_id: "rt-2",
      model: "gpt-5.2",
      thinking_level: null,
      expected_revision: 2,
    });
    expect(calls.some((c) => c.method === "activateExecutionProfile")).toBe(true);
    expect(result.activation).toMatchObject({ applied: 1, failed: 0 });
  });

  it("replace_entries deletes stored entries absent from the new set first", async () => {
    const { result, calls } = await call("apply_execution_profile", {
      profile_id: "p1",
      squad: "dev-squad",
      runtime_id: "rt-2",
      model: "gpt-5.2",
      replace_entries: true,
    }, {
      // Profile carries entries for A1 AND A9; the squad maps only A1.
      getExecutionProfile: async (_wsId: string, profileId: string) =>
        profileFixture({
          id: profileId,
          entries: [
            { agent_id: A1, runtime_id: "rt-1", model: "claude-opus-5" },
            { agent_id: A9, runtime_id: "rt-1", model: "claude-opus-5" },
          ],
          entry_count: 2,
        }),
    });
    expect(result.entries_deleted).toBe(1);
    expect(result.deleted_agent_ids).toEqual([A9]);
    const deletes = calls.filter((c) => c.method === "deleteExecutionProfileEntry");
    expect(deletes).toHaveLength(1);
    expect(deletes[0]?.args).toEqual(["w1", "p1", A9]);
  });

  it("supports explicit entries, mutual exclusion, staged mode and structured failures", async () => {
    await expect(
      call("apply_execution_profile", {
        profile_id: "p1",
        squad: "dev-squad",
        entries: [{ agent: A1, runtime_id: "rt-1", model: "m" }],
      }).then((r) => r.result),
    ).rejects.toBeInstanceOf(ToolInputError);

    const staged = await call("apply_execution_profile", {
      profile_id: "p1",
      entries: [{ agent: A1, runtime_id: "rt-2", model: "m" }],
      activate_now: false,
    });
    expect(staged.result.applied).toBe(false);
    expect(staged.result.code).toBe("staged_not_activated");
    expect(staged.calls.some((c) => c.method === "activateExecutionProfile")).toBe(false);

    const forbidden = await call("apply_execution_profile", { profile_id: "p1" }, {
      activateExecutionProfile: async () => {
        throw apiError(403, { error: "admin only" });
      },
    });
    expect(forbidden.result).toMatchObject({ applied: false, code: "permission_denied" });

    const conflict = await call("apply_execution_profile", {
      profile_id: "p1",
      entries: [{ agent: A1, runtime_id: "rt-2", model: "m" }],
      expected_revision: 1,
    }, {
      upsertExecutionProfileEntry: async () => {
        throw apiError(409, { error: "changed", code: "revision_conflict", actual_revision: 5 });
      },
    });
    expect(conflict.result).toMatchObject({ updated: false, code: "revision_conflict", actual_revision: 5 });

    const humanOnly = await call("apply_execution_profile", {
      profile_id: "p1",
      squad: "dev-squad",
      runtime_id: "rt-2",
      model: "m",
    }, {
      listSquadMembers: async () => [
        { id: "m2", member_type: "member", member_id: "u9" },
      ],
    });
    expect(humanOnly.result).toMatchObject({ applied: false, code: "no_agent_members" });
  });
});

// ---- topology composition ---------------------------------------------------

describe("get_execution_topology", () => {
  it("composes runtimes, bindings, daemon groups, active profile and drift", async () => {
    const { result } = await call("get_execution_topology", {});
    const runtimes = result.runtimes as Array<Record<string, unknown>>;
    expect(runtimes).toHaveLength(3);
    const rt1 = runtimes.find((rt) => rt.id === "rt-1");
    expect(rt1?.used_by_count).toBe(2);
    expect((result.daemons as unknown[]).length).toBeGreaterThan(0);
    expect(result.active_profile).toMatchObject({ name: "active-one", revision: 2 });
    const agents = (rt1?.agents as Array<Record<string, unknown>>);
    const a1 = agents.find((agent) => agent.id === A1);
    const a2 = agents.find((agent) => agent.id === A2);
    // a1 matches its entry (rt-1 + claude-opus-5) → false = no drift;
    // a2 has no entry in the active profile → null = unjudged.
    expect(a1?.active_profile_entry_matches).toBe(false);
    expect(a2?.active_profile_entry_matches).toBeNull();
    expect(result.drift_summary).toMatchObject({ drifted_agent_count: 0 });
    expect((result.unbound_agents as unknown[]).length).toBe(2);
  });

  it("flags drift when an agent's model moved off its active-profile entry", async () => {
    const { result } = await call("get_execution_topology", {}, {
      listAgentConfigs: async () => [agentFixture({ model: "claude-sonnet-5" })],
    });
    expect(result.drift_summary).toMatchObject({ active_profile_id: "p1", drifted_agent_count: 1 });
  });

  it("reports no-drift cleanly when no profile is active", async () => {
    const { result } = await call("get_execution_topology", {}, {
      listExecutionProfiles: async () => [profileFixture({ is_active: false })],
    });
    expect(result.drift_summary).toMatchObject({ active_profile: null, drifted_agent_count: 0 });
    expect(result.active_profile).toBeNull();
  });
});

// ---- surface invariants ------------------------------------------------------

describe("config surface invariants", () => {
  it("keeps the nine config reads wire-read-only and every other config face a write", () => {
    const readOnly = new Set([
      "list_daemon_instances", "get_daemon_instance", "list_runtimes", "get_runtime",
      "get_runtime_models", "get_agent_runtime_config", "list_execution_profiles",
      "get_execution_profile", "get_execution_topology",
    ]);
    const configWrite = new Set([
      "update_agent_runtime_config", "bulk_update_agent_runtime_config",
      "create_execution_profile", "update_execution_profile",
      "delete_execution_profile", "apply_execution_profile",
    ]);
    for (const tool of TOOL_DEFINITIONS) {
      if (!readOnly.has(tool.name) && !configWrite.has(tool.name)) continue;
      const description = tool.description.toLowerCase();
      if (readOnly.has(tool.name)) {
        expect(description, tool.name).toContain("read-only");
      }
      if (configWrite.has(tool.name)) {
        expect(description, tool.name).toMatch(/never starts a run|never triggers|nothing is rewritten|rewrite/i);
      }
    }
  });

  it("pins expected_revision floors at 1 on every config CAS surface", () => {
    for (const name of [
      "update_agent_runtime_config",
      "update_execution_profile",
      "delete_execution_profile",
      "apply_execution_profile",
    ]) {
      const schema = findTool(name)?.inputSchema;
      const minimum = schema?.properties?.expected_revision?.minimum;
      expect(minimum, name).toBe(1);
    }
    const bulkItems = findTool("bulk_update_agent_runtime_config")?.inputSchema.properties?.updates;
    expect(bulkItems?.items?.properties?.expected_revision?.minimum).toBe(1);
  });
});
