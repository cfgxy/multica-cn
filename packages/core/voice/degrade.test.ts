import { parseVoiceRejectionCode, voiceFailureMessageKey } from "./degrade";

describe("parseVoiceRejectionCode", () => {
  it.each([
    "no_voice_runtime",
    "instance_disabled",
    "instance_not_active",
    "capability_mismatch",
    "credential_missing",
    "credential_invalid",
  ] as const)("maps VOICE_UNAVAILABLE:%s to the specific reason", (reason) => {
    expect(parseVoiceRejectionCode(`VOICE_UNAVAILABLE:${reason}`)).toEqual({
      kind: "voice_unavailable",
      reason,
    });
  });

  it("maps the auxiliary gateway codes", () => {
    expect(parseVoiceRejectionCode("VOICE_AUTH_FAILED")).toEqual({ kind: "auth_failed" });
    expect(parseVoiceRejectionCode("VOICE_PROVIDER_UNREACHABLE")).toEqual({
      kind: "provider_unreachable",
    });
    expect(parseVoiceRejectionCode("VOICE_SESSION_REJECTED")).toEqual({ kind: "rejected" });
  });

  it("returns null for unknown codes and connection drops", () => {
    expect(parseVoiceRejectionCode(null)).toBeNull();
    expect(parseVoiceRejectionCode("")).toBeNull();
    expect(parseVoiceRejectionCode("VOICE_UNAVAILABLE:something_new")).toEqual({
      kind: "rejected",
    });
    expect(parseVoiceRejectionCode("whatever")).toBeNull();
  });
});

describe("voiceFailureMessageKey", () => {
  it("maps each gate reason to its own key", () => {
    expect(
      voiceFailureMessageKey({ kind: "voice_unavailable", reason: "credential_invalid" }),
    ).toBe("voice.failure.unavailable.credential_invalid");
    expect(
      voiceFailureMessageKey({ kind: "voice_unavailable", reason: "instance_disabled" }),
    ).toBe("voice.failure.unavailable.instance_disabled");
  });

  it("collapses auth internals into the generic key (no auth detail in copy)", () => {
    expect(voiceFailureMessageKey({ kind: "auth_failed" })).toBe(
      "voice.failure.connection_failed",
    );
    expect(voiceFailureMessageKey(null)).toBe("voice.failure.connection_failed");
  });
});
