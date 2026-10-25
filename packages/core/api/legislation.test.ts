// @vitest-environment node

import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

// RUYI-858: the approve endpoints now carry the owner's landing target
// (enactment_target / enactment_targets). Two contracts need pinning:
// the request body shape (default target omitted, explicit target sent)
// and the response parse (a malformed proposal must fall back, not leak
// the raw server payload).

const validProposal = {
  id: "p-1",
  workspace_id: "ws-1",
  carrier_scope: "workspace",
  carrier_scope_id: "ws-1",
  target_section: "",
  change_kind: "add_clause",
  clause_name: "naming",
  clause_text: "text",
  gate_answer_layer: "",
  gate_answer_retention: "",
  gate_answer_cost: "",
  gate_answer_conflict: "",
  gate_answer_dedup: "",
  evidence_anchors: [],
  status: "pending_owner",
  gate_errors: [],
  gate_warnings: [],
  rollback_reason: "",
  merged_from: [],
  source: "manual",
  created_by_type: "member",
  audit_log: [],
  created_at: "2026-10-24T00:00:00Z",
  updated_at: "2026-10-24T00:00:00Z",
};

const bodies: Record<string, unknown> = {};
const capturedBody = (key: string) => bodies[key];

function stubFetchResponses(responses: Response[]) {
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (typeof url === "string" && url.includes("/approve")) {
      const key = url.endsWith("/approve") ? "single" : "batch";
      bodies[key] = JSON.parse(String(init?.body ?? "{}"));
    }
    if (typeof url === "string" && url.includes("/preview")) {
      bodies.preview = JSON.parse(String(init?.body ?? "{}"));
    }
    return Promise.resolve(responses.shift() ?? new Response("{}", { status: 500 }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

afterEach(() => vi.unstubAllGlobals());

describe("legislation approve endpoints (RUYI-858)", () => {
  it("sends the selected landing target to preview", async () => {
    stubFetchResponses([new Response(JSON.stringify({ proposal: validProposal, diff: [], current_sha256: "", baseline_used: false }))]);
    const client = new ApiClient("https://api.example.test");

    await client.previewPromptProposal("p-1", "static_prompt");

    expect(capturedBody("preview")).toEqual({ enactment_target: "static_prompt" });
  });

  it("omits the landing target from the body when the default is kept", async () => {
    stubFetchResponses([new Response(JSON.stringify(validProposal))]);
    const client = new ApiClient("https://api.example.test");

    await client.approvePromptProposal("p-1", true);

    expect(capturedBody("single")).toEqual({ confirm_diff_previewed: true });
  });

  it("sends the explicit static-prompt landing target", async () => {
    stubFetchResponses([new Response(JSON.stringify(validProposal))]);
    const client = new ApiClient("https://api.example.test");

    await client.approvePromptProposal("p-1", true, "static_prompt");

    expect(capturedBody("single")).toEqual({
      confirm_diff_previewed: true,
      enactment_target: "static_prompt",
    });
  });

  it("sends per-id landing targets on the batch endpoint and omits them by default", async () => {
    const outcomes = [{ id: "p-1", status: 200, body: "{}" }];
    stubFetchResponses([
      new Response(JSON.stringify(outcomes)),
      new Response(JSON.stringify(outcomes)),
    ]);
    const client = new ApiClient("https://api.example.test");

    await client.batchApprovePromptProposals(["p-1", "p-2"], true);
    expect(capturedBody("batch")).toEqual({
      ids: ["p-1", "p-2"],
      confirm_diff_previewed: true,
    });

    await client.batchApprovePromptProposals(["p-1", "p-2"], true, {
      "p-1": "static_prompt",
      "p-2": "clause_library",
    });
    expect(capturedBody("batch")).toEqual({
      ids: ["p-1", "p-2"],
      confirm_diff_previewed: true,
      enactment_targets: { "p-1": "static_prompt", "p-2": "clause_library" },
    });
  });

  it("survives a drifted enactment_target value without dropping the row", async () => {
    // An installed client older than a future target value would fail the
    // enum: the approve endpoint's fallback is the raw payload by design
    // (no field loss on drift), so the row must round-trip intact, and the
    // unknown target must not masquerade as a known one.
    const drifted = { ...validProposal, enactment_target: "carrier_override" };
    stubFetchResponses([new Response(JSON.stringify(drifted))]);
    const client = new ApiClient("https://api.example.test");

    const out = await client.approvePromptProposal("p-1", true);

    expect(out.id).toEqual("p-1");
    expect(out.status).toEqual("pending_owner");
    expect(out.enactment_target).not.toEqual("clause_library");
    expect(out.enactment_target).not.toEqual("static_prompt");
  });

  it("keeps the known enactment_target values through the schema", async () => {
    stubFetchResponses([new Response(JSON.stringify({ ...validProposal, enactment_target: "static_prompt" }))]);
    const client = new ApiClient("https://api.example.test");

    const out = await client.approvePromptProposal("p-1", true);

    expect(out.enactment_target).toEqual("static_prompt");
  });
});
