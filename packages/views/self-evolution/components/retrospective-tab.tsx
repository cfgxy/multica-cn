"use client";

import { useEffect, useState } from "react";
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
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { ApiError, clientErrorMessage } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import {
  retrospectiveConfigOptions,
  retrospectiveRunsOptions,
  useTriggerRetrospectiveRun,
  useUpdateRetrospectiveConfig,
} from "@multica/core/self-evolution";
import type {
  RetrospectiveLLMPatch,
  RetrospectiveLLMSource,
  RetrospectiveRun,
} from "@multica/core/types";
import { useT } from "../../i18n";

/**
 * The daily retrospective tab (RUYI-305 E3).
 *
 * The job itself is base-platform plumbing: a registered scheduler task reads
 * the real execution content of completed issues inside the lookback window,
 * distills prompt-improvement drafts into the legislation pool, and keeps a
 * per-issue watermark for idempotency. It never posts issue comments — the
 * only visible traces are the config, the run records below (failures name
 * the reason, e.g. a missing LLM configuration) and, on success, new
 * proposals in the legislation tab.
 *
 * The LLM section (RUYI-552) makes that configuration a product config: the
 * workspace saves base URL / model / API key here, runs resolve it first and
 * fall back to deployment defaults per field. The key itself never comes
 * back — the server returns only whether one is saved plus a masked hint.
 */
