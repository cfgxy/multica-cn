"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Check, ChevronDown, Layers, Loader2, Settings2, Plus } from "lucide-react";
import type {
  ExecutionProfile,
  ExecutionProfileActivationResponse,
} from "@multica/core/types/execution-profile";
import {
  executionProfileListOptions,
  useActivateExecutionProfile,
  useCreateExecutionProfile,
} from "@multica/core/execution-profiles/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { useT } from "../../i18n";
import { ExecutionProfileManageSheet } from "./execution-profile-manage-sheet";
import { ExecutionProfileResultDialog } from "./execution-profile-result-dialog";

/**
 * The Profile control on the squad members page (RUYI-57).
 *
 * Switching every member's runtime and model is a bulk write, so selecting a
 * profile never applies it directly: the menu closes and a confirm dialog
 * states the blast radius first. The whole cluster is hidden for viewers who
 * cannot manage the workspace rather than shown disabled — a control that can
 * never do anything is noise on a read-only page.
 */
export function ExecutionProfilePicker({ wsId }: { wsId: string }) {
  const { t } = useT("squads");
  const [open, setOpen] = useState(false);
  const [pendingProfile, setPendingProfile] = useState<ExecutionProfile | null>(
    null,
  );
  const [manageOpen, setManageOpen] = useState(false);
  const [manageProfileId, setManageProfileId] = useState<string | null>(null);
  const [result, setResult] = useState<ExecutionProfileActivationResponse | null>(
    null,
  );

  const listQuery = useQuery(executionProfileListOptions(wsId));
  const activate = useActivateExecutionProfile(wsId);
  const create = useCreateExecutionProfile(wsId);

  const profiles = listQuery.data?.execution_profiles ?? [];
  const active = profiles.find((p) => p.is_active) ?? null;

  const openManage = (profileId: string | null) => {
    setOpen(false);
    setManageProfileId(profileId);
    setManageOpen(true);
  };

  const handleCreate = async () => {
    setOpen(false);
    try {
      const created = await create.mutateAsync({
        name: t(($) => $.execution_profile.default_new_name),
      });
      setManageProfileId(created.id);
      setManageOpen(true);
    } catch {
      toast.error(t(($) => $.execution_profile.create_failed));
    }
  };

  const handleActivate = async () => {
    const profile = pendingProfile;
    if (!profile) return;
    try {
      const res = await activate.mutateAsync(profile.id);
      setPendingProfile(null);
      // Any non-applied member gets the itemised dialog; a clean run is just
      // a toast, because there is nothing to read.
      if (res.skipped > 0 || res.failed > 0 || res.applied === 0) {
        setResult(res);
        return;
      }
      toast.success(
        t(($) => $.execution_profile.activated_toast, {
          name: profile.name,
          applied: res.applied,
          total: res.results.length,
        }),
      );
    } catch {
      setPendingProfile(null);
      toast.error(t(($) => $.execution_profile.activate_failed));
    }
  };

  const triggerLabel = listQuery.isPending
    ? t(($) => $.execution_profile.loading_label)
    : activate.isPending
      ? t(($) => $.execution_profile.switching_label)
      : (active?.name ?? t(($) => $.execution_profile.none_label));

  const busy = listQuery.isPending || activate.isPending;

  return (
    <>
      <DropdownMenu open={open} onOpenChange={setOpen}>
        <Tooltip>
          <TooltipTrigger
            render={
              <DropdownMenuTrigger
                render={
                  <Button size="sm" variant="outline" disabled={busy}>
                    {activate.isPending || listQuery.isPending ? (
                      <Loader2 className="size-3.5 mr-1.5 animate-spin" />
                    ) : (
                      <Layers className="size-3.5 mr-1.5" />
                    )}
                    <span className="text-muted-foreground mr-1">
                      {t(($) => $.execution_profile.trigger_prefix)}
                    </span>
                    <span
                      className={`max-w-[160px] sm:max-w-[160px] truncate ${
                        active ? "font-semibold" : "text-muted-foreground"
                      }`}
                    >
                      {triggerLabel}
                    </span>
                    <ChevronDown
                      className={`size-3.5 ml-1.5 transition-transform ${
                        open ? "rotate-180" : ""
                      }`}
                    />
                  </Button>
                }
              />
            }
          />
          {active && (
            <TooltipContent>
              {t(($) => $.execution_profile.active_tooltip, {
                name: active.name,
              })}
            </TooltipContent>
          )}
        </Tooltip>

        <DropdownMenuContent align="end" className="min-w-[240px] max-w-[280px]">
          <div className="max-h-[320px] overflow-y-auto">
            {listQuery.isPending ? (
              <div className="p-2 space-y-2">
                <Skeleton className="h-6 w-full" />
                <Skeleton className="h-6 w-full" />
              </div>
            ) : listQuery.isError ? (
              <div className="p-2">
                <p className="text-caption text-destructive mb-2">
                  {t(($) => $.execution_profile.load_failed)}
                </p>
                <Button
                  size="sm"
                  variant="outline"
                  className="w-full"
                  onClick={() => listQuery.refetch()}
                >
                  {t(($) => $.execution_profile.retry)}
                </Button>
              </div>
            ) : profiles.length === 0 ? (
              <div className="p-3 text-center">
                <p className="text-caption text-muted-foreground">
                  {t(($) => $.execution_profile.empty_hint)}
                </p>
              </div>
            ) : (
              profiles.map((profile) => (
                <DropdownMenuItem
                  key={profile.id}
                  className="h-8 rounded-md"
                  onClick={() => {
                    setOpen(false);
                    // Selecting what is already active is a no-op, not a
                    // reason to rewrite every agent again.
                    if (profile.is_active) return;
                    setPendingProfile(profile);
                  }}
                >
                  <span className="w-3.5 shrink-0">
                    {profile.is_active && <Check className="size-3.5" />}
                  </span>
                  <span className="flex-1 truncate">{profile.name}</span>
                  <span className="text-caption text-muted-foreground shrink-0">
                    {t(($) => $.execution_profile.entry_count, {
                      count: profile.entry_count,
                    })}
                  </span>
                </DropdownMenuItem>
              ))
            )}
          </div>

          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={handleCreate} disabled={create.isPending}>
            <Plus className="size-3.5 mr-1.5" />
            {t(($) => $.execution_profile.create_action)}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => openManage(null)}>
            <Settings2 className="size-3.5 mr-1.5" />
            {t(($) => $.execution_profile.manage_action)}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <AlertDialog
        open={pendingProfile !== null}
        onOpenChange={(next) => {
          // Locked while the write is in flight: dismissing mid-activation
          // would leave the user with no report of what changed.
          if (!next && !activate.isPending) setPendingProfile(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.execution_profile.confirm_title, {
                name: pendingProfile?.name ?? "",
              })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              <span className="block">
                {t(($) => $.execution_profile.confirm_overwrite, {
                  count: pendingProfile?.entry_count ?? 0,
                })}
              </span>
              <span className="block mt-1">
                {t(($) => $.execution_profile.confirm_running_tasks)}
              </span>
              <span className="block mt-1">
                {t(($) => $.execution_profile.confirm_audit)}
              </span>
              {pendingProfile?.entry_count === 0 && (
                <span className="block mt-2 text-destructive">
                  {t(($) => $.execution_profile.confirm_empty_hint)}
                </span>
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={activate.isPending}>
              {t(($) => $.execution_profile.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={activate.isPending || pendingProfile?.entry_count === 0}
              onClick={(e) => {
                e.preventDefault();
                void handleActivate();
              }}
            >
              {activate.isPending && (
                <Loader2 className="size-3.5 mr-1.5 animate-spin" />
              )}
              {activate.isPending
                ? t(($) => $.execution_profile.activating)
                : t(($) => $.execution_profile.confirm_activate)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <ExecutionProfileResultDialog
        wsId={wsId}
        result={result}
        onClose={() => setResult(null)}
        onRetry={() => {
          // Only offered when nothing applied, so re-opening the confirm
          // dialog costs the user nothing and keeps one code path for the
          // actual write.
          const profile = profiles.find((p) => p.id === result?.profile.id);
          setResult(null);
          if (profile) setPendingProfile(profile);
        }}
      />

      <ExecutionProfileManageSheet
        wsId={wsId}
        open={manageOpen}
        initialProfileId={manageProfileId}
        onOpenChange={setManageOpen}
      />
    </>
  );
}
