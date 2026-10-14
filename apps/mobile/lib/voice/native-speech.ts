/**
 * Typed wrapper over the local `modules/speech-recognition` native module
 * (Android only, RUYI-694 D3 — same pattern as lib/native-audio.ts). The
 * module owns the on-device recognizer lifecycle; the dictation state
 * machine and send semantics live in lib/voice/dictation.ts, so this stays
 * a thin shim.
 *
 * Returns null on non-Android platforms — the dictation entry stays hidden
 * there (modules/share-intent is the android-only precedent).
 */
import { NativeModule, requireNativeModule } from "expo-modules-core";
import { Platform } from "react-native";

export interface SpeechPartialEvent {
  text: string;
}

export interface SpeechFinalEvent {
  /** "" when the recognizer produced nothing usable. */
  text: string;
}

export interface SpeechErrorEvent {
  /** Raw SpeechRecognizer.ERROR_* value. */
  code: number;
}

export interface SpeechRmsEvent {
  /** Loudness normalized to 0..1 for the waveform meter. */
  value: number;
}

export interface OnDeviceAvailability {
  available: boolean;
  /** "permission" | "api_level" | "module_missing" | "ok". */
  reason: string;
}

declare class SpeechRecognitionNativeModule extends NativeModule<{
  onPartialResult: (event: SpeechPartialEvent) => void;
  onFinalResult: (event: SpeechFinalEvent) => void;
  onError: (event: SpeechErrorEvent) => void;
  onRms: (event: SpeechRmsEvent) => void;
}> {
  /** Resolves { granted } after the RECORD_AUDIO prompt settles. */
  requestPermissionsAsync(): Promise<{ granted: boolean }>;
  /** On-device recognizer availability — the dictation entry's gate. */
  isOnDeviceAvailableAsync(): Promise<OnDeviceAvailability>;
  /** Starts listening; rejects E_ON_DEVICE_UNAVAILABLE / E_START_FAILED. */
  start(): Promise<null>;
  /** Finalizes: the recognizer flushes onFinalResult, then self-destroys. */
  stop(): Promise<null>;
}

let cached: SpeechRecognitionNativeModule | null = null;

export function getSpeechRecognitionModule(): SpeechRecognitionNativeModule | null {
  if (Platform.OS !== "android") return null;
  cached ??= requireNativeModule<SpeechRecognitionNativeModule>("SpeechRecognition");
  return cached;
}
