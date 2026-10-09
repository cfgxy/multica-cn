/**
 * RUYI-626 — device-side credential probe status mapping. Pins the RUYI-619
 * boundary the direct path lives on: a network verdict is not a credential
 * verdict. 2xx is the only "ok"; 4xx means the provider saw and rejected
 * the key ("invalid"); network failures and 5xx stay "unreachable" and
 * never gate a session start.
 */
import { probeVoiceCredential } from "./probe";

function fetchStatus(status: number) {
  return (async () => ({ status })) as unknown as typeof fetch;
}

describe("probeVoiceCredential", () => {
  it("maps 2xx to ok with the HTTP status", async () => {
    const outcome = await probeVoiceCredential("k", fetchStatus(200));
    expect(outcome).toEqual({ status: "ok", httpStatus: 200 });
  });

  it("maps 4xx to invalid — the provider rejected the key", async () => {
    for (const status of [400, 401, 403]) {
      const outcome = await probeVoiceCredential("k", fetchStatus(status));
      expect(outcome).toEqual({ status: "invalid", httpStatus: status });
    }
  });

  it("maps 5xx to unreachable — a server-side verdict is not a key verdict", async () => {
    const outcome = await probeVoiceCredential("k", fetchStatus(503));
    expect(outcome).toEqual({ status: "unreachable", httpStatus: 503 });
  });

  it("maps network failures to unreachable", async () => {
    const outcome = await probeVoiceCredential("k", (async () => {
      throw new Error("network down");
    }) as unknown as typeof fetch);
    expect(outcome).toEqual({ status: "unreachable" });
  });

  it("sends the key in the x-goog-api-key header, never the URL", async () => {
    let seenUrl = "";
    let seenHeaders: Record<string, string> | undefined;
    const fetchImpl = (async (url: string | URL, init?: RequestInit) => {
      seenUrl = String(url);
      seenHeaders = init?.headers as Record<string, string>;
      return { status: 200 };
    }) as unknown as typeof fetch;
    await probeVoiceCredential("secret-key", fetchImpl);
    expect(seenUrl).toBe("https://generativelanguage.googleapis.com/v1beta/models");
    expect(seenUrl).not.toContain("secret-key");
    expect(seenHeaders?.["x-goog-api-key"]).toBe("secret-key");
  });
});
