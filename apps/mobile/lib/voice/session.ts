/**
 * Mobile voice-session glue (RUYI-449). Reuses the shared
 * @multica/core/voice controller (same wire format, same degrade mapping as
 * web/desktop) over two mobile-specific halves:
 *
 * - Transport: a plain RN WebSocket to
 *   `/api/agents/{id}/voice-session?workspace_slug=…`. RN sockets cannot set
 *   headers, so the session token travels as the FIRST auth frame — the
 *   gateway's header-less path (same pattern as /ws). The token never goes
 *   in the URL.
 * - Audio: the VoiceAudio native module — 16 kHz PCM16 mic chunks upstream,
 *   24 kHz PCM16 playback downstream, `interruptPlayback` on barge-in.
 */
import {
  buildVoiceSessionUrl,
  VoiceRejectionError,
  VoiceSessionController,
  type VoiceRejection,
  type VoiceSessionState,
} from "@multica/core/voice";
import { getApiUrl, useServerStore } from "@/data/server-store";
import { getToken } from "@/data/secure-storage";
import { probeHandshakeRejection } from "./handshake-reason";
import { getVoiceAudioModule } from "./native-audio";

/** RN WebSocket implementation of the core transport contract. */
class MobileVoiceTransport {
  private ws: WebSocket | null = null;

  onFrame: ((data: string) => void) | null = null;
  onClose: ((code: number) => void) | null = null;

  connect(url: string): Promise<void> {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(url);
      this.ws = ws;
      let opened = false;
      let deciding = false;
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
        // Handshake-level rejection (e.g. the header-auth path's plain HTTP
        // 409): the socket API hides the body, so recover the precise
        // reason with a plain authenticated GET on the same URL before
        // failing. Null keeps the generic degrade copy. The controller's
        // connect-catch owns the failure, so the close is NOT forwarded.
        if (deciding) return;
        deciding = true;
        void probeHandshakeRejection(url).then((rejection) => {
          reject(
            rejection
              ? new VoiceRejectionError(rejection)
              : new Error(`voice websocket closed (${event.code})`),
          );
        });
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

    const token = await getToken(useServerStore.getState().activeServerId);
    if (!token) throw new Error("VOICE_AUTH_MISSING");

    const controller = new VoiceSessionController({
      url: buildVoiceSessionUrl(getApiUrl(), this.agentId, {
        workspaceSlug: this.workspaceSlug || undefined,
      }),
      authToken: token,
      transport: new MobileVoiceTransport(),
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

  private releaseAudio(): void {
    this.unsubscribeChunk?.();
    this.unsubscribeChunk = null;
    const audio = getVoiceAudioModule();
    if (!audio) return;
    void audio.stop().catch(() => {});
    void audio.stopPlayback().catch(() => {});
  }
}
