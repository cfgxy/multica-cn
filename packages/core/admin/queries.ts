import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { AdminUserList, AdminWorkspaceList } from "./types";

// Admin data is instance-level, not workspace-scoped — the keys are
// deliberately free of wsId so navigation between workspaces never
// refetches the directory.
export const adminKeys = {
  all: () => ["admin"] as const,
  users: (params?: { query?: string; limit?: number; offset?: number }) =>
    [...adminKeys.all(), "users", params ?? {}] as const,
  workspaces: (params?: { query?: string; limit?: number; offset?: number }) =>
    [...adminKeys.all(), "workspaces", params ?? {}] as const,
};

export function adminUsersOptions(
  params?: { query?: string; limit?: number; offset?: number },
) {
  return queryOptions({
    queryKey: adminKeys.users(params),
    queryFn: () => api.adminListUsers(params),
    placeholderData: (prev: AdminUserList | undefined) => prev,
  });
}

export function adminWorkspacesOptions(
  params?: { query?: string; limit?: number; offset?: number },
) {
  return queryOptions({
    queryKey: adminKeys.workspaces(params),
    queryFn: () => api.adminListWorkspaces(params),
    placeholderData: (prev: AdminWorkspaceList | undefined) => prev,
  });
}
