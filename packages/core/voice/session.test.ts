import { describe, expect, it, vi } from "vitest";
import { parseVoiceRejectionCode } from "./degrade";
import {
  VoiceSessionController,
  type VoiceSessionCallbacks,
  type VoiceTransport,
} from "./session";

/** Scriptable in-memory transport: records frames, replays scripted gateway frames. */
function fakeTransport() {
  const sent: string[] = [];
  let opened = false;
  let openResolve: (() => void) | null = null;
  const t: VoiceTransport = {
    onFrame: null,
    onClose: null,
    connect: vi.fn(
      () =>
        new Promise<void>((resolve) => {
          if (opened) resolve();
          else openResolve = resolve;
        }),
    ),
    send: vi.fn((data: string) => { sent.push(data); }),
    close: vi.fn(() => { t.onClose?.(1000); }),
  };
  return {
    transport: t,
    sent,
    open() {
      opened = true;
      const resolve = openResolve;
      openResolve = null;
      resolve?.();
    },
    emit(frame: unknown) { t.onFrame?.(typeof frame === "string" ? frame : JSON.stringify(frame)); },
    drop(code = 1006) { t.onClose?.(code); },
  };
}

function controller(io: ReturnType<typeof fakeTransport>, overrides?: {
  authToken?: string | null;
  callbacks?: Partial<VoiceSessionCallbacks>;
}) {
  const events: {
    states: string[];
    userDeltas: string[];
    assistantDeltas: string[];
    userTurns: string[];
    audio: string[];
    degraded: (ReturnType<typeof parseVoiceRejectionCode> | null)[];
    ended: number;
  } = {
    states: [], userDeltas: [], assistantDeltas: [], userTurns: [],
    audio: [], degraded: [], ended: 0,
  };
  const c = new VoiceSessionController({
    url: "ws://gateway/api/agents/a/voice-session",
    authToken: overrides?.authToken ?? null,
    transport: io.transport,
    callbacks: {
      onStateChange: (s) => { events.states.push(s); },
      onUserTranscriptDelta: (d) => { events.userDeltas.push(d); },
      onAssistantTranscriptDelta: (d) => { events.assistantDeltas.push(d); },
      onUserTurn: (text) => { events.userTurns.push(text); },
      onAudio: (mime, data) => { events.audio.push(`${mime}:${data}`); },
      onDegrade: (r) => { events.degraded.push(r); },
      onEnded: () => { events.ended += 1; },
      ...overrides?.callbacks,
    },
  });
  return { c, events };
}

describe("VoiceSessionController", () => {
  it("goes live on setupComplete and relays audio upstream", async () => {
    const io = fakeTransport();
    const { c, events } = controller(io);
    const started = c.start();
    io.open();
    await started;
    expect(io.sent).toEqual([]);

    io.emit({ setupComplete: {} });
    expect(c.getState()).toBe("live");
    expect(events.states).toEqual(["connecting", "live"]);

    c.sendAudio("QUJD");
    expect(io.sent).toEqual([
      `{"realtimeInput":{"audio":{"data":"QUJD","mimeType":"audio/pcm;rate=16000"}}}`,
    ]);
  });

  it("sends the auth frame first on the token path, never in the URL", async () => {
    const io = fakeTransport();
    const { c } = controller(io, { authToken: "secret-token" });
    const started = c.start();
    io.open();
    await started;
    expect((io.transport.connect as ReturnType<typeof vi.fn>).mock.calls[0]?.[0]).not.toContain("secret-token");
    expect(JSON.parse(io.sent[0] ?? "")).toEqual({ type: "auth", payload: { token: "secret-token" } });
  });

  it("maps the gateway rejection frame to a degrade", async () => {
    const io = fakeTransport();
    const { c, events } = controller(io);
    io.open();
    await c.start();
    io.emit({ error: "no voice runtime is bound", code: "VOICE_UNAVAILABLE:no_voice_runtime" });
    expect(c.getState()).toBe("failed");
    expect(events.degraded).toEqual([{ kind: "voice_unavailable", reason: "no_voice_runtime" }]);
    expect(events.ended).toBe(1);
  });

  it("degrades a drop before setupComplete", async () => {
    const io = fakeTransport();
    const { c, events } = controller(io);
    io.open();
    await c.start();
    io.drop();
    expect(c.getState()).toBe("failed");
    expect(events.degraded).toEqual([null]);
  });

  it("flushes the pending user turn when the model starts answering", async () => {
    const io = fakeTransport();
    const { c, events } = controller(io);
    io.open();
    await c.start();
    io.emit({ setupComplete: {} });
    io.emit({ serverContent: { inputTranscription: { text: "帮我" } } });
    io.emit({ serverContent: { inputTranscription: { text: "建一个任务" } } });
    expect(events.userTurns).toEqual([]);
    io.emit({ serverContent: { modelTurn: { parts: [{ inlineData: { mimeType: "audio/pcm;rate=24000", data: "QQ" } }] } } });
    expect(events.userTurns).toEqual(["帮我建一个任务"]);
    expect(events.audio).toEqual(["audio/pcm;rate=24000:QQ"]);
  });

  it("barges in: interrupted clears the assistant buffer", async () => {
    const io = fakeTransport();
    const { c, events } = controller(io);
    io.open();
    await c.start();
    io.emit({ setupComplete: {} });
    io.emit({ serverContent: { outputTranscription: { text: "部分回答" } } });
    io.emit({ serverContent: { interrupted: true } });
    io.emit({ serverContent: { turnComplete: true } });
    expect(events.assistantDeltas).toEqual(["部分回答"]);
    expect(c.getState()).toBe("live");
  });

  it("ignores audio before live and after end", async () => {
    const io = fakeTransport();
    const { c } = controller(io);
    io.open();
    await c.start();
    c.sendAudio("early");
    expect(io.sent).toEqual([]);

    io.emit({ setupComplete: {} });
    c.end();
    expect(c.getState()).toBe("ended");
    c.sendAudio("late");
    expect(io.sent.filter((f) => f.includes("late"))).toEqual([]);
  });

  it("ends cleanly without a degrade", async () => {
    const io = fakeTransport();
    const { c, events } = controller(io);
    io.open();
    await c.start();
    io.emit({ setupComplete: {} });
    c.end();
    expect(c.isDegraded()).toBe(false);
    expect(events.degraded).toEqual([]);
    expect(events.ended).toBe(1);
  });
});
