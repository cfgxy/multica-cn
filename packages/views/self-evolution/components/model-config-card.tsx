"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { clientErrorMessage } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import {
  useRestoreSelfEvolutionModelDefault,
  useSaveSelfEvolutionModelConfig,
  useValidateSelfEvolutionModelConfig,
  selfEvolutionModelConfigOptions,
} from "@multica/core/self-evolution";
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
import { Button } from "@multica/ui/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Switch } from "@multica/ui/components/ui/switch";
import { useLocale, useT } from "../../i18n";
import { ConfigSourceBadge } from "./config-source-badge";
import { SecretField } from "./secret-field";

/**
 * The one model-service card (RUYI-551 §3): a single workspace-scoped LLM
 * endpoint behind the module's quality scoring. One card, not two — two
 * cards would invite diverging credentials. (The daily retrospective used
 * to consume this endpoint too, but since RUYI-552's agent-based rework it
 * runs as an agent run and no longer reads this config.)
 *
 * Save is validate-first: a config that fails its connectivity check is
 * never persisted (the server refuses too, but failing here keeps the
 * classified reason next to the fields). The stored key is never echoed —
 * an empty SecretField means "keep the existing key".
 */
export function ModelConfigCard({
  wsId,
  showConsumers = false,
}: {
  wsId: string;
  /** Module-config page variant: name both consumers and their switches. */
  showConsumers?: boolean;
}) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const currentMember = useCurrentMember(wsId);
  const canManage = currentMember.role === "owner";

  const config = useQuery(selfEvolutionModelConfigOptions(wsId));
  const save = useSaveSelfEvolutionModelConfig(wsId);
  const validate = useValidateSelfEvolutionModelConfig(wsId);
  const restore = useRestoreSelfEvolutionModelDefault(wsId);

  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [model, setModel] = useState("");
  const [scoringEnabled, setScoringEnabled] = useState(true);
  const [loaded, setLoaded] = useState(false);
  const [confirmRestore, setConfirmRestore] = useState(false);
  const [validation, setValidation] = useState<{ ok: boolean; message: string } | null>(null);

  useEffect(() => {
    const data = config.data;
    if (!data || loaded) return;
    setBaseUrl(data.override?.base_url ?? "");
    setModel(data.override?.model ?? "");
    setScoringEnabled(data.override?.scoring_enabled ?? data.scoring_enabled);
    setLoaded(true);
  }, [config.data, loaded]);

  if (config.isPending) {
    return (
      <Card data-testid="model-config-card">
        <CardHeader>
          <Skeleton className="h-5 w-32" />
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
        </CardContent>
      </Card>
    );
  }
  if (config.isError || !config.data) {
    return (
      <Card data-testid="model-config-card">
        <CardHeader>
          <CardTitle>{t(($) => $.modelConfig.title)}</CardTitle>
        </CardHeader>
        <CardContent>
          <p role="alert" className="text-body text-destructive">
            {t(($) => $.knowledge.error.read)}
            <Button
              size="sm"
              variant="outline"
              className="ml-2"
              onClick={() => void config.refetch()}
            >
              {t(($) => $.knowledge.error.retry)}
            </Button>
          </p>
        </CardContent>
      </Card>
    );
  }

  const data = config.data;
  const resolved = data.resolved;
  const override = data.override;

  const errorKindLabel = (kind: string) => {
    switch (kind) {
      case "credentials": return t(($) => $.modelConfig.errorKind.credentials);
      case "model": return t(($) => $.modelConfig.errorKind.model);
      case "connection": return t(($) => $.modelConfig.errorKind.connection);
      case "not_configured": return t(($) => $.modelConfig.errorKind.notConfigured);
      default: return t(($) => $.modelConfig.errorKind.other);
    }
  };

  /** Validate-first save: a failed check never reaches the PUT. */
  const saveWithValidation = () => {
    setValidation(null);
    validate.mutate(
      { base_url: baseUrl.trim(), api_key: apiKey, model: model.trim() },
      {
        onSuccess: (result) => {
          if (!result.ok) {
            setValidation({ ok: false, message: result.message || errorKindLabel(result.error_kind) });
            return;
          }
          save.mutate(
            {
              base_url: baseUrl.trim(),
              api_key: apiKey,
              model: model.trim(),
              scoring_enabled: scoringEnabled,
            },
            {
              onSuccess: () => {
                setApiKey("");
                setValidation({ ok: true, message: "" });
                toast.success(t(($) => $.modelConfig.saveOk));
              },
              onError: (e) => {
                toast.error(clientErrorMessage(e) ?? t(($) => $.knowledge.error.write));
              },
            },
          );
        },
        onError: (e) => {
          toast.error(clientErrorMessage(e) ?? t(($) => $.knowledge.error.write));
        },
      },
    );
  };

  const revalidateStored = () => {
    setValidation(null);
    validate.mutate(undefined, {
      onSuccess: (result) => {
        setValidation(
          result.ok
            ? { ok: true, message: "" }
            : { ok: false, message: result.message || errorKindLabel(result.error_kind) },
        );
      },
      onError: (e) => {
        toast.error(clientErrorMessage(e) ?? t(($) => $.knowledge.error.write));
      },
    });
  };

  const busy = validate.isPending || save.isPending;
  const lastValidated = override?.last_validated_at;

  return (
    <Card data-testid="model-config-card">
      <CardHeader>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <CardTitle>{t(($) => $.modelConfig.title)}</CardTitle>
          {resolved.source ? (
            <ConfigSourceBadge source={resolved.source} />
          ) : (
            <span className="text-caption text-muted-foreground">
              {t(($) => $.sources.states.unconfigured)}
            </span>
          )}
        </div>
        <CardDescription>{t(($) => $.modelConfig.description)}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {!data.encryption_ready ? (
          <p className="rounded-md border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-caption" role="note">
            {t(($) => $.modelConfig.encryptionNotReady)}
          </p>
        ) : null}

        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="model-config-base-url">{t(($) => $.modelConfig.baseUrl)}</Label>
            <Input
              id="model-config-base-url"
              value={baseUrl}
              disabled={!canManage || !data.encryption_ready}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder="https://llm-gateway.example.com/v1"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="model-config-api-key">{t(($) => $.modelConfig.apiKey)}</Label>
            <SecretField
              id="model-config-api-key"
              value={apiKey}
              onChange={setApiKey}
              hasExisting={override?.has_api_key === true}
              disabled={!canManage || !data.encryption_ready}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="model-config-model">{t(($) => $.modelConfig.model)}</Label>
            <Input
              id="model-config-model"
              value={model}
              disabled={!canManage || !data.encryption_ready}
              onChange={(e) => setModel(e.target.value)}
              placeholder={resolved.model || "gpt-4o-mini"}
            />
          </div>
        </div>

        {showConsumers ? (
          <div className="flex flex-col gap-2 rounded-md border px-3 py-2.5" data-testid="model-config-consumers">
            <span className="text-caption text-muted-foreground">
              {t(($) => $.modelConfig.consumersTitle)}
            </span>
            <div className="flex items-center justify-between gap-3">
              <span className="text-body">{t(($) => $.modelConfig.scoringConsumer)}</span>
              <Switch
                checked={scoringEnabled}
                disabled={!canManage}
                onCheckedChange={(v) => setScoringEnabled(v === true)}
                aria-label={t(($) => $.modelConfig.scoringConsumer)}
              />
            </div>
          </div>
        ) : null}

        <div className="flex flex-wrap items-center gap-3">
          {canManage && data.encryption_ready ? (
            <Button size="sm" disabled={busy || baseUrl.trim() === "" || model.trim() === ""} onClick={saveWithValidation}>
              {busy ? t(($) => $.modelConfig.busy) : t(($) => $.modelConfig.saveAndValidate)}
            </Button>
          ) : null}
          {override ? (
            <Button
              size="sm"
              variant="outline"
              disabled={restore.isPending || !canManage}
              onClick={() => setConfirmRestore(true)}
            >
              {t(($) => $.modelConfig.restoreDefault)}
            </Button>
          ) : null}
          {override?.base_url && override?.model ? (
            <Button
              size="sm"
              variant="outline"
              disabled={validate.isPending}
              onClick={revalidateStored}
            >
              {t(($) => $.modelConfig.revalidate)}
            </Button>
          ) : null}
          <span className="text-caption text-muted-foreground">
            {lastValidated
              ? t(($) => $.modelConfig.lastValidated, {
                  date: new Date(lastValidated).toLocaleString(locale),
                })
              : t(($) => $.modelConfig.neverValidated)}
          </span>
        </div>

        {validation ? (
          validation.ok ? (
            <p className="text-caption text-emerald-600" role="status" data-testid="model-config-validation-ok">
              {t(($) => $.modelConfig.validationOk)}
            </p>
          ) : (
            <p className="text-caption text-destructive" role="alert" data-testid="model-config-validation-error">
              {t(($) => $.modelConfig.validationFailed)}
              {validation.message ? `: ${validation.message}` : ""}
            </p>
          )
        ) : null}
        {resolved.status === "error" && !validation ? (
          <p className="text-caption text-destructive" role="alert">
            {t(($) => $.modelConfig.validationFailed)}
            {override?.last_validation_error ? `: ${override.last_validation_error}` : ""}
          </p>
        ) : null}
      </CardContent>

      <AlertDialog open={confirmRestore} onOpenChange={setConfirmRestore}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.modelConfig.restoreDefault)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.modelConfig.restoreConfirm)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.skills.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                restore.mutate(undefined, {
                  onSuccess: () => {
                    setApiKey("");
                    setBaseUrl("");
                    setModel("");
                    setValidation(null);
                    toast.success(t(($) => $.modelConfig.restoreOk));
                  },
                  onError: (e) => {
                    toast.error(clientErrorMessage(e) ?? t(($) => $.knowledge.error.write));
                  },
                })
              }
            >
              {t(($) => $.modelConfig.restoreDefault)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
