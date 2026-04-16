import { describe, expect, it, vi } from "vitest";

// createTabRouter transitively pulls in route modules that expect a browser
// router context. For pure-function tests we stub it out.
vi.mock("../routes", () => ({
  createTabRouter: vi.fn(() => ({ dispose: vi.fn() })),
}));

import { sanitizeTabPath } from "./tab-store";

describe("sanitizeTabPath", () => {
  it("passes through root sentinel", () => {
    expect(sanitizeTabPath("/")).toBe("/");
  });

  it("passes through global paths", () => {
    expect(sanitizeTabPath("/login")).toBe("/login");
    expect(sanitizeTabPath("/workspaces/new")).toBe("/workspaces/new");
    expect(sanitizeTabPath("/invite/abc")).toBe("/invite/abc");
    expect(sanitizeTabPath("/auth/callback")).toBe("/auth/callback");
  });

  it("passes through valid workspace-scoped paths", () => {
    expect(sanitizeTabPath("/acme/issues")).toBe("/acme/issues");
    expect(sanitizeTabPath("/my-team/projects/abc")).toBe("/my-team/projects/abc");
  });

  it("rejects paths whose first segment is a reserved slug", () => {
    // A stray "/issues" (pre-refactor leftover, missing workspace prefix)
    // would be interpreted as workspaceSlug="issues" → NoAccessPage.
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    expect(sanitizeTabPath("/issues")).toBe("/");
    expect(sanitizeTabPath("/issues/abc-123")).toBe("/");
    expect(sanitizeTabPath("/settings")).toBe("/");
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });

  it("passes through user slugs that happen to look path-like but aren't reserved", () => {
    // A workspace owner could legitimately pick "acme-issues" or
    // "project-x" as their slug — sanitize must not touch these.
    expect(sanitizeTabPath("/acme-issues/issues")).toBe("/acme-issues/issues");
    expect(sanitizeTabPath("/project-x/inbox")).toBe("/project-x/inbox");
  });
});
