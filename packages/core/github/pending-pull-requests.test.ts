// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { GitHubPullRequest } from "../types";
import { pendingPullRequests } from "./pending-pull-requests";

function pr(
  number: number,
  state: GitHubPullRequest["state"],
  provider: GitHubPullRequest["provider"] = "github",
): GitHubPullRequest {
  return {
    id: `${provider}-${number}`,
    provider,
    workspace_id: "workspace-1",
    repo_owner: "Acme",
    repo_name: "Project",
    number,
    title: `PR ${number}`,
    state,
    html_url: `https://example.test/${number}`,
    branch: "feature",
    author_login: null,
    author_avatar_url: null,
    merged_at: null,
    closed_at: null,
    pr_created_at: "2026-01-01T00:00:00Z",
    pr_updated_at: "2026-01-01T00:00:00Z",
  };
}

describe("pendingPullRequests", () => {
  it("keeps open and draft PRs from every provider and removes terminal or duplicate rows", () => {
    const input = [
      pr(1, "open"),
      pr(2, "draft", "gitlab"),
      pr(1, "open"),
      pr(3, "merged"),
      pr(4, "closed"),
    ];

    expect(pendingPullRequests(input).map(({ number, state }) => [number, state])).toEqual([
      [1, "open"],
      [2, "draft"],
    ]);
  });
});
