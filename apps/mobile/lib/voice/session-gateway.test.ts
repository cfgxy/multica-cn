/**
 * RUYI-694 D3 — mobile voice migration onto the D1 gateway. Pins the
 * client-side contract of the retired direct path's replacement:
 *
 *   - the session dials OUR server's `/api/agents/{id}/voice-session` with
 *     the workspace in the query string;
 *   - the first-frame auth token rides the controller options, never the
 *     URL (proxies and CDNs log URLs);
 *   - a pre-upgrade transport drop is a generic network verdict (the
 *     gateway owns every gate verdict via its post-upgrade error frames).
 */
import { describe, expect, it, vi, afterEach } from "vitest";
import {
  buildGatewayVoiceSessionConfig,
  MobileVoiceTransport,
} from "./session-gateway";

describe("buildGatewayVoiceSessionConfig", () => {
  it("dials the gateway route with the workspace in the query string", () => {
    const config = buildGatewayVoiceSessionConfig({
      apiUrl: "https://api.example.com",
      agentId: "ag-1",
      workspaceSlug: "acme",
      token: "sekrit-token",
    });
    expect(config.url).toBe(
      "wss://api.example.com/api/agents/ag-1/voice-session?workspace_slug=acme",
    );
    expect(config.mode).toBe("gateway");
    expect(config.authToken).toBe("sekrit-token");
  });

  it("never puts the auth token in the URL", () => {
    const config = buildGatewayVoiceSessionConfig({
      apiUrl: "http://10.0.0.2:21801",
      agentId: "ag-1",
      workspaceSlug: "acme",
      token: "sekrit-token",
    });
    expect(config.url).not.toContain("sekrit-token");
    expect(config.url.startsWith("ws://")).toBe(true);
  });

  it("carries the chat session link (RUYI-874) in the query", () => {
    const config = buildGatewayVoiceSessionConfig({
      apiUrl: "https://api.example.com",
      agentId: "ag-1",
      workspaceSlug: "acme",
      token: "t",
      chatSessionId: "chat-uuid",
    });
    expect(config.url).toBe(
      "wss://api.example.com/api/agents/ag-1/voice-session?workspace_slug=acme&chat_session_id=chat-uuid",
    );
    // A missing link leaves the URL exactly as the pre-RUYI-874 shape.
    const plain = buildGatewayVoiceSessionConfig({
      apiUrl: "https://api.example.com",
      agentId: "ag-1",
      workspaceSlug: "acme",
      token: "t",
      chatSessionId: null,
    });
    expect(plain.url).toBe(
      "wss://api.example.com/api/agents/ag-1/voice-session?workspace_slug=acme",
    );
  });

  it("omits the query string without a workspace slug", () => {
    const config = buildGatewayVoiceSessionConfig({
      apiUrl: "https://api.example.com/",
      agentId: "ag-1",
      workspaceSlug: null,
      token: "t",
    });
    expect(config.url).toBe("wss://api.example.com/api/agents/ag-1/voice-session");
  });
});

/** Minimal RN-shaped WebSocket stand-in: assign-once handlers, OPEN enum. */
class FakeWebSocket {
  static OPEN = 1;
  static instances: FakeWebSocket[] = [];
  readyState = FakeWebSocket.OPEN;
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: ((event: { code: number }) => void) | null = null;
  onerror: (() => void) | null = null;
  sent: string[] = [];
  closed = false;

  constructor(public url: string) {
    FakeWebSocket.instances.push(this);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.closed = true;
  }
  /** Test drives the native side of the handshake with these. */
  fireOpen() {
    this.onopen?.();
  }
  fireClose(code = 1006) {
    this.readyState = 3;
    this.onclose?.({ code });
  }
  fireMessage(data: unknown) {
    this.onmessage?.({ data });
  }
}

afterEach(() => {
  FakeWebSocket.instances = [];
  vi.unstubAllGlobals();
});

function lastSocket(): FakeWebSocket {
  return FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
}

describe("MobileVoiceTransport", () => {
  it("resolves connect on open and relays server frames as strings", async () => {
    vi.stubGlobal("WebSocket", FakeWebSocket as unknown as typeof WebSocket);
    const transport = new MobileVoiceTransport();
    const frames: string[] = [];
    transport.onFrame = (data) => frames.push(data);

    const pending = transport.connect("ws://gateway");
    lastSocket().fireOpen();
    await expect(pending).resolves.toBeUndefined();

    lastSocket().fireMessage('{"type":"auth"}');
    lastSocket().fireMessage("plain");
    // Binary payloads degrade to "" rather than crashing the frame lane.
    lastSocket().fireMessage({ blob: true });
    expect(frames).toEqual(['{"type":"auth"}', "plain", ""]);
  });

  it("rejects connect when the socket closes before opening", async () => {
    vi.stubGlobal("WebSocket", FakeWebSocket as unknown as typeof WebSocket);
    const transport = new MobileVoiceTransport();
    const pending = transport.connect("ws://gateway");
    lastSocket().fireClose(1006);
    await expect(pending).rejects.toThrow("voice gateway connect failed");
  });

  it("forwards the close code after a live session drops", async () => {
    vi.stubGlobal("WebSocket", FakeWebSocket as unknown as typeof WebSocket);
    const transport = new MobileVoiceTransport();
    const closes: number[] = [];
    transport.onClose = (code) => closes.push(code);

    const pending = transport.connect("ws://gateway");
    lastSocket().fireOpen();
    await pending;
    lastSocket().fireClose(1001);
    expect(closes).toEqual([1001]);
  });

  it("sends only while open and closes gracefully", () => {
    vi.stubGlobal("WebSocket", FakeWebSocket as unknown as typeof WebSocket);
    const transport = new MobileVoiceTransport();
    // No connection yet: send is a no-op, close must not throw.
    expect(() => transport.send("{}")).not.toThrow();
    expect(() => transport.close()).not.toThrow();

    let pending: Promise<void> | null = null;
    pending = transport.connect("ws://gateway");
    const ws = lastSocket();
    ws.fireOpen();
    transport.send("{}");
    expect(ws.sent).toEqual(["{}"]);
    transport.close();
    expect(ws.closed).toBe(true);
  });
});
