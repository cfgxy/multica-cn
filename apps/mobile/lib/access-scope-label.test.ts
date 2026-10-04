import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";
import type { AccessScope } from "@multica/core/agents";

import { accessScopeLabelKey } from "./access-scope-label";

const SCOPES: AccessScope[] = ["workspace", "specific-people", "owner-only"];

/**
 * The whole point of the hyphen→underscore mapping: every wire value must
 * resolve to a real leaf under `agents:access.scope_labels` in the en AND
 * zh-Hans resources — otherwise the badge renders the raw wire value via
 * defaultValue (the bug this helper exists to prevent).
 */
describe("accessScopeLabelKey", () => {
  const en = JSON.parse(
    readFileSync(
      join(__dirname, "../../../packages/views/locales/en/agents.json"),
      "utf8",
    ),
  ) as { access: { scope_labels: Record<string, string> } };
  const zh = JSON.parse(
    readFileSync(
      join(__dirname, "../../../packages/views/locales/zh-Hans/agents.json"),
      "utf8",
    ),
  ) as { access: { scope_labels: Record<string, string> } };

  it.each(SCOPES.map((s) => [s] as const))(
    "maps %s to a resource leaf in en and zh-Hans",
    (scope) => {
      const key = accessScopeLabelKey(scope);
      expect(en.access.scope_labels[key]).toBeTruthy();
      expect(zh.access.scope_labels[key]).toBeTruthy();
      // The translation must not just echo the en copy.
      expect(zh.access.scope_labels[key]).not.toBe(en.access.scope_labels[key]);
    },
  );

  it("covers every scope the resource knows about (no silent drift)", () => {
    const mapped = new Set(SCOPES.map(accessScopeLabelKey));
    expect([...mapped].sort()).toEqual(
      Object.keys(en.access.scope_labels).sort(),
    );
  });
});
