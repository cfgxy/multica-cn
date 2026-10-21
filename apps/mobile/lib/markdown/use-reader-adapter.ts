import { useQuery } from "@tanstack/react-query";
import { selectMarkdownRenderer } from "@multica/core/markdown";
import { appConfigOptions } from "@/data/queries/billing";

export function useMarkdownRenderer() {
  const config = useQuery(appConfigOptions());
  return selectMarkdownRenderer(config.data?.feature_flags);
}
