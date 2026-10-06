/**
 * Handshake-reason probe tests (RUYI-449 P2). The probe recovers the
 * gateway's precise rejection from the plain-HTTP response of the same URL
 * after a websocket handshake failure; every probe failure must collapse to
 * null so the UI keeps the generic degrade copy.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { probeHandshakeRejection, type FetchLike } from "./handshake-reason";

function fetchJson(status: number, body: unknown): FetchLike & { mock: any } {
  const fn = vi.fn(async () => new Response(JSON.stringify(body), { status }));
  return fn as unknown as FetchLike & { mock: any };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("probeHandshakeRejection", () => {
  it.each([
    "no_voice_runtime",
    "instance_disabled",
    "instance_not_active",
    "capability_mismatch",
    "credential_missing",
    "credential_invalid",
  ] as const)("reads the precise reason for %s", async (reason) => {
    const f = fetchJson(409, {
      code: `VOICE_UNAVAILABLE:${reason}`,
      error: "detail",
    });
    await expect(
      probeHandshakeRejection("wss://gw/api/agents/a/voice-session", f),
    ).resolves.toEqual({ kind: "voice_unavailable", reason });
  });

  it("probes the same URL over https with cookies and no token in the URL", async () => {
    const f = fetchJson(409, { code: "VOICE_UNAVAILABLE:no_voice_runtime" });
    await probeHandshakeRejection("wss://gw/api/agents/a/voice-session", f);
    expect(f.mock.calls[0]?.[0]).toBe("https://gw/api/agents/a/voice-session");
    expect(f.mock.calls[0]?.[0]).not.toContain("token");
    expect(f.mock.calls[0]?.[1]).toMatchObject({ method: "GET", credentials: "include" });
  });

  it("maps unknown gateway reasons to rejected (generic copy upstream)", async () => {
    const f = fetchJson(409, { code: "VOICE_UNAVAILABLE:some_future_reason" });
    await expect(
      probeHandshakeRejection("ws://gw/api/agents/a/voice-session", f),
    ).resolves.toEqual({ kind: "rejected" });
  });

  it("parses gate bodies on any status, not just 409", async () => {
    const f = fetchJson(401, { code: "VOICE_AUTH_FAILED" });
    await expect(
      probeHandshakeRejection("ws://gw/api/agents/a/voice-session", f),
    ).resolves.toEqual({ kind: "auth_failed" });
  });

  it("falls back to null on unparsable or codeless bodies", async () => {
    const text = vi.fn(async () => new Response("Bad Gateway", { status: 409 }));
    await expect(
      probeHandshakeRejection("ws://gw/x", text as unknown as FetchLike),
    ).resolves.toBeNull();
    const nocode = fetchJson(409, { error: "no machine code here" });
    await expect(probeHandshakeRejection("ws://gw/x", nocode)).resolves.toBeNull();
  });

  it("falls back to null when the probe itself fails", async () => {
    const f = vi.fn(async () => {
      throw new TypeError("network down");
    });
    await expect(
      probeHandshakeRejection("ws://gw/x", f as unknown as FetchLike),
    ).resolves.toBeNull();
  });
});
