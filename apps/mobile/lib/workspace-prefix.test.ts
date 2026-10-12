import { describe, expect, it } from "vitest";

import type { Workspace } from "@multica/core/types";

import { resolveWorkspacePrefix } from "./workspace-prefix";

const ws = (over: Partial<Workspace>): Workspace => ({
  id: "ws-1",
  name: "E2ER",
  slug: "e2er",
  description: null,
  context: null,
  settings: {},
  repos: [],
  issue_prefix: "E2ER",
  avatar_url: null,
  created_at: "",
  updated_at: "",
  ...over,
});

const LIST = [
  ws({ id: "ws-1", slug: "e2er", issue_prefix: "E2ER" }),
  ws({ id: "ws-2", slug: "other", issue_prefix: "OTH" }),
];

describe("resolveWorkspacePrefix", () => {
  it("resolves by slug when the id mirror lags (QA failure mode)", () => {
    expect(resolveWorkspacePrefix(LIST, "e2er", null)).toBe("E2ER");
  });

  it("falls back to the id mirror when the slug is absent", () => {
    expect(resolveWorkspacePrefix(LIST, null, "ws-2")).toBe("OTH");
  });

  it("prefers the slug match when both mirrors are populated", () => {
    expect(resolveWorkspacePrefix(LIST, "other", "ws-1")).toBe("OTH");
  });

  it("returns null for an unknown list, unknown slug+id, or empty prefix", () => {
    expect(resolveWorkspacePrefix(undefined, "e2er", "ws-1")).toBeNull();
    expect(resolveWorkspacePrefix(LIST, "nope", "nope")).toBeNull();
    expect(
      resolveWorkspacePrefix([ws({ issue_prefix: "" })], "e2er", null),
    ).toBeNull();
  });
});
