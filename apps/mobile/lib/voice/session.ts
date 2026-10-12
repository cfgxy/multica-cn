/**
 * Mobile voice-session glue — direct-connect edition (RUYI-626). Reuses the
 * shared @multica/core/voice controller over the mobile-specific halves:
 *
 * - Session start: REST `POST /api/agents/{id}/voice-direct-session` runs
 *   the §4.4 rule-3 gate and creates the live_session row, then hands back
 *   the provider websocket URL, the decrypted credential and the setup
 *   inputs. A gate rejection (409 `VOICE_UNAVAILABLE:<reason>`) surfaces as
 *   the same degrade copy the gateway path produced.
 * - Transport: a plain RN WebSocket straight to Google. The credential
 *   rides the connect headers (RN's third constructor argument) — never the
 *   URL, which proxies and CDNs log. The controller composes and sends the
 *   BidiGenerateContent setup frame itself (core direct mode).
 * - Terminal write-back: the controller collects the transcript and the
 *   freshest resumption handle; on ANY terminal state (hang-up, provider
 *   drop, failure) the record is POSTed to
 *   `/api/voice-sessions/{id}/complete` so the server-side live_session row
 *   and its facts/summary write-back reach the same state the gateway path
 *   produced. Idempotent server-side; best-effort client-side.
 * - Audio: the VoiceAudio native module — 16 kHz PCM16 mic chunks upstream,
 *   24 kHz PCM16 playback downstream, `interruptPlayback` on barge-in.
 */
import {
  VoiceRejectionError,
  VoiceSessionController,
  type VoiceRejection,
  type VoiceSessionState,
} from "@multica/core/voice";
import { api } from "@/data/api";
import { getVoiceAudioModule } from "./native-audio";
import { voiceRejectionFromApiError } from "./start-rejection";

/** RN WebSocket implementation of the core transport contract. */

// RN honors a third `options.headers` argument on the WebSocket constructor
// (iOS and Android both pass it to the native socket) even though the DOM
// typing doesn't declare it. The direct path relies on it: the credential
// rides the connect headers — never the URL.
type HeaderedWebSocket = new (
  url: string,
  protocols?: string | string[] | null,
  options?: { headers?: Record<string, string> },
) => WebSocket;

class MobileVoiceTransport {
  private ws: WebSocket | null = null;

  onFrame: ((data: string) => void) | null = null;
  onClose: ((code: number) => void) | null = null;

  constructor(private readonly headers: Record<string, string> = {}) {}

  connect(url: string): Promise<void> {
    return new Promise((resolve, reject) => {
      const ws = new (WebSocket as unknown as HeaderedWebSocket)(url, undefined, {
        headers: this.headers,
      });
      this.ws = ws;
      let opened = false;
      ws.onopen = () => {
        opened = true;
        resolve();
      };
      ws.onmessage = (event: WebSocketMessageEvent) => {
        this.onFrame?.(typeof event.data === "string" ? event.data : "");
      };
      ws.onclose = (event: WebSocketCloseEvent) => {
        this.ws = null;
        if (opened) {
          this.onClose?.(event.code ?? 1006);
          return;
        }
        // Handshake-level drop against the provider: no code recovery is
        // possible (and none needed — the gate already passed server-side),
        // so this is a network verdict, not a credential verdict. The
        // controller's connect-catch owns the failure; the close is NOT
        // forwarded.
        reject(new VoiceRejectionError({ kind: "provider_unreachable" }));
      };
      ws.onerror = () => {
        // onclose always follows; nothing actionable here.
      };
    });
  }

  send(data: string): void {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(data);
  }

  close(): void {
    const ws = this.ws;
    this.ws = null;
    if (ws && ws.readyState === WebSocket.OPEN) ws.close(1000);
  }
}

export interface MobileVoiceSessionCallbacks {
  onStateChange?: (state: VoiceSessionState) => void;
  onUserTranscriptDelta?: (delta: string) => void;
  onAssistantTranscriptDelta?: (delta: string) => void;
  onUserTurn?: (text: string) => void;
  onDegrade?: (rejection: VoiceRejection | null) => void;
  onEnded?: () => void;
}

/**
 * Drives one mobile voice session. Create, then call `start()`; `end()` hangs
 * up and releases the mic. Exactly one controller per instance.
 */
