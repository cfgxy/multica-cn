/**
 * RUYI-694 D3 — voice availability gate (板5 回退形态). Pins the dispatch
 * card's three-branch gate: missing/invalid fall back, unreachable (and a
 * never-run probe) keep the entry, and a re-probed pool entry re-derives
 * the verdict — the gate is a pure function of the current data, so there
 * is no verdict caching to invalidate.
 */
import { describe, expect, it } from "vitest";
import {
  resolveVoiceAvailability,
  type VoiceAvailabilityProvider,
} from "./availability";

function provider(overrides: Partial<VoiceAvailabilityProvider> = {}) {
  return {
    enabled: true,
    credential_status: "configured",
    credential_probe: { status: "ok" },
    ...overrides,
  };
}

describe("resolveVoiceAvailability", () => {
  it("falls back when neither chain level names a provider", () => {
    expect(
      resolveVoiceAvailability({ provider: provider() }),
    ).toEqual({ available: false, reason: "unconfigured" });
    expect(
      resolveVoiceAvailability({
        agentVoiceProviderId: "",
        workspaceDefaultProviderId: null,
        provider: provider(),
      }),
    ).toEqual({ available: false, reason: "unconfigured" });
  });

  it("falls back when the named provider is absent from the pool", () => {
    expect(
      resolveVoiceAvailability({
        workspaceDefaultProviderId: "vp-1",
        provider: null,
      }),
    ).toEqual({ available: false, reason: "provider_missing" });
  });

  it("falls back when the named provider is disabled", () => {
    expect(
      resolveVoiceAvailability({
        agentVoiceProviderId: "vp-1",
        provider: provider({ enabled: false }),
      }),
    ).toEqual({ available: false, reason: "provider_disabled" });
  });

  it("falls back when no usable credential is stored", () => {
    for (const status of ["not_configured", "missing"]) {
      expect(
        resolveVoiceAvailability({
          agentVoiceProviderId: "vp-1",
          provider: provider({ credential_status: status }),
        }),
      ).toEqual({ available: false, reason: "credential_missing" });
    }
  });

  it("falls back when the probe recorded the credential as invalid", () => {
    expect(
      resolveVoiceAvailability({
        agentVoiceProviderId: "vp-1",
        provider: provider({ credential_probe: { status: "invalid" } }),
      }),
    ).toEqual({ available: false, reason: "credential_invalid" });
    // Coarse badge carries invalid too (instance payloads may).
    expect(
      resolveVoiceAvailability({
        agentVoiceProviderId: "vp-1",
        provider: provider({ credential_status: "invalid" }),
      }),
    ).toEqual({ available: false, reason: "credential_invalid" });
  });

  it("keeps the entry when the probe is unreachable — a network verdict is not a credential verdict", () => {
    expect(
      resolveVoiceAvailability({
        agentVoiceProviderId: "vp-1",
        provider: provider({ credential_probe: { status: "unreachable" } }),
      }),
    ).toEqual({ available: true });
  });

  it("keeps the entry when the probe never ran", () => {
    expect(
      resolveVoiceAvailability({
        agentVoiceProviderId: "vp-1",
        provider: provider({ credential_probe: { status: "skipped" } }),
      }),
    ).toEqual({ available: true });
    expect(
      resolveVoiceAvailability({
        agentVoiceProviderId: "vp-1",
        provider: provider({ credential_probe: null }),
      }),
    ).toEqual({ available: true });
  });

  it("the agent override wins over the workspace default", () => {
    // Override resolves to a healthy provider even though the default
    // names a different one: the chain is level-wise, first hit decides.
    expect(
      resolveVoiceAvailability({
        agentVoiceProviderId: "vp-override",
        workspaceDefaultProviderId: "vp-default",
        provider: provider(),
      }),
    ).toEqual({ available: true });
    // A missing default does not shadow a healthy override either.
    expect(
      resolveVoiceAvailability({
        agentVoiceProviderId: "vp-override",
        workspaceDefaultProviderId: "vp-default",
        provider: null,
      }),
    ).toEqual({ available: false, reason: "provider_missing" });
  });

  it("re-derives from fresh data — 改参重新首次 is just new inputs", () => {
    const before = resolveVoiceAvailability({
      agentVoiceProviderId: "vp-1",
      provider: provider({ credential_probe: { status: "invalid" } }),
    });
    expect(before).toEqual({ available: false, reason: "credential_invalid" });
    // Admin rotated the key; 保存即探针 flipped the badge; the pool
    // refetch feeds the new entry → the mic reappears with no extra API.
    const after = resolveVoiceAvailability({
      agentVoiceProviderId: "vp-1",
      provider: provider({ credential_probe: { status: "ok" } }),
    });
    expect(after).toEqual({ available: true });
  });
});
