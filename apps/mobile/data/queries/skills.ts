import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const skillListOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: ["skills", wsId] as const,
    queryFn: ({ signal }) => api.listSkills({ signal }),
    enabled: !!wsId,
  });