export class MobileVoiceSession {
  private controller: VoiceSessionController | null = null;
  private unsubscribeChunk: (() => void) | null = null;
  private sessionId: string | null = null;
  private completeSent = false;

  constructor(
    private readonly agentId: string,
    private readonly workspaceSlug: string,
    private readonly callbacks: MobileVoiceSessionCallbacks,
  ) {}

  async start(): Promise<void> {
    if (this.controller) return;
    const audio = getVoiceAudioModule();
    if (!audio) throw new Error("VOICE_AUDIO_UNAVAILABLE");

    const permission = await audio.requestPermissionsAsync();
    if (!permission.granted) throw new Error("VOICE_PERMISSION_DENIED");

    // The gate chain runs server-side; a rejection arrives as an ApiError
    // whose body carries `VOICE_UNAVAILABLE:<reason>` — mapped to the same
    // degrade copy the gateway path produced, then rethrown as a typed
    // error so the overlay's catch keeps the specific message.
    let handoff;
    try {
      handoff = await api.startVoiceDirectSession(this.agentId);
    } catch (err) {
      const rejection = voiceRejectionFromApiError(err);
      this.callbacks.onDegrade?.(rejection);
      throw new VoiceRejectionError(rejection);
    }
    this.sessionId = handoff.session_id;

    const controller = new VoiceSessionController({
      url: handoff.provider_ws_url,
      mode: "direct",
      directSetup: {
        instructions: handoff.instructions,
        model: handoff.model,
        advanced: handoff.advanced ?? {},
      },
      transport: new MobileVoiceTransport({
        "x-goog-api-key": handoff.api_key,
      }),
      callbacks: {
        onStateChange: (state) => {
          this.callbacks.onStateChange?.(state);
          if (state === "live") {
            this.unsubscribeChunk?.();
            const sub = audio.addListener("onAudioChunk", (event) => {
              controller.sendAudio(event.data);
            });
            this.unsubscribeChunk = () => sub.remove();
            void audio.start().catch(() => {
              // Permission could have been revoked mid-flow; the session
              // stays up but silent — degrade surfaces on the next failure.
            });
          }
          if (state === "ended" || state === "failed") {
            this.sendComplete(controller);
          }
        },
        onUserTranscriptDelta: (delta) => this.callbacks.onUserTranscriptDelta?.(delta),
        onAssistantTranscriptDelta: (delta) =>
          this.callbacks.onAssistantTranscriptDelta?.(delta),
        onUserTurn: (text) => this.callbacks.onUserTurn?.(text),
        onAudio: (_mimeType, pcmBase64) => {
          void audio.playChunk(pcmBase64).catch(() => {});
        },
        onInterrupted: () => {
          void audio.interruptPlayback().catch(() => {});
        },
        onDegrade: (rejection) => this.callbacks.onDegrade?.(rejection ?? null),
        onEnded: () => {
          this.releaseAudio();
          this.callbacks.onEnded?.();
        },
      },
    });
    this.controller = controller;
    await audio.startPlayback().catch(() => {});
    await controller.start();
  }

  /** Hangs up. Safe to call twice; the controller finishes exactly once. */
  end(): void {
    this.controller?.end();
    this.controller = null;
    this.releaseAudio();
  }

  /**
   * Terminal write-back: relay the client-collected record (transcript +
   * resumption handle) so the live_session row and the facts/summary
   * projection land exactly as the gateway path produced them. Exactly once
   * per session, fire-and-forget — a failed relay must never surface as a
   * voice error (the row stays active and ages out server-side).
   */
  private sendComplete(controller: VoiceSessionController): void {
    if (this.completeSent || !this.sessionId) return;
    this.completeSent = true;
    const record = controller.getSessionRecord();
    if (!record) return;
    void api
      .completeVoiceDirectSession(this.sessionId, {
        transcript: record.transcript,
        session_handle: record.sessionHandle || undefined,
      })
      .catch(() => {});
  }

  private releaseAudio(): void {
    this.unsubscribeChunk?.();
    this.unsubscribeChunk = null;
    const audio = getVoiceAudioModule();
    if (!audio) return;
    void audio.stop().catch(() => {});
    void audio.stopPlayback().catch(() => {});
  }
}
