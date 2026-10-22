import { describe, expect, it } from "vitest";
import { resolveProjectContextNotice } from "./chat-project-context";

describe("resolveProjectContextNotice", () => {
  it("distinguishes an empty successful project list from a pending query", () => {
    expect(resolveProjectContextNotice({
      projectCount: 0,
      hasSelectedProject: false,
      isPending: true,
      isError: false,
    })).toBe("loading");

    expect(resolveProjectContextNotice({
      projectCount: 0,
      hasSelectedProject: false,
      isPending: false,
      isError: false,
    })).toBe("empty");
  });

  it("reports query failure instead of presenting it as an empty workspace", () => {
    expect(resolveProjectContextNotice({
      projectCount: 0,
      hasSelectedProject: false,
      isPending: false,
      isError: true,
    })).toBe("error");
  });

  it("keeps the picker available when projects or a removable context exist", () => {
    expect(resolveProjectContextNotice({
      projectCount: 2,
      hasSelectedProject: false,
      isPending: false,
      isError: false,
    })).toBeNull();

    expect(resolveProjectContextNotice({
      projectCount: 0,
      hasSelectedProject: true,
      isPending: false,
      isError: false,
    })).toBeNull();
  });
});
