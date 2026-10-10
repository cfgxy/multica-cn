/**
 * Agent management mutations (RUYI-346). Endpoint semantics mirror web
 * (packages/core/api/client.ts agents block); cache wiring follows the
 * optimistic-patch + server-wins pattern of `useUpdateProject`
 * (data/mutations/projects.ts).
 *
 * Policy per the design card: writes are await-server + settle-invalidate;
 * only toggles (skill enable, skill remove) patch the cache up-front because
 * they flip a boolean the user is staring at. Archive/restore/delete return
 * the full Agent, which we write into list + detail caches before the
 * settle invalidate reconciles anything the response didn't carry.
 *
 * Cache shapes touched (see data/queries/agents.ts for the factory):
 *   - agentKeys.list(wsId)           → `Agent[]`
 *   - agentKeys.detail(wsId, id)     → `Agent` (skills array lives here)
 *   - agentKeys.webhooks(wsId, id)   → `AgentWebhook[]`
 */
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type {
  Agent,
  AgentWebhook,
  CreateAgentRequest,
  SetAgentSkillsRequest,
  UpdateAgentEnvRequest,
  UpdateAgentRequest,
} from "@multica/core/types";
import { api } from "@/data/api";
import { agentKeys } from "@/data/queries/agents";
import { agentTaskSnapshotKeys } from "@/data/queries/agent-task-snapshot";
import { agentTasksKeys } from "@/data/queries/agent-tasks";
import { issueKeys } from "@/data/queries/issue-keys";
import { useWorkspaceStore } from "@/data/workspace-store";

/** Write the server-authoritative Agent into both the list row and the
 *  detail cache. Shared by every mutation whose endpoint returns an Agent. */
function writeAgent(
  qc: ReturnType<typeof useQueryClient>,
  wsId: string | null,
  agent: Agent,
) {
  qc.setQueryData(agentKeys.detail(wsId, agent.id), agent);
  qc.setQueryData<Agent[]>(agentKeys.list(wsId), (old) =>
    old ? old.map((a) => (a.id === agent.id ? agent : a)) : old,
  );
}

export function useCreateAgent() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (body: CreateAgentRequest) => api.createAgent(body),
    onSuccess: (agent) => {
      // Seed the detail cache so the post-create navigation lands populated;
      // prepend to the list (ordering is presence-driven client-side and the
      // WS agent:created event / next refetch reconciles).
      qc.setQueryData(agentKeys.detail(wsId, agent.id), agent);
      qc.setQueryData<Agent[]>(agentKeys.list(wsId), (old) =>
        old ? [agent, ...old.filter((a) => a.id !== agent.id)] : [agent],
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: agentKeys.all(wsId) });
    },
  });
}

export function useUpdateAgent(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["updateAgent", agentId] as const,
    mutationFn: (patch: UpdateAgentRequest) => api.updateAgent(agentId, patch),
    onMutate: async (patch) => {
      const detailKey = agentKeys.detail(wsId, agentId);
      const listKey = agentKeys.list(wsId);
      await Promise.all([
        qc.cancelQueries({ queryKey: detailKey }),
        qc.cancelQueries({ queryKey: listKey }),
      ]);
      const prevDetail = qc.getQueryData<Agent>(detailKey);
      const prevList = qc.getQueryData<Agent[]>(listKey);
      // Profile fields only — skills/env/webhooks sub-resources are never in
      // an UpdateAgentRequest, so a shallow merge can't clobber them. The
      // cast is cosmetic: TS can't narrow the spread of UpdateAgentRequest's
      // explicit-null "clear" fields down to Agent, and the merged value is
      // transient — the server response replaces it in onSuccess.
      if (prevDetail) {
        qc.setQueryData<Agent>(detailKey, { ...prevDetail, ...patch } as Agent);
      }
      qc.setQueryData<Agent[]>(listKey, (old) =>
        old
          ? old.map((a) =>
              a.id === agentId ? ({ ...a, ...patch } as Agent) : a,
            )
          : old,
      );
      return { prevDetail, prevList, detailKey, listKey };
    },
    onError: (_err, _vars, ctx) => {
      if (!ctx) return;
      if (ctx.prevDetail !== undefined) qc.setQueryData(ctx.detailKey, ctx.prevDetail);
      if (ctx.prevList !== undefined) qc.setQueryData(ctx.listKey, ctx.prevList);
    },
    onSuccess: (server) => {
      writeAgent(qc, wsId, server);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: agentKeys.all(wsId) });
    },
  });
}

