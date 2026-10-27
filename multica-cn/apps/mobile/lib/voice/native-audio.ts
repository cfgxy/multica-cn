/**
 * Typed wrapper over the local `modules/voice-audio` native module
 * (Android only, RUYI-449 — same pattern as lib/share-intent.ts). The
 * module owns mic capture (16 kHz PCM16 chunks) and speaker playback
 * (24 kHz PCM16 streaming); all session logic lives in
 * @multica/core/voice + lib/voice/session.ts, so this stays a thin shim.
 *
 * Returns null on non-Android platforms — voice entry points stay hidden
 * there (iOS capture/playback is not implemented yet; modules/share-intent
 * is the android-only precedent).
 */
import { NativeModule, requireNativeModule } from "expo-modules-core";
import { Platform } from "react-native";

/** Payload of the module's `onAudioChunk` event — base64 PCM16 @16 kHz. */
export interface VoiceAudioChunkEvent {
  data: string;
}

declare class VoiceAudioNativeModule extends NativeModule<{
  onAudioChunk: (event: VoiceAudioChunkEvent) => void;
  onCaptureError: (event: { message: string }) => void;
}> {
  /** Resolves { granted } after the RECORD_AUDIO prompt settles. */
  requestPermissionsAsync(): Promise<{ granted: boolean }>;
  /** Starts mic capture; rejects E_PERMISSION_DENIED when not granted. */
  start(): Promise<null>;
  stop(): Promise<null>;
  /** Creates/restarts the playback track (fresh turn = fresh queue). */
  startPlayback(): Promise<null>;
  /** Blocking-write one base64 PCM16 chunk @24 kHz. */
  playChunk(base64: string): Promise<null>;
  /** Drop everything queued right now (barge-in). */
  interruptPlayback(): Promise<null>;
  stopPlayback(): Promise<null>;
}

let cached: VoiceAudioNativeModule | null = null;

export function getVoiceAudioModule(): VoiceAudioNativeModule | null {
  if (Platform.OS !== "android") return null;
  cached ??= requireNativeModule<VoiceAudioNativeModule>("VoiceAudio");
  return cached;
}
