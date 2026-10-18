import type { GitHubPullRequest } from "../types";

/** Open and draft PRs still need attention before they can be considered merged. */
export function pendingPullRequests(
  pullRequests: readonly GitHubPullRequest[],
): GitHubPullRequest[] {
  const seen = new Set<string>();
  const pending: GitHubPullRequest[] = [];

  for (const pr of pullRequests) {
    if (pr.state !== "open" && pr.state !== "draft") continue;

    const identity = `${pr.provider ?? "github"}:${pr.repo_owner.toLowerCase()}/${pr.repo_name.toLowerCase()}#${pr.number}`;
    if (seen.has(identity)) continue;

    seen.add(identity);
    pending.push(pr);
  }

  return pending;
}
