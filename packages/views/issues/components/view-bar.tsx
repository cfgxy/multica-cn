"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Layers, Plus, Settings2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";
import type { IssueView } from "@multica/core/api/schemas";
import type { IssueViewScope } from "@multica/core/issue-views/queries";
import {
  applyViewBarPrefs,
  issueViewPreferenceOptions,
  useUpdateIssueViewPreference,
  EMPTY_VIEW_BAR_PREFS,
} from "@multica/core/issue-views/preferences";
import { useDeleteIssueView } from "@multica/core/issue-views/mutations";
import { useAuthStore } from "@multica/core/auth";
import { ManageViewsDialog, type ViewBarItem } from "./manage-views-dialog";
import { useT } from "../../i18n";

export interface ViewBarBuiltin {
  key: string;
  label: string;
  description?: string;
  active: boolean;
  onSelect: () => void;
}

/**
 * The view bar: built-in tabs and saved views as one flat, per-user
 * orderable row (wraps instead of overflowing), plus the [⧉] menu with the
 * create / manage entries. Owns the preference document and the manager
 * dialog; creating and editing stay with the host (they need the save
 * dialog wired to the surface).
 */
export function ViewBar({
  wsId,
  scope,
  builtins,
  views,
  activeView,
  onSelectView,
  onNewView,
  onEditView,
}: {
  wsId: string;
  scope: IssueViewScope;
  builtins: ViewBarBuiltin[];
  views: IssueView[];
  activeView: IssueView | null;
  onSelectView: (view: IssueView | null) => void;
  onNewView: () => void;
  /** Opens the edit dialog seeded from the view's own definition. */
  onEditView: (view: IssueView) => void;
}) {
  const { t } = useT("issues");
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const { data: preference } = useQuery(issueViewPreferenceOptions(wsId, scope));
  const prefs = preference?.prefs ?? EMPTY_VIEW_BAR_PREFS;
  const updatePreference = useUpdateIssueViewPreference(wsId, scope);
  const deleteView = useDeleteIssueView(wsId);
  const [manageOpen, setManageOpen] = useState(false);

  const anchorId = builtins.length > 0 ? `builtin:${builtins[0]!.key}` : "";

  const items = useMemo<ViewBarItem[]>(
    () => [
      ...builtins.map((b) => ({
        barItemId: `builtin:${b.key}`,
        label: b.label,
        kind: "builtin" as const,
      })),
      ...views.map((view) => ({
        barItemId: `view:${view.id}`,
        label: view.name,
        kind: "view" as const,
        view,
        canManage: view.owner_id === currentUserId,
      })),
    ],
    [builtins, views, currentUserId],
  );

  const { visible, hiddenSet, ordered } = useMemo(
    () => applyViewBarPrefs(items, prefs, anchorId),
    [items, prefs, anchorId],
  );

  const builtinByKey = useMemo(
    () => new Map(builtins.map((b) => [`builtin:${b.key}`, b])),
    [builtins],
  );

  const savePrefs = (next: { hidden: string[]; order: string[] }) => {
    // Prune ids that no longer resolve so deleted views don't accumulate.
    const known = new Set(items.map((item) => item.barItemId));
    updatePreference.mutate({
      hidden: next.hidden.filter((id) => known.has(id)),
      order: next.order.filter((id) => known.has(id)),
    });
  };

  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1">
      {visible.map((item) => {
        if (item.kind === "builtin") {
          const b = builtinByKey.get(item.barItemId);
          if (!b) return null;
          const button = (
            <Button
              variant="outline"
              size="sm"
              className={
                b.active
                  ? "bg-accent text-accent-foreground hover:bg-accent/80"
                  : "text-muted-foreground"
              }
              onClick={b.onSelect}
            >
              {b.label}
            </Button>
          );
          return b.description ? (
            <Tooltip key={item.barItemId}>
              <TooltipTrigger render={button} />
              <TooltipContent side="bottom">{b.description}</TooltipContent>
            </Tooltip>
          ) : (
            <span key={item.barItemId}>{button}</span>
          );
        }
        const view = item.view!;
        const active = activeView?.id === view.id;
        return (
          <Button
            key={item.barItemId}
            variant="outline"
            size="sm"
            className={cn(
              "max-w-48",
              active
                ? "bg-accent text-accent-foreground hover:bg-accent/80"
                : "text-muted-foreground",
            )}
            onClick={() => onSelectView(active ? null : view)}
          >
            <span className="truncate">{view.name}</span>
          </Button>
        );
      })}

      <DropdownMenu>
        <Tooltip>
          <DropdownMenuTrigger
            render={
              <TooltipTrigger
                render={
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t(($) => $.view_bar.menu_label)}
                    className="text-muted-foreground"
                  >
                    <Layers className="size-3.5" />
                  </Button>
                }
              />
            }
          />
          <TooltipContent side="bottom">{t(($) => $.view_bar.menu_label)}</TooltipContent>
        </Tooltip>
        <DropdownMenuContent align="start" className="w-44">
          <DropdownMenuItem onClick={onNewView}>
            <Plus className="size-3.5" />
            {t(($) => $.view_bar.menu_new)}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => setManageOpen(true)}>
            <Settings2 className="size-3.5" />
            {t(($) => $.view_bar.menu_manage)}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <ManageViewsDialog
        open={manageOpen}
        onOpenChange={setManageOpen}
        items={ordered}
        hiddenSet={hiddenSet}
        anchorId={anchorId}
        onReorder={(orderedIds) => savePrefs({ hidden: [...hiddenSet], order: orderedIds })}
        onToggleHidden={(barItemId, hidden) => {
          const nextHidden = new Set(hiddenSet);
          if (hidden) nextHidden.add(barItemId);
          else nextHidden.delete(barItemId);
          savePrefs({
            hidden: [...nextHidden],
            order: ordered.map((item) => item.barItemId),
          });
          // Hiding the open view exits it — an invisible active view would
          // strand the surface with no matching tab.
          if (hidden && activeView && barItemId === `view:${activeView.id}`) {
            onSelectView(null);
          }
        }}
        onEditView={(view) => {
          setManageOpen(false);
          onEditView(view);
        }}
        onDeleteView={async (view) => {
          await deleteView.mutateAsync(view.id);
          if (activeView?.id === view.id) onSelectView(null);
        }}
      />
    </div>
  );
}
