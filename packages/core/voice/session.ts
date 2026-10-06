/**
 * Voice session state machine (RUYI-449).
 *
 * Pure orchestration over an injected transport: connect → optional
 * first-frame auth → wait setupComplete → live → relay audio/transcripts →
 * end. No DOM, no WebSocket, no React — the browser and React Native
 * transports own the platform-specific sockets and audio pipelines and drive
 * this controller through the same surface.
 *
 * Turn attribution: Gemini streams the user's speech as inputTranscription
 * deltas and the model's reply as outputTranscription deltas + audio. There
 * is no explicit "user turn done" event, so a pending user turn is flushed
 * the moment model output starts or the session ends — good enough for the
 * UI and for the new-issue prompt backfill, and never wrong by more than
 * the trailing overlap of two speakers.
 */

import {
  parseVoiceRejectionCode,
  rejectionFromError,
  type VoiceRejection,
} from "./degrade";
import { parseVoiceServerFrame, voiceAudioInputFrame } from "./protocol";

export type VoiceSessionState = "idle" | "connecting" | "live" | "ended" | "failed";

export interface VoiceTranscriptTurn {
  role: "user" | "assistant";
  text: string;
}

/** The platform socket surface the controller needs; nothing more. */
export interface VoiceTransport {
  connect(url: string): Promise<void>;
  send(data: string): void;
  close(): void;
  onFrame: ((data: string) => void) | null;
  onClose: ((code: number) => void) | null;
}

export interface VoiceSessionCallbacks {
  onStateChange?(state: VoiceSessionState): void;
  /** Incremental transcription of the user's speech. */
  onUserTranscriptDelta?(delta: string): void;
  /** Incremental transcription of the model's speech. */
  onAssistantTranscriptDelta?(delta: string): void;
  /** A user turn finalized — the new-issue entry backfills its text. */
  onUserTurn?(text: string): void;
  /** One downstream audio blob (base64 PCM, mimeType carries the rate). */
  onAudio?(mimeType: string, pcmBase64: string): void;
  /** User barged in — playback must stop immediately. */
  onInterrupted?(): void;
  /** Session start was rejected; the UI shows the mapped degrade message. */
  onDegrade?(rejection: VoiceRejection | null): void;
  onEnded?(): void;
}

export interface VoiceSessionOptions {
  url: string;
  /**
   * Token for the first-frame auth path (header-less clients — mobile).
   * Null/undefined uses cookie auth (desktop/web): the upgrade request
   * carries the HttpOnly cookie and no auth frame is sent.
   */
  authToken?: string | null;
  transport: VoiceTransport;
  callbacks: VoiceSessionCallbacks;
}

export class VoiceSessionController {
  private readonly transport: VoiceTransport;
  private readonly callbacks: VoiceSessionCallbacks;
  private readonly url: string;
  private readonly authToken: string | null;

  private state: VoiceSessionState = "idle";
  private userBuffer = "";
  private assistantBuffer = "";
  private degraded = false;
  private finished = false;

  constructor(opts: VoiceSessionOptions) {
    this.url = opts.url;
    this.authToken = opts.authToken ?? null;
    this.transport = opts.transport;
    this.callbacks = opts.callbacks;
    this.transport.onFrame = (data) => this.handleFrame(data);
    this.transport.onClose = (code) => this.handleClose(code);
  }

  getState(): VoiceSessionState {
    return this.state;
  }

  /** Opens the connection and (first-frame path) authenticates. */
  async start(): Promise<void> {
    if (this.state !== "idle") return;
    this.setState("connecting");
    try {
      await this.transport.connect(this.url);
    } catch (error) {
      // A transport that recovered the gateway's code (mobile HTTP probe)
      // reports it via VoiceRejectionError; anything else stays generic.
      this.fail(rejectionFromError(error));
      return;
    }
    // First-frame auth (RUYI-429 pattern): the token rides the first
    // websocket message — never the URL, which proxies and CDNs log.
    if (this.authToken) {
      this.transport.send(
        JSON.stringify({ type: "auth", payload: { token: this.authToken } }),
      );
    }
  }

  /** Upstream audio — ignored unless the session is live. */
  sendAudio(pcmBase64: string): void {
    if (this.state !== "live" || !pcmBase64) return;
    this.transport.send(voiceAudioInputFrame(pcmBase64));
  }

  /** Graceful local hang-up. */
  end(): void {
    if (this.state === "ended" || this.state === "failed") return;
    this.transport.close();
    this.finish("ended");
  }

  private handleFrame(data: string): void {
    const frame = parseVoiceServerFrame(data);
    if (!frame) return;

    // The gateway answers rejections with one error frame, then closes.
    // A stable `code` maps to the specific degrade; provider-relayed errors
    // carry none and fall back to the generic connection-failure copy.
    if (frame.errorMessage || frame.errorCode) {
      this.fail(parseVoiceRejectionCode(frame.errorCode));
      return;
    }

    if (frame.setupComplete) {
      this.setState("live");
      return;
    }

    if (frame.interrupted) {
      this.assistantBuffer = "";
      this.callbacks.onInterrupted?.();
    }

    if (frame.inputTranscription) {
      this.userBuffer += frame.inputTranscription;
      this.callbacks.onUserTranscriptDelta?.(frame.inputTranscription);
    }

    const hasModelOutput =
      frame.outputTranscription !== "" || frame.audioParts.length > 0;
    if (hasModelOutput) {
      // The model answering back is proof the user's turn is complete.
      this.flushUserTurn();
    }
    if (frame.outputTranscription) {
      this.assistantBuffer += frame.outputTranscription;
      this.callbacks.onAssistantTranscriptDelta?.(frame.outputTranscription);
    }
    for (const part of frame.audioParts) {
      this.callbacks.onAudio?.(part.mimeType, part.data);
    }

    if (frame.turnComplete) {
      this.flushUserTurn();
      if (this.assistantBuffer) {
        this.assistantBuffer = "";
      }
    }
  }

  private handleClose(_code: number): void {
    if (this.state === "ended" || this.state === "failed") return;
    // Closed before setupComplete (or without a prior error frame): a
    // degraded start unless the caller already ended the session cleanly.
    if (this.state === "connecting") {
      this.fail(null);
      return;
    }
    this.flushUserTurn();
    this.finish("ended");
  }

  private flushUserTurn(): void {
    const text = this.userBuffer.trim();
    this.userBuffer = "";
    if (text) this.callbacks.onUserTurn?.(text);
  }

  private fail(rejection: VoiceRejection | null): void {
    this.degraded = true;
    this.callbacks.onDegrade?.(rejection);
    this.finish("failed");
  }

  private finish(state: VoiceSessionState): void {
    // Exactly one terminal transition even when the transport's close
    // callback fires synchronously inside end().
    if (this.finished) return;
    this.finished = true;
    this.setState(state);
    this.callbacks.onEnded?.();
  }

  private setState(state: VoiceSessionState): void {
    if (this.state === state) return;
    this.state = state;
    this.callbacks.onStateChange?.(state);
  }

  /** True when the session ended in a degrade rather than a normal hang-up. */
  isDegraded(): boolean {
    return this.degraded;
  }
}

export { parseVoiceRejectionCode };
