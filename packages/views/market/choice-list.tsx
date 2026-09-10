"use client";

import { Check } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";

/**
 * A single-select list of mutually exclusive options.
 *
 * `packages/ui` ships no radio-group primitive and this Issue may not add a
 * component library, so the semantics are written out by hand rather than
 * approximated with buttons: the container is a `radiogroup`, each row is a
 * `radio` carrying `aria-checked`, and arrow keys move the selection the way a
 * native group does. Getting this wrong would be worse than a plain listbox —
 * the two places this is used are the overwrite strategy and the visibility
 * choice, where a screen reader user must be able to tell which of two
 * consequential options is currently armed.
 */
export function ChoiceList<T extends string>({
  value,
  options,
  onChange,
  className,
  name,
}: {
  value: T;
  options: readonly { value: T; label: string; description?: string; tone?: "danger" }[];
  onChange: (value: T) => void;
  className?: string;
  /** Prefix for the generated element ids, so two groups can share a page. */
  name: string;
}) {
  const move = (delta: number) => {
    const index = options.findIndex((option) => option.value === value);
    // Wrapping keeps the group navigable from either end, matching a native
    // radio group rather than dead-ending at the last option.
    const next = options[(index + delta + options.length) % options.length];
    if (next) onChange(next.value);
  };

  return (
    <div
      role="radiogroup"
      className={cn("space-y-2", className)}
      onKeyDown={(event) => {
        if (event.key === "ArrowDown" || event.key === "ArrowRight") {
          event.preventDefault();
          move(1);
        } else if (event.key === "ArrowUp" || event.key === "ArrowLeft") {
          event.preventDefault();
          move(-1);
        }
      }}
    >
      {options.map((option) => {
        const selected = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            id={`${name}-${option.value}`}
            aria-checked={selected}
            // Only the selected row is in the tab order, so Tab leaves the
            // group instead of walking every option.
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(option.value)}
            className={cn(
              "flex w-full items-start gap-2.5 rounded-lg border px-3 py-2.5 text-left transition-colors",
              selected
                ? option.tone === "danger"
                  ? "border-destructive bg-destructive/5"
                  : "border-primary bg-primary/5"
                : "border-surface-border hover:bg-muted/40",
            )}
          >
            <span
              className={cn(
                "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border",
                selected
                  ? option.tone === "danger"
                    ? "border-destructive bg-destructive text-destructive-foreground"
                    : "border-primary bg-primary text-primary-foreground"
                  : "border-surface-border",
              )}
              aria-hidden="true"
            >
              {selected ? <Check className="h-2.5 w-2.5" /> : null}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block text-body">{option.label}</span>
              {option.description ? (
                <span className="mt-0.5 block text-caption leading-5 text-muted-foreground">
                  {option.description}
                </span>
              ) : null}
            </span>
          </button>
        );
      })}
    </div>
  );
}
