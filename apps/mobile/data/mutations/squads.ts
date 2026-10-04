/**
 * Squad management mutations (RUYI-346). Same policy as agents mutations:
 * await-server + settle-invalidate for lifecycle writes, optimistic patch for
 * the roster edits a user is staring at (role change, member removal).
 *
 * Squad DELETE is a one-way archive (no restore endpoint) — removal from the
 * caches is final on success.
 *
 * Cache shapes touched (see data/queries/squads.ts for the factory):
 *   - squadKeys.list(wsId)              → `Squad[]` (member_count/preview)
 *   - squadKeys.detail(wsId, id)        → `Squad`
 *   - squadKeys.members(wsId, id)       → `SquadMember[]`
 *   - squadKeys.memberStatus(wsId, id)  → `SquadMemberStatusListResponse`
 */
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type {
  AddSquadMemberRequest,
  CreateSquadRequest,
  RemoveSquadMemberRequest,
  Squad,
  SquadMember,
  UpdateSquadMemberRoleRequest,
  UpdateSquadRequest,
} from "@multica/core/types";
import { api } from "@/data/api";
import { squadKeys } from "@/data/queries/squads";
import { useWorkspaceStore } from "@/data/workspace-store";

export function useCreateSquad() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationFn: (body: CreateSquadRequest) => api.createSquad(body),
    onSuccess: (squad) => {
      qc.setQueryData(squadKeys.detail(wsId, squad.id), squad);
      qc.setQueryData<Squad[]>(squadKeys.list(wsId), (old) =>
        old ? [squad, ...old.filter((s) => s.id !== squad.id)] : [squad],
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: squadKeys.all(wsId) });
    },
  });
}

export function useUpdateSquad(squadId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["updateSquad", squadId] as const,
    mutationFn: (patch: UpdateSquadRequest) => api.updateSquad(squadId, patch),
    onMutate: async (patch) => {
      const detailKey = squadKeys.detail(wsId, squadId);
      const listKey = squadKeys.list(wsId);
      await Promise.all([
        qc.cancelQueries({ queryKey: detailKey }),
        qc.cancelQueries({ queryKey: listKey }),
      ]);
      const prevDetail = qc.getQueryData<Squad>(detailKey);
      const prevList = qc.getQueryData<Squad[]>(listKey);
      if (prevDetail) qc.setQueryData<Squad>(detailKey, { ...prevDetail, ...patch });
      qc.setQueryData<Squad[]>(listKey, (old) =>
        old ? old.map((s) => (s.id === squadId ? { ...s, ...patch } : s)) : old,
      );
      return { prevDetail, prevList, detailKey, listKey };
    },
    onError: (_err, _vars, ctx) => {
      if (!ctx) return;
      if (ctx.prevDetail !== undefined) qc.setQueryData(ctx.detailKey, ctx.prevDetail);
      if (ctx.prevList !== undefined) qc.setQueryData(ctx.listKey, ctx.prevList);
    },
    onSuccess: (server) => {
      qc.setQueryData(squadKeys.detail(wsId, squadId), server);
      qc.setQueryData<Squad[]>(squadKeys.list(wsId), (old) =>
        old ? old.map((s) => (s.id === squadId ? server : s)) : old,
      );
    },
    onSettled: () => {
      // A leader_id change also rewrites member roles server-side; the
      // settle invalidate refreshes both derived caches.
      qc.invalidateQueries({ queryKey: squadKeys.all(wsId) });
    },
  });
}

export function useDeleteSquad(squadId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["deleteSquad", squadId] as const,
    mutationFn: () => api.deleteSquad(squadId),
    onMutate: async () => {
      const listKey = squadKeys.list(wsId);
      await qc.cancelQueries({ queryKey: listKey });
      const prevList = qc.getQueryData<Squad[]>(listKey);
      qc.setQueryData<Squad[]>(listKey, (old) =>
        old ? old.filter((s) => s.id !== squadId) : old,
      );
      return { prevList, listKey };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prevList !== undefined) {
        qc.setQueryData(ctx.listKey, ctx.prevList);
      }
    },
    onSettled: () => {
      // One-way archive: drop the per-squad caches instead of invalidating.
      qc.removeQueries({ queryKey: squadKeys.detail(wsId, squadId) });
      qc.removeQueries({ queryKey: squadKeys.members(wsId, squadId) });
      qc.removeQueries({ queryKey: squadKeys.memberStatus(wsId, squadId) });
      qc.invalidateQueries({ queryKey: squadKeys.list(wsId) });
    },
  });
}

// Roster edits move four caches: the membership rows, the derived status
// snapshot, and the squad's own member_count/preview on both detail and
// list. None are optimistic except member removal (row visibly disappears);
// server responses decide, settle invalidate reconciles.
function useSquadRosterInvalidation(squadId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  return () => {
    qc.invalidateQueries({ queryKey: squadKeys.members(wsId, squadId) });
    qc.invalidateQueries({ queryKey: squadKeys.memberStatus(wsId, squadId) });
    qc.invalidateQueries({ queryKey: squadKeys.detail(wsId, squadId) });
    qc.invalidateQueries({ queryKey: squadKeys.list(wsId) });
  };
}

export function useAddSquadMember(squadId: string) {
  const settle = useSquadRosterInvalidation(squadId);

  return useMutation({
    mutationKey: ["addSquadMember", squadId] as const,
    mutationFn: (body: AddSquadMemberRequest) => api.addSquadMember(squadId, body),
    onSettled: settle,
  });
}

export function useRemoveSquadMember(squadId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const settle = useSquadRosterInvalidation(squadId);
  const membersKey = squadKeys.members(wsId, squadId);

  return useMutation({
    mutationKey: ["removeSquadMember", squadId] as const,
    // Members are addressed by the (member_type, member_id) pair — cache
    // patches key on the pair, never on the row id.
    mutationFn: (target: RemoveSquadMemberRequest) =>
      api.removeSquadMember(squadId, target),
    onMutate: async (target) => {
      await qc.cancelQueries({ queryKey: membersKey });
      const prev = qc.getQueryData<SquadMember[]>(membersKey);
      qc.setQueryData<SquadMember[]>(membersKey, (old) =>
        old
          ? old.filter(
              (m) =>
                !(
                  m.member_type === target.member_type &&
                  m.member_id === target.member_id
                ),
            )
          : old,
      );
      return { prev, membersKey };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev !== undefined) qc.setQueryData(ctx.membersKey, ctx.prev);
    },
    onSettled: settle,
  });
}

export function useUpdateSquadMemberRole(squadId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const settle = useSquadRosterInvalidation(squadId);
  const membersKey = squadKeys.members(wsId, squadId);

  return useMutation({
    mutationKey: ["updateSquadMemberRole", squadId] as const,
    mutationFn: (body: UpdateSquadMemberRoleRequest) =>
      api.updateSquadMemberRole(squadId, body),
    onMutate: async (body) => {
      await qc.cancelQueries({ queryKey: membersKey });
      const prev = qc.getQueryData<SquadMember[]>(membersKey);
      qc.setQueryData<SquadMember[]>(membersKey, (old) =>
        old
          ? old.map((m) =>
              m.member_type === body.member_type && m.member_id === body.member_id
                ? { ...m, role: body.role }
                : m,
            )
          : old,
      );
      return { prev, membersKey };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev !== undefined) qc.setQueryData(ctx.membersKey, ctx.prev);
    },
    onSettled: settle,
  });
}
