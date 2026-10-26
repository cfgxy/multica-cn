// @vitest-environment node

import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

const emptyOverview = {
  workspace_id: "ws-1",
  since: "2026-10-01T00:00:00Z",
  dynamic_injection_disabled: false,
  clauses: [],
  status_counts: {},
  totals: { injections: 0, runs: 0, degraded_rows: 0, degraded_runs: 0, clause_tokens: 0 },
  migration: { total_carriers: 0, bound_carriers: 0, by_scope: {}, by_scope_total: {} },
  rollback_suggestions: [],
};

const emptyReconciliation = {
  since: "2026-10-01T00:00:00Z",
  healthy: true,
  layer_set_violations: [],
  totals: { injections: 0, runs: 0, degraded_rows: 0, degraded_runs: 0, clause_tokens: 0 },
  drift: [],
  carriers_scanned: 0,
};

afterEach(() => vi.unstubAllGlobals());

describe("clause governance API", () => {
  it("reads overview and reconciliation using the requested window", async () => {
    const fetchMock = vi.fn().mockImplementation((url: string) =>
      Promise.resolve(new Response(JSON.stringify(url.includes("reconciliation") ? emptyReconciliation : emptyOverview))),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await client.getClauseGovernanceOverview(90);
    await client.getClauseGovernanceReconciliation(90);

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      "https://api.example.test/api/prompt-legislation/governance/clauses?days=90",
      "https://api.example.test/api/prompt-legislation/governance/reconciliation?days=90",
    ]);
  });

  it("encodes detail identifiers and falls back safely for malformed data", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ id: 42, content: "must not leak into fallback" })),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    const detail = await client.getClauseGovernanceDetail("clause/1", 7);

    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/prompt-legislation/governance/clauses/clause%2F1?days=7",
      expect.any(Object),
    );
    expect(detail).toMatchObject({ id: "", versions: [], bindings: [], proposals: [], quality_daily: [] });
  });

  it("falls back when a carrier name has an incompatible type", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        ...emptyReconciliation,
        drift: [{ scope: "workspace", scope_id: "ws-1", scope_name: 42, entries: [] }],
      })),
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    const reconciliation = await client.getClauseGovernanceReconciliation(30);

    expect(reconciliation.healthy).toBe(false);
    expect(reconciliation.drift).toEqual([]);
  });

  it("sends clause management requests with encoded identifiers", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await client.updateClauseContent("clause/1", "Revised text", 4);
    await client.deactivateClause("clause/1");
    await client.deleteClause("clause/1");

    expect(fetchMock.mock.calls.map(([url, init]) => [url, init?.method, init?.body])).toEqual([
      ["https://api.example.test/api/prompt-legislation/governance/clauses/clause%2F1", "PATCH", JSON.stringify({ content: "Revised text", expected_version: 4 })],
      ["https://api.example.test/api/prompt-legislation/governance/clauses/clause%2F1/deactivate", "POST", undefined],
      ["https://api.example.test/api/prompt-legislation/governance/clauses/clause%2F1", "DELETE", undefined],
    ]);
  });
});
