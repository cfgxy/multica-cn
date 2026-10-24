/**
 * useVoiceSession lifecycle + degrade tests (RUYI-866 P1).
 *
 * Canonical layer for the three regression classes QA flagged on the
 * shipped candidate:
 *  1. Cross-talk — a dismissed, agent-switched, or unmounted session must
 *     abort its owned socket and capture; late handshakes, late frames, and
 *     late mic grants must not write into the surviving UI state.
 *  2. Permission persistence — a denied mic must keep the visible
 *     `failed + permission` overlay until the user dismisses; the session
 *     must abort (not end) so the terminal transition cannot unmount it.
 *  3. Gate-reason recovery — a bare handshake drop on the cookie/token path
 *     must be refined by the plain-HTTP probe into the D2 config-gate
 *     reason; a failed probe keeps the generic copy.
 *
 * jsdom provides neither a real WebSocket nor getUserMedia, so both are
 * stubbed with scriptable fakes; the probe's fetch is a vi.fn.
 */
import { beforeEach, describe, expect, it, vi, afterEach } from "vitest";
import { act, render, renderHook, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enVoice from "../locales/en/voice.json";
import { useVoiceSession } from "./use-voice-session";
import { VoiceOverlay } from "./voice-overlay";

vi.mock("@multica/core/api", () => ({
  api: {},
  getApi: () => ({ getBaseUrl: () => "http://localhost:3000" }),
  setApiInstance: vi.fn(),
}));

const OVERLAY_RESOURCES = { en: { voice: enVoice } };

/** Flush pending microtasks (promise continuations in the hook) inside act. */
const flush = async (ticks = 8) => {
  await act(async () => {
    for (let i = 0; i < ticks; i++) await Promise.resolve();
  });
};

/* ------------------------------ socket fake ------------------------------ */

class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  /** Every socket the fake has created, newest last — sessions index in. */
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readyState = 0;
  onopen: (() => void) | null = null;
  onclose: ((event: { code: number }) => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onerror: (() => void) | null = null;
  /** True when close() hit a socket that was still handshaking. */
  closedWhileConnecting = false;

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }

  open() {
    if (this.readyState !== 0) return;
    this.readyState = 1;
    this.onopen?.();
  }

  /** Mirrors the browser: a close() during the handshake reports 1006. */
  close(code = 1000) {
    if (this.readyState === 3) return;
    if (this.readyState === 0) this.closedWhileConnecting = true;
    this.readyState = 3;
    this.onclose?.({ code: this.closedWhileConnecting ? 1006 : code });
  }

  emit(data: unknown) {
    this.onmessage?.({ data: typeof data === "string" ? data : JSON.stringify(data) });
  }

  send() {}
}

const lastSocket = () =>
  FakeWebSocket.instances[FakeWebSocket.instances.length - 1] as FakeWebSocket;

function setupSocketFake() {
  vi.stubGlobal("WebSocket", FakeWebSocket);
}

/* ------------------------------ media fake ------------------------------ */

type FakeTrack = { stopped: boolean; stop(): void };
type FakeStream = { getTracks(): FakeTrack[] };

function makeStream(): FakeStream {
  const tracks: FakeTrack[] = [
    {
      stopped: false,
      stop() {
        this.stopped = true;
      },
    },
  ];
  return { getTracks: () => tracks };
}

type MediaEntry = {
  promise: Promise<FakeStream>;
  resolve: () => void;
  reject: (error: Error) => void;
  /** The stream a successful settle resolves with. */
  stream: FakeStream;
};

/**
 * Scriptable getUserMedia: each call yields a deferred promise plus a
 * stand-in stream whose tracks record `stop()`.
 */
function installMediaDevices() {
  const calls: MediaEntry[] = [];

  const getUserMedia = vi.fn(() => {
    const entry: MediaEntry = {
      promise: null as unknown as Promise<FakeStream>,
      resolve: () => {},
      reject: () => {},
      stream: makeStream(),
    };
    entry.promise = new Promise((resolve, reject) => {
      entry.resolve = () => resolve(entry.stream);
      entry.reject = (error) => reject(error);
    });
    calls.push(entry);
    return entry.promise;
  });

  Object.defineProperty(window.navigator, "mediaDevices", {
    value: { getUserMedia },
    configurable: true,
  });

  return {
    getUserMedia,
    /** The stream a successful settle would hand out (track stop is the assertion target). */
    streamAt: (index: number) => calls[index]?.stream,
    settleSuccess: (index: number) => calls[index]?.resolve(),
    settleDeny: (index: number) => calls[index]?.reject(new Error("NotAllowedError")),
  };
}

/* -------------------------------- helpers -------------------------------- */

function renderOverlay(snapshot: {
  phase: ReturnType<typeof useVoiceSession>["phase"];
  failure: ReturnType<typeof useVoiceSession>["failure"];
  onEnd: () => void;
  onDismiss: () => void;
}) {
  return render(
    <I18nProvider locale="en" resources={OVERLAY_RESOURCES}>
      <VoiceOverlay
        phase={snapshot.phase}
        failure={snapshot.failure}
        liveUserText=""
        liveAssistantText=""
        onEnd={snapshot.onEnd}
        onDismiss={snapshot.onDismiss}
      />
    </I18nProvider>,
  );
}

