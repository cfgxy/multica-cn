// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { RuntimeDevice } from "@multica/core/types";
import { runtimeCredentialDisplayState, runtimeNodeKind } from "./runtime-node";

function runtime(overrides: Partial<RuntimeDevice> = {}): RuntimeDevice {
  return {
    id: "runtime-1",
    workspace_id: "workspace-1",
    daemon_id: "daemon-1",
    name: "Node",
    runtime_mode: "local",
    provider: "codex",
    launch_header: "",
    status: "online",
    last_seen_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    device_info: "",
    metadata: {},
    owner_id: "user-1",
    visibility: "private",
    ...overrides,
  };
}

describe("runtime node display", () => {
  it("classifies explicit voice instances ahead of their daemon association", () => {
    expect(runtimeNodeKind(runtime({ capabilities: { text: false, realtime_voice: true, tools: true } }))).toBe("voice");
    expect(runtimeNodeKind(runtime({ registration_source: "manual", daemon_id: null }))).toBe("voice");
  });

  it("distinguishes daemon-backed and standalone CLI nodes", () => {
    expect(runtimeNodeKind(runtime())).toBe("daemon");
    expect(runtimeNodeKind(runtime({ daemon_id: null }))).toBe("cli");
  });

  it("collapses invalid and unreachable credentials into the visible error state", () => {
    expect(runtimeCredentialDisplayState("configured")).toBe("configured");
    expect(runtimeCredentialDisplayState("not_configured")).toBe("missing");
    expect(runtimeCredentialDisplayState(undefined)).toBe("missing");
    expect(runtimeCredentialDisplayState("invalid")).toBe("error");
    expect(runtimeCredentialDisplayState("unreachable")).toBe("error");
  });
});
