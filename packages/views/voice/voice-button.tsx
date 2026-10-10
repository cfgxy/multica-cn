"use client";

import { Mic } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { useT } from "../i18n";

/**
 * The voice entry occupying the send-button slot while the composer is empty
 * (RUYI-449 three-state input: empty → mic, text → send, cleared → mic).
 * Mirrors SubmitButton's visual contract (icon-sm round button, composer
 * focus preserved on pointer-down) so the slot swap is seamless.
 *
 * `disabledReason` (RUYI-626 stage 1): when set, the entry renders disabled
 * and the tooltip explains why — the direct-connect architecture ships
 * mobile-first, so desktop/web entries must say so explicitly instead of
 * failing silently (or succeeding against the retired relay path).
 */
export function VoiceButton({
  onStart,
  disabled,
  disabledReason,
}: {
  onStart: () => void;
  disabled?: boolean;
  /** Non-null renders the entry disabled with this tooltip text. */
  disabledReason?: string | null;
}) {
  const { t } = useT("voice");
  const unavailable = disabledReason != null;
  const button = (
    <Button
      size="icon-sm"
      className="rounded-full"
      disabled={disabled || unavailable}
      onPointerDown={(event) => event.preventDefault()}
      onMouseDown={(event) => event.preventDefault()}
      onClick={onStart}
      aria-label={unavailable ? disabledReason : t(($) => $.button.start)}
      title={unavailable ? disabledReason : t(($) => $.button.start)}
    >
      <Mic className="size-4" aria-hidden="true" />
    </Button>
  );
  return (
    <Tooltip>
      <TooltipTrigger render={button} />
      <TooltipContent side="top">
        {unavailable ? disabledReason : t(($) => $.button.start)}
      </TooltipContent>
    </Tooltip>
  );
}