beforeEach(() => {
  FakeWebSocket.instances = [];
  setupSocketFake();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

/* ------------------------- P1-1: lifecycle races ------------------------- */

describe("useVoiceSession lifecycle races (RUYI-866 P1-1)", () => {
  it("dismiss during connect aborts the socket; late handshake stays a no-op", async () => {
    const { result } = renderHook(() => useVoiceSession({ agentId: "agent-a" }));

    await act(async () => {
      result.current.start();
    });
    const socket = lastSocket();
    expect(socket.readyState).toBe(FakeWebSocket.CONNECTING);

    await act(async () => {
      result.current.dismiss();
    });
    // The owned connection was aborted, not just dropped: close() on a
    // still-handshaking socket reports the 1006 settle, and the controller
    // treats it as a no-op because the session was already finished.
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED);
    expect(socket.closedWhileConnecting).toBe(true);
    expect(result.current.phase).toBe("idle");
    expect(result.current.failure).toBeNull();

    // Late settle: open + setupComplete on the dead socket must not
    // resurrect the dismissed session's state.
    socket.open();
    socket.emit({ setupComplete: {} });
    await flush();
    expect(result.current.phase).toBe("idle");

    // And a fresh start is accepted with a FRESH socket.
    await act(async () => {
      result.current.start();
    });
    expect(lastSocket()).not.toBe(socket);
    expect(lastSocket().readyState).toBe(FakeWebSocket.CONNECTING);
  });

  it("agent switch cancels the old session and kills a late capture grant", async () => {
    const media = installMediaDevices();
    const { result, rerender } = renderHook(
      ({ agentId }: { agentId: string | null }) => useVoiceSession({ agentId }),
      { initialProps: { agentId: "agent-a" } },
    );

    await act(async () => {
      result.current.start();
    });
    const socketA = lastSocket();
    await act(async () => {
      socketA.open();
      socketA.emit({ setupComplete: {} });
    });
    expect(result.current.phase).toBe("live");
    // The capture is in flight (deferred getUserMedia) — the race window.
    expect(media.getUserMedia).toHaveBeenCalledTimes(1);

    await act(async () => {
      rerender({ agentId: "agent-b" });
    });
    expect(socketA.readyState).toBe(FakeWebSocket.CLOSED);
    expect(result.current.phase).toBe("idle");

    // The mic grant arrives AFTER the switch: it must release its tracks
    // instead of holding the microphone for a dead session.
    await act(async () => {
      media.settleSuccess(0);
      await flush();
    });
    expect(media.streamAt(0)!.getTracks()[0]!.stopped).toBe(true);
    expect(result.current.phase).toBe("idle");

    // Late frames on the dead socket must not resurrect the old session.
    socketA.emit({ setupComplete: {} });
    await flush();
    expect(result.current.phase).toBe("idle");

    // The new agent's session is fully independent.
    await act(async () => {
      result.current.start();
    });
    expect(lastSocket()).not.toBe(socketA);
  });

  it("end preserves ended state and releases a late capture grant without rebuilding audio", async () => {
    const media = installMediaDevices();
    const createdContexts: unknown[] = [];
    const createdWorklets: unknown[] = [];
    vi.stubGlobal("AudioContext", class {
      constructor() {
        createdContexts.push(this);
      }
    });
    vi.stubGlobal("AudioWorkletNode", class {
      constructor() {
        createdWorklets.push(this);
      }
    });
    const { result } = renderHook(() => useVoiceSession({ agentId: "agent-a" }));

    await act(async () => {
      result.current.start();
    });
    const socket = lastSocket();
    await act(async () => {
      socket.open();
      socket.emit({ setupComplete: {} });
    });
    expect(result.current.phase).toBe("live");
    expect(media.getUserMedia).toHaveBeenCalledTimes(1);

    await act(async () => {
      result.current.end();
    });
    expect(result.current.phase).toBe("ended");
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED);

    // The permission prompt resolves after End. The stale stream must be
    // released before AudioContext/worklet construction can restart capture.
    await act(async () => {
      media.settleSuccess(0);
      await flush();
    });
    expect(media.streamAt(0)!.getTracks()[0]!.stopped).toBe(true);
    expect(createdContexts).toHaveLength(0);
    expect(createdWorklets).toHaveLength(0);
    expect(result.current.phase).toBe("ended");
    expect(result.current.failure).toBeNull();
  });

  it("unmount closes the in-flight socket", async () => {
    const { result, unmount } = renderHook(() => useVoiceSession({ agentId: "agent-a" }));

    await act(async () => {
      result.current.start();
    });
    const socket = lastSocket();
    expect(socket.readyState).toBe(FakeWebSocket.CONNECTING);

    act(() => {
      unmount();
    });
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED);
    expect(socket.closedWhileConnecting).toBe(true);
  });
});

/* --------------------- P1-2: permission persistence --------------------- */

