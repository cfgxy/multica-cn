"use client";

import { useState } from "react";
import { ArrowLeft, Cloud } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";

const DESCRIPTION_MAX_LENGTH = 500;

export function StepCloudWaitlist({
  onBack,
  onSubmit,
}: {
  onBack: () => void;
  onSubmit: (email: string, description: string | null) => void;
}) {
  const [email, setEmail] = useState("");
  const [description, setDescription] = useState("");
  const valid = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email);

  const trimmedDesc = description.trim();

  return (
    <div className="flex w-full flex-col gap-6">
      <button
        type="button"
        onClick={onBack}
        className="flex items-center gap-1.5 self-start text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to options
      </button>

      <div className="flex flex-col items-center gap-3 text-center">
        <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-muted">
          <Cloud className="h-5 w-5 text-muted-foreground" />
        </div>
        <h1 className="text-2xl font-semibold tracking-tight">
          Join the cloud waitlist
        </h1>
        <p className="text-sm text-muted-foreground">
          Cloud runtimes are coming. We&apos;ll email you when they&apos;re ready.
        </p>
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!valid) return;
          onSubmit(email, trimmedDesc === "" ? null : trimmedDesc);
        }}
        className="flex flex-col gap-4"
      >
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="waitlist-email">Email</Label>
          <Input
            id="waitlist-email"
            type="email"
            placeholder="you@example.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoFocus
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <div className="flex items-baseline justify-between">
            <Label htmlFor="waitlist-description">
              What would you like to use Multica for?{" "}
              <span className="text-xs font-normal text-muted-foreground">
                (optional)
              </span>
            </Label>
            <span className="text-xs text-muted-foreground">
              {description.length}/{DESCRIPTION_MAX_LENGTH}
            </span>
          </div>
          <Textarea
            id="waitlist-description"
            placeholder="e.g. Coordinate a small team of agents to triage bugs, draft release notes, and keep a changelog up to date."
            value={description}
            maxLength={DESCRIPTION_MAX_LENGTH}
            onChange={(e) => setDescription(e.target.value)}
            rows={4}
          />
          <p className="text-xs text-muted-foreground">
            Helps us prioritize what cloud runtimes should support first.
          </p>
        </div>

        <Button type="submit" size="lg" disabled={!valid}>
          Join waitlist
        </Button>
      </form>
    </div>
  );
}
