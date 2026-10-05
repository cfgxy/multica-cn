/**
 * IM integration installations — mobile mirror of the five core
 * installations options (packages/core/lark/queries.ts and the slack /
 * dingtalk / wecom / telegram counterparts). Mobile only consumes the
 * `configured` bit (agent detail integrations-row visibility, mirroring
 * web agent-overview-pane), so all five share one minimal shape.
 */
import { queryOptions } from "@tanstack/react-query";
import type { IntegrationInstallations } from "@/data/schemas";
import { api } from "@/data/api";

export const integrationKeys = {
  all: (wsId: string) => ["integrations", wsId] as const,
  installations: (wsId: string, platform: string) =>
    [...integrationKeys.all(wsId), "installations", platform] as const,
};

export function larkInstallationsOptions(wsId: string | null) {
  return queryOptions({
    queryKey: integrationKeys.installations(wsId ?? "_", "lark"),
    queryFn: () => api.listLarkInstallations(wsId as string),
    enabled: !!wsId,
  });
}

export function slackInstallationsOptions(wsId: string | null) {
  return queryOptions({
    queryKey: integrationKeys.installations(wsId ?? "_", "slack"),
    queryFn: () => api.listSlackInstallations(wsId as string),
    enabled: !!wsId,
  });
}

export function dingTalkInstallationsOptions(wsId: string | null) {
  return queryOptions({
    queryKey: integrationKeys.installations(wsId ?? "_", "dingtalk"),
    queryFn: () => api.listDingTalkInstallations(wsId as string),
    enabled: !!wsId,
  });
}

export function wecomInstallationsOptions(wsId: string | null) {
  return queryOptions({
    queryKey: integrationKeys.installations(wsId ?? "_", "wecom"),
    queryFn: () => api.listWecomInstallations(wsId as string),
    enabled: !!wsId,
  });
}

export function telegramInstallationsOptions(wsId: string | null) {
  return queryOptions({
    queryKey: integrationKeys.installations(wsId ?? "_", "telegram"),
    queryFn: () => api.listTelegramInstallations(wsId as string),
    enabled: !!wsId,
  });
}

export type { IntegrationInstallations };
