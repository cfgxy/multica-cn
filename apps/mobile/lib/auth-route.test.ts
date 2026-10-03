// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  resolveAppGate,
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

describe("resolveAppGate", () => {
  it("holds the current route while startup resolves, preserving a cold-start deep link", () => {
    // RUYI-346: a cold-start deep link's target route is already in the
    // stack when the (app) gate mounts — the gate must hold, not redirect.
    expect(resolveAppGate("checking", false, true, false, false)).toEqual({
      kind: "startup-hold",
    });
    expect(resolveAppGate("checking", true, true, false, false)).toEqual({
      kind: "startup-hold",
    });
  });

  it("routes a converged explicit-selection state to the picker", () => {
    // RUYI-346 round 3: "select" is not an in-flight startup phase — the
    // store parks there only when startup converged on "the user must pick
    // a server". This is the one navigation the startup window may emit:
    // no deep-link content can render without a connected server.
    expect(resolveAppGate("select", false, true, false, false)).toEqual({
      kind: "server-select",
    });
  });

  it("holds while session restoration settles after the startup store converges", () => {
    // RUYI-346: after the phase flips to ready, auth initialize() may still
    // be restoring the session; a login redirect here discards a cold-start
    // deep link exactly like a startup-window navigation would.
    expect(resolveAppGate("ready", false, true, false, false)).toEqual({
      kind: "startup-hold",
    });
  });

  it("renders the stack once startup is ready and the session is authenticated", () => {
    expect(resolveAppGate("ready", true, false, false, true)).toEqual({
      kind: "stack",
    });
  });

  it("keeps an authenticated route through session restoration and rollback", () => {
    expect(resolveAppGate("ready", false, true, false, true)).toEqual({
      kind: "stack",
    });
    expect(resolveAppGate("ready", false, false, true, true)).toEqual({
      kind: "stack",
    });
  });

  it("redirects a settled unauthenticated session to login", () => {
    expect(resolveAppGate("ready", false, false, false, false)).toEqual({
      kind: "login",
    });
    expect(resolveAppGate("ready", false, false, false, true)).toEqual({
      kind: "login",
    });
  });
});

describe("shouldHandleUnauthorized", () => {
  it("defers global sign-out while a server switch owns session restoration", () => {
    expect(shouldHandleUnauthorized(true)).toBe(false);
    expect(shouldHandleUnauthorized(false)).toBe(true);
  });
});
