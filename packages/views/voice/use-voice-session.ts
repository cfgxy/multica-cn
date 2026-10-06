"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { getApi } from "@multica/core/api";
import {
  createLogger,
} from "@multica/core/logger";
import { defaultStorage, getCurrentSlug } from "@multica/core/platform";
import {
  VoiceSessionController,
  buildVoiceSessionUrl,
  type VoiceRejection,
  type VoiceSessionState,
} from "@multica/core/voice";
import { BrowserVoiceAudio } from "./browser-audio";
import { BrowserVoiceTransport } from "./browser-transport";

const logger = createLogger("voice.ui");

/** Why a voice session is showing its failure surface. */
export type VoiceFailure =
  | { kind: "permission" }
  | { kind: "rejection"; rejection: VoiceRejection | null };

export type VoicePhase = "idle" | VoiceSessionState;

export interface UseVoiceSessionResult {
  phase: VoicePhase;
  failure: VoiceFailure | null;
  /** Finalized user turns, in order — the new-issue entry backfills these. */
  userTurns: string[];
  /** In-flight transcription fragments for the live overlay. */
  liveUserText: string;
  liveAssistantText: string;
  start: () => void;
  end: () => void;
  /** Clears failure state and returns to idle (overlay dismiss). */
  dismiss: () => void;
}

/**
 * Drives one browser voice session against the agent's bound Voice Runtime
 * (RUYI-449). Auth follows the realtime WS pattern: cookie mode rides the
 * HttpOnly cookie on the upgrade request; token mode sends the first-frame
 * auth — the token never travels in a URL.
 *
 * The overlay renders transcript state; text sending is untouched — a failed
 * or ended session never mutates composer drafts.
 */
export function useVoiceSession(options: {
  agentId: string | null;
  /** Fired per finalized user turn (new-task voice entry backfills the prompt). */
  onUserTurn?: (text: string) => void;
}): UseVoiceSessionResult {
  const { agentId, onUserTurn } = options;

  const [phase, setPhase] = useState<VoicePhase>("idle");
  const [failure, setFailure] = useState<VoiceFailure | null>(null);
  const [userTurns, setUserTurns] = useState<string[]>([]);
  const [liveUserText, setLiveUserText] = useState("");
  const [liveAssistantText, setLiveAssistantText] = useState("");

  const controllerRef = useRef<VoiceSessionController | null>(null);
  const audioRef = useRef<BrowserVoiceAudio | null>(null);
  const onUserTurnRef = useRef(onUserTurn);
  onUserTurnRef.current = onUserTurn;

  const teardownAudio = useCallback(() => {
    audioRef.current?.dispose();
    audioRef.current = null;
  }, []);

  const start = useCallback(() => {
    if (!agentId || controllerRef.current) return;
    setFailure(null);
    setUserTurns([]);
    setLiveUserText("");
    setLiveAssistantText("");
    setPhase("connecting");

    const audio = new BrowserVoiceAudio({
      onAudioChunk: (chunk) => controllerRef.current?.sendAudio(chunk),
      onCaptureError: (message) => logger.warn(`voice capture/playback: ${message}`),
    });
    audioRef.current = audio;

    const controller = new VoiceSessionController({
      url: buildVoiceSessionUrl(getApi().getBaseUrl(), agentId, {
        workspaceSlug: getCurrentSlug() ?? undefined,
      }),
      // Token mode sends the first-frame auth frame; cookie mode (null)
      // relies on the HttpOnly cookie attached to the upgrade request.
      authToken: defaultStorage.getItem("multica_token"),
      transport: new BrowserVoiceTransport(),
      callbacks: {
        onStateChange: (state) => {
          setPhase(state);
          if (state === "live") {
            void audio.startCapture().catch(() => {
              // startCapture rejects on permission denial (onCaptureError
              // already logged the detail): end the session so the socket
              // and provider minutes are not held by a dead mic.
              setFailure({ kind: "permission" });
              setPhase("failed");
              controllerRef.current?.end();
              teardownAudio();
              controllerRef.current = null;
            });
          }
        },
        onUserTranscriptDelta: (delta) => setLiveUserText((prev) => prev + delta),
        onAssistantTranscriptDelta: (delta) =>
          setLiveAssistantText((prev) => prev + delta),
        onUserTurn: (text) => {
          setUserTurns((prev) => [...prev, text]);
          setLiveUserText("");
          onUserTurnRef.current?.(text);
        },
        onAudio: (mimeType, pcmBase64) => audio.play(mimeType, pcmBase64),
        onInterrupted: () => audio.stopPlayback(),
        onDegrade: (rejection) => {
          logger.warn(`voice session degraded: ${rejection?.kind ?? "connection"}`);
          setFailure({ kind: "rejection", rejection });
        },
        onEnded: () => {
          teardownAudio();
          controllerRef.current = null;
          setPhase((prev) => (prev === "failed" ? prev : "ended"));
        },
      },
    });
    controllerRef.current = controller;
    controller.start().catch(() => {
      // Connection failures arrive via onDegrade/onClose; this guard only
      // prevents an unhandled promise rejection on the connect path.
      teardownAudio();
      controllerRef.current = null;
      setPhase("failed");
    });
  }, [agentId, teardownAudio]);

  const end = useCallback(() => {
    controllerRef.current?.end();
    // A live socket finalizes itself synchronously inside end() (close →
    // onClose → onEnded nulls the ref). If it already died without that
    // close callback, finish the teardown here so the overlay never hangs
    // on a ghost session.
    if (controllerRef.current) {
      controllerRef.current = null;
      teardownAudio();
      setPhase((prev) => (prev === "failed" ? prev : "ended"));
    }
  }, [teardownAudio]);

  const dismiss = useCallback(() => {
    teardownAudio();
    controllerRef.current = null;
    setPhase("idle");
    setFailure(null);
  }, [teardownAudio]);

  // Unmount / agent switch: kill mic + socket unconditionally.
  useEffect(
    () => () => {
      controllerRef.current?.end();
      teardownAudio();
    },
    [teardownAudio],
  );

  return { phase, failure, userTurns, liveUserText, liveAssistantText, start, end, dismiss };
}
