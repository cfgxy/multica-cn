// @vitest-environment node
import { describe, expect, it } from "vitest";
import { shouldResolveWorkspaceMembership } from "./workspace-route";

describe("shouldResolveWorkspaceMembership", () => {
  it("defers membership resolution while a server switch owns the auth boundary", () => {
    expect(shouldResolveWorkspaceMembership(true)).toBe(false);
  });

  it("resolves membership outside a server switch", () => {
    expect(shouldResolveWorkspaceMembership(false)).toBe(true);
  });
});
