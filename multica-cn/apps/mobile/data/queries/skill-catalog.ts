import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

// Workspace skill catalog (RUYI-288): authored skills unioned with runtime
// discovery sightings. The server resolves the workspace from the slug
// header, mirroring the mobile skills list.
export const skillCatalogKeys = {
  all: (wsId: string | null) => ["skill-catalog", wsId] as const,
  list: (wsId: string | null) => [...skillCatalogKeys.all(wsId), "list"] as const,
};

export const skillCatalogOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: skillCatalogKeys.list(wsId),
    queryFn: ({ signal }) => api.listSkillCatalog({ signal }),
    enabled: !!wsId,
  });
