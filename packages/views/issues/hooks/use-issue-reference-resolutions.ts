"use client";

import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import {
  issueDetailOptions,
  issueIdentifierOptions,
} from "@multica/core/issues/queries";
import {
  pullRequestLookupByNumberOptions,
  pullRequestLookupOptions,
} from "@multica/core/github";
import {
  identifierMatchesPrefix,
  parseMergeRequestReference,
  parsePullRequestReference,
} from "@multica/core/markdown";
import { useCurrentWorkspace } from "@multica/core/paths";
import { isIssueIdentifier } from "@multica/ui/markdown";
import type { IssueReference } from "@multica/core/markdown";
import type { GitHubPullRequest, Issue } from "@multica/core/types";

/** What a tail row can render: a resolved issue, a resolved PR, or `null`
 *  (loading / 404 / wrong prefix — every miss degrades to plain text). */
export type IssueReferenceResolution = Issue | GitHubPullRequest | null;

export function isPullRequestResolution(
  value: IssueReferenceResolution,
): value is GitHubPullRequest {
  return value !== null && "html_url" in value;
}

/**
 * Resolve every collected reference (RUYI-635) to its issue / PR, or `null`.
 *
 * Backs the tail aggregation list: each form uses the lookup the rest of the
 * app already uses for it:
 *   - identifier → `issueIdentifierOptions`, the exact point read behind
 *     `useResolveIssueIdentifier` (including its workspace-prefix gate);
 *   - UUID → `issueDetailOptions`, the same query an issue detail page or
 *     `IssueChip`'s fallback fetch runs, so cache entries are shared;
 *   - `owner/repo#N` (RUYI-635 增量) → `pullRequestLookupOptions`, the
 *     workspace's PR snapshot; a PR the platform has never seen returns
 *     `null` and the row degrades to plain text, same as issues;
 *   - `MR !N` (RUYI-728) → `pullRequestLookupByNumberOptions`, the same
 *     snapshot by number alone — unique hit or nothing, same degradation.
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
): Map<string, IssueReferenceResolution> {
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id ?? "";
  const prefix = workspace?.issue_prefix;

  const queries = useMemo(
    () =>
      references.map((ref) => {
        if (ref.form === "identifier") {
          // Same gate as useResolveIssueIdentifier: skip the network when the
          // token is not identifier-shaped or the prefix cannot match.
          const prefixMatches = identifierMatchesPrefix(ref.ref, prefix);
          return {
            ...issueIdentifierOptions(wsId, ref.ref),
            enabled:
              Boolean(wsId) && isIssueIdentifier(ref.ref) && prefixMatches,
          };
        }
        if (ref.form === "pull_request") {
          // The extractor only emits parseable tokens; the re-parse here is a
          // cheap guard so a malformed ref (or an empty workspace) never
          // spends a query.
          const parts = parsePullRequestReference(ref.ref);
          return {
            ...pullRequestLookupOptions(
              wsId,
              parts?.owner ?? "",
              parts?.repo ?? "",
              parts?.number ?? 0,
            ),
            staleTime: 60_000,
          };
        }
        if (ref.form === "merge_request") {
          const parts = parseMergeRequestReference(ref.ref);
          return {
            ...pullRequestLookupByNumberOptions(wsId, parts?.number ?? 0),
            staleTime: 60_000,
          };
        }
        return {
          ...issueDetailOptions(wsId, ref.ref),
          enabled: Boolean(wsId),
          staleTime: 60_000,
        };
      }),
    [references, wsId, prefix],
  );

  return useQueries({
    queries,
    combine: (results) => {
      const map = new Map<string, IssueReferenceResolution>();
      references.forEach((ref, i) => {
        map.set(
          `${ref.form}:${ref.ref}`,
          (results[i]?.data as IssueReferenceResolution | undefined) ?? null,
        );
      });
      return map;
    },
  });
}
