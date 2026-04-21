"use client";

import { useState } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";

/**
 * Step 0 — the one-shot product intro shown to users entering
 * onboarding for the first time. Not a persisted step: clicking
 * Get started advances the store's `current_step` to
 * `"questionnaire"`, after which any refresh / resume goes straight
 * to the questionnaire. Returning users who already have progress
 * never see this screen again (the flow orchestrator skips it based
 * on store state).
 *
 * The copy answers three questions every first-time user has:
 *   1. What is Multica?           → "AI agents as first-class teammates"
 *   2. What can I do with it?     → "Assign tasks, chat, share context"
 *   3. Is this worth my time?     → "Takes about 3 minutes"
 *
 * Deliberately text-only for now. Later passes can add an
 * illustration / hero loop; the structure leaves vertical room.
 */
export function StepWelcome({ onNext }: { onNext: () => void | Promise<void> }) {
  const [submitting, setSubmitting] = useState(false);
  const handleClick = async () => {
    if (submitting) return;
    setSubmitting(true);
    try {
      await onNext();
    } finally {
      setSubmitting(false);
    }
  };
  // min-h + justify-center gives Welcome its own vertical centering.
  // The outer shell no longer applies `my-auto` (removed so all other
  // steps align to the top around a stable StepHeader anchor), so
  // Welcome compensates internally — short copy needs the space or
  // it clumps awkwardly at the top of the viewport.
  return (
    <div className="flex min-h-[60vh] w-full flex-col items-center justify-center">
      <div className="flex w-full max-w-md flex-col items-center gap-8 text-center">
        <div className="flex flex-col gap-3">
          <h1 className="text-4xl font-semibold tracking-tight">
            Welcome to Multica
          </h1>
          <p className="text-lg text-muted-foreground">
            AI agents as first-class teammates.
          </p>
        </div>

        <p className="text-sm leading-relaxed text-muted-foreground">
          Assign them tasks, chat with them, and let your workspace,
          agents, and conversations all build on each other — so every
          interaction makes them smarter.
        </p>

        <div className="flex flex-col items-center gap-3">
          <Button size="lg" onClick={handleClick} disabled={submitting}>
            {submitting && <Loader2 className="h-4 w-4 animate-spin" />}
            Get started
          </Button>
          <p className="text-xs text-muted-foreground">
            Takes about 3 minutes.
          </p>
        </div>
      </div>
    </div>
  );
}
