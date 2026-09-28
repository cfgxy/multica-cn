// @vitest-environment node
import { describe, it, expect } from "vitest";
import {
  gitRepoShortLabel,
  githubShortLabel,
  isWebLinkableRepoUrl,
  midTruncate,
} from "./github-url";

describe("githubShortLabel", () => {
  it("extracts owner/repo from an https URL", () => {
    expect(githubShortLabel("https://github.com/octocat/hello-world")).toBe(
      "octocat/hello-world",
    );
  });

  it("ignores extra path segments after owner/repo", () => {
    expect(
      githubShortLabel("https://github.com/octocat/hello-world/tree/main"),
    ).toBe("octocat/hello-world");
  });

  it("strips a trailing .git suffix", () => {
    expect(githubShortLabel("https://github.com/octocat/hello-world.git")).toBe(
      "octocat/hello-world",
    );
  });

  it("handles the www.github.com host", () => {
    expect(githubShortLabel("https://www.github.com/octocat/hello-world")).toBe(
      "octocat/hello-world",
    );
  });

  it("handles ssh:// URLs and strips .git", () => {
    expect(
      githubShortLabel("ssh://git@github.com/octocat/hello-world.git"),
    ).toBe("octocat/hello-world");
  });

  it("handles scp-style shorthand (git@github.com:owner/repo.git)", () => {
    expect(githubShortLabel("git@github.com:octocat/hello-world.git")).toBe(
      "octocat/hello-world",
    );
  });

  it("handles scp-style shorthand without a .git suffix", () => {
    expect(githubShortLabel("git@github.com:octocat/hello-world")).toBe(
      "octocat/hello-world",
    );
  });

  it("middle-truncates so a shared long prefix stays distinguishable", () => {
    const a = githubShortLabel(
      "https://github.com/example-organization/very-long-repository-name-for-customer-alpha.git",
    );
    const b = githubShortLabel(
      "https://github.com/example-organization/very-long-repository-name-for-customer-beta.git",
    );
    expect(a).not.toBe(b);
    expect(a.length).toBeLessThanOrEqual(40);
    expect(a.endsWith("alpha")).toBe(true);
    expect(b.endsWith("beta")).toBe(true);
  });

  it("returns enterprise-host URLs unchanged", () => {
    const url = "https://github.enterprise.com/octocat/hello-world";
    expect(githubShortLabel(url)).toBe(url);
  });

  it("returns malformed input unchanged", () => {
    expect(githubShortLabel("not a url")).toBe("not a url");
  });

  it("returns a github URL without a repo segment unchanged", () => {
    const url = "https://github.com/octocat";
    expect(githubShortLabel(url)).toBe(url);
  });
});

