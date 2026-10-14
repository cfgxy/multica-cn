/**
 * RUYI-694 P1 rework — muted upstream keepalive. QA round 3: a muted
 * session died at ~2-3min because the mic-entry drop meant the gateway and
 * provider saw zero realtimeInput frames. Pins the contract: muted swaps
 * real mic chunks for a fixed silent PCM keepalive frame (the session stays
 * live upstream, real audio never leaves the device, playback is
 * unaffected); unmuted forwards chunks verbatim.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MobileVoiceSession } from "./session";

interface UpstreamAudio {
  data: string;
  mimeType: string;
}

interface FakeTransport {
  sent: string[];
  onFrame: ((data: string) => void) | null;
  onClose: ((code: number) => void) | null;
}

vi.mock("@/data/server-store", () => ({
  getApiUrl: () => "https://api.test",
  useServerStore: { getState: () => ({ activeServerId: "srv-1" }) },
}));

vi.mock("@/data/secure-storage", () => ({
  getToken: async () => "token-123",
}));

vi.mock("./native-audio", () => {
  const state: { chunkListener: ((e: { data: string }) => void) | null } = {
    chunkListener: null,
  };
  const audio = {
    requestPermissionsAsync: async () => ({ granted: true }),
    start: async () => null,
    stop: async () => null,
    startPlayback: async () => null,
    playChunk: vi.fn(async () => null),
    interruptPlayback: async () => null,
    stopPlayback: async () => null,
    addListener: (_event: string, cb: (e: { data: string }) => void) => {
      state.chunkListener = cb;
      return {
        remove: () => {
          state.chunkListener = null;
        },
      };
    },
  };
  return { getVoiceAudioModule: () => audio, __testAudio: { state, audio } };
});

vi.mock("./session-gateway", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./session-gateway")>();
  const transports: FakeTransport[] = [];
  class FakeTransport implements FakeTransport {
    sent: string[] = [];
    onFrame: ((data: string) => void) | null = null;
    onClose: ((code: number) => void) | null = null;
    constructor() {
      transports.push(this);
    }
    connect(): Promise<void> {
      return Promise.resolve();
    }
    send(data: string): void {
      this.sent.push(data);
    }
    close(): void {}
  }
  return {
    ...actual,
    MobileVoiceTransport: FakeTransport,
    __testTransports: transports,
  };
});

type SessionGatewayModule = typeof import("./session-gateway") & {
  __testTransports: FakeTransport[];
};
type NativeAudioModule = typeof import("./native-audio") & {
  __testAudio: {
    state: { chunkListener: ((e: { data: string }) => void) | null };
    audio: { playChunk: ReturnType<typeof vi.fn> };
  };
};

async function startLiveSession(): Promise<{
  session: MobileVoiceSession;
  transport: FakeTransport;
}> {
  const session = new MobileVoiceSession("agent-1", "ws-slug", {});
  await session.start();
  const { __testTransports } = (await import(
    "./session-gateway"
  )) as SessionGatewayModule;
  const transport = __testTransports[__testTransports.length - 1];
  transport.onFrame?.(JSON.stringify({ setupComplete: {} }));
  return { session, transport };
}

async function emitMicChunk(data: string): Promise<void> {
  const { __testAudio } = (await import("./native-audio")) as NativeAudioModule;
  __testAudio.state.chunkListener?.({ data });
}

function upstreamAudioFrames(transport: FakeTransport): UpstreamAudio[] {
  return transport.sent
    .filter((raw) => raw.includes("realtimeInput"))
    .map(
      (raw) =>
        JSON.parse(raw).realtimeInput.audio as UpstreamAudio,
    );
}

describe("MobileVoiceSession muted upstream keepalive (RUYI-694 P1)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("keeps the upstream alive with silent frames while muted", async () => {
    const { session, transport } = await startLiveSession();
    session.setMuted(true);

    await emitMicChunk("cmVhbC1hdWRpbw==");
    await emitMicChunk("Y2h1bmstdHdv");

    const frames = upstreamAudioFrames(transport);
    expect(frames).toHaveLength(2);
    for (const frame of frames) {
      expect(frame.mimeType).toBe("audio/pcm;rate=16000");
      // 静音保活帧是 100ms 16 kHz PCM16 全零采样——不是透传的真实音频。
      const decoded = Buffer.from(frame.data, "base64");
      expect(decoded.length).toBe(3200);
      expect([...decoded].every((b) => b === 0)).toBe(true);
    }
    expect(frames[0].data).not.toContain("cmVhbC1hdWRpbw==");
    expect(frames[1].data).not.toContain("Y2h1bmstdHdv");
  });

  it("forwards real mic chunks verbatim when not muted", async () => {
    const { session, transport } = await startLiveSession();

    await emitMicChunk("cmVhbC1hdWRpbw==");

    const frames = upstreamAudioFrames(transport);
    expect(frames).toHaveLength(1);
    expect(frames[0].data).toBe("cmVhbC1hdWRpbw==");
  });

  it("restores verbatim forwarding after unmute", async () => {
    const { session, transport } = await startLiveSession();
    session.setMuted(true);
    await emitMicChunk("bXV0ZWQtMQ==");
    session.setMuted(false);
    await emitMicChunk("bGl2ZS0y");

    const frames = upstreamAudioFrames(transport);
    expect(frames).toHaveLength(2);
    expect(Buffer.from(frames[0].data, "base64").every((b) => b === 0)).toBe(
      true,
    );
    expect(frames[1].data).toBe("bGl2ZS0y");
  });

  it("keeps downstream playback unaffected while muted", async () => {
    const { session, transport } = await startLiveSession();
    session.setMuted(true);

    transport.onFrame?.(
      JSON.stringify({
        serverContent: {
          modelTurn: {
            parts: [
              {
                inlineData: {
                  data: "cGxheQ==",
                  mimeType: "audio/pcm;rate=24000",
                },
              },
            ],
          },
        },
      }),
    );

    const { __testAudio } = (await import("./native-audio")) as NativeAudioModule;
    expect(__testAudio.audio.playChunk).toHaveBeenCalledWith("cGxheQ==");
  });
});
