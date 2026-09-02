import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { adminKeys } from "./queries";

// Directory mutations invalidate BOTH lists: enabling/disabling a user or
// granting/revoking admin changes rows the user page renders, and the
// workspace page shows member counts that a direct add moves.

export function useAdminSetUserDisabled() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ userId, disabled, reason }: { userId: string; disabled: boolean; reason?: string }) =>
      api.adminSetUserDisabled(userId, disabled, reason),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: adminKeys.all() });
    },
  });
}

export function useAdminSetUserSuperAdmin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ userId, granted, reason }: { userId: string; granted: boolean; reason?: string }) =>
      api.adminSetUserSuperAdmin(userId, granted, reason),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: adminKeys.all() });
    },
  });
}

export function useAdminAddWorkspaceMember() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ workspaceId, ...data }: {
      workspaceId: string;
      email?: string;
      user_id?: string;
      role: "member" | "admin";
      reason?: string;
    }) => api.adminAddWorkspaceMember(workspaceId, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: adminKeys.all() });
    },
  });
}