describe("gitRepoShortLabel", () => {
  it("keeps GitHub labels byte-identical to githubShortLabel", () => {
    // Zero-regression guard for existing github_repo resources: every input
    // the GitHub label handles must come back unchanged through the generic
    // entry point.
    for (const url of [
      "https://github.com/octocat/hello-world",
      "https://github.com/octocat/hello-world/tree/main",
      "https://github.com/octocat/hello-world.git",
      "https://www.github.com/octocat/hello-world",
      "ssh://git@github.com/octocat/hello-world.git",
      "git@github.com:octocat/hello-world.git",
      "git@github.com:octocat/hello-world",
    ]) {
      expect(gitRepoShortLabel(url)).toBe(githubShortLabel(url));
      expect(gitRepoShortLabel(url)).toBe("octocat/hello-world");
    }
  });

  it("keeps every namespace level of a GitLab subgroup path", () => {
    // Two subgroups can hold a repo with the same final name, so collapsing
    // the path to the last two segments would render them identically.
    expect(gitRepoShortLabel("https://gitlab.test/group/sub/repo.git")).toBe(
      "gitlab.test/group/sub/repo",
    );
    expect(gitRepoShortLabel("git@gitlab.test:group/sub/deep/repo.git")).toBe(
      "gitlab.test/group/sub/deep/repo",
    );
    expect(gitRepoShortLabel("ssh://git@gitlab.test/group/sub/repo.git")).toBe(
      "gitlab.test/group/sub/repo",
    );
  });

  it("distinguishes same-named repos in sibling subgroups", () => {
    const a = gitRepoShortLabel("https://gitlab.test/group/alpha/api.git");
    const b = gitRepoShortLabel("https://gitlab.test/group/beta/api.git");
    expect(a).not.toBe(b);
  });

  it("retains distinct middle subgroups in long GitLab namespace paths", () => {
    const prefix = "group/long-shared-prefix/subgroup";
    const suffix = "/nested/repository-with-a-shared-long-tail";
    for (const makeUrl of [
      (path: string) => `https://gitlab.test/${path}.git`,
      (path: string) => `git@gitlab.test:${path}.git`,
      (path: string) => `ssh://git@gitlab.test/${path}.git`,
    ]) {
      const alpha = `gitlab.test/${prefix}/alpha${suffix}`;
      const beta = `gitlab.test/${prefix}/beta${suffix}`;
      expect(gitRepoShortLabel(makeUrl(`${prefix}/alpha${suffix}`))).toBe(alpha);
      expect(gitRepoShortLabel(makeUrl(`${prefix}/beta${suffix}`))).toBe(beta);
    }
  });

  it("keeps an explicit port so two instances on one host stay distinct", () => {
    expect(gitRepoShortLabel("ssh://git@gitlab.test:2222/group/repo.git")).toBe(
      "gitlab.test:2222/group/repo",
    );
  });

  it("never leaks credentials embedded in the URL", () => {
    const label = gitRepoShortLabel(
      "https://oauth2:s3cr3t-token@gitlab.test/group/sub/repo.git",
    );
    expect(label).toBe("gitlab.test/group/sub/repo");
    expect(label).not.toContain("s3cr3t-token");
    expect(label).not.toContain("oauth2");
  });

  it("returns unrecognized input unchanged", () => {
    expect(gitRepoShortLabel("not a url")).toBe("not a url");
    expect(gitRepoShortLabel("")).toBe("");
  });
});

describe("isWebLinkableRepoUrl", () => {
  it("accepts http(s) URLs", () => {
    expect(isWebLinkableRepoUrl("https://gitlab.test/group/sub/repo.git")).toBe(true);
    expect(isWebLinkableRepoUrl("http://gitlab.test/group/repo.git")).toBe(true);
    expect(isWebLinkableRepoUrl("https://github.com/octocat/hello-world")).toBe(true);
  });

  it("rejects ssh and other non-web git transports", () => {
    // These are clone URLs, not pages: rendering them as an anchor produces a
    // link the browser cannot follow.
    expect(isWebLinkableRepoUrl("git@gitlab.test:group/sub/repo.git")).toBe(false);
    expect(isWebLinkableRepoUrl("ssh://git@gitlab.test/group/sub/repo.git")).toBe(false);
    expect(isWebLinkableRepoUrl("git://gitlab.test/group/repo.git")).toBe(false);
  });

  it("rejects malformed input and non-network schemes", () => {
    expect(isWebLinkableRepoUrl("not a url")).toBe(false);
    expect(isWebLinkableRepoUrl("")).toBe(false);
    expect(isWebLinkableRepoUrl("javascript:alert(1)")).toBe(false);
    expect(isWebLinkableRepoUrl("file:///etc/passwd")).toBe(false);
  });
});

describe("midTruncate", () => {
  it("leaves short strings untouched", () => {
    expect(midTruncate("short")).toBe("short");
  });

  it("caps the result at maxLen and inserts an ellipsis", () => {
    const out = midTruncate("a".repeat(100), 21);
    expect(out.length).toBe(21);
    expect(out).toContain("…");
    expect(out.startsWith("aaaaaaaaaa")).toBe(true);
    expect(out.endsWith("aaaaaaaaaa")).toBe(true);
  });
});
