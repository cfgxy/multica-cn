"use client";

import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import {
  issueDetailOptions,
  issueIdentifierOptions,
} from "@multica/core/issues/queries";
import { useCurrentWorkspace } from "@multica/core/paths";
import { isIssueIdentifier } from "@multica/ui/markdown";
import type { IssueReference } from "@multica/core/markdown";
import type { Issue } from "@multica/core/types";

/**
 * Resolve every collected issue reference (RUYI-635) to its issue, or `null`.
 *
 * Backs the tail aggregation list: mention-form references carry a UUID,
 * autolinked bare identifiers carry the identifier — each form uses the
 * lookup the rest of the app already uses for it:
 *   - identifier → `issueIdentifierOptions`, the exact point read behind
 *     `useResolveIssueIdentifier` (including its workspace-prefix gate);
 *   - UUID → `issueDetailOptions`, the same query an issue detail page or
 *     `IssueChip`'s fallback fetch runs, so cache entries are shared.
 *
 * The reference count varies per content string, so per-reference `useQuery`
 * calls would violate the rules of hooks on content change; `useQueries`
 * exists for exactly this dynamic-list shape. Resolution misses (404 /
 * wrong-prefix / still loading) all surface as `null` — the tail row degrades
 * to plain text either way.
 *
 * `references` should already be raw-deduped (`dedupeIssueReferences`); the
 * returned map is keyed by `"<form>:<ref>"` in the same order.
 */
export function useIssueReferenceResolutions(
  references: IssueReference[],
): Map<string, Issue | null> {
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id ?? "";
  const prefix = workspace?.issue_prefix;

  const queries = useMemo(
    () =>
      references.map((ref) => {
        if (ref.form === "identifier") {
          // Same gate as useResolveIssueIdentifier: skip the network when the
          // token is not identifier-shaped or the prefix cannot match.
          const prefixMatches =
            !prefix ||
            ref.ref.toUpperCase().startsWith(`${prefix.toUpperCase()}-`);
          return {
            ...issueIdentifierOptions(wsId, ref.ref),
            enabled:
              Boolean(wsId) && isIssueIdentifier(ref.ref) && prefixMatches,
          };
        }
        return { ...issueDetailOptions(wsId, ref.ref), enabled: Boolean(wsId) };
      }),
    [references, wsId, prefix],
  );

  return useQueries({
    queries,
    combine: (results) => {
      const map = new Map<string, Issue | null>();
      references.forEach((ref, i) => {
        map.set(
          `${ref.form}:${ref.ref}`,
          (results[i]?.data as Issue | undefined) ?? null,
        );
      });
      return map;
    },
  });
}
