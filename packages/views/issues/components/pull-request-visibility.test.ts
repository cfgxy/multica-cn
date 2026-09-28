// @vitest-environment node

import { describe, expect, it } from "vitest";
import type { GitHubPullRequest } from "@multica/core/types";
import { shouldShowPullRequestSection } from "./pull-request-visibility";

const github = { provider: "github" } as GitHubPullRequest;
const legacy = { provider: undefined } as GitHubPullRequest;
const gitlab = { provider: "gitlab" } as GitHubPullRequest;
const referenceOnly = { ...gitlab, reference_only: true };

describe("pull request sidebar visibility", () => {
  it.each([
    [false, false, [gitlab], false],
    [false, true, [], false],
    [false, true, [github], false],
    [false, true, [legacy], false],
    [false, true, [referenceOnly], false],
    [false, true, [gitlab], true],
    [false, true, [referenceOnly, gitlab], true],
    [true, false, [], true],
    [true, true, [github], true],
  ] as const)(
    "GitHub sidebar=%s, VCS available=%s, rows=%j => visible=%s",
    (showGitHub, vcsAvailable, prs, expected) => {
      expect(shouldShowPullRequestSection(showGitHub, vcsAvailable, prs)).toBe(expected);
    },
  );
});
