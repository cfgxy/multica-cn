import { VOICE_INPUT_MIME_TYPE } from "@multica/core/voice";

/**
 * Browser/Electron audio pipeline for voice sessions (RUYI-449).
 *
 * Capture: getUserMedia → AudioWorklet (worklet source inlined as a Blob URL
 * so no extra build artifact or network fetch is needed) → Float32 blocks on
 * the main thread → linear-interpolation resample to 16 kHz → PCM16 → base64
 * chunks of ~100 ms, matching the gateway's `audio/pcm;rate=16000` upstream
 * contract.
 *
 * Playback: a dedicated AudioContext scheduled sequentially; every turn's
 * chunks queue after the previous ones, and an `interrupted` server flag (or
 * end/teardown) stops all pending sources immediately.
 */

const TARGET_CAPTURE_RATE = 16000;
const PLAYBACK_RATE = 24000;
/** ~100 ms of 16 kHz mono PCM16 per upstream frame. */
const SAMPLES_PER_CHUNK = 1600;

/** Worklet source: buffers render quanta and posts ~2048-frame blocks. */
const CAPTURE_WORKLET_SOURCE = `
class VoiceCaptureProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this._buffer = new Float32Array(2048);
    this._fill = 0;
  }
  process(inputs) {
    const input = inputs[0];
    if (input && input[0]) {
      const channel = input[0];
      let offset = 0;
      while (offset < channel.length) {
        const take = Math.min(channel.length - offset, this._buffer.length - this._fill);
        this._buffer.set(channel.subarray(offset, offset + take), this._fill);
        this._fill += take;
        offset += take;
        if (this._fill === this._buffer.length) {
          this.port.postMessage(this._buffer.slice(0));
          this._fill = 0;
        }
      }
    }
    return true;
  }
}
registerProcessor("voice-capture", VoiceCaptureProcessor);
`;

/** Streaming Float32(at inputRate) → Int16(16 kHz) converter. */
class Resampler {
  private carry: Float32Array = new Float32Array(0);
  private carryPos = 0;

  constructor(private readonly inputRate: number) {}

  push(input: Float32Array): Int16Array {
    if (this.inputRate === TARGET_CAPTURE_RATE) {
      return floatToPcm16(input);
    }
    // Keep the last carried sample as the interpolation anchor: ratio math
    // runs on a virtual stream [carry..., input...].
    const merged = new Float32Array(this.carry.length - this.carryPos + input.length);
    merged.set(this.carry.subarray(this.carryPos));
    merged.set(input, this.carry.length - this.carryPos);
    const ratio = this.inputRate / TARGET_CAPTURE_RATE;
    const outCount = Math.max(0, Math.floor((merged.length - 1) / ratio));
    const out = new Int16Array(outCount);
    for (let i = 0; i < outCount; i++) {
      const pos = i * ratio;
      const idx = Math.floor(pos);
      const frac = pos - idx;
      const cur = merged[idx] ?? 0;
      const next = merged[idx + 1] ?? cur;
      out[i] = floatToPcm16Sample(cur + (next - cur) * frac);
    }
    // Carry from just before the last emitted position so the next push can
    // interpolate across the block boundary.
    this.carry = merged;
    this.carryPos = Math.floor(outCount * ratio);
    return out;
  }
}

function floatToPcm16Sample(sample: number): number {
  const clamped = Math.max(-1, Math.min(1, sample));
  return clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff;
}

function floatToPcm16(input: Float32Array): Int16Array {
  const out = new Int16Array(input.length);
  for (let i = 0; i < input.length; i++) {
    out[i] = floatToPcm16Sample(input[i] ?? 0);
  }
  return out;
}

