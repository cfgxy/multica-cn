import { describe, expect, it } from "vitest";
import { isMikaAgent, workspaceNeedsMika } from "./mika";

describe("workspaceNeedsMika", () => {
  it("is true for an empty workspace", () => {
    expect(workspaceNeedsMika([])).toBe(true);
  });

  // The regression this guards: the Runtimes recovery card gated on
  // `agents.length === 0`, so creating any ordinary agent first hid the only
  // surface that can mint a Mika. The generic agent endpoint accepts neither
  // `kind` nor `system_key`, so there was no other way back.
  it("stays true when the workspace has ordinary agents but no Mika", () => {
    expect(
      workspaceNeedsMika([{ system_key: undefined }, { system_key: "" }]),
    ).toBe(true);
  });

  it("is false once a Mika exists", () => {
    expect(workspaceNeedsMika([{ system_key: "mika" }])).toBe(false);
    expect(
      workspaceNeedsMika([{ system_key: undefined }, { system_key: "mika" }]),
    ).toBe(false);
  });

  // Identity is the system key, never the display name — Mika is renameable.
  it("does not treat a renamed-to-Mika ordinary agent as Mika", () => {
    expect(isMikaAgent({ system_key: undefined })).toBe(false);
    expect(isMikaAgent({ system_key: "agent_builder" })).toBe(false);
  });
});
