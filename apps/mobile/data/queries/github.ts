/**
 * GitHub pull-request cache keys (RUYI-43) — mirrors web
 * `packages/core/github/queries.ts` verbatim.
 *
 * Parity note: web's `pullRequests` key intentionally omits the wsId
 * segment (issue ids are globally unique, and the `pull_request` WS event
 * invalidates by this prefix). Mobile copies that shape so the key layout
 * stays comparable across clients. Mobile does NOT subscribe to the
 * `pull_request` WS event for this feature: the list lives in a transient
 * read-only modal and refetches whenever it is opened stale (query-client
 * default staleTime 60s) — a per-record WS subscription would run on every
 * PR event for a screen that is usually closed (mobile CLAUDE.md: "a new
 * event with no consumer on mobile → don't subscribe").
 */
import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const githubKeys = {
  all: (wsId: string | null) => ["github", wsId] as const,
  pullRequests: (issueId: string | null) =>
    ["github", "pull-requests", issueId] as const,
};

export const issuePullRequestsOptions = (issueId: string | null) =>
  queryOptions({
    queryKey: githubKeys.pullRequests(issueId),
    queryFn: ({ signal }) => api.listIssuePullRequests(issueId ?? "", { signal }),
    enabled: !!issueId,
  });
