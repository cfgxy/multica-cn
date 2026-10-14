/**
 * Patch-builder tests for the agent profile editor (RUYI-538 ①) — the
 * acceptance clause "调整提交后的生效路径有代码级断言": the PUT body must
 * carry only fields that actually changed, keep bounded-number revert
 * semantics (never clamp), honor the tri-state "" = explicit clear, and
 * round-trip the subagents toggle through runtime_config.
 */
import { describe, expect, it } from "vitest";
import type { Agent } from "@multica/core/types";
import {
  AGENT_MAX_CONCURRENT_TASKS_MAX,
  AGENT_MAX_CONCURRENT_TASKS_MIN,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED,
} from "@multica/core/agents/constants";
import { buildProfilePatch, parseBounded } from "./agent-profile-patch";

function agent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: "agent-1",
    name: "Coder",
    description: "does things",
    instructions: "be nice",
    avatar_url: "mc://file/a.png",
    runtime_id: "runtime-1",
    voice_runtime_id: "",
    model: "glm-5.3",
    thinking_level: "",
    service_tier: "",
    max_concurrent_tasks: 4,
    session_max_context_tokens: 200000,
    session_compact_pct: 80,
    runtime_config: {},
    ...overrides,
  } as Agent;
}

function draft(overrides: Partial<Parameters<typeof buildProfilePatch>[1]> = {}) {
  return {
    name: "Coder",
    description: "does things",
    instructions: "be nice",
    avatarUrl: "mc://file/a.png",
    model: "glm-5.3",
    runtimeId: "runtime-1",
    thinking: "",
    tier: "",
    concurrency: "4",
    maxContext: "200000",
    compactPct: "80",
    subagents: false,
    voiceProviderId: "",
    voiceModel: "",
    voiceAdvanced: "",
    ...overrides,
  };
}

describe("parseBounded", () => {
  it("admits in-range integers", () => {
    expect(parseBounded("3", 1, 16)).toBe(3);
  });

  it("rejects non-numeric and out-of-range drafts (revert, not clamp)", () => {
    expect(parseBounded("abc", 1, 16)).toBeNull();
    expect(parseBounded("0", AGENT_MAX_CONCURRENT_TASKS_MIN, AGENT_MAX_CONCURRENT_TASKS_MAX)).toBeNull();
    expect(parseBounded("999", AGENT_MAX_CONCURRENT_TASKS_MIN, AGENT_MAX_CONCURRENT_TASKS_MAX)).toBeNull();
  });

  it("admits the extra sentinel outside the range (0 = gate disabled)", () => {
    expect(
      parseBounded("0", 1000, 10000000, AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED),
    ).toBe(0);
  });
});