export function RetrospectiveTab({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const currentMember = useCurrentMember(wsId);
  const canManage = currentMember.role === "owner";

  const config = useQuery(retrospectiveConfigOptions(wsId));
  const runs = useQuery(retrospectiveRunsOptions(wsId));
  const saveConfig = useUpdateRetrospectiveConfig(wsId);
  const trigger = useTriggerRetrospectiveRun(wsId);

  const [enabled, setEnabled] = useState(false);
  const [includeInReview, setIncludeInReview] = useState(false);
  const [windowDays, setWindowDays] = useState(7);
  const [llmBaseUrl, setLlmBaseUrl] = useState("");
  const [llmModel, setLlmModel] = useState("");
  const [llmApiKey, setLlmApiKey] = useState("");
  const [clearApiKey, setClearApiKey] = useState(false);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (config.data && !loaded) {
      setEnabled(config.data.enabled);
      setIncludeInReview(config.data.include_in_review);
      setWindowDays(config.data.window_days);
      setLlmBaseUrl(config.data.llm?.stored.base_url ?? "");
      setLlmModel(config.data.llm?.stored.model ?? "");
      setLoaded(true);
    }
  }, [config.data, loaded]);

  const mutationError = (e: unknown) =>
    toast.error(clientErrorMessage(e) ?? t(($) => $.retrospective.errorLabel));

  // The server's 409 on "run now" is the no-configured-LLM state; the UI
  // renders its own localized copy with the fix in view (the section above),
  // not the server's sentence.
  const triggerError = (e: unknown) => {
    if (e instanceof ApiError && e.status === 409) {
      toast.error(t(($) => $.retrospective.llmTriggerBlocked));
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

  const llmSourceLabel = (s: RetrospectiveLLMSource | string) => {
    switch (s) {
      case "workspace": return t(($) => $.retrospective.llmSourceWorkspace);
      case "deployment": return t(($) => $.retrospective.llmSourceDeployment);
      default: return t(($) => $.retrospective.llmSourceNone);
    }
  };

  const llm = config.data?.llm;

  const saveLlmPatch = (): RetrospectiveLLMPatch => {
    const patch: RetrospectiveLLMPatch = {
      base_url: llmBaseUrl.trim(),
      model: llmModel.trim(),
    };
    if (llmApiKey.trim()) {
      patch.api_key = llmApiKey.trim();
    } else if (clearApiKey) {
      patch.api_key = "";
    }
    return patch;
  };

  const scrollToLlmConfig = () =>
    document.getElementById("retrospective-llm-config")?.scrollIntoView({ behavior: "smooth", block: "center" });

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

            <div className="mt-2 flex flex-col gap-3 border-t pt-3" data-testid="retrospective-llm-config" id="retrospective-llm-config">
              <div className="flex flex-col gap-1">
                <div className="text-body font-medium">{t(($) => $.retrospective.llmTitle)}</div>
                <p className="text-muted-foreground text-caption">{t(($) => $.retrospective.llmDescription)}</p>
              </div>
              <div className="flex flex-col gap-1">
                <Label htmlFor="retro-llm-base-url">{t(($) => $.retrospective.llmBaseUrl)}</Label>
                <Input
                  id="retro-llm-base-url"
                  value={llmBaseUrl}
                  placeholder={t(($) => $.retrospective.llmBaseUrlPlaceholder)}
                  autoComplete="off"
                  spellCheck={false}
                  disabled={!canManage}
                  onChange={(e) => setLlmBaseUrl(e.target.value)}
                />
              </div>
              <div className="flex flex-col gap-1">
                <Label htmlFor="retro-llm-model">{t(($) => $.retrospective.llmModel)}</Label>
                <Input
                  id="retro-llm-model"
                  value={llmModel}
                  placeholder={t(($) => $.retrospective.llmModelPlaceholder)}
                  autoComplete="off"
                  spellCheck={false}
                  disabled={!canManage}
                  onChange={(e) => setLlmModel(e.target.value)}
                />
              </div>
              <div className="flex flex-col gap-1">
                <Label htmlFor="retro-llm-api-key">{t(($) => $.retrospective.llmApiKey)}</Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="retro-llm-api-key"
                    type="password"
                    value={llmApiKey}
                    placeholder={
                      llmApiKey.trim() || clearApiKey
                        ? t(($) => $.retrospective.llmApiKeyPlaceholderNew)
                        : llm?.stored.api_key_set && llm.stored.api_key_hint
                          ? t(($) => $.retrospective.llmApiKeySavedHint, { hint: llm.stored.api_key_hint })
                          : t(($) => $.retrospective.llmApiKeyPlaceholderNew)
                    }
                    autoComplete="new-password"
                    disabled={!canManage}
                    onChange={(e) => {
                      setLlmApiKey(e.target.value);
                      if (e.target.value) setClearApiKey(false);
                    }}
                  />
                  {canManage && llm?.stored.api_key_set ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={clearApiKey}
                      onClick={() => {
                        setLlmApiKey("");
                        setClearApiKey(true);
                      }}
                    >
                      {t(($) => $.retrospective.llmApiKeyClear)}
                    </Button>
                  ) : null}
                </div>
              </div>
              {llm ? (
                <div className="flex flex-col gap-1 text-caption" data-testid="retrospective-llm-effective">
                  <span className="font-medium">{t(($) => $.retrospective.llmEffective)}</span>
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant={llm.effective.issue ? "destructive" : llm.effective.source === "none" ? "secondary" : "default"}>
                      {llmSourceLabel(llm.effective.source)}
                    </Badge>
                    <span className="text-muted-foreground">
                      {llm.effective.base_url || "—"} · {llm.effective.model || "—"}
                      {llm.effective.api_key_hint ? ` · ${llm.effective.api_key_hint}` : ""}
                    </span>
                  </div>
                  {llm.effective.issue ? (
                    <span className="text-destructive" data-testid="retrospective-llm-issue">
                      {t(($) => $.retrospective.llmIssue)}: {llm.effective.issue}
                    </span>
                  ) : null}
                </div>
              ) : null}
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
                        llm: saveLlmPatch(),
                      },
                      {
                        onError: mutationError,
                        onSuccess: () => {
                          setLlmApiKey("");
                          setClearApiKey(false);
                          toast.success(t(($) => $.retrospective.saveOk));
                        },
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
              <EmptyDescription>{t(($) => $.retrospective.llmUnavailable)}</EmptyDescription>
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
                    {run.detail?.llm_configured === false ? (
                      <Button
                        size="sm"
                        variant="link"
                        className="h-auto p-0 pl-2 align-baseline"
                        onClick={scrollToLlmConfig}
                      >
                        {t(($) => $.retrospective.llmConfigEntry)}
                      </Button>
                    ) : null}
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
