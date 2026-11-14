/**
 * Mobile voice-session glue — gateway edition (RUYI-694 D3). Reuses the
 * shared @multica/core/voice controller in gateway mode over the
 * mobile-specific halves:
 *
 * - Endpoint: the D1 gateway's `GET /api/agents/{id}/voice-session`
 *   websocket upgrade (see session-gateway.ts for the transport + URL
 *   wiring). Identity rides the first frame `{"type":"auth"}` — the token
 *   never touches the URL, and no provider credential ever reaches the
 *   device (the D2 model: credentials live server-side).
 * - Gate rejections and provider-dial failures arrive as one
 *   `{"error","code"}` frame post-upgrade, which the controller maps onto
 *   the same typed degrade vocabulary the retired direct path produced.
 * - The server also owns the terminal transcript write-back (gateway
 *   teardown projection), so there is no client-side `complete` relay —
 *   the RUYI-626 direct hand-off and its plaintext-key transport are gone.
 * - Audio: the VoiceAudio native module — 16 kHz PCM16 mic chunks upstream,
 *   24 kHz PCM16 playback downstream, `interruptPlayback` on barge-in.
 */
import {
  VoiceSessionController,
  type VoiceRejection,
  type VoiceSessionState,
} from "@multica/core/voice";
import { getApiUrl, useServerStore } from "@/data/server-store";
import { getToken } from "@/data/secure-storage";
import { getVoiceAudioModule } from "./native-audio";
import { buildGatewayVoiceSessionConfig, MobileVoiceTransport } from "./session-gateway";

export interface MobileVoiceSessionCallbacks {
  onStateChange?: (state: VoiceSessionState) => void;
  onUserTranscriptDelta?: (delta: string) => void;
  onAssistantTranscriptDelta?: (delta: string) => void;
  onUserTurn?: (text: string) => void;
  /** User barged in — playback stopped; UI shows the interrupted transient. */
  onInterrupted?: () => void;
  onDegrade?: (rejection: VoiceRejection | null) => void;
  onEnded?: () => void;
}

/**
 * 100ms of 16 kHz mono PCM16 silence, base64 (3200 zero bytes). The muted
 * keepalive chunk: the provider tears a live session down after ~2min with
 * zero upstream realtimeInput (QA round 3 — muted sessions died at 2m04s
 * and 3m00s), so mute swaps real mic chunks for this fixed silent frame
 * instead of dropping the upstream.
 */
const MUTED_KEEPALIVE_CHUNK = `${"A".repeat(4264)}AAA=`;

/**
 * Drives one mobile voice session through the D1 gateway. Create, then call
 * `start()`; `end()` hangs up and releases the mic. Exactly one controller
 * per instance.
 */
export class MobileVoiceSession {
  private controller: VoiceSessionController | null = null;
  private unsubscribeChunk: (() => void) | null = null;
  private muted = false;

  constructor(
    private readonly agentId: string,
    private readonly workspaceSlug: string,
    private readonly callbacks: MobileVoiceSessionCallbacks,
    /** RUYI-874 ①: the chat session the voice surface was opened from. */
    private readonly chatSessionId: string | null = null,
    /** Issue-comment entry context, validated by the gateway. */
    private readonly issueId: string | null = null,
  ) {}

  async start(): Promise<void> {
    if (this.controller) return;
    const audio = getVoiceAudioModule();
    if (!audio) throw new Error("VOICE_AUDIO_UNAVAILABLE");

    const permission = await audio.requestPermissionsAsync();
    if (!permission.granted) throw new Error("VOICE_PERMISSION_DENIED");

    // Per-server session snapshot, same contract as the realtime hub: the
    // token rides the first websocket frame, never the URL.
    const token = await getToken(useServerStore.getState().activeServerId);
    if (!token) throw new Error("VOICE_TOKEN_MISSING");

    const controller = new VoiceSessionController({
      ...buildGatewayVoiceSessionConfig({
        apiUrl: getApiUrl(),
        agentId: this.agentId,
        workspaceSlug: this.workspaceSlug,
        chatSessionId: this.chatSessionId,
        issueId: this.issueId,
        token,
      }),
      transport: new MobileVoiceTransport(),
      callbacks: {
        onStateChange: (state) => {
          this.callbacks.onStateChange?.(state);
          if (state === "live") {
            this.unsubscribeChunk?.();
            const sub = audio.addListener("onAudioChunk", (event) => {
              // 静音（RUYI-694 板3 右上键）：上行换发固定静音帧——上游在
              // 收不到任何 realtimeInput 约 2 分钟后断链，换发静音帧让会
              // 话保持 live，真实音频不出设备，下行播放不受影响。
              controller.sendAudio(this.muted ? MUTED_KEEPALIVE_CHUNK : event.data);
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
          this.callbacks.onInterrupted?.();
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

  /** 板3 右上静音键：上行静默，下行不受影响。 */
  setMuted(muted: boolean): void {
    this.muted = muted;
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
