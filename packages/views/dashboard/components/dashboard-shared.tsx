"use client";

import {
  NumberFlow,
  NumberFlowGroup,
} from "@multica/ui/components/ui/number-flow";
import { formatDuration } from "../utils";

// Period vocabulary and window arithmetic live in ../window — the quick
// ranges and the period navigation share one module so the selector and the
// page can never disagree about what a range means. Re-exported here so the
// card components keep their import site.
export {
  TIME_RANGES,
  type TimeRange,
  type Dim,
  dimsForDays,
  dimsForWindowLength,
  type StatWindow,
  quickWindow,
  shiftedWindow,
  windowLength,
  shiftWindow,
  canShiftNext,
  isCurrentWindow,
} from "../window";

/** Sentinel for "no project filter" — kept distinct from the empty string so
 *  it survives a refactor that ever lets a project be slug-keyed. */
export const ALL_PROJECTS = "__all__";

/**
 * Card-local segmented control — the pill toggle used *inside* a card to pick
 * what that card shows. The page header deliberately uses outline Buttons
 * instead, so the shape of a control tells you its scope: pills change one
 * card, buttons change the page.
 *
 * shadcn's Tabs is wired for full tab pages with ARIA semantics the compact
 * toolbar pill doesn't need. Which option is active was expressed only as a
 * colour swap, which no screen reader can see, so `aria-pressed` carries it
 * too. `label` is required rather than optional because a naked group of
 * toggle buttons is announced without saying WHAT it toggles — "Rate, pressed"
 * is useless until you know the group is the offender ranking. Toggle buttons
 * rather than a radiogroup: a radiogroup owes the user arrow-key roving focus,
 * and these are tab stops wherever they appear in the page.
 */
export function Segmented<T extends string | number>({
  value,
  onChange,
  options,
  label,
}: {
  value: T;
  onChange: (v: T) => void;
  options: readonly { label: string; value: T }[];
  label: string;
}) {
  return (
    <div
      role="group"
      aria-label={label}
      className="inline-flex items-center gap-0.5 rounded-md bg-muted p-0.5"
    >
      {options.map((o) => (
        <button
          key={String(o.value)}
          type="button"
          aria-pressed={o.value === value}
          onClick={() => onChange(o.value)}
          className={`rounded-sm px-2.5 py-1 text-caption font-medium transition-colors ${
            o.value === value
              ? "bg-background text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function DurationNumberFlow({
  seconds,
  lessThanMinuteLabel,
  locales,
}: {
  seconds: number;
  lessThanMinuteLabel: string;
  locales?: Intl.LocalesArgument;
}) {
  const label = formatDuration(seconds, lessThanMinuteLabel);
  const parts = Array.from(label.matchAll(/(\d+)([a-z]+)/gi), (match) => ({
    value: Number(match[1]),
    unit: match[2] ?? "",
  }));

  if (parts.length === 0) return label;

  return (
    <>
      <span className="sr-only">{label}</span>
      <NumberFlowGroup>
        <span aria-hidden className="inline-flex items-baseline gap-1">
          {parts.map((part) => (
            <NumberFlow
              key={part.unit}
              value={part.value}
              locales={locales}
              suffix={part.unit}
              format={{ maximumFractionDigits: 0, useGrouping: false }}
            />
          ))}
        </span>
      </NumberFlowGroup>
    </>
  );
}
