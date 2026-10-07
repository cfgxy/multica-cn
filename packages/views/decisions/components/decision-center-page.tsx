"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Gavel } from "lucide-react";
import {
  workspaceDecisionInboxQueryOptions,
} from "@multica/core/issues/decisions";
import {
  DECISION_ANCHOR_PREFIX,
  useCurrentWorkspace,
  useWorkspacePaths,
} from "@multica/core/paths";
import type { IssueDecisionStatus, WorkspaceDecisionInboxItem } from "@multica/core/types";
import { PageHeader } from "../../layout/page-header";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n";
import { DecisionInboxRow } from "./decision-inbox-row";

// Decision Center (RUYI-494): the workspace-wide aggregation of decision
// cards. Status is carried by the list sections — 待决策 first, then 已决策,
// then 已失效 — and an empty section renders nothing. Section headers show the
// server's workspace-wide counts, so the numbers stay true even when the list
// window is bounded.

const SECTION_ORDER: IssueDecisionStatus[] = ["open", "answered", "cancelled"];

export function DecisionCenterPage() {
  const { t } = useT("decisions");
  const workspace = useCurrentWorkspace();
  const p = useWorkspacePaths();

  const { data, isPending, isError } = useQuery(
    workspaceDecisionInboxQueryOptions(workspace?.id),
  );

  const sections = useMemo(() => {
    const byStatus = new Map<IssueDecisionStatus, WorkspaceDecisionInboxItem[]>(
      SECTION_ORDER.map((s) => [s, []]),
    );
    for (const item of data?.items ?? []) {
      byStatus.get(item.status)?.push(item);
    }
    return SECTION_ORDER.map((status) => ({
      status,
      items: byStatus.get(status) ?? [],
      count: data?.counts[status] ?? 0,
    }));
  }, [data]);

  const isEmpty = !isPending && !isError && (data?.items.length ?? 0) === 0;

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader>
        <Gavel className="h-4 w-4 text-muted-foreground" />
        <h1 className="text-body font-medium">{t(($) => $.page.breadcrumb)}</h1>
      </PageHeader>

      <div className="flex-1 min-h-0 overflow-y-auto px-4 py-3" data-testid="decision-center-body">
        {isPending && (
          <div className="flex flex-col gap-2" data-testid="decision-center-loading">
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

        {sections.map(
          (section) =>
            section.items.length > 0 && (
              <section
                key={section.status}
                data-testid={`decision-section-${section.status}`}
                className="mb-4"
              >
                <h2 className="mb-1.5 flex items-center gap-1.5 px-3 text-caption font-medium text-muted-foreground">
                  {t(($) => $.section[section.status])}
                  <span className="text-faint-foreground">{section.count}</span>
                </h2>
                <div className="flex flex-col gap-1">
                  {section.items.map((item) => (
                    <DecisionInboxRow
                      key={item.id}
                      item={item}
                      href={`${p.issueDetail(item.issue_id)}#${DECISION_ANCHOR_PREFIX}${item.id}`}
                    />
                  ))}
                </div>
              </section>
            ),
        )}
      </div>
    </div>
  );
}
