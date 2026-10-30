"use client";

import { useMemo, useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useStore } from "zustand";
import {
  ChevronDown,
  ChevronRight,
  Gavel,
  Search,
  ShieldCheck,
  X,
} from "lucide-react";
import {
  workspaceDecisionInboxPageOptions,
} from "@multica/core/issues/decisions";
import {
  workspaceDecisionRequestsHistoryPageOptions,
  workspaceDecisionRequestsQueryOptions,
} from "@multica/core/issues/decision-requests";
import {
  useCurrentWorkspace,
} from "@multica/core/paths";
import {
  decisionCenterPrefsStore,
  type DecisionCenterStatusFilter,
} from "@multica/core/issues/stores/decision-center-prefs-store";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import type { IssueDecisionStatus, WorkspaceDecisionInboxItem } from "@multica/core/types";
import { useDebouncedValue } from "../../common/use-debounced-value";
import { PageHeader } from "../../layout/page-header";
import { useT } from "../../i18n";
import { DecisionListView } from "./decision-list-view";
import { DecisionRequestRow } from "./decision-request-row";
import { DecisionDetailSheet } from "./decision-detail-sheet";
import {
  DECISION_STATUS_CONFIG,
  DECISION_STATUS_ORDER,
} from "./decision-status-config";

// Decision Center (RUYI-547), refolded by RUYI-864: the page keeps only the
// decision list. The board view and its pseudo-drag went away, and the
// multi-status "hide" filter was replaced by one single-value status window
// (全部/未决策/已决策/已失效) plus a debounced keyword search — both owned by
// the server, so the list, the section counts, and every empty state follow
// the same filter window. The authorization queue above the list is a
// single card: the server returns exactly the oldest pending row, so
// processing one auto-advances the next and the area vanishes when the
// queue empties — no client-side queue state. A low-key collapsed history
// entry shares the toolbar space for the processed requests.

const STATUS_FILTER_OPTIONS: DecisionCenterStatusFilter[] = [
  "all",
  ...DECISION_STATUS_ORDER,
];

