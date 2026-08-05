"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, Loader2 } from "lucide-react";
import { toast } from "sonner";
import type {
  Agent,
  AgentRuntime,
  MemberWithUser,
  MigrateAgentsToRuntimeResponse,
} from "@multica/core/types";
import { api } from "@multica/core/api";
import { useMigrateAgentsToRuntime } from "@multica/core/runtimes/mutations";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { RuntimePicker } from "./inspector/runtime-picker";

/**
 * Runtime switch for one agent or many, behind a single confirmation dialog
 * (MUL-5758).
 *
 * Three entry points share this component — the Agent List row menu, the Agent
 * List batch toolbar, and the Runtime detail page's "migrate agents" action —
 * and they differ only in what they pass in. A single agent is `agentIds` of
 * length one, not a separate flow, so the confirmation the user sees and the
 * write the server performs are the same in every case.
 *
 * What the dialog must state before the user commits:
 *   - which runtime the agents move to;
 *   - which model / thinking / speed settings this discards, per agent, by
 *     value (they are runtime-native and cannot survive a provider change);
 *   - how many queued tasks travel along, and how many stay behind because a
 *     daemon is already running them;
 *   - how many selected agents the caller may not move.
 *
 * The task numbers come from a server dry run, not from presence: the presence
 * projection merges 'dispatched' into "queued" and omits 'deferred', so it can
 * state neither group correctly.
 */
