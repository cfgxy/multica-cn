// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { PluginConfigField } from "@multica/core/types";

import { configPayload, missingRequiredFields } from "./plugin-config-field";

// The canonical matrix for "which required fields still need an answer". The
// component suite mounts the happy path and the wiring; the boundaries live
// here, where they cost no DOM.

function field(partial: Partial<PluginConfigField> & { key: string }): PluginConfigField {
  return {
    type: "string",
    label: partial.key,
    required: true,
    ...partial,
  };
}

describe("missingRequiredFields", () => {
  it("ignores fields the manifest does not require", () => {
    const schema = [field({ key: "note", required: false })];
    expect(missingRequiredFields(schema, {}, {})).toEqual([]);
  });

  it("counts false and zero as answers", () => {
    const schema = [field({ key: "verbose", type: "bool" }), field({ key: "count", type: "number" })];
    expect(missingRequiredFields(schema, { verbose: false, count: 0 }, {})).toEqual([]);
    expect(missingRequiredFields(schema, {}, {})).toEqual(["verbose", "count"]);
  });

  it("does not accept whitespace as a string answer", () => {
    const schema = [field({ key: "repo" })];
    expect(missingRequiredFields(schema, { repo: "   " }, {})).toEqual(["repo"]);
  });

  // A secret is write-only, so the form has no value to inspect: the only thing
  // that can say it is answered is the server naming it as configured.
  it("treats a secret as answered when it is typed or already stored", () => {
    const schema = [field({ key: "token", type: "secret" })];
    expect(missingRequiredFields(schema, {}, {})).toEqual(["token"]);
    expect(missingRequiredFields(schema, {}, { token: "sk-typed" })).toEqual([]);
    expect(missingRequiredFields(schema, {}, {}, new Set(["token"]))).toEqual([]);
  });

  // The upgrade case, and the reason `configuredKeys` covers plain fields too:
  // v1's value sits on the installation row, not in this form's state, while a
  // field v2 introduces is held by nothing at all.
  it("separates a value the upgrade already holds from one the new version adds", () => {
    const schema = [field({ key: "repo" }), field({ key: "channel" })];
    const configured = new Set(["repo"]);
    expect(missingRequiredFields(schema, {}, {}, configured)).toEqual(["channel"]);
  });

  // Editing a stored field down to empty must go back to blocking, otherwise
  // "it used to be set" would let an upgrade clear it.
  it("still blocks when a configured field is edited to empty", () => {
    const schema = [field({ key: "repo" })];
    expect(missingRequiredFields(schema, { repo: "" }, {}, new Set(["repo"]))).toEqual(["repo"]);
  });
});

describe("configPayload", () => {
  it("omits a secret the administrator did not type", () => {
    expect(configPayload({ repo: "r" }, { token: "" })).toEqual({ repo: "r" });
    expect(configPayload({ repo: "r" }, { token: "sk-typed" })).toEqual({ repo: "r", token: "sk-typed" });
  });
});
