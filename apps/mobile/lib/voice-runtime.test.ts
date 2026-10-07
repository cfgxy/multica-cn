import { describe, expect, it } from "vitest";
import type { AgentRuntime, RuntimeProfile } from "@multica/core/types";

import {
  VOICE_INSTANCE_CREDENTIAL_KEY,
  credentialSaveFailureDetail,
  isVoiceProfile,
  isVoiceProtocolRuntime,
  parseAdvancedParams,
  readVoiceInstanceSettings,
} from "./voice-runtime";

/**
 * Mirror of the desktop suites (packages/views/runtimes/components/
 * voice-instance-settings.test.tsx + voice-instance-create-dialog.test.tsx):
 * the mobile helper is a semantic re-implementation of the desktop functions
 * (views is outside the mobile import whitelist), so the desktop cases are
 * the source of truth these tests pin against.
 */

const voiceRuntime = (overrides: Partial<AgentRuntime> = {}): AgentRuntime =>
  ({
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Gemini Live",
    runtime_mode: "local",
    provider: "gemini_live",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: null,
    visibility: "public",
    last_seen_at: null,
    created_at: "",
    updated_at: "",
    ...overrides,
  }) as AgentRuntime;

const voiceProfile = (overrides: Partial<RuntimeProfile> = {}): RuntimeProfile =>
  ({
    id: "prof-1",
    workspace_id: "ws-1",
    display_name: "Gemini Live",
    protocol_family: "gemini_live",
    command_name: "",
    description: null,
    fixed_args: [],
    visibility: "workspace",
    created_by: null,
    enabled: true,
    created_at: "",
    updated_at: "",
    ...overrides,
  }) as RuntimeProfile;

describe("isVoiceProtocolRuntime", () => {
  it("accepts a runtime whose capabilities advertise realtime_voice", () => {
    const runtime = voiceRuntime({
      capabilities: { text: false, realtime_voice: true, tools: false },
    });
    expect(isVoiceProtocolRuntime(runtime)).toBe(true);
  });

  it("rejects text-only instances (older backend / CLI instance)", () => {
    const runtime = voiceRuntime({
      capabilities: { text: true, realtime_voice: false, tools: true },
    });
    expect(isVoiceProtocolRuntime(runtime)).toBe(false);
  });

  it("treats a missing capabilities block as not voice", () => {
    expect(isVoiceProtocolRuntime(voiceRuntime())).toBe(false);
  });
});

describe("isVoiceProfile", () => {
  it("accepts a profile advertising realtime_voice", () => {
    expect(
      isVoiceProfile(
        voiceProfile({
          capabilities: { text: false, realtime_voice: true, tools: false },
        }),
      ),
    ).toBe(true);
  });

  it("rejects CLI profiles and capability-less profiles", () => {
    expect(
      isVoiceProfile(
        voiceProfile({
          capabilities: { text: true, realtime_voice: false, tools: true },
        }),
      ),
    ).toBe(false);
    expect(isVoiceProfile(voiceProfile())).toBe(false);
  });
});

describe("readVoiceInstanceSettings", () => {
  it("reads model / advanced / disabled out of the metadata bag", () => {
    const settings = readVoiceInstanceSettings({
      model: "gemini-3.8-live",
      advanced: { temperature: 0.7 },
      disabled: true,
      unrelated: "kept-out",
    });
    expect(settings).toEqual({
      model: "gemini-3.8-live",
      advanced: { temperature: 0.7 },
      disabled: true,
    });
  });

  it("defaults missing metadata to empty settings", () => {
    expect(readVoiceInstanceSettings(null)).toEqual({
      model: "",
      advanced: null,
      disabled: false,
    });
    expect(readVoiceInstanceSettings(undefined)).toEqual({
      model: "",
      advanced: null,
      disabled: false,
    });
    expect(readVoiceInstanceSettings({})).toEqual({
      model: "",
      advanced: null,
      disabled: false,
    });
  });

  it("fails closed on wrong-typed values (desktop parity)", () => {
    expect(readVoiceInstanceSettings({ model: 42 })).toMatchObject({ model: "" });
    expect(readVoiceInstanceSettings({ advanced: [1, 2] })).toMatchObject({
      advanced: null,
    });
    expect(readVoiceInstanceSettings({ advanced: "nope" })).toMatchObject({
      advanced: null,
    });
    // Only an explicit true means disabled — desktop: `bag.disabled === true`.
    expect(readVoiceInstanceSettings({ disabled: "yes" })).toMatchObject({
      disabled: false,
    });
  });
});

describe("parseAdvancedParams", () => {
  it("accepts empty text as a cleared object", () => {
    expect(parseAdvancedParams("")).toEqual({ ok: true, value: {} });
    expect(parseAdvancedParams("   \n  ")).toEqual({ ok: true, value: {} });
  });

  it("parses a JSON object", () => {
    expect(parseAdvancedParams('{"temperature":0.7}')).toEqual({
      ok: true,
      value: { temperature: 0.7 },
    });
  });

  it("rejects invalid JSON", () => {
    expect(parseAdvancedParams("{temperature:0.7}")).toEqual({
      ok: false,
      reason: "invalid_json",
    });
  });

  it("rejects scalars and arrays — JSON-object only (fail closed)", () => {
    expect(parseAdvancedParams("42")).toEqual({ ok: false, reason: "not_object" });
    expect(parseAdvancedParams('"text"')).toEqual({
      ok: false,
      reason: "not_object",
    });
    expect(parseAdvancedParams("[1,2]")).toEqual({
      ok: false,
      reason: "not_object",
    });
    expect(parseAdvancedParams("null")).toEqual({
      ok: false,
      reason: "not_object",
    });
  });
});

describe("VOICE_INSTANCE_CREDENTIAL_KEY", () => {
  // Mirrors the Go handler tests' credential key; a drift here silently
  // stores the key under the wrong slot.
  it("stays api_key", () => {
    expect(VOICE_INSTANCE_CREDENTIAL_KEY).toBe("api_key");
  });
});

describe("credentialSaveFailureDetail", () => {
  const SERVER_503_MESSAGE =
    "runtime credential encryption is not configured on this server (MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY missing)";

  it("surfaces the server's readable message (never a bare status code)", () => {
    expect(credentialSaveFailureDetail(new Error(SERVER_503_MESSAGE))).toBe(
      SERVER_503_MESSAGE,
    );
  });

  it("trims surrounding whitespace but keeps the message intact", () => {
    expect(credentialSaveFailureDetail(new Error("  502 Bad Gateway  "))).toBe(
      "502 Bad Gateway",
    );
  });

  it("returns null for non-errors so callers fall back to localized copy", () => {
    expect(credentialSaveFailureDetail(null)).toBeNull();
    expect(credentialSaveFailureDetail(undefined)).toBeNull();
    expect(credentialSaveFailureDetail("503")).toBeNull();
    expect(credentialSaveFailureDetail({ status: 503 })).toBeNull();
  });

  it("returns null for blank-message errors", () => {
    expect(credentialSaveFailureDetail(new Error("   "))).toBeNull();
  });
});
