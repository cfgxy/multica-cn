// @vitest-environment node

import { describe, expect, it } from "vitest";
import en from "../../locales/en/self-evolution.json";
import ja from "../../locales/ja/self-evolution.json";
import ko from "../../locales/ko/self-evolution.json";
import zhHans from "../../locales/zh-Hans/self-evolution.json";

const locales = { en, "zh-Hans": zhHans, ja, ko } as const;

describe("knowledge governance i18n parity", () => {
  const expectedKeys = Object.keys(en.knowledgeSeg.governance).toSorted();
  const expectedStatusKeys = Object.keys(en.knowledgeSeg.governance.status).toSorted();

  it("keeps governance labels and status labels in every locale", () => {
    for (const [name, locale] of Object.entries(locales)) {
      const governance = locale.knowledgeSeg.governance;
      expect(Object.keys(governance).toSorted(), `${name}: governance keys drifted`).toEqual(expectedKeys);
      expect(Object.keys(governance.status).toSorted(), `${name}: governance.status keys drifted`).toEqual(expectedStatusKeys);
      for (const [key, value] of Object.entries(governance)) {
        if (typeof value === "string") {
          expect(value.length, `${name}: governance.${key} is empty`).toBeGreaterThan(0);
        }
      }
      for (const [key, value] of Object.entries(governance.status)) {
        expect(value.length, `${name}: governance.status.${key} is empty`).toBeGreaterThan(0);
      }
    }
  });
});
