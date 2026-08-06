"use client";

import { CalendarDays, ChevronDown, FolderKanban, SlidersHorizontal } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubTrigger,
  DropdownMenuSubContent,
} from "@multica/ui/components/ui/dropdown-menu";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { ProjectIcon } from "../../projects/components/project-icon";
import { useT } from "../../i18n";
import { ALL_PROJECTS, TIME_RANGES, type TimeRange } from "./dashboard-shared";

type DashboardProject = { id: string; title: string; icon: string | null };

/**
 * Page-scoped time range.
 *
 * A button that states the current value plus a single-select menu, rather
 * than five permanently-expanded segments. The five segments were the widest
 * thing in the header and the least informative: the value they encode is
 * already repeated in every KPI label ("Cost · 30D"). Collapsing them costs one
 * click per change and buys the header back.
 *
 * No "clear" entry: the range is a required parameter of every query on the
 * page, so it has no empty value to return to.
 */
export function TimeRangeFilter({
  days,
  onChange,
}: {
  days: TimeRange;
  onChange: (days: TimeRange) => void;
}) {
  const { t } = useT("usage");
  const current = TIME_RANGES.find((r) => r.days === days) ?? TIME_RANGES[2];

  return (
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
            <span className="tabular-nums">{current.label}</span>
            <ChevronDown className="size-3 text-muted-foreground" />
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="w-auto min-w-32">
        <DropdownMenuRadioGroup
          value={String(days)}
          onValueChange={(value) => onChange(Number(value) as TimeRange)}
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
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * Page-scoped filters, behind one entry point.
 *
 * Same grammar as the issues surface (`IssueDisplayControls`): neutral outline
 * while nothing is filtered, the filled `brand` tier plus a count once
 * something is, one submenu per dimension with its current value shown on the
 * trigger. Learning it on Issues is what makes it free here — and a new
 * dimension (agent, model, runtime) becomes one more submenu rather than one
 * more control competing for header width.
 *
 * `variant="brand"` rather than brand classes on top of `outline`: the outline
 * variant ships `dark:bg-input/30` and `hover:bg-muted`, which win the cascade
 * and repaint the chip neutral (MUL-4884).
 */
export function DashboardFilterMenu({
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
  const selected = projects.find((p) => p.id === projectValue);
  // A project id that no longer resolves (deleted project, or a stale id left
  // over from another workspace) counts as no filter — the same reading the
  // page applies when it derives the effective `projectId` for the queries, so
  // the chip cannot claim a filter the data is not actually narrowed by.
  const activeCount = selected ? 1 : 0;
  const hasFilters = activeCount > 0;

  return (
    <DropdownMenu>
      <Tooltip>
        <DropdownMenuTrigger
          render={
            <TooltipTrigger
              render={
                <Button
                  variant={hasFilters ? "brand" : "outline"}
                  size="sm"
                  className={hasFilters ? "gap-1 px-2.5" : "gap-1 px-2.5 text-muted-foreground"}
                >
                  <SlidersHorizontal className="size-3.5" />
                  <span>
                    {hasFilters
                      ? t(($) => $.filter.active_count, { count: activeCount })
                      : t(($) => $.filter.filter_label)}
                  </span>
                </Button>
              }
            />
          }
        />
        <TooltipContent side="bottom">{t(($) => $.filter.filter_label)}</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="w-auto min-w-44">
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <FolderKanban className="size-3.5" />
            <span className="flex-1">{t(($) => $.filter.project_label)}</span>
            {selected ? (
              <span className="max-w-32 truncate text-caption font-medium text-primary">
                {selected.title}
              </span>
            ) : null}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="max-h-72 w-auto min-w-52">
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
          </DropdownMenuSubContent>
        </DropdownMenuSub>

        {hasFilters ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => onProjectChange(ALL_PROJECTS)}>
              {t(($) => $.filter.clear)}
            </DropdownMenuItem>
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