// Archive / restore: await the server, write the returned Agent everywhere,
// settle-invalidate the domain. Neither is optimistic — the row visibly
// leaves/enters the default list scope, so a rollback flash would be worse
// than a brief spinner. The id is a mutation argument (not a hook closure)
// because both the list's long-press menu and the detail screen fire these.
export function useArchiveAgent() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["archiveAgent"] as const,
    mutationFn: (agentId: string) => api.archiveAgent(agentId),
    onSuccess: (server) => writeAgent(qc, wsId, server),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: agentKeys.all(wsId) });
    },
  });
}

export function useRestoreAgent() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["restoreAgent"] as const,
    mutationFn: (agentId: string) => api.restoreAgent(agentId),
    onSuccess: (server) => writeAgent(qc, wsId, server),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: agentKeys.all(wsId) });
    },
  });
}

// RUYI-538 ③: per-task cancel replaces the batch cancel-all entry. The
// task id is a mutation argument (not a hook closure), so a swipe on row A
// can only ever fire /cancel with A's id — the neighbor-untouched guarantee
// is asserted in the component test.
export function useCancelAgentTask() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["cancelAgentTask"] as const,
    mutationFn: (taskId: string) => api.cancelTaskById(taskId),
    onSettled: () => {
      // Server broadcasts task:cancelled per row (presence realtime
      // invalidates again); the settle sweep covers missed socket events.
      qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: agentTasksKeys.all(wsId) });
    },
  });
}

// RUYI-538 ②: retry a failed/cancelled run straight from the agent run
// history — same run-level endpoint as the issue runs sheet (RUYI-292).
// Anti-storm 409s surface structured codes; the row maps them through
// retryFailureMessage instead of a blanket error string.
export function useRetryAgentRun() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["retryAgentRun"] as const,
    mutationFn: ({ issueId, taskId }: { issueId: string; taskId: string }) =>
      api.retryIssueRun(issueId, taskId),
    onSettled: (_data, _error, vars) => {
      // The retry mints a new queued task on the source issue and flips the
      // retried row's lineage — refresh both the per-agent list and the
      // issue's runs sheet.
      qc.invalidateQueries({ queryKey: agentTasksKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.tasks(wsId, vars.issueId) });
    },
  });
}

export function useUpdateAgentEnv(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["updateAgentEnv", agentId] as const,
    // Env plaintext is never cached (audit-on-fetch, MUL-2600) — nothing to
    // patch up-front. The response echoes the saved map; the editor consumes
    // it, the cache only needs the detail payload's has_custom_env /
    // custom_env_key_count refreshed.
    mutationFn: (body: UpdateAgentEnvRequest) => api.updateAgentEnv(agentId, body),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: agentKeys.detail(wsId, agentId) });
    },
  });
}

// --- Skills ---

export function useAddAgentSkills(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["addAgentSkills", agentId] as const,
    mutationFn: (body: SetAgentSkillsRequest) => api.addAgentSkills(agentId, body),
    onSettled: () => {
      // Attached skills live inside the detail payload's `skills` array.
      qc.invalidateQueries({ queryKey: agentKeys.detail(wsId, agentId) });
    },
  });
}

export function useSetAgentSkillEnabled(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["setAgentSkillEnabled", agentId] as const,
    mutationFn: ({ skillId, enabled }: { skillId: string; enabled: boolean }) =>
      api.setAgentSkillEnabled(agentId, skillId, enabled),
    onMutate: async ({ skillId, enabled }) => {
      const detailKey = agentKeys.detail(wsId, agentId);
      await qc.cancelQueries({ queryKey: detailKey });
      const prev = qc.getQueryData<Agent>(detailKey);
      if (prev) {
        qc.setQueryData<Agent>(detailKey, {
          ...prev,
          skills: prev.skills.map((s) =>
            s.id === skillId ? { ...s, enabled } : s,
          ),
        });
      }
      return { prev, detailKey };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev !== undefined) qc.setQueryData(ctx.detailKey, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: agentKeys.detail(wsId, agentId) });
    },
  });
}

