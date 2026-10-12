import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const githubKeys = {
  all: (wsId: string) => ["github", wsId] as const,
  installations: (wsId: string) => [...githubKeys.all(wsId), "installations"] as const,
  repositories: (wsId: string, installationId: string) =>
    [...githubKeys.all(wsId), "installations", installationId, "repositories"] as const,
  pullRequests: (issueId: string) => ["github", "pull-requests", issueId] as const,
  pullRequestLookup: (wsId: string, owner: string, repo: string, number: number) =>
    [...githubKeys.all(wsId), "pull-requests", "lookup", owner, repo, number] as const,
};

export const githubInstallationsOptions = (wsId: string) =>
  queryOptions({
    queryKey: githubKeys.installations(wsId),
    queryFn: () => api.listGitHubInstallations(wsId),
    enabled: !!wsId,
  });

export const githubInstallationRepositoriesOptions = (
  wsId: string,
  installationId: string,
) =>
  infiniteQueryOptions({
    queryKey: githubKeys.repositories(wsId, installationId),
    queryFn: ({ pageParam }) =>
      api.listGitHubInstallationRepositories(wsId, installationId, {
        page: pageParam,
        per_page: 100,
      }),
    initialPageParam: 1,
    getNextPageParam: (lastPage) => lastPage.next_page ?? undefined,
    enabled: !!wsId && !!installationId,
  });

export const issuePullRequestsOptions = (issueId: string) =>
  queryOptions({
    queryKey: githubKeys.pullRequests(issueId),
    queryFn: () => api.listIssuePullRequests(issueId),
    enabled: !!issueId,
  });

/**
 * One `owner/repo#N` text-reference lookup for the reference tail (RUYI-635
 * 增量). `null` data = the platform has never seen this PR — the tail
 * degrades the token to plain text. Disabled (never fetched) when the
 * workspace or the parsed number is missing.
 */
export const pullRequestLookupOptions = (
  wsId: string,
  owner: string,
  repo: string,
  number: number,
) =>
  queryOptions({
    queryKey: githubKeys.pullRequestLookup(wsId, owner, repo, number),
    queryFn: () => api.lookupPullRequest(wsId, owner, repo, number),
    enabled: !!wsId && Number.isInteger(number) && number > 0,
  });
