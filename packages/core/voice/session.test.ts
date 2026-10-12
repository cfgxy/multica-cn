import { describe, expect, it, vi } from "vitest";
import { parseVoiceRejectionCode, VoiceRejectionError } from "./degrade";
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
  let openReject: ((error: Error) => void) | null = null;
  const t: VoiceTransport = {
    onFrame: null,
    onClose: null,
    connect: vi.fn(
      () =>
        new Promise<void>((resolve, reject) => {
          if (opened) resolve();
          else {
            openResolve = resolve;
            openReject = reject;
          }
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
      openReject = null;
      resolve?.();
    },
    emit(frame: unknown) { t.onFrame?.(typeof frame === "string" ? frame : JSON.stringify(frame)); },
    drop(code = 1006) { t.onClose?.(code); },
    refuse() {
      // The handshake itself failed (provider unreachable / rejected): the
      // connect promise rejects, matching the real transports' onclose
      // handling before the socket ever opened.
      const reject = openReject;
      openResolve = null;
      openReject = null;
      reject?.(new Error("connection refused"));
    },
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

  it("degrades with the precise reason when the transport fails with a typed rejection", async () => {
    const io = fakeTransport();
    // Mobile probes the handshake's HTTP 409 body and reports what it finds;
    // the controller must surface that instead of the generic message.
    (io.transport.connect as ReturnType<typeof vi.fn>).mockRejectedValue(
      new VoiceRejectionError({ kind: "voice_unavailable", reason: "no_voice_runtime" }),
    );
    const { c, events } = controller(io);
    await c.start();
    expect(c.getState()).toBe("failed");
    expect(events.degraded).toEqual([{ kind: "voice_unavailable", reason: "no_voice_runtime" }]);
    expect(events.ended).toBe(1);
  });

  it("keeps the generic degrade when connect fails with a plain error", async () => {
    const io = fakeTransport();
    (io.transport.connect as ReturnType<typeof vi.fn>).mockRejectedValue(
      new Error("voice websocket closed (1006)"),
    );
    const { c, events } = controller(io);
    await c.start();
    expect(c.getState()).toBe("failed");
    expect(events.degraded).toEqual([null]);
    expect(events.ended).toBe(1);
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

describe("VoiceSessionController direct mode (RUYI-626)", () => {
  function directController(io: ReturnType<typeof fakeTransport>, overrides?: {
    setup?: { instructions: string; model: string; advanced: Record<string, unknown> };
    callbacks?: Partial<VoiceSessionCallbacks>;
  }) {
    const events: {
      states: string[];
      degraded: (ReturnType<typeof parseVoiceRejectionCode> | null)[];
      ended: number;
    } = { states: [], degraded: [], ended: 0 };
    const c = new VoiceSessionController({
      url: "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent",
      mode: "direct",
      directSetup: overrides?.setup ?? {
        instructions: "be terse",
        model: "gemini-3.8-live",
        advanced: {},
      },
      transport: io.transport,
      callbacks: {
        onStateChange: (s) => { events.states.push(s); },
        onDegrade: (r) => { events.degraded.push(r); },
        onEnded: () => { events.ended += 1; },
        ...overrides?.callbacks,
      },
    });
    return { c, events };
  }

  it("sends the composed setup frame right after connect (no auth frame)", async () => {
    const io = fakeTransport();
    const { c } = directController(io);
    const started = c.start();
    io.open();
    await started;
    expect(io.sent).toHaveLength(1);
    const parsed = JSON.parse(io.sent[0] ?? "");
    expect(parsed.setup.model).toBe("models/gemini-3.8-live");
    expect(parsed.setup.systemInstruction).toEqual({ parts: [{ text: "be terse" }] });
    expect(parsed.setup.inputAudioTranscription).toEqual({});
    expect(parsed.setup.sessionResumption).toEqual({});

    io.emit({ setupComplete: {} });
    expect(c.getState()).toBe("live");
  });

  it("collects transcript entries and the resumption handle", async () => {
    const io = fakeTransport();
    const { c } = directController(io);
    io.open();
    await c.start();
    io.emit({ setupComplete: {} });
    io.emit({ sessionResumptionUpdate: { newHandle: "h1" } });
    io.emit({ serverContent: { inputTranscription: { text: "你好" } } });
    io.emit({ serverContent: { outputTranscription: { text: "hi" } } });
    io.emit({ serverContent: { outputTranscription: { text: " there" } } });

    c.end();
    const record = c.getSessionRecord();
    expect(record).not.toBeNull();
    expect(record?.sessionHandle).toBe("h1");
    expect(record?.transcript.map((e) => ({ role: e.role, text: e.text }))).toEqual([
      { role: "user", text: "你好" },
      { role: "assistant", text: "hi" },
      { role: "assistant", text: " there" },
    ]);
    for (const entry of record?.transcript ?? []) {
      expect(entry.at).toMatch(/^\d{4}-\d{2}-\d{2}T/);
    }
  });

  it("returns the partial record when the provider errors mid-session", async () => {
    const io = fakeTransport();
    const { c, events } = directController(io);
    io.open();
    await c.start();
    io.emit({ setupComplete: {} });
    io.emit({ serverContent: { inputTranscription: { text: "partial" } } });
    io.emit({ error: { message: "boom" } });

    expect(c.getState()).toBe("failed");
    expect(events.degraded).toHaveLength(1);
    expect(c.getSessionRecord()?.transcript).toEqual([
      expect.objectContaining({ role: "user", text: "partial" }),
    ]);
  });

  it("returns the collected record when the provider drops mid-session", async () => {
    const io = fakeTransport();
    const { c } = directController(io);
    io.open();
    await c.start();
    io.emit({ setupComplete: {} });
    io.emit({ serverContent: { inputTranscription: { text: "partial" } } });
    io.drop(1006);

    // A live-side drop is a normal end (no degrade), but the record still
    // carries what was transcribed before the disconnect.
    expect(c.getState()).toBe("ended");
    expect(c.getSessionRecord()?.transcript).toEqual([
      expect.objectContaining({ role: "user", text: "partial" }),
    ]);
  });

  it("returns an empty record when the provider never accepted the connection", async () => {
    const io = fakeTransport();
    const { c } = directController(io);
    const started = c.start();
    io.refuse();
    await started.catch(() => {});
    expect(c.getState()).toBe("failed");
    expect(c.getSessionRecord()).toEqual({ transcript: [], sessionHandle: "" });
  });

  it("gateway mode never collects a record", async () => {
    const io = fakeTransport();
    const { c } = controller(io);
    io.open();
    await c.start();
    io.emit({ setupComplete: {} });
    io.emit({ serverContent: { inputTranscription: { text: "x" } } });
    c.end();
    expect(c.getSessionRecord()).toBeNull();
  });

  it("ignores an unknown mode option and stays gateway-shaped", async () => {
    const io = fakeTransport();
    const events: string[] = [];
    const c = new VoiceSessionController({
      url: "ws://gateway/x",
      authToken: "tok-1",
      transport: io.transport,
      callbacks: { onStateChange: (s) => events.push(s) },
    });
    const started = c.start();
    io.open();
    await started;
    expect(io.sent).toEqual([
      JSON.stringify({ type: "auth", payload: { token: "tok-1" } }),
    ]);
  });
});
