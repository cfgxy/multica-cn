import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// The emoji-mart picker is ~1MB of emoji data and is code-split via lazy()
// everywhere (see quick-emoji-picker.tsx, avatar-upload-control.tsx). A static
// import from an eagerly-bundled surface pulls it back into the main chunk and
// turns those dynamic imports into no-ops — the desktop build flags exactly
// that with INEFFECTIVE_DYNAMIC_IMPORT. These assertions pin the lazy-only
// boundary for the two surfaces that regressed.
const LAZY_IMPORT = /lazy\(\(\)\s*=>\s*import\("[^"]*emoji-picker"\)/;
const STATIC_IMPORT =
  /import\s*\{[^}]*EmojiPicker[^}]*\}\s*from\s*"[^"]*emoji-picker"/;

describe("emoji-picker is only loaded via lazy()", () => {
  const surfaces = [
    "modals/create-project.tsx",
    "projects/components/project-detail.tsx",
  ];

  for (const surface of surfaces) {
    it(`keeps ${surface} on a lazy import`, () => {
      const source = readFileSync(resolve(process.cwd(), surface), "utf8");

      expect(source).toMatch(LAZY_IMPORT);
      expect(source).not.toMatch(STATIC_IMPORT);
    });
  }
});
