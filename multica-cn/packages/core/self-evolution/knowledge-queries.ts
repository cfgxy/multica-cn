import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { RegisterKnowledgeDirRequest } from "../types";

/** Knowledge mirror keys (RUYI-265 §K). */
export const knowledgeKeys = {
  all: (wsId: string) => ["knowledge", wsId] as const,
  dirs: (wsId: string) => [...knowledgeKeys.all(wsId), "dirs"] as const,
  entries: (wsId: string, dirId: string, query: string) =>
    [...knowledgeKeys.all(wsId), "entries", dirId, query] as const,
};

export function knowledgeDirsOptions(wsId: string) {
  return queryOptions({
    queryKey: knowledgeKeys.dirs(wsId),
    queryFn: () => api.listKnowledgeDirs(),
    enabled: !!wsId,
  });
}

export function knowledgeEntriesOptions(wsId: string, dirId: string, query: string) {
  return queryOptions({
    queryKey: knowledgeKeys.entries(wsId, dirId, query),
    queryFn: () => api.listKnowledgeEntries({
      ...(dirId ? { dir_id: dirId } : {}),
      ...(query ? { q: query } : {}),
    }),
    enabled: !!wsId,
  });
}

/** Registering a directory runs the initial scan server-side, so the entry
 * mirror under the old directory list is stale too — invalidate the tree. */
export function useRegisterKnowledgeDir(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: RegisterKnowledgeDirRequest) => api.registerKnowledgeDir(data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) });
    },
  });
}

export function useScanKnowledgeDir(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.scanKnowledgeDir(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) });
    },
  });
}

export function useUnregisterKnowledgeDir(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.unregisterKnowledgeDir(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) });
    },
  });
}

/** Adoption writes into the ultimate library; the dirs panel's counts and
 * the ultimate dir's own mirror both change. */
export function useAdoptKnowledgeEntry(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.adoptKnowledgeEntry(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) });
    },
  });
}
