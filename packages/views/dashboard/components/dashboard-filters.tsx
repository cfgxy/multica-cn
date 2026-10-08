"use client";

import { useMemo, useState } from "react";
import type { Matcher } from "react-day-picker";
import {
  CalendarDays,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  FolderKanban,
  Undo2,
} from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Calendar } from "@multica/ui/components/ui/calendar";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@multica/ui/components/ui/popover";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  dateOnlyToLocalDate,
  toDateOnly,
} from "@multica/core/issues/date";
import { ProjectIcon } from "../../projects/components/project-icon";
import { formatShortDate } from "../../runtimes/utils";
import { useT } from "../../i18n";
import { windowLength } from "../window";
import {
  ALL_PROJECTS,
  TIME_RANGES,
  canShiftNext,
  isCurrentWindow,
  type StatWindow,
  type TimeRange,
} from "./dashboard-shared";

type DashboardProject = { id: string; title: string; icon: string | null };

/**
 * What the window control holds: a quick range sitting `offset` whole periods
 * back from today, or an explicitly picked date range. Both resolve to one
 * `StatWindow` — the page owns that derivation, the control only reports
 * intent.
 */
export type WindowSelection =
  | { kind: "quick"; days: TimeRange; offset: number }
  | { kind: "custom"; window: StatWindow };

// The server rejects explicit windows wider than a year (365 inclusive days)
// with a 400; the picker disables the days that would cross the cap instead of
// letting the user pick their way into an error response.
const MAX_WINDOW_DAYS = 365;

/**
 * Page-scoped stat window: period navigation flanking a picker that states
 * the current window. The quick-range menu stays for one-click access; the
 * calendar underneath picks any past span. ‹ / › slide by one window of the
 * CURRENT length — 1d steps a day, 30d slides in 30-day blocks — and "back
 * to current" re-anchors the same length on today, because a control that
 * can leave "now" owes the user a one-click way home.
 */
