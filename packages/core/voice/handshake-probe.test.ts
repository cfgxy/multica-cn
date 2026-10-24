// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  probeVoiceHandshakeRejection,
  VOICE_HANDSHAKE_PROBE_TIMEOUT_MS,
  type ProbeVoiceHandshakeOptions,
} from "./handshake-probe";

type FetchLike = NonNullable<ProbeVoiceHandshakeOptions["fetchImpl"]>;

function fetchJson(status: number, body: unknown) {
  return vi.fn(
    async () => new Response(JSON.stringify(body), { status }),
  ) as unknown as FetchLike & { mock: { calls: unknown[][] } };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("probeVoiceHandshakeRejection", () => {
  it.each([
    // Legacy runtime gates
    "no_voice_runtime",
    "instance_disabled",
    "instance_not_active",
    "capability_mismatch",
    "credential_missing",
    "credential_invalid",
    // D2 config-gate reasons (RUYI-866 P1-3): the same stable codes the
    // gateway emits on its 409 / error frames.
    "voice_unconfigured",
    "voice_provider_missing",
    "voice_provider_disabled",
  ] as const)("reads the precise reason for %s", async (reason) => {
    const f = fetchJson(409, { code: `VOICE_UNAVAILABLE:${reason}`, error: "detail" });
    await expect(
      probeVoiceHandshakeRejection("wss://gw/api/agents/a/voice-session", { fetchImpl: f }),
    ).resolves.toEqual({ kind: "voice_unavailable", reason });
  });

  it("probes the same URL over https with cookies and no token in the URL", async () => {
    const f = fetchJson(409, { code: "VOICE_UNAVAILABLE:voice_unconfigured" });
    await probeVoiceHandshakeRejection("wss://gw/api/agents/a/voice-session", {
      fetchImpl: f,
    });
    expect(f.mock.calls[0]?.[0]).toBe("https://gw/api/agents/a/voice-session");
    expect(f.mock.calls[0]?.[0]).not.toContain("token");
    expect(f.mock.calls[0]?.[1]).toMatchObject({
      method: "GET",
      credentials: "include",
    });
  });

  it("adds the bearer header only when a token exists", async () => {
    const withToken = fetchJson(409, { code: "VOICE_UNAVAILABLE:voice_unconfigured" });
    await probeVoiceHandshakeRejection("wss://gw/x", {
      fetchImpl: withToken,
      authorizationHeader: "Bearer t-1",
    });
    expect(withToken.mock.calls[0]?.[1]).toMatchObject({
      headers: { Authorization: "Bearer t-1" },
    });
    const noToken = fetchJson(409, { code: "VOICE_UNAVAILABLE:voice_unconfigured" });
    await probeVoiceHandshakeRejection("ws://gw/x", { fetchImpl: noToken });
    expect((noToken.mock.calls[0]?.[1] as RequestInit | undefined)?.headers).toBeUndefined();
  });

  it("maps unknown gateway reasons to rejected (generic copy upstream)", async () => {
    const f = fetchJson(409, { code: "VOICE_UNAVAILABLE:some_future_reason" });
    await expect(
      probeVoiceHandshakeRejection("ws://gw/api/agents/a/voice-session", { fetchImpl: f }),
    ).resolves.toEqual({ kind: "rejected" });
  });

  it("parses gate bodies on any status, not just 409", async () => {
    const f = fetchJson(401, { code: "VOICE_AUTH_FAILED" });
    await expect(
      probeVoiceHandshakeRejection("ws://gw/api/agents/a/voice-session", { fetchImpl: f }),
    ).resolves.toEqual({ kind: "auth_failed" });
  });

  it("falls back to null on unparsable or codeless bodies", async () => {
    const text = vi.fn(async () => new Response("Bad Gateway", { status: 409 }));
    await expect(
      probeVoiceHandshakeRejection("ws://gw/x", { fetchImpl: text as unknown as FetchLike }),
    ).resolves.toBeNull();
    const nocode = fetchJson(409, { error: "no machine code here" });
    await expect(
      probeVoiceHandshakeRejection("ws://gw/x", { fetchImpl: nocode }),
    ).resolves.toBeNull();
  });

  it("falls back to null when the probe itself fails (CORS / network)", async () => {
    const f = vi.fn(async () => {
      throw new TypeError("blocked by CORS");
    });
    await expect(
      probeVoiceHandshakeRejection("ws://gw/x", { fetchImpl: f as unknown as FetchLike }),
    ).resolves.toBeNull();
  });

  it("aborts the probe after the timeout budget", async () => {
    const late = vi.fn(((_url: string, init?: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => {
          reject(new DOMException("Aborted", "AbortError"));
        });
      }))) as unknown as FetchLike;
    const started = probeVoiceHandshakeRejection("ws://gw/x", {
      fetchImpl: late,
      timeoutMs: 10,
    });
    await expect(started).resolves.toBeNull();
    expect(VOICE_HANDSHAKE_PROBE_TIMEOUT_MS).toBe(3000);
  });
});
