// @vitest-environment node
import { describe, expect, it } from "vitest";
import { repositoryIdentity } from "./repository-identity";

describe("repositoryIdentity", () => {
  it("matches allowed SCP, SSH and HTTPS clone URLs by host and path", () => {
    const identity = "git.test/group/subgroup/app";
    for (const url of [
      "deploy@Git.Test:group/subgroup/app.git",
      "git@git.test:group/subgroup/app.git",
      "ssh://git@git.test/group/subgroup/app.git",
      "https://git.test/group/subgroup/app.git",
    ]) {
      expect(repositoryIdentity(url)).toBe(identity);
    }
    expect(repositoryIdentity("https://other.test/group/subgroup/app.git")).not.toBe(identity);
    expect(repositoryIdentity("https://git.test/group/subgroup/other.git")).not.toBe(identity);
    expect(repositoryIdentity("https://GitHub.com/Acme/Repo.git")).toBe("github.com/Acme/Repo");
    expect(repositoryIdentity("git@github.com:acme/repo.git")).toBe("github.com/acme/repo");
  });

  it.each([
    "deploy:secret@git.test:group/app.git",
    "https://user:secret@git.test/group/app.git",
    "ssh://deploy:secret@git.test/group/app.git",
    "deploy@git.test:group/app.git?token=secret",
    "https://git.test/group/app.git#fragment",
    "https://git.test/group/app%0a.git",
    "\nhttps://git.test/group/app.git",
    "deploy@git.test:group/app%0d.git",
    "https://git.test/group/app%2fother.git",
    "deploy@git.test:group/app\\other.git",
    "deploy@git.test:../app.git",
    "deploy@git.test:group//app.git",
    "deploy@git.test:.git",
    "deploy@git.test:group/.git",
    "https://git.test/group//app.git",
    "deploy@git.test:/",
  ])("rejects unsafe or malformed repository location", (url) => {
    expect(repositoryIdentity(url)).toBeNull();
  });
});
