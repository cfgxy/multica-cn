"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CirclePause, Filter } from "lucide-react";
import {
  workspaceSuspensionsQueryOptions,
} from "@multica/core/issues/suspensions";
import { useCurrentWorkspace } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { PageHeader } from "../../layout/page-header";
import { useT } from "../../i18n";
import { SuspensionRow } from "./suspension-row";

// Suspension Center (RUYI-690): the workspace-wide aggregation of issue
// suspensions, mirroring the Decision Center's shape — a toolbar row with a
// status filter and a flat row list. The server filters; the client only
// renders, so what it shows is always the server's answer.

const STATUS_FILTERS = ["active", "resumed", "cancelled", "all"] as const;
type StatusFilter = (typeof STATUS_FILTERS)[number];

export function SuspensionCenterPage() {
  const { t } = useT("suspensions");
  const workspace = useCurrentWorkspace();
  const [filter, setFilter] = useState<StatusFilter>("active");

  const { data, isPending, isError } = useQuery(
    workspaceSuspensionsQueryOptions(workspace?.id, filter),
  );

  const rows = data?.suspensions ?? [];

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader>
        <CirclePause className="h-4 w-4 text-muted-foreground" />
        <h1 className="text-body font-medium">{t(($) => $.page.breadcrumb)}</h1>
      </PageHeader>

      <div
        className="flex h-11 shrink-0 items-center gap-1 px-4"
        data-testid="suspension-center-toolbar"
      >
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                variant={filter !== "active" ? "default" : "outline"}
                size="sm"
                data-testid="suspension-center-filter-trigger"
                className="h-7 gap-1"
              >
                <Filter className="size-3.5" />
                <span className="hidden md:inline">
                  {filter === "all"
                    ? t(($) => $.filter.all)
                    : t(($) => $.filter[filter])}
                </span>
              </Button>
            }
          />
          <DropdownMenuContent align="start" className="w-44">
            <DropdownMenuRadioGroup
              value={filter}
              onValueChange={(v) => setFilter(v as StatusFilter)}
            >
              {STATUS_FILTERS.map((s) => (
                <DropdownMenuRadioItem
                  key={s}
                  value={s}
                  data-testid={`suspension-filter-${s}`}
                >
                  {s === "all" ? t(($) => $.filter.all) : t(($) => $.filter[s])}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>

        {data != null && (
          <span
            className="ml-1 text-caption text-muted-foreground"
            data-testid="suspension-center-total"
          >
            {data.total}
          </span>
        )}
      </div>

      {isPending && (
        <div className="flex flex-col gap-2 px-4 py-3" data-testid="suspension-center-loading">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-3/4" />
        </div>
      )}

      {isError && (
        <div
          className="py-10 text-center text-caption text-muted-foreground"
          data-testid="suspension-center-error"
        >
          {t(($) => $.page.load_failed)}
        </div>
      )}

      {!isPending && !isError && rows.length === 0 && (
        <div
          className="flex flex-1 min-h-0 flex-col items-center justify-center gap-2 py-20 text-muted-foreground"
          data-testid="suspension-center-empty"
        >
          <CirclePause className="h-10 w-10 text-faint-foreground" />
          <p className="text-body">{t(($) => $.page.empty_title)}</p>
          <p className="text-caption">{t(($) => $.page.empty_description)}</p>
        </div>
      )}

      {!isPending && !isError && rows.length > 0 && (
        <div className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto px-4 pb-6">
          {rows.map((s) => (
            <SuspensionRow key={s.id} suspension={s} />
          ))}
        </div>
      )}
    </div>
  );
}
