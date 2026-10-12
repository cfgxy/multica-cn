// @vitest-environment node
import { expect, it } from "vitest";
import { RuntimeCacheSchema } from "./runtime-cache-schema";
it("defaults missing cache fields and unfamiliar release states", () => {
  expect(RuntimeCacheSchema.parse({})).toEqual({ bytes: 0, limit_bytes: 0, downloads: 0, entries: [], catalog: [] });
  expect(RuntimeCacheSchema.parse({ bytes: -1, entries: [{ id: "broken" }], catalog: [{ id: "codex", display_name: "Codex", latest_version_state: "future" }] })).toMatchObject({ bytes: 0, entries: [], catalog: [{ latest_version: "", latest_version_state: "unavailable" }] });
});
it("parses cached files without installation capabilities", () => {
  const entry = { id: "hash", url: "https://registry.npmjs.org/tool/-/tool.tgz", size: 100, sha256: "digest", cached_at: "2026-09-28T00:00:00Z", last_used_at: "2026-09-28T00:00:00Z", metadata: false };
  expect(RuntimeCacheSchema.parse({ bytes: 100, limit_bytes: 1000, downloads: 1, entries: [entry] }).entries).toEqual([entry]);
});
