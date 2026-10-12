import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type {
  ModelConfigAction,
  ModelConfigInput,
  ModelTemplate,
} from "./model-config-schema";

export const modelConfigKeys = {
  admin: (userId: string) => ["model-configuration", userId, "admin"] as const,
  options: (userId: string, bindingId: string, kind: string) =>
    ["model-configuration", userId, bindingId, kind, "keys"] as const,
};
export function useModelKeyAdmin(
  userId: string,
  search: string,
  runtime: string,
  page: number,
  userSearch: string,
  targetUserId: string,
) {
  const client = useQueryClient();
  const key = modelConfigKeys.admin(userId);
  const source = useQuery({
    queryKey: [...key, "source"],
    queryFn: () => api.getModelKeySource(),
    retry: false,
  });
  const directory = useQuery({
    queryKey: [...key, "directory", search, runtime, page],
    queryFn: () => api.getModelKeyDirectory({ search, runtime, page }),
    enabled: source.data?.configured === true,
    retry: false,
  });
  const users = useQuery({
    queryKey: [...key, "users", userSearch],
    queryFn: () => api.getModelKeyUsers(userSearch),
  });
  const grants = useQuery({
    queryKey: [...key, "grants", targetUserId],
    queryFn: () => api.getUserModelKeys(targetUserId),
    enabled: !!targetUserId,
  });
  const invalidate = () => client.invalidateQueries({ queryKey: key });
  const saveSource = useMutation({
    gcTime: 0,
    mutationFn: (input: {
      baseURL: string;
      directoryToken: string;
      revision: number;
    }) => api.saveModelKeySource(input),
    onSuccess: invalidate,
  });
  const refresh = useMutation({
    mutationFn: () => api.refreshModelKeyDirectory(),
    onSettled: invalidate,
  });
  const saveGrants = useMutation({
    mutationFn: (ids: string[]) => api.saveUserModelKeys(targetUserId, ids),
    onSuccess: async () => {
      await invalidate();
      await client.invalidateQueries({
        queryKey: ["model-configuration", targetUserId],
      });
    },
  });
  return { source, directory, users, grants, saveSource, refresh, saveGrants };
}
export function useModelTemplates(userId: string) {
  const client = useQueryClient();
  const key = [...modelConfigKeys.admin(userId), "templates"];
  const templates = useQuery({
    queryKey: key,
    queryFn: () => api.getModelTemplates(),
    retry: false,
  });
  const save = useMutation({
    gcTime: 0,
    mutationFn: (template: ModelTemplate) => api.saveModelTemplate(template),
    onSuccess: () => client.invalidateQueries({ queryKey: key }),
  });
  return { templates, save };
}
export function useRuntimeModelConfig(
  userId: string,
  bindingId: string,
  kind: string,
) {
  const client = useQueryClient();
  const keys = useQuery({
    queryKey: modelConfigKeys.options(userId, bindingId, kind),
    queryFn: () => api.getRuntimeModelKeys(bindingId, kind),
    retry: false,
  });
  // Passwords and file bodies live only while the editor is mounted. Mutation
  // records are reset after each request so devtools do not retain snapshots.
  const action = useMutation({
    gcTime: 0,
    retry: false,
    mutationFn: ({
      action,
      input,
    }: {
      action: ModelConfigAction;
      input: ModelConfigInput;
    }) => api.runtimeModelConfig(bindingId, kind, action, input),
    onSettled: async (_result, _error, variables) => {
      if (variables.action === "save" || variables.action === "test")
        await client.invalidateQueries({ queryKey: ["computers", userId] });
    },
  });
  return { keys, action };
}