export function DecisionCenterPage() {
  const { t } = useT("decisions");
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id;

  const [selectedDecision, setSelectedDecision] =
    useState<WorkspaceDecisionInboxItem | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);

  // The persisted single status window (one real card status or "all").
  const statusFilter = useStore(
    decisionCenterPrefsStore,
    (s) => s.statusFilter,
  );
  const setStatusFilter = useStore(
    decisionCenterPrefsStore,
    (s) => s.setStatusFilter,
  );

  // Session-scoped keyword: a stale search string on the next visit is
  // pure noise, so it is kept out of the persisted prefs.
  const [searchInput, setSearchInput] = useState("");
  const searchQ = useDebouncedValue(searchInput.trim(), 250);

  const status = statusFilter === "all" ? undefined : statusFilter;
  const q = searchQ || undefined;
  const hasWindow = status !== undefined || q !== undefined;

  // The decision list, keyset-paged: the server walks (created_at, id)
  // under the filter window, and "Load more" fetches the next page.
  const pageQuery = useInfiniteQuery(
    workspaceDecisionInboxPageOptions(wsId, { status, q }),
  );
  const items = useMemo(
    () => (pageQuery.data?.pages ?? []).flatMap((page) => page.items),
    [pageQuery.data],
  );
  // Counts ride the last fetched page — they are scoped to the filter
  // window (status + keyword, never the page window), so section headers
  // stay true after paging.
  const counts = pageQuery.data?.pages.at(-1)?.counts ?? {
    open: 0,
    answered: 0,
    cancelled: 0,
  };

  // The pending authorization queue: one row, the server's oldest pending
  // (order=asc, limit=1). Approve/deny invalidates the whole request
  // subtree (see the answer mutation in DecisionRequestRow), so the next
  // oldest row slides into the card and the area vanishes when none is
  // left. The row's operable flag carries the processing/validity split —
  // read-only projections still render with their hint.
  const { data: requestsData, isPending: requestsPending } = useQuery(
    workspaceDecisionRequestsQueryOptions(wsId, {
      status: "pending",
      order: "asc",
      limit: 1,
    }),
  );
  const pendingRow = requestsData?.items[0];
  // RUYI-864 rework: the entry's total is the server's `history` count —
  // every non-pending row (approved ∪ closed ∪ executed). It is the SAME
  // set the history window below walks, so an "approved, waiting on the
  // other space" row keeps the entry visible even though the queue's own
  // single-card query (status=pending) already stopped showing it. Never
  // recombine bucket counts client-side.
  const historyTotal = requestsData?.counts.history ?? 0;

  // The collapsed history entry: the server's terminal-state window
  // (status=history, newest-first), keyset-paged and lazily fetched on open.
  // Pending rows never show up here — the window excludes them at the
  // source, and the queue owns them.
  const [historyOpen, setHistoryOpen] = useState(false);
  const historyPageQuery = useInfiniteQuery({
    ...workspaceDecisionRequestsHistoryPageOptions(wsId),
    enabled: historyOpen,
  });
  const historyRows = useMemo(
    () => (historyPageQuery.data?.pages ?? []).flatMap((page) => page.items),
    [historyPageQuery.data],
  );

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader>
        <Gavel className="h-4 w-4 text-muted-foreground" />
        <h1 className="text-body font-medium">{t(($) => $.page.breadcrumb)}</h1>
      </PageHeader>

      <div
        className="flex h-11 shrink-0 items-center gap-2 px-4"
        data-testid="decision-center-toolbar"
      >
        <div
          role="group"
          className="flex items-center gap-1"
          data-testid="decision-status-filter"
        >
          {STATUS_FILTER_OPTIONS.map((value) => {
            const active = statusFilter === value;
            const Icon =
              value === "all" ? undefined : DECISION_STATUS_CONFIG[value].icon;
            return (
              <Button
                key={value}
                type="button"
                variant={active ? "default" : "outline"}
                size="sm"
                className={cn(
                  "h-7 gap-1",
                  active && "bg-brand text-white hover:bg-brand/90",
                )}
                onClick={() => setStatusFilter(value)}
                data-testid={`decision-filter-${value}`}
              >
                {Icon ? <Icon className="size-3.5" /> : null}
                <span className="hidden md:inline">
                  {value === "all"
                    ? t(($) => $.section.all)
                    : t(($) => $.section[value as IssueDecisionStatus])}
                </span>
              </Button>
            );
          })}
        </div>

        <div className="relative ml-auto w-64" data-testid="decision-search">
          <Search
            className="absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
            aria-hidden
          />
          <Input
            value={searchInput}
            onChange={(event) => setSearchInput(event.target.value)}
            aria-label={t(($) => $.search.placeholder)}
            placeholder={t(($) => $.search.placeholder)}
            className="h-7 pl-7 pr-7 text-caption"
            data-testid="decision-search-input"
          />
          {searchInput ? (
            <button
              type="button"
              onClick={() => setSearchInput("")}
              aria-label={t(($) => $.search.clear_search)}
              data-testid="decision-search-clear"
              className="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              <X className="size-3.5" />
            </button>
          ) : null}
        </div>
      </div>

      {/*
        RUYI-738: the skeleton stands in for the page body, so it may only
        hold while the page would otherwise be blank — the list's first page
        still loading, or an unfiltered empty inbox that cannot yet be
        declared empty because the pending-queue query has not settled. A
        filter or search window means the user is already mid-page: show a
        slim loading strip instead of covering rendered context.
      */}
      {pageQuery.isPending && !hasWindow && (
        <div className="flex flex-col gap-2 px-4 py-3" data-testid="decision-center-loading">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-3/4" />
        </div>
      )}
      {pageQuery.isPending && hasWindow && (
        <div className="flex flex-col gap-2 px-4 py-3" data-testid="decision-list-loading">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      )}
      {!pageQuery.isPending && !hasWindow && items.length === 0 && requestsPending && (
        <div className="flex flex-col gap-2 px-4 py-3" data-testid="decision-center-loading">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-3/4" />
        </div>
      )}

      {pageQuery.isError && (
        <div
          className="py-10 text-center text-caption text-muted-foreground"
          data-testid="decision-center-error"
        >
          {t(($) => $.page.load_failed)}
        </div>
      )}

      {!pageQuery.isPending && !pageQuery.isError && pendingRow && (
        <section className="px-4 pb-2" data-testid="decision-queue">
          <div className="flex items-center gap-2 py-2">
            <ShieldCheck className="size-3.5 text-brand" aria-hidden />
            <h2 className="text-caption font-medium text-muted-foreground">
              {t(($) => $.queue.title)}
            </h2>
            <span className="text-caption text-faint-foreground">
              {requestsData?.counts.pending ?? 0}
            </span>
          </div>
          <DecisionRequestRow request={pendingRow} />
        </section>
      )}

      {!requestsPending && !pageQuery.isError && historyTotal > 0 && (
        <section className="px-4 pb-1" data-testid="decision-history">
          <button
            type="button"
            onClick={() => setHistoryOpen((open) => !open)}
            aria-expanded={historyOpen}
            data-testid="decision-history-toggle"
            className="flex items-center gap-1.5 py-1.5 text-caption text-muted-foreground hover:text-foreground"
          >
            {historyOpen ? (
              <ChevronDown className="size-3.5" aria-hidden />
            ) : (
              <ChevronRight className="size-3.5" aria-hidden />
            )}
            {t(($) => $.history.title)}
            <span className="text-faint-foreground">{historyTotal}</span>
          </button>
          {historyOpen && (
            <div className="flex flex-col">
              {historyRows.map((row) => (
                <DecisionRequestRow key={row.id} request={row} />
              ))}
              {historyPageQuery.hasNextPage && (
                <div className="flex justify-center pb-1">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void historyPageQuery.fetchNextPage()}
                    disabled={historyPageQuery.isFetchingNextPage}
                    data-testid="decision-history-load-more"
                  >
                    {historyPageQuery.isFetchingNextPage
                      ? t(($) => $.loading_more)
                      : t(($) => $.load_more)}
                  </Button>
                </div>
              )}
            </div>
          )}
        </section>
      )}

      {!pageQuery.isPending &&
        !pageQuery.isError &&
        items.length === 0 &&
        hasWindow && (
          <div
            className="flex flex-1 min-h-0 flex-col items-center justify-center gap-3 text-muted-foreground"
            data-testid="decision-filtered-empty"
          >
            <Search className="h-10 w-10 text-faint-foreground" />
            <p className="text-body">{t(($) => $.search.no_results)}</p>
            <Button
              variant="outline"
              size="sm"
              className="mt-1"
              onClick={() => {
                setStatusFilter("all");
                setSearchInput("");
              }}
              data-testid="decision-window-clear"
            >
              {t(($) => $.clear_window)}
            </Button>
          </div>
        )}

      {!pageQuery.isPending &&
        !pageQuery.isError &&
        !requestsPending &&
        !hasWindow &&
        items.length === 0 &&
        !pendingRow &&
        historyTotal === 0 && (
          <div
            className="flex flex-1 min-h-0 flex-col items-center justify-center gap-2 py-20 text-muted-foreground"
            data-testid="decision-center-empty"
          >
            <Gavel className="h-10 w-10 text-faint-foreground" />
            <p className="text-body">{t(($) => $.page.empty_title)}</p>
            <p className="text-caption">{t(($) => $.page.empty_description)}</p>
          </div>
        )}

      {!pageQuery.isPending && !pageQuery.isError && items.length > 0 && (
        <>
          <DecisionListView
            items={items}
            counts={counts}
            onSelect={(item) => {
              setSelectedDecision(item);
              setDetailOpen(true);
            }}
          />
          {pageQuery.hasNextPage && (
            <div className="flex justify-center px-4 pb-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => void pageQuery.fetchNextPage()}
                disabled={pageQuery.isFetchingNextPage}
                data-testid="decision-load-more"
              >
                {pageQuery.isFetchingNextPage
                  ? t(($) => $.loading_more)
                  : t(($) => $.load_more)}
              </Button>
            </div>
          )}
        </>
      )}
      <DecisionDetailSheet
        item={selectedDecision}
        open={detailOpen}
        onOpenChange={setDetailOpen}
      />
    </div>
  );
}
