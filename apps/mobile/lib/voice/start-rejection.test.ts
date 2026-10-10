/**
 * RUYI-626 — REST session-start rejection mapping. The §4.4 gate answers a
 * rejected direct-session start with a 409 whose body carries the stable
 * `VOICE_UNAVAILABLE:<reason>` code; the voice overlay must show that
 * specific degrade copy, while any other failure (network, 401, 5xx, other
 * codes) falls back to the generic message and never leaks auth internals.
 */
import { voiceRejectionFromApiError } from "./start-rejection";

function apiError(status: number, body: unknown): Error {
  return Object.assign(new Error(`${status}`), { status, body });
}

describe("voiceRejectionFromApiError", () => {
  it("maps the gate's VOICE_UNAVAILABLE:<reason> to the specific degrade", () => {
    const rejection = voiceRejectionFromApiError(
      apiError(409, { code: "VOICE_UNAVAILABLE:credential_invalid" }),
    );
    expect(rejection).toEqual({
      kind: "voice_unavailable",
      reason: "credential_invalid",
    });
  });

  it("maps an unknown VOICE_UNAVAILABLE reason to the generic rejected kind", () => {
    const rejection = voiceRejectionFromApiError(
      apiError(409, { code: "VOICE_UNAVAILABLE:some_future_reason" }),
    );
    expect(rejection).toEqual({ kind: "rejected" });
  });

  it("returns null for non-gate failures — the copy stays generic", () => {
    expect(voiceRejectionFromApiError(apiError(401, { code: "AUTH_FAILED" }))).toBeNull();
    expect(voiceRejectionFromApiError(apiError(500, { message: "boom" }))).toBeNull();
    expect(voiceRejectionFromApiError(new Error("network down"))).toBeNull();
    expect(voiceRejectionFromApiError(null)).toBeNull();
  });
});