describe("useVoiceSession permission overlay (RUYI-866 P1-2)", () => {
  it("keeps the visible failed + permission surface until the user dismisses", async () => {
    const media = installMediaDevices();
    const { result } = renderHook(() => useVoiceSession({ agentId: "agent-a" }));

    await act(async () => {
      result.current.start();
    });
    const socket = lastSocket();
    await act(async () => {
      socket.open();
      socket.emit({ setupComplete: {} });
    });
    expect(media.getUserMedia).toHaveBeenCalledTimes(1);

    // The browser denies the mic.
    await act(async () => {
      media.settleDeny(0);
      await flush();
    });
    expect(result.current.phase).toBe("failed");
    expect(result.current.failure).toEqual({ kind: "permission" });
    // The socket was closed WITHOUT a terminal transition: no "ended".
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED);

    // The overlay survives: failed + permission renders the alert.
    const overlay = renderOverlay({
      phase: result.current.phase,
      failure: result.current.failure,
      onEnd: result.current.end,
      onDismiss: result.current.dismiss,
    });
    expect(
      screen.getByRole("dialog", { name: "Voice conversation" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Microphone access is required for voice. Enable it in your system settings and try again.",
    );

    // Late socket noise (setup + gate error frame) cannot flip the state.
    socket.emit({ setupComplete: {} });
    socket.emit({ error: { message: "late" }, code: "VOICE_UNAVAILABLE:voice_unconfigured" });
    await flush();
    expect(result.current.phase).toBe("failed");
    expect(result.current.failure).toEqual({ kind: "permission" });

    // Dismiss is the only thing that clears it.
    await act(async () => {
      result.current.dismiss();
    });
    expect(result.current.phase).toBe("idle");
    expect(result.current.failure).toBeNull();
    overlay.unmount();
  });
});

/* ------------------- P1-3: 409 / D2 gate-reason path -------------------- */

describe("useVoiceSession gate-reason recovery (RUYI-866 P1-3)", () => {
  it("refines a bare drop into the D2 config reason via the probe", async () => {
    const fetchMock = vi.fn(
      async (_url: string, _init?: RequestInit) =>
        new Response(JSON.stringify({ code: "VOICE_UNAVAILABLE:voice_unconfigured" }), {
          status: 409,
        }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(() => useVoiceSession({ agentId: "agent-a" }));
    await act(async () => {
      result.current.start();
    });
    // Handshake fails with no error frame — the cookie path's 409 shape:
    // the WebSocket API only sees the close code, and the degrade starts
    // out with a null rejection.
    await act(async () => {
      lastSocket().close(1006);
      await flush();
    });

    // The probe refined the failure to the precise D2 reason.
    expect(result.current.failure).toEqual({
      kind: "rejection",
      rejection: { kind: "voice_unavailable", reason: "voice_unconfigured" },
    });
    expect(result.current.phase).toBe("failed");
    // The probe read the gateway over the same URL, cookies included.
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "http://localhost:3000/api/agents/agent-a/voice-session",
    );
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({
      method: "GET",
      credentials: "include",
    });
  });

  it.each([
    "voice_provider_missing",
    "voice_provider_disabled",
  ] as const)("maps the %s 409 to the precise D2 reason", async (reason) => {
    const fetchMock = vi.fn(
      async (_url: string, _init?: RequestInit) =>
        new Response(JSON.stringify({ code: `VOICE_UNAVAILABLE:${reason}` }), { status: 409 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(() => useVoiceSession({ agentId: "agent-a" }));
    await act(async () => {
      result.current.start();
    });
    await act(async () => {
      lastSocket().close(1006);
      await flush();
    });
    expect(result.current.failure).toEqual({
      kind: "rejection",
      rejection: { kind: "voice_unavailable", reason },
    });
  });

  it("keeps the generic copy when the probe itself fails (CORS / network)", async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => {
      throw new TypeError("blocked by CORS");
    });
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(() => useVoiceSession({ agentId: "agent-a" }));
    await act(async () => {
      result.current.start();
    });
    await act(async () => {
      lastSocket().close(1006);
      await flush();
    });

    // The degrade settled with a null rejection — the generic copy — and
    // the failed probe must not replace it with a second failure write.
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(result.current.failure).toEqual({ kind: "rejection", rejection: null });
    expect(result.current.phase).toBe("failed");
  });

  it("ignores the probe result for a session that was dismissed meanwhile", async () => {
    let resolveFetch: ((response: Response) => void) | null = null;
    const fetchMock = vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          resolveFetch = resolve;
        }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(() => useVoiceSession({ agentId: "agent-a" }));
    await act(async () => {
      result.current.start();
    });
    await act(async () => {
      lastSocket().close(1006);
      await flush();
    });
    expect(result.current.failure).toEqual({ kind: "rejection", rejection: null });
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // Dismiss while the probe is in flight: the late probe result must
    // not resurrect a rejection on the idle UI.
    await act(async () => {
      result.current.dismiss();
    });
    await act(async () => {
      resolveFetch!(new Response(JSON.stringify({ code: "VOICE_UNAVAILABLE:voice_unconfigured" }), { status: 409 }));
      await flush();
    });
    expect(result.current.phase).toBe("idle");
    expect(result.current.failure).toBeNull();
  });
});
