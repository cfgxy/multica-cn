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
  probeVoiceHandshakeRejection,
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
 * One session's isolated state (RUYI-866 P1-1): the controller and the audio
 * pipeline are owned by the handle, and every late callback from either
 * checks the handle before touching shared UI state — a dismissed,
 * switched-away, or unmounted session can never write into the next one, and
 * a late capture grant releases its stream instead of holding the mic.
 */
type VoiceSessionHandle = {
  controller: VoiceSessionController | null;
  /** Assigned synchronously right after construction; null only in between. */
  audio: BrowserVoiceAudio | null;
  cancelled: boolean;
};

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

  const handleRef = useRef<VoiceSessionHandle | null>(null);
  /**
   * Session generation (RUYI-866 P1): incremented by every start, dismiss,
   * agent switch, and unmount. The async probe's result may only land while
   * its own generation is current — a dismissed or replaced session keeps
   * its stale 409 refinement away from the next session's UI. A naturally
   * finalized session (overlay still visible on a failed start) keeps its
   * generation current so the refinement still improves the visible copy
   * until the user dismisses or starts again.
   */
  const genRef = useRef(0);
  const onUserTurnRef = useRef(onUserTurn);
  onUserTurnRef.current = onUserTurn;

  /**
   * Closes the handle's owned connection and mic pipeline: abort() keeps the
   * controller away from any terminal transition, so a late handshake
   * settle and a late capture grant both settle into no-ops.
   */
  const cancelHandle = useCallback((handle: VoiceSessionHandle) => {
    handle.cancelled = true;
    handle.controller?.abort();
    handle.audio?.dispose();
  }, []);

  const start = useCallback(() => {
    if (!agentId || handleRef.current) return;
    // Claim a fresh generation: every path that replaces the current
    // session (dismiss, agent switch, unmount, this very start) bumps it,
    // so only this session's late probe can still refine its own message.
    const gen = ++genRef.current;
    setFailure(null);
    setUserTurns([]);
    setLiveUserText("");
    setLiveAssistantText("");
    setPhase("connecting");

    // The handle is filled before either owner is constructed: audio chunks
    // and controller callbacks must resolve each other through the handle,
    // never through a shared "current" ref.
    const handle: VoiceSessionHandle = { controller: null, audio: null, cancelled: false };

    const audio = new BrowserVoiceAudio({
      onAudioChunk: (chunk) => {
        if (!handle.cancelled && handle.controller) handle.controller.sendAudio(chunk);
      },
      onCaptureError: (message) => logger.warn(`voice capture/playback: ${message}`),
    });
    handle.audio = audio;

    const url = buildVoiceSessionUrl(getApi().getBaseUrl(), agentId, {
      workspaceSlug: getCurrentSlug() ?? undefined,
    });

    const controller = new VoiceSessionController({
      url,
      // Token mode sends the first-frame auth frame; cookie mode (null)
      // relies on the HttpOnly cookie attached to the upgrade request.
      authToken: defaultStorage.getItem("multica_token"),
      transport: new BrowserVoiceTransport(),
      callbacks: {
        onStateChange: (state) => {
          if (handle.cancelled) return;
          setPhase(state);
          if (state === "live") {
            void audio
              .startCapture(() => handle.cancelled)
              .catch(() => {
                // A capture that settled after a dismiss/switch/unmount
                // already released its stream inside startCapture.
                if (handle.cancelled) return;
                // startCapture rejects on permission denial (onCaptureError
                // already logged the detail): abort() closes the socket
                // WITHOUT a terminal transition so the overlay keeps the
                // visible permission message until the user dismisses it
                // (RUYI-866 P1-2) — end() would flip the phase to "ended"
                // and unmount the overlay.
                setFailure({ kind: "permission" });
                setPhase("failed");
                controller.abort();
              });
          }
        },
        onUserTranscriptDelta: (delta) => {
          if (handle.cancelled) return;
          setLiveUserText((prev) => prev + delta);
        },
        onAssistantTranscriptDelta: (delta) => {
          if (handle.cancelled) return;
          setLiveAssistantText((prev) => prev + delta);
        },
        onUserTurn: (text) => {
          if (handle.cancelled) return;
          setUserTurns((prev) => [...prev, text]);
          setLiveUserText("");
          onUserTurnRef.current?.(text);
        },
        onAudio: (mimeType, pcmBase64) => {
          if (handle.cancelled) return;
          audio.play(mimeType, pcmBase64);
        },
        onInterrupted: () => {
          if (handle.cancelled) return;
          audio.stopPlayback();
        },
        onDegrade: (rejection) => {
          if (handle.cancelled) return;
          logger.warn(`voice session degraded: ${rejection?.kind ?? "connection"}`);
          setFailure({ kind: "rejection", rejection });
          if (rejection) return;
          // A bare failure: the WebSocket API cannot read the pre-upgrade
          // 409 body that carries the gate's stable code (RUYI-866 P1-3).
          // Probe the same URL over plain HTTP; any probe failure keeps the
          // generic copy, and a session that was dismissed, switched, or
          // already finalized in the meantime no longer accepts the result.
          const token = defaultStorage.getItem("multica_token");
          void probeVoiceHandshakeRejection(url, {
            authorizationHeader: token ? `Bearer ${token}` : null,
          }).then((probed) => {
            // The refinement only belongs to its own generation: dismiss,
            // agent switch, unmount, and a fresh start all bump genRef, so
            // a stale probe result never lands on the next session's UI.
            // A naturally finalized failure keeps its generation current —
            // the overlay is still visible and the precise reason improves
            // the generic copy in place.
            if (handle.cancelled || genRef.current !== gen) return;
            if (probed) setFailure({ kind: "rejection", rejection: probed });
          });
        },
        onEnded: () => {
          // Natural terminal path only — permission aborts and dismissed
          // sessions never reach here (abort suppresses the transition).
          handleRef.current = null;
          audio.dispose();
          setPhase((prev) => (prev === "failed" ? prev : "ended"));
        },
      },
    });
    handle.controller = controller;
    handleRef.current = handle;

    void controller.start().catch(() => {
      // The controller swallows connect failures internally (they surface
      // through onDegrade); this only guards an unexpected rejection so a
      // half-constructed session never lingers open.
      if (handleRef.current === handle) {
        handleRef.current = null;
        handle.audio?.dispose();
        setPhase("failed");
      }
    });
  }, [agentId]);

  const end = useCallback(() => {
    const handle = handleRef.current;
    if (!handle) return;
    // Preserve the normal ended transition, but invalidate pending capture
    // before controller.end() can synchronously finalize and dispose audio.
    handle.cancelled = true;
    handle.controller?.end();
    // A live socket finalizes itself synchronously inside end() (close →
    // onClose → onEnded). If it already died without that close callback,
    // finish the teardown here so the overlay never hangs on a ghost session.
    if (handleRef.current === handle) {
      handleRef.current = null;
      handle.audio?.dispose();
      setPhase((prev) => (prev === "failed" ? prev : "ended"));
    }
  }, []);

  const dismiss = useCallback(() => {
    const handle = handleRef.current;
    handleRef.current = null;
    genRef.current += 1; // A dismissed session's in-flight probe must not land here again.
    if (handle) cancelHandle(handle);
    setPhase("idle");
    setFailure(null);
  }, [cancelHandle]);

  // Agent switch / unmount (RUYI-866 P1): a session belongs to the agent it
  // started with. When the agent context changes, the previous session's
  // connection and mic are cancelled so no late handshake or capture grant
  // can cross into the new agent's overlay; the UI state resets with it.
  // Unmount runs the same teardown — the cleanup form, because an effect
  // body never runs when a component unmounts.
  useEffect(() => {
    const handle = handleRef.current;
    handleRef.current = null;
    genRef.current += 1;
    if (handle) cancelHandle(handle);
    setPhase("idle");
    setFailure(null);
    setUserTurns([]);
    setLiveUserText("");
    setLiveAssistantText("");
    return () => {
      const handle = handleRef.current;
      handleRef.current = null;
      genRef.current += 1;
      if (handle) cancelHandle(handle);
    };
  }, [agentId, cancelHandle]);

  return { phase, failure, userTurns, liveUserText, liveAssistantText, start, end, dismiss };
}