describe("buildProfilePatch", () => {
  it("emits an empty patch when nothing changed", () => {
    expect(buildProfilePatch(agent(), draft())).toEqual({});
  });

  it("carries only the fields that changed", () => {
    const patch = buildProfilePatch(
      agent(),
      draft({ model: "glm-5.3-flash", concurrency: "6" }),
    );
    expect(patch).toEqual({ model: "glm-5.3-flash", max_concurrent_tasks: 6 });
  });

  it("omits a concurrency draft outside [min, max] instead of clamping", () => {
    const patch = buildProfilePatch(agent(), draft({ concurrency: "999" }));
    expect(patch).toEqual({});
  });

  it("treats thinking '' as explicit clear and a value as set (tri-state)", () => {
    const cleared = buildProfilePatch(
      agent({ thinking_level: "high" }),
      draft({ thinking: "" }),
    );
    expect(cleared).toEqual({ thinking_level: "" });

    const set = buildProfilePatch(
      agent({ thinking_level: "" }),
      draft({ thinking: "high" }),
    );
    expect(set).toEqual({ thinking_level: "high" });
  });

  it("treats service_tier '' as explicit clear (tri-state)", () => {
    const patch = buildProfilePatch(
      agent({ service_tier: "priority" }),
      draft({ tier: "" }),
    );
    expect(patch).toEqual({ service_tier: "" });
  });

  it("rejects a blank name draft with null (nothing valid to save)", () => {
    expect(buildProfilePatch(agent(), draft({ name: "   " }))).toBeNull();
  });

  it("returns null before the agent row is loaded (empty id)", () => {
    expect(buildProfilePatch(agent({ id: "" }), draft())).toBeNull();
  });

  it("round-trips the subagents toggle through runtime_config", () => {
    const patch = buildProfilePatch(agent(), draft({ subagents: true }));
    expect(patch?.runtime_config).toBeDefined();
  });

  describe("voice provider override triple (RUYI-749 D1)", () => {
    const bound = {
      voice_provider_id: "p1",
      voice_model: "m-1",
      voice_advanced: { a: 1 },
    } as Partial<Agent>;

    it("emits an empty patch when the triple is untouched", () => {
      expect(buildProfilePatch(agent(bound), draft({
        voiceProviderId: "p1",
        voiceModel: "m-1",
        voiceAdvanced: "{\n  \"a\": 1\n}",
      }))).toEqual({});
    });

    it("binds a fresh override as one unit, carrying non-empty model/advanced drafts", () => {
      const patch = buildProfilePatch(agent(), draft({
        voiceProviderId: "p1",
        voiceModel: "m-1",
        voiceAdvanced: '{"a": 1}',
      }));
      expect(patch).toEqual({
        voice_provider_id: "p1",
        voice_model: "m-1",
        voice_advanced: { a: 1 },
      });
    });

    it("binds with only the provider when model/advanced drafts are empty", () => {
      const patch = buildProfilePatch(agent(), draft({ voiceProviderId: "p1" }));
      expect(patch).toEqual({ voice_provider_id: "p1" });
    });

    it("unbinds with voice_provider_id alone — model/advanced must not ride along", () => {
      const patch = buildProfilePatch(agent(bound), draft({
        voiceProviderId: "",
        voiceModel: "changed",
        voiceAdvanced: '{"b": 2}',
      }));
      expect(patch).toEqual({ voice_provider_id: "" });
    });

    it("refines only the model when the provider is unchanged", () => {
      const patch = buildProfilePatch(agent(bound), draft({
        voiceProviderId: "p1",
        voiceModel: "m-2",
        voiceAdvanced: '{\n  "a": 1\n}',
      }));
      expect(patch).toEqual({ voice_model: "m-2" });
    });

    it("clears the model with '' while the provider stays bound", () => {
      const patch = buildProfilePatch(agent(bound), draft({
        voiceProviderId: "p1",
        voiceModel: "",
        voiceAdvanced: '{\n  "a": 1\n}',
      }));
      expect(patch).toEqual({ voice_model: "" });
    });

    it("refines only advanced when model is unchanged", () => {
      const patch = buildProfilePatch(agent(bound), draft({
        voiceProviderId: "p1",
        voiceModel: "m-1",
        voiceAdvanced: '{"b": 2}',
      }));
      expect(patch).toEqual({ voice_advanced: { b: 2 } });
    });

    it("clears stored advanced with an explicit {} when the draft is emptied", () => {
      const patch = buildProfilePatch(agent(bound), draft({
        voiceProviderId: "p1",
        voiceModel: "m-1",
        voiceAdvanced: "",
      }));
      expect(patch).toEqual({ voice_advanced: {} });
    });

    it("omits an invalid advanced draft instead of sending it (revert, not clamp)", () => {
      const patch = buildProfilePatch(agent(bound), draft({
        voiceProviderId: "p1",
        voiceModel: "m-1",
        voiceAdvanced: "{nope",
      }));
      expect(patch).toEqual({});
    });

    it("omits model/advanced edits when no provider is bound anywhere", () => {
      // The UI disables these fields until a provider is picked; the builder
      // holds the same invariant — a stray draft edit never leaks into a
      // 400 ("set voice_provider_id first").
      const patch = buildProfilePatch(agent(), draft({
        voiceModel: "m-1",
        voiceAdvanced: '{"a": 1}',
      }));
      expect(patch).toEqual({});
    });
  });
});
