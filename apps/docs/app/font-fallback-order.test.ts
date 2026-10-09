import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const chineseFonts = ["PingFang SC", "Microsoft YaHei", "Noto Sans CJK SC"];
const koreanFonts = ["Apple SD Gothic Neo", "Malgun Gothic", "Noto Sans CJK KR"];
const japaneseFonts = ["Hiragino Sans", "Yu Gothic", "Noto Sans CJK JP"];

function expectChineseFontsBeforeKoreanFonts(source: string) {
  const chineseIndexes = chineseFonts.map((font) => source.indexOf(font));
  const koreanIndexes = koreanFonts.map((font) => source.indexOf(font));

  expect(chineseIndexes).not.toContain(-1);
  expect(koreanIndexes).not.toContain(-1);

  for (const chineseIndex of chineseIndexes) {
    for (const koreanIndex of koreanIndexes) {
      expect(chineseIndex).toBeLessThan(koreanIndex);
    }
  }
}

// Japanese Kanji share the Han Unicode block with Chinese, so the docs
// Japanese-first CJK stack must be scoped to html[lang|="ja"] (zh/en keep
// Chinese-first) and order Japanese fonts before the Chinese families.
function expectJapaneseScopedOverride(source: string) {
  expect(source).toContain('html[lang|="ja"]');

  const japaneseIndexes = japaneseFonts.map((font) => source.indexOf(font));
  expect(japaneseIndexes).not.toContain(-1);

  const firstJapanese = Math.min(...japaneseIndexes);
  const lastChinese = Math.max(
    ...chineseFonts.map((font) => source.lastIndexOf(font)),
  );
  expect(firstJapanese).toBeLessThan(lastChinese);
}

describe("CJK font fallback order", () => {
  it("keeps docs Chinese font fallbacks before Korean font fallbacks", () => {
    const cssSource = readFileSync(
      resolve(process.cwd(), "app/global.css"),
      "utf8",
    );

    expectChineseFontsBeforeKoreanFonts(cssSource);
  });

  it("scopes the Japanese-first CJK stack to html[lang|='ja']", () => {
    const cssSource = readFileSync(
      resolve(process.cwd(), "app/global.css"),
      "utf8",
    );

    expectJapaneseScopedOverride(cssSource);
  });
});

// Docs must keep fonts self-hosted: next/font/google fetches
// fonts.googleapis.com at build time and stalls builds on hosts that cannot
// reach Google (this already regressed once in apps/web, see its
// app/layout.tsx). The layout imports fontsource packages and global.css
// composes the static fontsource family names.
describe("fonts are self-hosted", () => {
  it("keeps next/font/google out of the docs layout", () => {
    const layoutSource = readFileSync(
      resolve(process.cwd(), "app/[lang]/layout.tsx"),
      "utf8",
    );

    // Match import statements only — comments may still mention why
    // next/font/google was rejected.
    expect(layoutSource).not.toMatch(/import\s+[^;]*from\s+"next\/font/);
    expect(layoutSource).toMatch(/import\s+"@fontsource-variable\//);
  });

  it("declares the fontsource family names in global.css", () => {
    const cssSource = readFileSync(resolve(process.cwd(), "app/global.css"), "utf8");

    expect(cssSource).toContain('"Inter Variable"');
    expect(cssSource).toContain('"Geist Mono Variable"');
    expect(cssSource).toContain('"Source Serif 4 Variable"');
  });
});
