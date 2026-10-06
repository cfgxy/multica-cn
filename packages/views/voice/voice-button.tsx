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
 */
export function VoiceButton({
  onStart,
  disabled,
}: {
  onStart: () => void;
  disabled?: boolean;
}) {
  const { t } = useT("voice");
  const button = (
    <Button
      size="icon-sm"
      className="rounded-full"
      disabled={disabled}
      onPointerDown={(event) => event.preventDefault()}
      onMouseDown={(event) => event.preventDefault()}
      onClick={onStart}
      aria-label={t(($) => $.button.start)}
      title={t(($) => $.button.start)}
    >
      <Mic className="size-4" aria-hidden="true" />
    </Button>
  );
  return (
    <Tooltip>
      <TooltipTrigger render={button} />
      <TooltipContent side="top">{t(($) => $.button.start)}</TooltipContent>
    </Tooltip>
  );
}
