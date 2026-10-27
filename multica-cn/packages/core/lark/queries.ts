import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/** Query key namespace for everything Lark-installation-related. Realtime
 * sync invalidates `installations(wsId)` on `lark_installation:*` events
 * so the Settings panel updates without a refetch. */
export const larkKeys = {
  all: (wsId: string) => ["lark", wsId] as const,
  installations: (wsId: string) => [...larkKeys.all(wsId), "installations"] as const,
};

export const larkInstallationsOptions = (wsId: string) =>
  queryOptions({
    queryKey: larkKeys.installations(wsId),
    queryFn: () => api.listLarkInstallations(wsId),
    enabled: !!wsId,
  });

/** The static capability→scope catalog (RUYI-400). It only changes with
 * a server deploy, so cache it for the session instead of refetching on
 * every dialog open. */
export const larkPermissionCatalogOptions = (wsId: string) =>
  queryOptions({
    queryKey: [...larkKeys.all(wsId), "permission-catalog"] as const,
    queryFn: () => api.getLarkPermissionCatalog(wsId),
    enabled: !!wsId,
    staleTime: Infinity,
    gcTime: 30 * 60 * 1000,
  });
