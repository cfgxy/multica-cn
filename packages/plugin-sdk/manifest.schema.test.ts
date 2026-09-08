// @vitest-environment node
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import Ajv2020 from "ajv/dist/2020";
import { describe, expect, it } from "vitest";
import schema from "./manifest.schema.json" with { type: "json" };

/**
 * The published schema, held against the manifests that already exist.
 *
 * Two directions, and the second is the one that gives the first any meaning: a
 * schema that accepts every official demo is worth nothing if it also accepts a
 * manifest the host would reject, and `{}` would pass the first half. So every
 * demo must validate, and every deliberately broken sample must fail — for the
 * rule its file name names, checked against the reported error path rather than
 * against "something went wrong".
 */

const here = dirname(fileURLToPath(import.meta.url));
const examples = join(here, "..", "..", "examples", "plugins");

const ajv = new Ajv2020({ allErrors: true, strict: true });
const validate = ajv.compile(schema);

function manifest(...parts: string[]): unknown {
  return JSON.parse(readFileSync(join(examples, ...parts), "utf8"));
}

const officialDemos = readdirSync(examples, { withFileTypes: true })
  .filter((entry) => entry.isDirectory() && entry.name !== "invalid-manifests")
  .map((entry) => entry.name);

describe("the official demos", () => {
  // Guards the guard: a glob that silently matched nothing would make every
  // assertion below vacuous.
  it("are all present", () => {
    expect(officialDemos.length).toBeGreaterThanOrEqual(6);
  });

  it.each(officialDemos)("%s validates", (name) => {
    const valid = validate(manifest(name, "multica.plugin.json"));
    expect(validate.errors ?? []).toEqual([]);
    expect(valid).toBe(true);
  });
});

describe("the deliberately invalid samples", () => {
  // Each entry names the schema path the rejection must come from, so a sample
  // that starts failing for an unrelated reason — a typo in the fixture, say —
  // is not counted as the rule still being enforced.
  const cases: Array<{ file: string; instancePath: string }> = [
    { file: "unknown-scope.json", instancePath: "/scopes/1" },
    { file: "enum-without-options.json", instancePath: "/config/environment" },
    { file: "secret-with-options.json", instancePath: "/config/api_token" },
    { file: "event-hook-without-events.json", instancePath: "/contributes/hooks/0" },
    { file: "schedule-on-mcp-transport.json", instancePath: "/contributes/hooks/0/transport/type" },
    { file: "surface-entry-not-a-script.json", instancePath: "/contributes/surfaces/0/entry" },
    { file: "unknown-field.json", instancePath: "" },
    { file: "empty-contributes.json", instancePath: "/contributes" },
    {
      file: "skill-entry-not-under-skills.json",
      instancePath: "/contributes/resources/0/entry",
    },
  ];

  it.each(cases)("$file is rejected", ({ file, instancePath }) => {
    const valid = validate(manifest("invalid-manifests", file));
    expect(valid).toBe(false);
    const paths = (validate.errors ?? []).map((error) => error.instancePath);
    expect(paths).toContain(instancePath);
  });
});

/**
 * The other half of the contract, and the one that used to be a lie.
 *
 * Every sample here is shape-valid and the host still refuses it, because the
 * rule it breaks is cross-field and a JSON Schema cannot see it. Asserting they
 * PASS is what keeps the schema honest about its own reach: an author whose
 * editor is green has been told the shape is right, not that the publish will
 * be accepted. `invalid-manifests/host-only/README.md` lists the same rules in
 * prose, and `server/pkg/plugincontract` asserts the host rejects each file.
 */
describe("the samples only the host can reject", () => {
  const hostOnly = readdirSync(join(examples, "invalid-manifests", "host-only"))
    .filter((name) => name.endsWith(".json"));

  it("are all present", () => {
    expect(hostOnly.length).toBe(5);
  });

  it.each(hostOnly)("%s passes the schema, so the schema does not claim to catch it", (file) => {
    const valid = validate(manifest("invalid-manifests", "host-only", file));
    expect(validate.errors ?? []).toEqual([]);
    expect(valid).toBe(true);
  });
});
