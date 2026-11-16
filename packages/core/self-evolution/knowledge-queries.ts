import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type {
  RegisterKnowledgeDirRequest,
  RestoreKnowledgeEntryRequest,
  UpdateKnowledgeLifecycleRequest,
} from "../types";

/** Knowledge mirror keys (RUYI-265 §K). */
export const knowledgeKeys = {
  all: (wsId: string) => ["knowledge", wsId] as const,
  dirs: (wsId: string) => [...knowledgeKeys.all(wsId), "dirs"] as const,
  entries: (wsId: string, dirId: string, query: string, lifecycle: "active" | "archived" | "all") =>
    [...knowledgeKeys.all(wsId), "entries", dirId, query, lifecycle] as const,
  versions: (wsId: string, entryId: string) => [...knowledgeKeys.all(wsId), "versions", entryId] as const,
  lifecycle: (wsId: string, entryId: string) => [...knowledgeKeys.all(wsId), "lifecycle", entryId] as const,
  governanceRuns: (wsId: string, dirId?: string) => [...knowledgeKeys.all(wsId), "governance-runs", dirId ?? ""] as const,
  governanceRun: (wsId: string, runId: string) => [...knowledgeKeys.all(wsId), "governance-run", runId] as const,
};

export function knowledgeDirsOptions(wsId: string) {
  return queryOptions({
    queryKey: knowledgeKeys.dirs(wsId),
    queryFn: () => api.listKnowledgeDirs(),
    enabled: !!wsId,
  });
}

export function knowledgeEntriesOptions(wsId: string, dirId: string, query: string, lifecycle: "active" | "archived" | "all" = "active") {
  return queryOptions({
		queryKey: knowledgeKeys.entries(wsId, dirId, query, lifecycle),
    queryFn: () => api.listKnowledgeEntries({
      ...(dirId ? { dir_id: dirId } : {}),
      ...(query ? { q: query } : {}),
		lifecycle,
    }),
    refetchInterval: (query) =>
      query.state.data?.some((entry) => entry.pending_operation !== undefined) ? 5_000 : false,
    enabled: !!wsId,
  });
}

export function knowledgeEntryVersionsOptions(wsId: string, entryId: string) {
	return queryOptions({
		queryKey: knowledgeKeys.versions(wsId, entryId),
		queryFn: () => api.listKnowledgeEntryVersions(entryId),
		enabled: !!wsId && !!entryId,
	});
}

export function knowledgeEntryLifecycleOptions(wsId: string, entryId: string) {
	return queryOptions({
		queryKey: knowledgeKeys.lifecycle(wsId, entryId),
		queryFn: () => api.listKnowledgeEntryLifecycle(entryId),
		enabled: !!wsId && !!entryId,
	});
}

export function knowledgeGovernanceRunsOptions(wsId: string, dirId?: string) {
  return queryOptions({
    queryKey: knowledgeKeys.governanceRuns(wsId, dirId),
    queryFn: () => api.listKnowledgeGovernanceRuns(dirId),
    enabled: !!wsId,
    refetchInterval: (query) => query.state.data?.some((run) => run.status === "running") ? 3000 : false,
  });
}

export function knowledgeGovernanceRunOptions(wsId: string, runId: string) {
  return queryOptions({
    queryKey: knowledgeKeys.governanceRun(wsId, runId),
    queryFn: () => api.getKnowledgeGovernanceRun(runId),
    enabled: !!wsId && !!runId,
    refetchInterval: (query) => query.state.data?.run.status === "running" ? 3000 : false,
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

function useKnowledgeOperation<T>(wsId: string, mutationFn: (input: T) => Promise<unknown>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) });
    },
  });
}

export function useEditKnowledgeEntry(wsId: string) {
  return useKnowledgeOperation(wsId, ({ id, content }: { id: string; content: string }) =>
    api.editKnowledgeEntry(id, content));
}

export function useDeleteKnowledgeEntry(wsId: string) {
  return useKnowledgeOperation(wsId, (id: string) => api.deleteKnowledgeEntry(id));
}

export function useMergeKnowledgeEntries(wsId: string) {
  return useKnowledgeOperation(wsId, ({ id, content, sourceEntryIds }: {
    id: string;
    content: string;
    sourceEntryIds: string[];
  }) => api.mergeKnowledgeEntries(id, content, sourceEntryIds));
}

export function useSetKnowledgeDirManaged(wsId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: ({ id, managed }: { id: string; managed: boolean }) => api.setKnowledgeDirManaged(id, managed),
		onSuccess: () => qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) }),
	});
}

export function useUpdateKnowledgeEntryLifecycle(wsId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: ({ id, data }: { id: string; data: UpdateKnowledgeLifecycleRequest }) => api.updateKnowledgeEntryLifecycle(id, data),
		onSuccess: () => qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) }),
	});
}

export function useRestoreKnowledgeEntry(wsId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: ({ id, data }: { id: string; data: RestoreKnowledgeEntryRequest }) => api.restoreKnowledgeEntry(id, data),
		onSuccess: () => qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) }),
	});
}

export function useStartKnowledgeGovernanceRun(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (dirId: string) => api.startKnowledgeGovernanceRun(dirId),
    onSuccess: () => qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) }),
  });
}

export function useCancelKnowledgeGovernanceRun(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (runId: string) => api.cancelKnowledgeGovernanceRun(runId),
    onSuccess: () => qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) }),
  });
}

export function useRetryKnowledgeGovernanceRun(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (runId: string) => api.retryKnowledgeGovernanceRun(runId),
    onSuccess: () => qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) }),
  });
}
