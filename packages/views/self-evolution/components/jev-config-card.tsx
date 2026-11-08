"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { clientErrorMessage } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import {
  selfEvolutionJevConfigOptions,
  useDeleteSelfEvolutionJevConfig,
  useRecheckSelfEvolutionJevConfig,
  useSaveSelfEvolutionJevConfig,
} from "@multica/core/self-evolution";
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
import { useLocale, useT } from "../../i18n";
import { SecretField } from "./secret-field";

export function JevConfigCard({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const currentMember = useCurrentMember(wsId);
  const canManage = currentMember.role === "owner";
  const config = useQuery(selfEvolutionJevConfigOptions(wsId));
  const save = useSaveSelfEvolutionJevConfig(wsId);
  const remove = useDeleteSelfEvolutionJevConfig(wsId);
  const recheck = useRecheckSelfEvolutionJevConfig(wsId);

  const [endpoint, setEndpoint] = useState("");
  const [apiKey, setAPIKey] = useState("");
  const [model, setModel] = useState("");
  const [timeoutMS, setTimeoutMS] = useState("1500");
  const [loadedWorkspaceID, setLoadedWorkspaceID] = useState<string | null>(null);

  useEffect(() => {
    if (!canManage || !config.data || loadedWorkspaceID === wsId) return;
    setEndpoint(config.data.endpoint);
    setModel(config.data.model);
    setTimeoutMS(String(config.data.timeout_ms));
    setLoadedWorkspaceID(wsId);
  }, [canManage, config.data, loadedWorkspaceID, wsId]);

  if (config.isPending) {
    return (
      <Card data-testid="jev-config-card">
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
      <Card data-testid="jev-config-card">
        <CardHeader>
          <CardTitle>{t(($) => $.jevConfig.title)}</CardTitle>
        </CardHeader>
        <CardContent>
          <p role="alert" className="text-body text-destructive">
            {t(($) => $.knowledge.error.read)}
            <Button size="sm" variant="outline" className="ml-2" onClick={() => void config.refetch()}>
              {t(($) => $.knowledge.error.retry)}
            </Button>
          </p>
        </CardContent>
      </Card>
    );
  }

  const data = config.data;
  const busy = save.isPending || remove.isPending || recheck.isPending;
  const timeout = Number(timeoutMS);
  const canSave =
    endpoint.trim() !== "" &&
    model.trim() !== "" &&
    Number.isInteger(timeout) &&
    timeout >= 100 &&
    timeout <= 30000;

  const statusLabel = () => {
    switch (data.status) {
      case "available":
        return t(($) => $.jevConfig.status.available);
      case "offline":
        return t(($) => $.jevConfig.status.offline);
      default:
        return t(($) => $.jevConfig.status.unconfigured);
    }
  };

  const sourceLabel = () => {
    switch (data.source) {
      case "deployment":
        return t(($) => $.jevConfig.source.deployment);
      case "workspace":
        return t(($) => $.jevConfig.source.workspace);
      default:
        return null;
    }
  };

  if (!canManage) {
    return (
      <Card data-testid="jev-config-card">
        <CardHeader>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <CardTitle>{t(($) => $.jevConfig.title)}</CardTitle>
            <span className="text-caption text-muted-foreground" data-testid="jev-config-status">
              {[statusLabel(), sourceLabel()].filter(Boolean).join(" · ")}
            </span>
          </div>
        </CardHeader>
      </Card>
    );
  }

  const saveConfig = () => {
    save.mutate(
      {
        endpoint: endpoint.trim(),
        api_key: apiKey,
        model: model.trim(),
        timeout_ms: timeout,
      },
      {
        onSuccess: () => {
          setAPIKey("");
          toast.success(t(($) => $.jevConfig.saveOk));
        },
        onError: (error) => {
          toast.error(clientErrorMessage(error) ?? t(($) => $.knowledge.error.write));
        },
      },
    );
  };

  const recheckConfig = () => {
    recheck.mutate(undefined, {
      onError: (error) => {
        toast.error(clientErrorMessage(error) ?? t(($) => $.knowledge.error.write));
      },
    });
  };

  const deleteConfig = () => {
    remove.mutate(undefined, {
      onSuccess: () => {
        setEndpoint("");
        setAPIKey("");
        setModel("");
        setTimeoutMS("1500");
        toast.success(t(($) => $.jevConfig.deleteOk));
      },
      onError: (error) => {
        toast.error(clientErrorMessage(error) ?? t(($) => $.knowledge.error.write));
      },
    });
  };

  return (
    <Card data-testid="jev-config-card">
      <CardHeader>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <CardTitle>{t(($) => $.jevConfig.title)}</CardTitle>
          <span className="text-caption text-muted-foreground" data-testid="jev-config-status">
            {[statusLabel(), sourceLabel()].filter(Boolean).join(" · ")}
          </span>
        </div>
        <CardDescription>{t(($) => $.jevConfig.description)}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {!data.encryption_ready ? (
          <p className="rounded-md border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-caption" role="note">
            {t(($) => $.jevConfig.encryptionNotReady)}
          </p>
        ) : null}

        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="jev-config-endpoint">{t(($) => $.jevConfig.endpoint)}</Label>
            <Input
              id="jev-config-endpoint"
              value={endpoint}
              disabled={!data.encryption_ready}
              onChange={(event) => setEndpoint(event.target.value)}
              placeholder="https://jev.example.com"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="jev-config-api-key">{t(($) => $.jevConfig.apiKey)}</Label>
            <SecretField
              id="jev-config-api-key"
              value={apiKey}
              onChange={setAPIKey}
              hasExisting={data.has_api_key}
              disabled={!data.encryption_ready}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="jev-config-model">{t(($) => $.jevConfig.model)}</Label>
            <Input
              id="jev-config-model"
              value={model}
              disabled={!data.encryption_ready}
              onChange={(event) => setModel(event.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="jev-config-timeout">{t(($) => $.jevConfig.timeout)}</Label>
            <Input
              id="jev-config-timeout"
              type="number"
              min={100}
              max={30000}
              value={timeoutMS}
              disabled={!data.encryption_ready}
              onChange={(event) => setTimeoutMS(event.target.value)}
            />
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-3">
          {canManage && data.encryption_ready ? (
            <Button size="sm" disabled={busy || !canSave} onClick={saveConfig}>
              {busy ? t(($) => $.jevConfig.busy) : t(($) => $.jevConfig.save)}
            </Button>
          ) : null}
          {canManage && data.status !== "unconfigured" ? (
            <Button size="sm" variant="outline" disabled={busy} onClick={recheckConfig}>
              {t(($) => $.jevConfig.recheck)}
            </Button>
          ) : null}
          {canManage && data.source === "workspace" ? (
            <Button size="sm" variant="outline" disabled={busy} onClick={deleteConfig}>
              {t(($) => $.jevConfig.delete)}
            </Button>
          ) : null}
        </div>

        {data.last_checked_at ? (
          <p className="text-caption text-muted-foreground" data-testid="jev-config-last-checked">
            {t(($) => $.jevConfig.lastChecked, {
              date: new Date(data.last_checked_at).toLocaleString(locale),
            })}
          </p>
        ) : null}
        {data.engine ? (
          <p className="text-caption text-muted-foreground" data-testid="jev-config-engine">
            {t(($) => $.jevConfig.engine, { engine: data.engine })}
          </p>
        ) : null}
        {data.status === "offline" && data.last_check_error ? (
          <p className="text-caption text-destructive" role="alert" data-testid="jev-config-error">
            {t(($) => $.jevConfig.lastCheckError, { reason: data.last_check_error })}
          </p>
        ) : null}
      </CardContent>
    </Card>
  );
}
