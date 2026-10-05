import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { oauthAdminKeys } from "./queries";

// Every OAuth management mutation invalidates the whole oauth-admin
// subtree: client edits move grant tallies, disable/enable and revocation
// move the grants directory, and the status panel counts both.

export function useAdminCreateOAuthClient() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; redirect_uris: string[] }) => api.adminCreateOAuthClient(body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: oauthAdminKeys.all() });
    },
  });
}

export function useAdminUpdateOAuthClient() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; name: string; redirect_uris: string[] }) =>
      api.adminUpdateOAuthClient(id, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: oauthAdminKeys.all() });
    },
  });
}

export function useAdminSetOAuthClientDisabled() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, disabled, reason }: { id: string; disabled: boolean; reason?: string }) =>
      api.adminSetOAuthClientDisabled(id, disabled, reason),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: oauthAdminKeys.all() });
    },
  });
}

export function useAdminRotateOAuthClientSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, reason }: { id: string; reason?: string }) => api.adminRotateOAuthClientSecret(id, reason),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: oauthAdminKeys.all() });
    },
  });
}

export function useAdminDeleteOAuthClient() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, reason }: { id: string; reason?: string }) => api.adminDeleteOAuthClient(id, reason),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: oauthAdminKeys.all() });
    },
  });
}

export function useAdminRevokeOAuthGrant() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, reason }: { id: string; reason?: string }) => api.adminRevokeOAuthGrant(id, reason),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: oauthAdminKeys.all() });
    },
  });
}

export function useRevokeMyOAuthGrant() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.revokeMyOAuthGrant(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: oauthAdminKeys.myGrants() });
    },
  });
}