export function useRemoveAgentSkill(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["removeAgentSkill", agentId] as const,
    mutationFn: (skillId: string) => api.removeAgentSkill(agentId, skillId),
    onMutate: async (skillId) => {
      const detailKey = agentKeys.detail(wsId, agentId);
      await qc.cancelQueries({ queryKey: detailKey });
      const prev = qc.getQueryData<Agent>(detailKey);
      if (prev) {
        qc.setQueryData<Agent>(detailKey, {
          ...prev,
          skills: prev.skills.filter((s) => s.id !== skillId),
        });
      }
      return { prev, detailKey };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev !== undefined) qc.setQueryData(ctx.detailKey, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: agentKeys.detail(wsId, agentId) });
    },
  });
}

// --- Webhooks ---
// Every webhook write returns the full AgentWebhook (rotate mints a new
// token and returns the fresh row) — patch the cached list in place, settle
// invalidate reconciles the manager-gated fields.

function patchWebhookList(
  qc: ReturnType<typeof useQueryClient>,
  key: readonly unknown[],
  next: AgentWebhook,
) {
  qc.setQueryData<import("@multica/core/types").AgentWebhook[]>(key, (old) =>
    old ? old.map((w) => (w.id === next.id ? next : w)) : old,
  );
}

export function useCreateAgentWebhook(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const key = agentKeys.webhooks(wsId, agentId);

  return useMutation({
    mutationKey: ["createAgentWebhook", agentId] as const,
    mutationFn: (body: { name: string; prompt: string }) =>
      api.createAgentWebhook(agentId, body),
    onSuccess: (webhook) => {
      qc.setQueryData<AgentWebhook[]>(key, (old) =>
        old ? [...old, webhook] : [webhook],
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: key });
    },
  });
}

export function useUpdateAgentWebhook(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const key = agentKeys.webhooks(wsId, agentId);

  return useMutation({
    mutationKey: ["updateAgentWebhook", agentId] as const,
    mutationFn: ({
      webhookId,
      body,
    }: {
      webhookId: string;
      body: { name: string; prompt: string };
    }) => api.updateAgentWebhook(agentId, webhookId, body),
    onSuccess: (webhook) => patchWebhookList(qc, key, webhook),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: key });
    },
  });
}

export function useToggleAgentWebhook(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const key = agentKeys.webhooks(wsId, agentId);

  return useMutation({
    mutationKey: ["toggleAgentWebhook", agentId] as const,
    mutationFn: ({ webhookId, enabled }: { webhookId: string; enabled: boolean }) =>
      api.setAgentWebhookEnabled(agentId, webhookId, enabled),
    onSuccess: (webhook) => patchWebhookList(qc, key, webhook),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: key });
    },
  });
}

export function useRotateAgentWebhook(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const key = agentKeys.webhooks(wsId, agentId);

  return useMutation({
    mutationKey: ["rotateAgentWebhook", agentId] as const,
    mutationFn: (webhookId: string) => api.rotateAgentWebhook(agentId, webhookId),
    onSuccess: (webhook) => patchWebhookList(qc, key, webhook),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: key });
    },
  });
}

export function useDeleteAgentWebhook(agentId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const key = agentKeys.webhooks(wsId, agentId);

  return useMutation({
    mutationKey: ["deleteAgentWebhook", agentId] as const,
    mutationFn: (webhookId: string) => api.deleteAgentWebhook(agentId, webhookId),
    onMutate: async (webhookId) => {
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<AgentWebhook[]>(key);
      qc.setQueryData<AgentWebhook[]>(key, (old) =>
        old ? old.filter((w) => w.id !== webhookId) : old,
      );
      return { prev, key };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev !== undefined) qc.setQueryData(ctx.key, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: key });
    },
  });
}