function pcm16ToBase64(pcm: Int16Array): string {
  const bytes = new Uint8Array(pcm.buffer, pcm.byteOffset, pcm.byteLength);
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

export interface BrowserVoiceAudioCallbacks {
  onAudioChunk: (base64Pcm16: string) => void;
  onCaptureError: (message: string) => void;
}

/**
 * Owns one session's microphone capture plus playback context. Create a fresh
 * instance per session; `dispose()` releases everything even after a partial
 * start failure.
 */
export class BrowserVoiceAudio {
  private captureCtx: AudioContext | null = null;
  private captureStream: MediaStream | null = null;
  private worklet: AudioWorkletNode | null = null;
  private resampler: Resampler | null = null;
  private pending: Int16Array[] = [];
  private pendingSamples = 0;
  private playbackCtx: AudioContext | null = null;
  private playbackQueue: AudioBufferSourceNode[] = [];
  private nextPlayTime = 0;

  constructor(private readonly callbacks: BrowserVoiceAudioCallbacks) {}

  async startCapture(): Promise<void> {
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
        },
      });
    } catch {
      this.callbacks.onCaptureError("microphone permission denied or unavailable");
      throw new Error("voice capture unavailable");
    }
    this.captureStream = stream;
    const ctx = new AudioContext();
    this.captureCtx = ctx;
    const blob = new Blob([CAPTURE_WORKLET_SOURCE], { type: "application/javascript" });
    const workletUrl = URL.createObjectURL(blob);
    try {
      await ctx.audioWorklet.addModule(workletUrl);
    } finally {
      URL.revokeObjectURL(workletUrl);
    }
    const source = ctx.createMediaStreamSource(stream);
    const worklet = new AudioWorkletNode(ctx, "voice-capture");
    worklet.port.onmessage = (event: MessageEvent<Float32Array>) => {
      if (!this.resampler) return;
      this.emitPcm(this.resampler.push(event.data));
    };
    this.resampler = new Resampler(ctx.sampleRate);
    source.connect(worklet);
    // Worklet needs a destination to pull; a zero-gain sink keeps mic audio
    // off the speakers while the graph keeps running.
    const sink = ctx.createGain();
    sink.gain.value = 0;
    worklet.connect(sink);
    sink.connect(ctx.destination);
    this.worklet = worklet;
  }

  stopCapture(): void {
    this.worklet?.port.close();
    this.worklet?.disconnect();
    this.worklet = null;
    this.captureStream?.getTracks().forEach((track) => track.stop());
    this.captureStream = null;
    void this.captureCtx?.close().catch(() => {});
    this.captureCtx = null;
    this.resampler = null;
    this.pending = [];
    this.pendingSamples = 0;
  }

  /** Queues one downstream audio chunk (24 kHz PCM16 base64) for playback. */
  play(mimeType: string, base64Pcm16: string): void {
    // rate=8000 legacy codecs aside, the gateway relays provider audio at
    // 24 kHz; anything explicitly non-24k is still played through the same
    // buffer (Web Audio resamples), so only a gross mismatch is worth
    // noting — never a hard failure.
    void mimeType;
    const binary = atob(base64Pcm16);
    const pcm = new Int16Array(binary.length / 2);
    for (let i = 0; i < pcm.length; i++) {
      pcm[i] = binary.charCodeAt(i * 2) | (binary.charCodeAt(i * 2 + 1) << 8);
    }
    const ctx = this.ensurePlaybackCtx();
    if (!ctx) return;
    const buffer = ctx.createBuffer(1, Math.max(1, pcm.length), PLAYBACK_RATE);
    const channel = buffer.getChannelData(0);
    for (let i = 0; i < pcm.length; i++) {
      channel[i] = (pcm[i] ?? 0) / 0x8000;
    }
    const source = ctx.createBufferSource();
    source.buffer = buffer;
    source.connect(ctx.destination);
    // Schedule strictly after everything already queued, so transcript-timed
    // chunks play back contiguously.
    const now = ctx.currentTime;
    if (this.nextPlayTime < now) this.nextPlayTime = now;
    source.start(this.nextPlayTime);
    this.nextPlayTime += buffer.duration;
    this.playbackQueue.push(source);
    source.onended = () => {
      this.playbackQueue = this.playbackQueue.filter((s) => s !== source);
    };
  }

  /** Stops all pending playback immediately (barge-in `interrupted`, end). */
  stopPlayback(): void {
    for (const source of this.playbackQueue.splice(0)) {
      try {
        source.stop();
      } catch {
        // Already finished; nothing to do.
      }
    }
    this.nextPlayTime = 0;
  }

  dispose(): void {
    this.stopCapture();
    this.stopPlayback();
    void this.playbackCtx?.close().catch(() => {});
    this.playbackCtx = null;
  }

  private emitPcm(pcm: Int16Array): void {
    this.pending.push(pcm);
    this.pendingSamples += pcm.length;
    while (this.pendingSamples >= SAMPLES_PER_CHUNK) {
      const chunk = new Int16Array(SAMPLES_PER_CHUNK);
      let fill = 0;
      while (fill < SAMPLES_PER_CHUNK) {
        const head = this.pending[0];
        if (!head) break;
        const take = Math.min(head.length, SAMPLES_PER_CHUNK - fill);
        chunk.set(head.subarray(0, take), fill);
        fill += take;
        if (take === head.length) {
          this.pending.shift();
        } else {
          this.pending[0] = head.subarray(take);
        }
      }
      this.pendingSamples -= SAMPLES_PER_CHUNK;
      this.callbacks.onAudioChunk(pcm16ToBase64(chunk));
    }
  }

  private ensurePlaybackCtx(): AudioContext | null {
    if (this.playbackCtx) return this.playbackCtx;
    try {
      this.playbackCtx = new AudioContext({ sampleRate: PLAYBACK_RATE });
    } catch {
      try {
        this.playbackCtx = new AudioContext();
      } catch {
        this.callbacks.onCaptureError("audio playback unavailable");
        return null;
      }
    }
    return this.playbackCtx;
  }
}

export { VOICE_INPUT_MIME_TYPE };
