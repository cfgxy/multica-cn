import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "@multica/core/types";

import { isSelfHealingRuntime } from "./utils";

describe("isSelfHealingRuntime", () => {
  function makeRuntime(overrides: Partial<AgentRuntime>): AgentRuntime {
    return {
      id: "rt-1",
      workspace_id: "ws-1",
      daemon_id: null,
      name: "rt",
      runtime_mode: "local",
      provider: "claude",
      launch_header: "",
      status: "online",
      device_info: "",
      metadata: {},
      owner_id: null,
      visibility: "private",
      last_seen_at: null,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      ...overrides,
    };
  }

  it("flags an online local runtime as self-healing", () => {
    expect(
      isSelfHealingRuntime(
        makeRuntime({ runtime_mode: "local", status: "online" }),
      ),
    ).toBe(true);
  });

  it("treats an offline local runtime as safe to delete", () => {
    // Daemon isn't running, so the server-side delete is final — no
    // re-registration race to worry about.
    expect(
      isSelfHealingRuntime(
        makeRuntime({ runtime_mode: "local", status: "offline" }),
      ),
    ).toBe(false);
  });

  it("treats cloud runtimes as safe to delete regardless of status", () => {
    // Cloud workers are managed by Fleet, not a self-restarting local daemon.
    expect(
      isSelfHealingRuntime(
        makeRuntime({ runtime_mode: "cloud", status: "online" }),
      ),
    ).toBe(false);
    expect(
      isSelfHealingRuntime(
        makeRuntime({ runtime_mode: "cloud", status: "offline" }),
      ),
    ).toBe(false);
  });
});
