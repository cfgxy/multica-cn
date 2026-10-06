"use client";

import { createPortal } from "react-dom";
import { Loader2, Mic, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import type { VoiceDegradeReason } from "@multica/core/voice";
import { useT } from "../i18n";
import type { VoiceFailure, VoicePhase } from "./use-voice-session";

/**
 * Full-screen voice-session surface (RUYI-449). Mounted separately from the
 * composer so every failure path degrades to a dismissible message and text
 * sending is never blocked or mutated.
 */
export function VoiceOverlay({
  phase,
  failure,
  liveUserText,
  liveAssistantText,
  onEnd,
  onDismiss,
}: {
  phase: VoicePhase;
  failure: VoiceFailure | null;
  liveUserText: string;
  liveAssistantText: string;
  onEnd: () => void;
  onDismiss: () => void;
}) {
  const { t } = useT("voice");
  if (phase === "idle" || phase === "ended") return null;

  const failureText = failure ? voiceFailureText(failure, t) : null;
  const live = phase === "live";

  // Portaled to body: hosts (create-issue dialog, chat surface) animate with
  // transforms, and a `fixed` panel inside a transformed ancestor would
  // position against that ancestor instead of the viewport.
  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="dialog"
      aria-modal="true"
      aria-label={t(($) => $.overlay.title)}
    >
      <div className="relative w-full max-w-md rounded-xl border border-surface-border bg-surface p-6 shadow-lg">
        <Button
          size="icon-sm"
          variant="ghost"
          className="absolute right-3 top-3"
          onClick={failure || !live ? onDismiss : onEnd}
          aria-label={t(($) => $.overlay.end)}
        >
          <X aria-hidden="true" />
        </Button>
        <div className="flex flex-col items-center gap-4 text-center">
          <div
            className={
              live
                ? "flex size-14 animate-pulse items-center justify-center rounded-full bg-primary/10 text-primary"
                : "flex size-14 items-center justify-center rounded-full bg-surface-raised text-muted-foreground"
            }
          >
            <Mic className="size-6" aria-hidden="true" />
          </div>
          <div className="text-title font-semibold">{t(($) => $.overlay.title)}</div>
          {failureText ? (
            <p className="text-body text-destructive" role="alert">
              {failureText}
            </p>
          ) : (
            <>
              <p className="text-body text-muted-foreground">
                {live
                  ? t(($) => $.overlay.live)
                  : t(($) => $.overlay.connecting)}
                {!live && <Loader2 className="ml-1 inline size-3 animate-spin" aria-hidden="true" />}
              </p>
              {liveUserText && (
                <p className="max-h-24 w-full overflow-y-auto text-body">
                  <span className="font-medium text-foreground">
                    {t(($) => $.overlay.user_label)}:
                  </span>{" "}
                  {liveUserText}
                </p>
              )}
              {liveAssistantText && (
                <p className="max-h-32 w-full overflow-y-auto text-body text-muted-foreground">
                  <span className="font-medium">
                    {t(($) => $.overlay.assistant_label)}:
                  </span>{" "}
                  {liveAssistantText}
                </p>
              )}
              {live && (
                <Button className="mt-2" variant="outline" onClick={onEnd}>
                  {t(($) => $.overlay.end)}
                </Button>
              )}
            </>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}

/** Typed copy mapping for the degrade surfaces — see core voiceFailureMessageKey. */
type VoiceT = ReturnType<typeof useT<"voice">>["t"];
function voiceFailureText(failure: VoiceFailure, t: VoiceT): string {
  if (failure.kind === "permission") {
    return t(($) => $.overlay.permission_denied);
  }
  const rejection = failure.rejection;
  if (rejection?.kind !== "voice_unavailable") {
    // auth internals and unknown shapes collapse to the generic message —
    // the UI never surfaces authentication detail.
    return t(($) => $.failure.connection_failed);
  }
  const reasons: Record<VoiceDegradeReason, string> = {
    no_voice_runtime: t(($) => $.failure.unavailable.no_voice_runtime),
    instance_disabled: t(($) => $.failure.unavailable.instance_disabled),
    instance_not_active: t(($) => $.failure.unavailable.instance_not_active),
    capability_mismatch: t(($) => $.failure.unavailable.capability_mismatch),
    credential_missing: t(($) => $.failure.unavailable.credential_missing),
    credential_invalid: t(($) => $.failure.unavailable.credential_invalid),
  };
  return reasons[rejection.reason];
}
