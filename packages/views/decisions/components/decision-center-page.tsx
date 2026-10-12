"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useStore } from "zustand";
import { Columns3, Filter, FilterX, Gavel, List } from "lucide-react";
import {
  workspaceDecisionInboxQueryOptions,
} from "@multica/core/issues/decisions";
import {
  useCurrentWorkspace,
} from "@multica/core/paths";
import {
  decisionCenterPrefsStore,
  type DecisionCenterViewMode,
} from "@multica/core/issues/stores/decision-center-prefs-store";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { PageHeader } from "../../layout/page-header";
import { useT } from "../../i18n";
import { DecisionListView } from "./decision-list-view";
import { DecisionBoardView } from "./decision-board-view";
import {
  DECISION_STATUS_CONFIG,
  DECISION_STATUS_ORDER,
} from "./decision-status-config";

// Decision Center (RUYI-547): the workspace-wide aggregation of decision
// cards, rebuilt on the issues surface's display model — a toolbar row with
// a status filter and a list/board toggle (same control shapes as the issues
// header), a sectioned list, and a board of status columns. Status is carried
// by the view: section headers and column headers show the server's
// workspace-wide counts, so the numbers stay true even when the list window
// is bounded.

const VIEW_ICON: Record<DecisionCenterViewMode, typeof List> = {
  list: List,
  board: Columns3,
};

export function DecisionCenterPage() {
  const { t } = useT("decisions");
  const workspace = useCurrentWorkspace();

  const viewMode = useStore(decisionCenterPrefsStore, (s) => s.viewMode);
  const hiddenStatuses = useStore(decisionCenterPrefsStore, (s) => s.hiddenStatuses);
  const setViewMode = useStore(decisionCenterPrefsStore, (s) => s.setViewMode);
  const toggleStatusHidden = useStore(decisionCenterPrefsStore, (s) => s.toggleStatusHidden);
  const showAllStatuses = useStore(decisionCenterPrefsStore, (s) => s.showAllStatuses);

  const { data, isPending, isError } = useQuery(
    workspaceDecisionInboxQueryOptions(workspace?.id),
  );

  const visibleStatuses = useMemo(
    () => DECISION_STATUS_ORDER.filter((s) => !hiddenStatuses.includes(s)),
    [hiddenStatuses],
  );
  const filteredItems = useMemo(
    () => (data?.items ?? []).filter((item) => visibleStatuses.includes(item.status)),
    [data, visibleStatuses],
  );
  const activeFilterCount = hiddenStatuses.length;
  const isFilteredEmpty = activeFilterCount > 0 && filteredItems.length === 0;
  const isEmpty =
    !isPending && !isError && activeFilterCount === 0 && (data?.items.length ?? 0) === 0;

  const ViewIcon = VIEW_ICON[viewMode];

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader>
        <Gavel className="h-4 w-4 text-muted-foreground" />
        <h1 className="text-body font-medium">{t(($) => $.page.breadcrumb)}</h1>
      </PageHeader>

      <div
        className="flex h-11 shrink-0 items-center gap-1 px-4"
        data-testid="decision-center-toolbar"
      >
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                variant={activeFilterCount > 0 ? "default" : "outline"}
                size="sm"
                data-testid="decision-center-filter-trigger"
                className={cn(
                  "h-7 gap-1",
                  activeFilterCount > 0 &&
                    "bg-brand text-white hover:bg-brand/90",
                )}
              >
                <Filter className="size-3.5" />
                <span className="hidden md:inline">
                  {activeFilterCount > 0
                    ? t(($) => $.filter.active_count, { count: activeFilterCount })
                    : t(($) => $.filter.tooltip)}
                </span>
              </Button>
            }
          />
          <DropdownMenuContent align="start" className="w-56">
            <DropdownMenuGroup>
              <DropdownMenuLabel>{t(($) => $.filter.tooltip)}</DropdownMenuLabel>
            </DropdownMenuGroup>
            {DECISION_STATUS_ORDER.map((status) => {
              const cfg = DECISION_STATUS_CONFIG[status];
              const Icon = cfg.icon;
              return (
                <DropdownMenuCheckboxItem
                  key={status}
                  checked={!hiddenStatuses.includes(status)}
                  onCheckedChange={() => toggleStatusHidden(status)}
                  closeOnClick={false}
                  data-testid={`decision-filter-${status}`}
                >
                  <Icon className={`size-3.5 ${cfg.iconColor}`} />
                  {t(($) => $.section[status])}
                </DropdownMenuCheckboxItem>
              );
            })}
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={showAllStatuses}>
              <FilterX className="size-3.5" />
              {t(($) => $.filter.reset)}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        <div className="ml-auto flex items-center gap-1">
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  variant="outline"
                  size="sm"
                  className="h-7 gap-1"
                  data-testid="decision-center-view-trigger"
                >
                  <ViewIcon className="size-3.5" />
                  <span className="hidden md:inline">
                    {t(($) => $.view[viewMode])}
                  </span>
                </Button>
              }
            />
            <DropdownMenuContent align="end">
              <DropdownMenuGroup>
                <DropdownMenuLabel>{t(($) => $.view.section)}</DropdownMenuLabel>
              </DropdownMenuGroup>
              <DropdownMenuRadioGroup
                value={viewMode}
                onValueChange={(v) => setViewMode(v as DecisionCenterViewMode)}
              >
                <DropdownMenuRadioItem value="board">
                  <Columns3 />
                  {t(($) => $.view.board)}
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="list">
                  <List />
                  {t(($) => $.view.list)}
                </DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      {isPending && (
        <div className="flex flex-col gap-2 px-4 py-3" data-testid="decision-center-loading">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-3/4" />
        </div>
      )}

      {isError && (
        <div className="py-10 text-center text-caption text-muted-foreground" data-testid="decision-center-error">
          {t(($) => $.page.load_failed)}
        </div>
      )}

      {isFilteredEmpty && (
        <div
          className="flex flex-1 min-h-0 flex-col items-center justify-center gap-3 text-muted-foreground"
          data-testid="decision-filtered-empty"
        >
          <FilterX className="h-10 w-10 text-faint-foreground" />
          <p className="text-body">{t(($) => $.filtered_empty.title)}</p>
          <p className="text-caption">{t(($) => $.filtered_empty.hint)}</p>
          <Button
            variant="outline"
            size="sm"
            className="mt-1"
            onClick={showAllStatuses}
            data-testid="decision-filter-clear"
          >
            {t(($) => $.filtered_empty.clear_button)}
          </Button>
        </div>
      )}

      {isEmpty && (
        <div
          className="flex flex-1 min-h-0 flex-col items-center justify-center gap-2 py-20 text-muted-foreground"
          data-testid="decision-center-empty"
        >
          <Gavel className="h-10 w-10 text-faint-foreground" />
          <p className="text-body">{t(($) => $.page.empty_title)}</p>
          <p className="text-caption">{t(($) => $.page.empty_description)}</p>
        </div>
      )}

      {!isPending && !isError && !isFilteredEmpty && !isEmpty && (
        <>
          {viewMode === "list" && (
            <DecisionListView
              items={data?.items ?? []}
              counts={data?.counts ?? { open: 0, answered: 0, cancelled: 0 }}
              visibleStatuses={visibleStatuses}
            />
          )}
          {viewMode === "board" && (
            <DecisionBoardView
              items={data?.items ?? []}
              counts={data?.counts ?? { open: 0, answered: 0, cancelled: 0 }}
              visibleStatuses={visibleStatuses}
            />
          )}
        </>
      )}
    </div>
  );
}
