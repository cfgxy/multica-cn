import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { AdminMCPStatus, AdminOAuthClientList, AdminOAuthGrantList, MyOAuthGrantList } from "./types";

// OAuth management data is instance-level, not workspace-scoped — the
// keys are deliberately free of wsId (same reasoning as adminKeys).
export const oauthAdminKeys = {
  all: () => ["oauth-admin"] as const,
  clients: () => [...oauthAdminKeys.all(), "clients"] as const,
  grants: () => [...oauthAdminKeys.all(), "grants"] as const,
  status: () => [...oauthAdminKeys.all(), "mcp-status"] as const,
  myGrants: () => ["oauth", "my-grants"] as const,
};

export function adminOAuthClientsOptions() {
  return queryOptions({
    queryKey: oauthAdminKeys.clients(),
    queryFn: () => api.adminListOAuthClients(),
    placeholderData: (prev: AdminOAuthClientList | undefined) => prev,
  });
}

export function adminOAuthGrantsOptions() {
  return queryOptions({
    queryKey: oauthAdminKeys.grants(),
    queryFn: () => api.adminListOAuthGrants(),
    placeholderData: (prev: AdminOAuthGrantList | undefined) => prev,
  });
}

export function adminMCPStatusOptions() {
  return queryOptions({
    queryKey: oauthAdminKeys.status(),
    queryFn: () => api.adminMCPServerStatus(),
    placeholderData: (prev: AdminMCPStatus | undefined) => prev,
  });
}

export function myOAuthGrantsOptions() {
  return queryOptions({
    queryKey: oauthAdminKeys.myGrants(),
    queryFn: () => api.listMyOAuthGrants(),
    placeholderData: (prev: MyOAuthGrantList | undefined) => prev,
  });
}
