// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import {
  EMPTY_EXECUTION_PROFILE,
  EMPTY_EXECUTION_PROFILE_ACTIVATION,
  EMPTY_EXECUTION_PROFILE_LIST,
} from "./schemas";

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubJSON(body: unknown, status = 200) {
  const fetchMock = vi.fn().mockResolvedValue(
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

const client = () => new ApiClient("https://api.example.test");

describe("execution profile reads", () => {
  it("parses the list and its active pointer", async () => {
    stubJSON({
      execution_profiles: [
        {
          id: "p1",
          workspace_id: "w1",
          name: "备用供应商",
          description: null,
          created_by: "u1",
          is_active: true,
          entry_count: 3,
          last_activated_at: "2026-09-06T00:00:00Z",
          created_at: "2026-09-01T00:00:00Z",
          updated_at: "2026-09-06T00:00:00Z",
          entries: [],
        },
      ],
      active_execution_profile_id: "p1",
    });

    const res = await client().listExecutionProfiles("w1");
    expect(res.active_execution_profile_id).toBe("p1");
    expect(res.execution_profiles).toHaveLength(1);
    expect(res.execution_profiles[0]?.entry_count).toBe(3);
    expect(res.execution_profiles[0]?.is_active).toBe(true);
  });

  it("keeps rendering when the list response is malformed", async () => {
    stubJSON({ execution_profiles: "not-an-array" });
    await expect(client().listExecutionProfiles("w1")).resolves.toEqual(
      EMPTY_EXECUTION_PROFILE_LIST,
    );
  });

  it("keeps rendering when the detail response is malformed", async () => {
    stubJSON({ nope: true });
    await expect(client().getExecutionProfile("w1", "p1")).resolves.toEqual(
      EMPTY_EXECUTION_PROFILE,
    );
  });

  it("tolerates unknown fields and missing optionals", async () => {
    stubJSON({
      id: "p1",
      name: "Fallback",
      future_field: { anything: true },
    });
    const profile = await client().getExecutionProfile("w1", "p1");
    expect(profile.id).toBe("p1");
    expect(profile.name).toBe("Fallback");
    // Absent optionals take their documented defaults rather than undefined,
    // so the picker never renders "undefined members".
    expect(profile.entry_count).toBe(0);
    expect(profile.entries).toEqual([]);
    expect(profile.is_active).toBe(false);
  });
});

describe("execution profile activation", () => {
  it("parses per-member results", async () => {
    stubJSON({
      profile: { id: "p1", name: "备用供应商", is_active: true },
      applied: 2,
      skipped: 1,
      failed: 0,
      results: [
        { agent_id: "a1", status: "applied" },
        { agent_id: "a2", status: "applied" },
        { agent_id: "a3", status: "skipped", reason: "agent_archived" },
      ],
    });

    const res = await client().activateExecutionProfile("w1", "p1");
    expect(res.applied).toBe(2);
    expect(res.skipped).toBe(1);
    expect(res.results).toHaveLength(3);
    expect(res.results[2]).toMatchObject({
      agent_id: "a3",
      status: "skipped",
      reason: "agent_archived",
    });
  });

  it("reads an unknown result status without dropping the row", async () => {
    // A status kind added server-side must still reach the UI, which has a
    // default branch for it — dropping the row would silently under-report
    // how many members the activation touched.
    stubJSON({
      profile: { id: "p1", name: "P" },
      applied: 1,
      skipped: 0,
      failed: 0,
      results: [
        { agent_id: "a1", status: "applied" },
        { agent_id: "a2", status: "deferred_to_daemon" },
      ],
    });
    const res = await client().activateExecutionProfile("w1", "p1");
    expect(res.results).toHaveLength(2);
    expect(res.results[1]?.status).toBe("deferred_to_daemon");
  });

  it("falls back to the all-failed shape on a malformed response", async () => {
    // The critical direction: a response we cannot parse must never look like
    // a success, or the picker would move its checkmark to a profile that may
    // have changed nothing.
    stubJSON({ applied: "many" });
    const res = await client().activateExecutionProfile("w1", "p1");
    expect(res).toEqual(EMPTY_EXECUTION_PROFILE_ACTIVATION);
    expect(res.applied).toBe(0);
    expect(res.profile.is_active).toBe(false);
  });
});

describe("execution profile writes", () => {
  it("sends the entry upsert as a PUT with the full configuration", async () => {
    const fetchMock = stubJSON({
      agent_id: "a1",
      runtime_id: "r1",
      model: "gpt-5",
      thinking_level: "",
      updated_at: "2026-09-06T00:00:00Z",
    });

    await client().upsertExecutionProfileEntry("w1", "p1", {
      agent_id: "a1",
      runtime_id: "r1",
      model: "gpt-5",
    });

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("/api/workspaces/w1/execution-profiles/p1/entries");
    expect(init.method).toBe("PUT");
    expect(JSON.parse(String(init.body))).toEqual({
      agent_id: "a1",
      runtime_id: "r1",
      model: "gpt-5",
    });
  });

  it("keeps null and \"\" apart when parsing an entry", async () => {
    // The two states drive different activation behaviour — null leaves the
    // member's thinking level alone, "" clears it — so coercing null to ""
    // here would silently turn "no opinion" into an explicit clear.
    stubJSON({
      agent_id: "a1",
      runtime_id: "r1",
      model: "gpt-5",
      thinking_level: null,
      updated_at: "2026-09-06T00:00:00Z",
    });
    const noOpinion = await client().upsertExecutionProfileEntry("w1", "p1", {
      agent_id: "a1",
      runtime_id: "r1",
      model: "gpt-5",
    });
    expect(noOpinion.thinking_level).toBeNull();

    stubJSON({
      agent_id: "a1",
      runtime_id: "r1",
      model: "gpt-5",
      thinking_level: "",
      updated_at: "2026-09-06T00:00:00Z",
    });
    const cleared = await client().upsertExecutionProfileEntry("w1", "p1", {
      agent_id: "a1",
      runtime_id: "r1",
      model: "gpt-5",
      thinking_level: "",
    });
    expect(cleared.thinking_level).toBe("");
  });

  it("sends an explicit empty thinking level through untouched", async () => {
    const fetchMock = stubJSON({
      agent_id: "a1",
      runtime_id: "r1",
      model: "gpt-5",
      thinking_level: "",
      updated_at: "2026-09-06T00:00:00Z",
    });

    await client().upsertExecutionProfileEntry("w1", "p1", {
      agent_id: "a1",
      runtime_id: "r1",
      model: "gpt-5",
      thinking_level: "",
    });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    // Dropping the key would make the server read "no opinion" and keep the
    // member's stale level next to the new runtime and model.
    expect(JSON.parse(String(init.body))).toHaveProperty("thinking_level", "");
  });

  it("targets the member slot on entry delete", async () => {
    // 204 carries no body, so it cannot go through stubJSON.
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    await client().deleteExecutionProfileEntry("w1", "p1", "a1");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain(
      "/api/workspaces/w1/execution-profiles/p1/entries/a1",
    );
    expect(init.method).toBe("DELETE");
  });
});
