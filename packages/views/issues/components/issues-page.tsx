"use client";

import { CirclePause, ListTodo } from "lucide-react";
import type {
  Issue,
  IssueTableFacetSpec,
  IssueTableFacetsResponse,
  WorkingAgentSummary,
} from "@multica/core/types";
import { useCurrentWorkspace } from "@multica/core/paths";
import { useIssuesScope } from "@multica/core/issues/stores/issues-scope-store";
import { useViewStore } from "@multica/core/issues/stores/view-store-context";
import { PageHeader } from "../../layout/page-header";
import { useT } from "../../i18n";
import { IssueSurface } from "../surface/issue-surface";
import { IssuesHeader } from "./issues-header";

function IssuesSurfaceHeader({
  issues,
  workingAgents,
  isRefreshing,
  facetCountsExact,
  tableFacetCounts,
  onTableFacetChange,
}: {
  issues: Issue[];
  workingAgents: WorkingAgentSummary[] | undefined;
  isRefreshing: boolean;
  facetCountsExact: boolean;
  tableFacetCounts?: IssueTableFacetsResponse;
  onTableFacetChange: (facet: IssueTableFacetSpec | null) => void;
}) {
  const dateFilter = useViewStore((s) => s.dateFilter);
  const setDateFilter = useViewStore((s) => s.setDateFilter);

  return (
    <IssuesHeader
      scopedIssues={issues}
      workingAgents={workingAgents}
      dateFilter={dateFilter}
      onDateFilterChange={setDateFilter}
      isRefreshing={isRefreshing}
      facetCountsExact={facetCountsExact}
      tableFacetCounts={tableFacetCounts}
      onTableFacetChange={onTableFacetChange}
    />
  );
}

export function IssuesPage() {
  const { t } = useT("issues");
  const scope = useIssuesScope("issues");
  // Workspace-level freeze projection (RUYI-608): the tasks page must show
  // the pause state and the frozen backlog alongside the queue itself. The
  // workspace DTO fields arrive via the workspace list cache, refreshed by
  // the scheduling realtime events.
  const workspace = useCurrentWorkspace();
  const schedulingPaused = !!workspace?.scheduling_paused;

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader>
        <ListTodo className="h-4 w-4 text-muted-foreground" />
        <h1 className="text-body font-medium">{t(($) => $.page.breadcrumb_title)}</h1>
      </PageHeader>

      {schedulingPaused && (
        <div className="flex shrink-0 items-center gap-2 border-b border-amber-500/30 bg-amber-500/10 px-6 py-2 text-caption text-amber-900 dark:text-amber-100">
          <CirclePause className="h-3.5 w-3.5 shrink-0" />
          <span className="flex-1">
            {t(($) => $.page.scheduling_paused_banner, {
              count: workspace?.scheduling_queued_count ?? 0,
            })}
          </span>
        </div>
      )}

      <IssueSurface
        scope={{ type: "workspace", actorKind: scope }}
        modes={["board", "list", "table", "swimlane"]}
        batchToolbar="list"
        renderHeader={({ controller }) => (
          <IssuesSurfaceHeader
            issues={controller.surfaceIssues}
            workingAgents={controller.workingAgents}
            isRefreshing={controller.isRefreshing}
            facetCountsExact={controller.facetCountsExact}
            tableFacetCounts={controller.tableFacetCounts}
            onTableFacetChange={controller.setActiveTableFacet}
          />
        )}
        renderEmpty={() => (
          <div className="flex flex-1 min-h-0 flex-col items-center justify-center gap-2 text-muted-foreground">
            <ListTodo className="h-10 w-10 text-faint-foreground" />
            <p className="text-body">{t(($) => $.page.empty_title)}</p>
            <p className="text-caption">{t(($) => $.page.empty_hint)}</p>
          </div>
        )}
      />
    </div>
  );
}