export function SwitchAgentRuntimeDialog({
  open,
  onOpenChange,
  agents,
  runtimes,
  members,
  currentUserId,
  wsId,
  expectedSourceRuntimeId,
  onMigrated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The agents to move. One entry is the single-agent case. */
  agents: Agent[];
  runtimes: AgentRuntime[];
  members: MemberWithUser[];
  currentUserId: string | null;
  wsId: string;
  /** Set by the Runtime detail entry point so the server can refuse a plan
   *  that drifted since the page rendered it. */
  expectedSourceRuntimeId?: string;
  onMigrated?: (result: MigrateAgentsToRuntimeResponse) => void;
}) {
  const { t } = useT("agents");
  const migrate = useMigrateAgentsToRuntime(wsId);

  const [targetRuntimeId, setTargetRuntimeId] = useState("");
  const [excluded, setExcluded] = useState<ReadonlySet<string>>(new Set());

  // Reset on every open so a previous selection can never leak into the next
  // dialog session (same discipline as the bulk access dialog).
  useEffect(() => {
    if (!open) return;
    setTargetRuntimeId("");
    setExcluded(new Set());
  }, [open]);

  const selected = agents.filter((a) => !excluded.has(a.id));
  const selectedIds = selected.map((a) => a.id);
  const isBulk = agents.length > 1;

  // Server-computed projection of this exact selection. Keyed on the selection
  // and the target so unchecking an agent re-asks rather than showing counts
  // for a set the user has since changed.
  const preview = useQuery({
    queryKey: [
      "agents",
      wsId,
      "migrate-preview",
      targetRuntimeId,
      [...selectedIds].sort().join(","),
    ],
    queryFn: () =>
      api.migrateAgentsToRuntime(targetRuntimeId, {
        agent_ids: selectedIds,
        dry_run: true,
      }),
    enabled: open && !!targetRuntimeId && selectedIds.length > 0,
    staleTime: 0,
  });

  const targetRuntime = runtimes.find((r) => r.id === targetRuntimeId) ?? null;
  const skipped = preview.data?.skipped ?? [];
  const movable = preview.data?.migrated ?? [];
  // Only agents that actually carry a runtime-native setting are worth naming;
  // listing "cleared nothing" rows would bury the ones that matter.
  const clearing = movable.filter(
    (m) =>
      !!m.cleared_model || !!m.cleared_thinking_level || !!m.cleared_service_tier,
  );

  const canConfirm =
    !!targetRuntimeId &&
    selectedIds.length > 0 &&
    !preview.isFetching &&
    movable.length > 0 &&
    !migrate.isPending;

  const toggleExcluded = (id: string) => {
    setExcluded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const handleConfirm = async () => {
    try {
      const result = await migrate.mutateAsync({
        targetRuntimeId,
        agentIds: selectedIds,
        expectedSourceRuntimeId,
      });
      onOpenChange(false);
      onMigrated?.(result);
      toast.success(
        t(($) => $.migrate_dialog.success_toast, {
          count: result.migrated.length,
          tasks: result.tasks_migrated,
        }),
      );
      if (result.skipped.length > 0) {
        toast.warning(
          t(($) => $.migrate_dialog.skipped_toast, {
            count: result.skipped.length,
          }),
        );
      }
    } catch (e) {
      // A 409 means the agent set moved under the user (Runtime detail entry
      // point). Nothing was written, so the honest recovery is to close and
      // let the refreshed page offer the current set.
      toast.error(
        e instanceof Error ? e.message : t(($) => $.migrate_dialog.failed_toast),
      );
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {isBulk
              ? t(($) => $.migrate_dialog.title_bulk, { count: agents.length })
              : t(($) => $.migrate_dialog.title_single, {
                  name: agents[0]?.name ?? "",
                })}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.migrate_dialog.description)}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <RuntimePicker
            variant="field"
            value={targetRuntimeId}
            runtimes={runtimes}
            members={members}
            currentUserId={currentUserId}
            onChange={(id) => setTargetRuntimeId(id)}
          />

          {isBulk && (
            <div className="max-h-48 space-y-1 overflow-y-auto rounded-lg border p-2">
              {agents.map((agent) => (
                <button
                  key={agent.id}
                  type="button"
                  onClick={() => toggleExcluded(agent.id)}
                  className="flex w-full items-center gap-2 rounded-md px-1.5 py-1 text-left transition-colors hover:bg-accent"
                >
                  <Checkbox
                    checked={!excluded.has(agent.id)}
                    tabIndex={-1}
                    className="pointer-events-none"
                  />
                  <ActorAvatar
                    actorType="agent"
                    actorId={agent.id}
                    size="sm"
                  />
                  <span className="min-w-0 flex-1 truncate text-body">
                    {agent.name}
                  </span>
                </button>
              ))}
            </div>
          )}

          {/* Everything below is the consequence summary. It stays empty until
              a target is picked, because none of it is knowable before then. */}
          {preview.isFetching && (
            <p
              className="flex items-center gap-2 text-caption text-muted-foreground"
              aria-live="polite"
            >
              <Loader2 className="size-3.5 animate-spin" />
              {t(($) => $.migrate_dialog.checking)}
            </p>
          )}

          {!preview.isFetching && preview.data && (
            <div className="space-y-3 text-caption" aria-live="polite">
              <p className="text-muted-foreground">
                {t(($) => $.migrate_dialog.tasks_migrating, {
                  count: preview.data.tasks_migrated,
                })}
              </p>
              {preview.data.tasks_staying_active > 0 && (
                <p className="text-muted-foreground">
                  {t(($) => $.migrate_dialog.tasks_staying, {
                    count: preview.data.tasks_staying_active,
                  })}
                </p>
              )}

              {clearing.length > 0 && (
                <div className="rounded-lg border border-amber-500/40 bg-amber-500/5 p-2.5">
                  <p className="flex items-center gap-1.5 font-medium text-amber-600 dark:text-amber-400">
                    <AlertTriangle className="size-3.5 shrink-0" />
                    {t(($) => $.migrate_dialog.clearing_title)}
                  </p>
                  <ul className="mt-1.5 space-y-0.5 text-muted-foreground">
                    {clearing.map((m) => (
                      <li key={m.agent_id} className="truncate">
                        {m.name}
                        {": "}
                        {[
                          m.cleared_model,
                          m.cleared_thinking_level,
                          m.cleared_service_tier,
                        ]
                          .filter(Boolean)
                          .join(" · ")}
                      </li>
                    ))}
                  </ul>
                </div>
              )}

              {skipped.length > 0 && (
                <p className="text-muted-foreground">
                  {t(($) => $.migrate_dialog.skipped_notice, {
                    count: skipped.length,
                  })}
                </p>
              )}

              {targetRuntime && targetRuntime.status !== "online" && (
                <p className="text-amber-600 dark:text-amber-400">
                  {t(($) => $.migrate_dialog.target_offline)}
                </p>
              )}

              {movable.length === 0 && (
                <p className="text-muted-foreground">
                  {t(($) => $.migrate_dialog.nothing_to_do)}
                </p>
              )}
            </div>
          )}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={migrate.isPending}
            onClick={() => onOpenChange(false)}
          >
            {t(($) => $.migrate_dialog.cancel)}
          </Button>
          <Button
            type="button"
            size="sm"
            disabled={!canConfirm}
            onClick={handleConfirm}
          >
            {migrate.isPending ? (
              <Loader2 className="mr-1 size-3.5 animate-spin" />
            ) : null}
            {t(($) => $.migrate_dialog.confirm)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
