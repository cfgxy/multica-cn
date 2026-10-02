/**
 * Squad queries + cache-key factory (RUYI-346). Same three-segment shape as
 * `agents.ts`. `members` / `memberStatus` are per-squad sub-resources keyed
 * under the same workspace prefix so `squadKeys.all(wsId)` (the only surface
 * the WS layer uses) invalidates everything squad-shaped in one call.
 */
import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const squadKeys = {
  all: (wsId: string | null) => ["squads", wsId] as const,
  list: (wsId: string | null) => [...squadKeys.all(wsId), "list"] as const,
  detail: (wsId: string | null, id: string) =>
    [...squadKeys.all(wsId), "detail", id] as const,
  // GET /api/squads/:id/members — membership rows addressed by the
  // (member_type, member_id) pair.
  members: (wsId: string | null, id: string) =>
    [...squadKeys.all(wsId), "members", id] as const,
  // GET /api/squads/:id/members/status — derived working/idle/offline/
  // unstable/archived snapshot. Kept separate from `members` because the two
  // mutate on different clocks (roster edits vs task lifecycle).
  memberStatus: (wsId: string | null, id: string) =>
    [...squadKeys.all(wsId), "member-status", id] as const,
};

export const squadListOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: squadKeys.list(wsId),
    queryFn: ({ signal }) => api.listSquads({ signal }),
    enabled: !!wsId,
  });

export const squadDetailOptions = (wsId: string | null, squadId: string) =>
  queryOptions({
    queryKey: squadKeys.detail(wsId, squadId),
    queryFn: ({ signal }) => api.getSquad(squadId, { signal }),
    enabled: !!wsId && !!squadId,
  });

export const squadMembersOptions = (wsId: string | null, squadId: string) =>
  queryOptions({
    queryKey: squadKeys.members(wsId, squadId),
    queryFn: ({ signal }) => api.listSquadMembers(squadId, { signal }),
    enabled: !!wsId && !!squadId,
  });

export const squadMemberStatusOptions = (
  wsId: string | null,
  squadId: string,
) =>
  queryOptions({
    queryKey: squadKeys.memberStatus(wsId, squadId),
    queryFn: ({ signal }) => api.getSquadMemberStatus(squadId, { signal }),
    enabled: !!wsId && !!squadId,
  });
