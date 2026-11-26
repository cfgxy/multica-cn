import { z } from "zod";

export const RuntimeCacheSchema = z.object({
  bytes: z.number().nonnegative().catch(0),
  limit_bytes: z.number().nonnegative().catch(0),
  downloads: z.number().nonnegative().catch(0),
  entries: z
    .array(
      z.object({
        id: z.string(),
        url: z.string(),
        size: z.number().nonnegative(),
        sha256: z.string(),
        cached_at: z.string(),
        last_used_at: z.string(),
        metadata: z.boolean().catch(false),
      }),
    )
    .catch([]),
  catalog: z
    .array(
      z.object({
        id: z.string(),
        display_name: z.string(),
        latest_version: z.string().catch(""),
        latest_version_state: z
          .enum(["checking", "ready", "unavailable", "unsupported"])
          .catch("unavailable"),
      }),
    )
    .catch([]),
});
export type RuntimeCache = z.infer<typeof RuntimeCacheSchema>;
