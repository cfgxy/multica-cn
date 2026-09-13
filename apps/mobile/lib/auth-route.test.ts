// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  shouldHandleUnauthorized,
  shouldRenderAuthenticatedStack,
} from "./auth-route";

describe("shouldRenderAuthenticatedStack", () => {
  it("renders for an authenticated session", () => {
    expect(shouldRenderAuthenticatedStack(true, false, false, true)).toBe(true);
  });

  it("keeps an existing authenticated route during server session restoration", () => {
    expect(shouldRenderAuthenticatedStack(false, true, false, true)).toBe(true);
  });

  it("keeps the route during an asynchronous rollback after loading has settled", () => {
    expect(shouldRenderAuthenticatedStack(false, false, true, true)).toBe(true);
  });

  it("redirects an initial unauthenticated launch and a settled signed-out session", () => {
    expect(shouldRenderAuthenticatedStack(false, true, false, false)).toBe(false);
    expect(shouldRenderAuthenticatedStack(false, false, false, true)).toBe(false);
  });
});

describe("shouldHandleUnauthorized", () => {
  it("defers global sign-out while a server switch owns session restoration", () => {
    expect(shouldHandleUnauthorized(true)).toBe(false);
    expect(shouldHandleUnauthorized(false)).toBe(true);
  });
});
