import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const skillKeys = {
  all: (wsId: string | null) => ["skills", wsId] as const,
  list: (wsId: string | null) => [...skillKeys.all(wsId), "list"] as const,
};

export const skillListOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: skillKeys.list(wsId),
    queryFn: ({ signal }) => api.listSkills({ signal }),
    enabled: !!wsId,
  });

// Union catalog: authored skills plus metadata-only runtime discovery
// sightings (RUYI-288). The agent skills add-picker offers the discovery
// half as import rows (RUYI-418 A13), mirroring web's skill-add-dialog.
export const skillCatalogKeys = {
  all: (wsId: string | null) => ["skill-catalog", wsId] as const,
};

export const skillCatalogOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: skillCatalogKeys.all(wsId),
    queryFn: ({ signal }) => api.listSkillCatalog({ signal }),
    enabled: !!wsId,
  });
