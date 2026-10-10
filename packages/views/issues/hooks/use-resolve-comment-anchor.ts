"use client";

import { useQuery } from "@tanstack/react-query";
import { commentAnchorOptions } from "@multica/core/issues/queries";
import type { CommentAnchor } from "@multica/core/types";

/**
 * Resolve a `mention://comment/<id>` target the current render context could
 * NOT resolve locally (RUYI-643). Returns the owning issue's anchor info, or
 * null when the target is unreachable — the server folds unknown, deleted,
 * and foreign-workspace into one 404, so null is the single degraded reading.
 *
 * Pass null to disable: the caller only probes on a local miss, so a chip the
 * in-issue timeline already resolved never hits the network (same-issue jumps
 * stay fetch-free exactly as RUYI-108 designed).
 */
export function useResolveCommentAnchor(
  commentId: string | null,
): CommentAnchor | null {
  const { data } = useQuery({
    ...commentAnchorOptions(commentId ?? ""),
    enabled: Boolean(commentId),
  });

  return data ?? null;
}
