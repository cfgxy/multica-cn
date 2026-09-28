import type { GitHubPullRequest } from "@multica/core/types";

export function isVisiblePullRequest(pr: GitHubPullRequest, showGitHub: boolean): boolean {
  return (!("reference_only" in pr) || pr.reference_only !== true)
    && (showGitHub || (pr.provider != null && pr.provider !== "github"));
}

export function shouldShowPullRequestSection(
  showGitHub: boolean,
  vcsAvailable: boolean,
  prs: readonly GitHubPullRequest[],
): boolean {
  return showGitHub || (vcsAvailable && prs.some((pr) => isVisiblePullRequest(pr, false)));
}
