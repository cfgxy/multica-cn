import { describe, expect, it } from "vitest";
import type { RuntimeDevice } from "../types";
import {
  agentSlotChoices,
  runtimeCredentialStatus,
  runtimeSupportsCapability,
} from "./agent-slots";

const OWNER = "user-owner";
const OTHER = "user-other";

function makeRuntime(overrides: Partial<RuntimeDevice> = {}): RuntimeDevice {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Test Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: OWNER,
    visibility: "private",
    last_seen_at: null,
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    ...overrides,
  };
}

describe("runtimeSupportsCapability", () => {
  it("treats a missing capabilities block as unfilterable (older backends)", () => {
    const rt = makeRuntime();
    expect(rt.capabilities).toBeUndefined();
    expect(runtimeSupportsCapability(rt, "text")).toBe(true);
    expect(runtimeSupportsCapability(rt, "realtime_voice")).toBe(true);
  });

  it("honors explicit capability flags", () => {
    const voiceOnly = makeRuntime({
      provider: "gemini_live",
      capabilities: { text: false, realtime_voice: true, tools: true },
    });
    expect(runtimeSupportsCapability(voiceOnly, "text")).toBe(false);
    expect(runtimeSupportsCapability(voiceOnly, "realtime_voice")).toBe(true);
  });
});

describe("runtimeCredentialStatus", () => {
  it("falls back to not_configured when the backend omits the field", () => {
    expect(runtimeCredentialStatus(makeRuntime())).toBe("not_configured");
  });

  it("passes the tri-state through", () => {
    expect(
      runtimeCredentialStatus(makeRuntime({ credential_status: "configured" })),
    ).toBe("configured");
    expect(
      runtimeCredentialStatus(makeRuntime({ credential_status: "invalid" })),
    ).toBe("invalid");
  });
});

describe("agentSlotChoices", () => {
  const textRt = makeRuntime({ id: "rt-text" });
  const voiceRt = makeRuntime({
    id: "rt-voice",
    provider: "gemini_live",
    capabilities: { text: false, realtime_voice: true, tools: true },
  });
  const offlineRt = makeRuntime({ id: "rt-offline", status: "offline" });
  const othersRt = makeRuntime({ id: "rt-other", owner_id: OTHER });

  it("filters by online + usable + capability", () => {
    const choices = agentSlotChoices(
      [textRt, voiceRt, offlineRt, othersRt],
      "text",
      { currentUserId: OWNER },
    );
    expect(choices.map((r) => r.id)).toEqual(["rt-text"]);
  });

  it("excludes the other slot's selection", () => {
    const both = makeRuntime({
      id: "rt-both",
      provider: "vllm_omni",
      capabilities: { text: true, realtime_voice: true, tools: true },
    });
    const choices = agentSlotChoices([textRt, both], "realtime_voice", {
      currentUserId: OWNER,
      excludeRuntimeId: "rt-text",
    });
    expect(choices.map((r) => r.id)).toEqual(["rt-both"]);
  });

  it("keeps the slot's own binding even when it matches the exclusion (legacy conflict)", () => {
    const both = makeRuntime({
      id: "rt-both",
      provider: "vllm_omni",
      capabilities: { text: true, realtime_voice: true, tools: true },
    });
    const choices = agentSlotChoices([both], "text", {
      currentUserId: OWNER,
      excludeRuntimeId: "rt-both",
      keepRuntimeId: "rt-both",
    });
    expect(choices.map((r) => r.id)).toEqual(["rt-both"]);
  });

  it("does not exclude when the other slot is unbound", () => {
    const choices = agentSlotChoices([textRt, voiceRt], "text", {
      currentUserId: OWNER,
      excludeRuntimeId: "",
    });
    expect(choices.map((r) => r.id)).toEqual(["rt-text"]);
  });
});