export function WindowFilter({
  selection,
  window,
  today,
  onQuickRange,
  onCustomRange,
  onShift,
  onBackToCurrent,
}: {
  selection: WindowSelection;
  window: StatWindow;
  today: string;
  onQuickRange: (days: TimeRange) => void;
  onCustomRange: (w: StatWindow) => void;
  onShift: (periods: number) => void;
  onBackToCurrent: () => void;
}) {
  const { t } = useT("usage");
  const atCurrent = isCurrentWindow(window, today);
  const label =
    selection.kind === "quick" && atCurrent
      ? (TIME_RANGES.find((r) => r.days === selection.days)?.label ??
        `${selection.days}d`)
      : `${formatShortDate(window.start)} – ${formatShortDate(window.end)}`;

  return (
    <div
      className="flex items-center gap-1"
      role="group"
      aria-label={t(($) => $.filter.period_label)}
    >
      <Button
        variant="outline"
        size="icon-sm"
        aria-label={t(($) => $.filter.period_prev)}
        onClick={() => onShift(-1)}
      >
        <ChevronLeft />
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="outline"
              size="sm"
              aria-label={t(($) => $.filter.period_label)}
              className="gap-1 px-2.5"
            >
              <CalendarDays className="size-3.5 text-muted-foreground" />
              <span className="tabular-nums">{label}</span>
              <ChevronDown className="size-3 text-muted-foreground" />
            </Button>
          }
        />
        <DropdownMenuContent align="end" className="w-auto min-w-40">
          <DropdownMenuRadioGroup
            value={selection.kind === "quick" ? String(selection.days) : ""}
            onValueChange={(value) => onQuickRange(Number(value) as TimeRange)}
          >
            {TIME_RANGES.map((range) => (
              <DropdownMenuRadioItem
                key={range.days}
                value={String(range.days)}
                className="tabular-nums"
              >
                {range.label}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
          <CustomRangePicker
            window={window}
            today={today}
            onApply={onCustomRange}
          />
        </DropdownMenuContent>
      </DropdownMenu>
      <Button
        variant="outline"
        size="icon-sm"
        aria-label={t(($) => $.filter.period_next)}
        disabled={!canShiftNext(window, today)}
        onClick={() => onShift(1)}
      >
        <ChevronRight />
      </Button>
      {!atCurrent && (
        <Button
          variant="ghost"
          size="sm"
          aria-label={t(($) => $.filter.period_back_to_current)}
          title={t(($) => $.filter.period_back_to_current)}
          className="gap-1 px-2 text-muted-foreground"
          onClick={onBackToCurrent}
        >
          <Undo2 className="size-3.5" />
        </Button>
      )}
    </div>
  );
}

/**
 * The custom-range half of the picker: a nested popover with the same
 * range-mode Calendar the issues header uses (one precedent for the whole
 * app). Future days are disabled — there is no data there — and so are days
 * that would push the span past the server's 365-day cap once a start is
 * chosen.
 */
function CustomRangePicker({
  window,
  today,
  onApply,
}: {
  window: StatWindow;
  today: string;
  onApply: (w: StatWindow) => void;
}) {
  const { t } = useT("usage");
  const [open, setOpen] = useState(false);
  const [range, setRange] = useState<{ from: Date; to?: Date } | undefined>(
    () => ({
      from: dateOnlyToLocalDate(window.start) ?? new Date(),
      to: dateOnlyToLocalDate(window.end),
    }),
  );

  const disabled = useMemo(() => {
    const matchers: Matcher[] = [];
    const todayDate = dateOnlyToLocalDate(today);
    if (todayDate) matchers.push({ after: todayDate });
    const fromIso = range?.from ? toDateOnly(range.from) : null;
    if (fromIso) {
      matchers.push(
        (date: Date) =>
          windowLength({ start: fromIso, end: toDateOnly(date) }) >
          MAX_WINDOW_DAYS,
      );
    }
    return matchers;
  }, [range?.from, today]);

  const apply = () => {
    const from = range?.from;
    if (!from) return;
    // A single click (no second day yet) means a one-day window — the same
    // reading the issues date filter gives an unpaired start.
    const to = range?.to ?? from;
    const [start, end] =
      toDateOnly(from) <= toDateOnly(to)
        ? [toDateOnly(from), toDateOnly(to)]
        : [toDateOnly(to), toDateOnly(from)];
    onApply({ start, end });
    setOpen(false);
  };

  return (
    <div className="p-1">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          render={
            <Button
              variant="ghost"
              size="sm"
              className="h-7 w-full justify-start px-2 text-body font-normal"
            >
              {t(($) => $.filter.period_custom)}
            </Button>
          }
        />
        <PopoverContent align="start" side="left" className="w-auto gap-0 p-0">
          <Calendar
            mode="range"
            selected={range}
            onSelect={(next) =>
              setRange(next?.from ? { from: next.from, to: next.to } : undefined)
            }
            disabled={disabled}
            captionLayout="dropdown"
          />
          <div className="flex justify-end border-t p-2">
            <Button size="sm" onClick={apply} disabled={!range?.from}>
              {t(($) => $.filter.period_apply)}
            </Button>
          </div>
        </PopoverContent>
      </Popover>
    </div>
  );
}

/**
 * Page-scoped project filter.
 *
 * A single-level dropdown, deliberately not a generic "Filter" menu: with one
 * dimension, a wrapper menu only hides what is filterable behind an extra
 * hover. The trigger states the current value like `WindowFilter` does:
 * a neutral outline throughout, showing the selected project's own icon and
 * name once narrowed — the named value is the active-state signal, no filled
 * tier. If a second dimension ships (agent, model, runtime), fold this back
 * into a combined menu in the `IssueDisplayControls` grammar.
 */
export function ProjectFilter({
  projects,
  projectValue,
  onProjectChange,
}: {
  projects: DashboardProject[];
  projectValue: string;
  onProjectChange: (value: string) => void;
}) {
  const { t } = useT("usage");
  const allLabel = t(($) => $.filter.all_projects);
  // A project id that no longer resolves (deleted project, or a stale id left
  // over from another workspace) counts as no filter — the same reading the
  // page applies when it derives the effective `projectId` for the queries, so
  // the chip cannot claim a filter the data is not actually narrowed by.
  const selected = projects.find((p) => p.id === projectValue);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="outline"
            size="sm"
            aria-label={t(($) => $.filter.project_label)}
            className={selected ? "gap-1 px-2.5" : "gap-1 px-2.5 text-muted-foreground"}
          >
            {selected ? (
              <ProjectIcon project={selected} size="sm" />
            ) : (
              <FolderKanban className="size-3.5" />
            )}
            <span className="max-w-40 truncate">
              {selected ? selected.title : allLabel}
            </span>
            <ChevronDown className="size-3 text-muted-foreground" />
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="max-h-72 w-auto min-w-52">
        <DropdownMenuRadioGroup
          value={projectValue}
          onValueChange={(value) => onProjectChange(value ?? ALL_PROJECTS)}
        >
          <DropdownMenuRadioItem value={ALL_PROJECTS}>
            <FolderKanban className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate">{allLabel}</span>
          </DropdownMenuRadioItem>
          {projects.map((project) => (
            <DropdownMenuRadioItem key={project.id} value={project.id}>
              <ProjectIcon project={project} size="sm" />
              <span className="truncate">{project.title}</span>
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
