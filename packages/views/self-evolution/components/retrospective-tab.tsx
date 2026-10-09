"use client";

import { useEffect, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { ApiError, clientErrorMessage } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import { agentListOptions } from "@multica/core/workspace/queries";
import {
  retrospectiveConfigOptions,
  retrospectiveRunsOptions,
  useTriggerRetrospectiveRun,
  useUpdateRetrospectiveConfig,
} from "@multica/core/self-evolution";
import type { RetrospectiveRun } from "@multica/core/types";
import { useT } from "../../i18n";

/**
 * The daily retrospective tab (RUYI-305 E3).
 *
 * The job itself is base-platform plumbing (RUYI-552 direction 3): a
 * registered scheduler task (or the "run now" button) starts a single run of
 * the configured execution agent. That run reads the real execution content
 * of completed issues inside the lookback window and distills
 * prompt-improvement drafts into the legislation pool. It never creates or
 * comments on issues — the only visible traces are the config, the run
 * records below and, on success, new proposals in the legislation tab.
 */
export function RetrospectiveTab({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const currentMember = useCurrentMember(wsId);
  const canManage = currentMember.role === "owner";

  const config = useQuery(retrospectiveConfigOptions(wsId));
  const runs = useQuery(retrospectiveRunsOptions(wsId));
  const agents = useQuery(agentListOptions(wsId));
  const saveConfig = useUpdateRetrospectiveConfig(wsId);
  const trigger = useTriggerRetrospectiveRun(wsId);

  const [enabled, setEnabled] = useState(false);
  const [includeInReview, setIncludeInReview] = useState(false);
  const [windowDays, setWindowDays] = useState(7);
  const [agentId, setAgentId] = useState("");
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (config.data && !loaded) {
      setEnabled(config.data.enabled);
      setIncludeInReview(config.data.include_in_review);
      setWindowDays(config.data.window_days);
      setAgentId(config.data.agent_id ?? "");
      setLoaded(true);
    }
  }, [config.data, loaded]);

  const mutationError = (e: unknown) =>
    toast.error(clientErrorMessage(e) ?? t(($) => $.retrospective.errorLabel));

  // The server's 409 on "run now" is the no-usable-agent state; the UI
  // renders its own localized copy with the fix in view (the selector above),
  // not the server's sentence.
  const triggerError = (e: unknown) => {
    if (e instanceof ApiError && e.status === 409) {
      toast.error(t(($) => $.retrospective.agentTriggerBlocked));
      return;
    }
    mutationError(e);
  };

  const runsList: RetrospectiveRun[] = runs.data ?? [];

  const statusLabel = (s: string) => {
    switch (s) {
      case "succeeded": return t(($) => $.retrospective.status.succeeded);
      case "failed": return t(($) => $.retrospective.status.failed);
      case "running": return t(($) => $.retrospective.status.running);
      default: return s;
    }
  };

  // Archived agents stay listed when one is the saved selection (the server
  // still reports it), but only live agents are selectable going forward.
  const agentChoices = (agents.data ?? []).filter(
    (a) => !a.archived_at || a.id === agentId,
  );
  const selectedAgent = agentChoices.find((a) => a.id === agentId);
  // "none" stands in for the empty selection — the select's value is never
  // "" so the placeholder item stays a real, pickable option.
  const agentItems: Record<string, ReactNode> = {
    none: t(($) => $.retrospective.agentPlaceholder),
    ...Object.fromEntries(
      agentChoices.map((a) => [
        a.id,
        a.name + (a.archived_at ? t(($) => $.retrospective.agentArchivedSuffix) : ""),
      ]),
    ),
  };

  return (
    <div className="flex flex-col gap-6" data-testid="retrospective-tab">
      <div className="flex flex-col gap-4 rounded-md border p-4" data-testid="retrospective-config">
        <p className="text-muted-foreground text-body">{t(($) => $.retrospective.description)}</p>
        {config.isPending ? (
          <Skeleton className="h-20 w-full" />
        ) : (
          <div className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Checkbox
                id="retro-enabled"
                checked={enabled}
                disabled={!canManage}
                onCheckedChange={(v) => setEnabled(v === true)}
              />
              <Label htmlFor="retro-enabled">{t(($) => $.retrospective.enabled)}</Label>
            </div>
            <div className="flex items-center gap-2">
              <Checkbox
                id="retro-in-review"
                checked={includeInReview}
                disabled={!canManage}
                onCheckedChange={(v) => setIncludeInReview(v === true)}
              />
              <Label htmlFor="retro-in-review">{t(($) => $.retrospective.includeInReview)}</Label>
            </div>
            <div className="flex items-center gap-2">
              <Label htmlFor="retro-window">{t(($) => $.retrospective.windowDays)}</Label>
              <Input
                id="retro-window"
                type="number"
                min={1}
                max={30}
                className="w-24"
                value={windowDays}
                disabled={!canManage}
                onChange={(e) => {
                  const n = Number(e.target.value);
                  if (Number.isFinite(n)) setWindowDays(n);
                }}
              />
            </div>

            <div className="mt-2 flex flex-col gap-3 border-t pt-3" data-testid="retrospective-agent-config">
              <div className="flex flex-col gap-1">
                <div className="text-body font-medium">{t(($) => $.retrospective.agentTitle)}</div>
                <p className="text-muted-foreground text-caption">{t(($) => $.retrospective.agentDescription)}</p>
              </div>
              <div className="flex flex-col gap-1">
                <Label htmlFor="retro-agent">{t(($) => $.retrospective.agentLabel)}</Label>
                <Select
                  items={agentItems}
                  value={agentId || "none"}
                  onValueChange={(v) => setAgentId(!v || v === "none" ? "" : v)}
                  disabled={!canManage}
                >
                  <SelectTrigger id="retro-agent" className="w-72" data-testid="retrospective-agent-select">
                    <SelectValue placeholder={t(($) => $.retrospective.agentPlaceholder)} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">{t(($) => $.retrospective.agentPlaceholder)}</SelectItem>
                    {agentChoices.map((a) => (
                      <SelectItem key={a.id} value={a.id}>
                        {a.name}
                        {a.archived_at ? t(($) => $.retrospective.agentArchivedSuffix) : ""}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {selectedAgent?.archived_at ? (
                  <p className="text-muted-foreground text-caption" data-testid="retrospective-agent-archived">
                    {t(($) => $.retrospective.agentArchivedHint)}
                  </p>
                ) : null}
              </div>
            </div>

            {canManage ? (
              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  disabled={saveConfig.isPending}
                  onClick={() =>
                    saveConfig.mutate(
                      {
                        enabled,
                        include_in_review: includeInReview,
                        window_days: windowDays,
                        agent_id: agentId,
                      },
                      {
                        onError: mutationError,
                        onSuccess: () => toast.success(t(($) => $.retrospective.saveOk)),
                      },
                    )
                  }
                >
                  {t(($) => $.retrospective.save)}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={trigger.isPending}
                  onClick={() =>
                    trigger.mutate(undefined, {
                      onError: triggerError,
                      onSuccess: () => toast.success(t(($) => $.retrospective.triggerOk)),
                    })
                  }
                >
                  {t(($) => $.retrospective.trigger)}
                </Button>
              </div>
            ) : null}
          </div>
        )}
      </div>

      <div className="flex flex-col gap-2">
        <div className="text-body font-medium">{t(($) => $.retrospective.runs)}</div>
        {runs.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : runsList.length === 0 ? (
          <Empty data-testid="retrospective-empty">
            <EmptyHeader>
              {/* eslint-disable-next-line i18next/no-literal-string -- icon glyph, not copy */}
              <EmptyMedia variant="icon">↻</EmptyMedia>
              <EmptyTitle>{t(($) => $.retrospective.empty)}</EmptyTitle>
              <EmptyDescription>{t(($) => $.retrospective.emptyDescription)}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ul className="flex flex-col gap-2" data-testid="retrospective-runs">
            {runsList.map((run) => (
              <li key={run.id} className="rounded-md border p-3 text-body">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge
                    variant={run.status === "succeeded" ? "default" : run.status === "failed" ? "destructive" : "secondary"}
                  >
                    {statusLabel(run.status)}
                  </Badge>
                  <span className="text-muted-foreground text-caption">
                    {new Date(run.window_start).toLocaleString()} –{" "}
                    {new Date(run.window_end).toLocaleString()} · {run.trigger}
                  </span>
                </div>
                <div className="text-muted-foreground mt-1 text-caption">
                  {t(($) => $.retrospective.scanned)} {run.issues_scanned} ·{" "}
                  {t(($) => $.retrospective.analyzed)} {run.issues_analyzed} ·{" "}
                  {t(($) => $.retrospective.created)} {run.proposals_created} ·{" "}
                  {t(($) => $.retrospective.merged)} {run.proposals_merged} ·{" "}
                  {t(($) => $.retrospective.duplicates)} {run.duplicates_skipped}
                </div>
                {run.error ? (
                  <div className="text-destructive mt-1 text-caption" data-testid="retrospective-run-error">
                    {t(($) => $.retrospective.errorLabel)}: {run.error}
                  </div>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
