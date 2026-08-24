import { describe, expect, it, vi, beforeEach } from "vitest";

/**
 * lib/i18n.ts 的单测。
 * mock 掉 expo-localization 和 @multica/views/locales（Node 环境无 RN/JSON import）。
 */

// vi.mock 工厂会被提升到顶层，其中引用的变量必须用 vi.hoisted 声明。
const { mockInit, mockGetLocales } = vi.hoisted(() => ({
  mockInit: vi.fn(),
  mockGetLocales: vi.fn(),
}));

vi.mock("expo-localization", () => ({
  getLocales: mockGetLocales,
}));

vi.mock("@multica/views/locales", () => ({
  RESOURCES: {
    en: {
      common: { "layout.nav.inbox": "Inbox", "layout.nav.chat": "Chat" },
      inbox: { "list.empty": "Inbox zero" },
    },
    "zh-Hans": {
      common: { "layout.nav.inbox": "收件箱", "layout.nav.chat": "聊天" },
      inbox: { "list.empty": "零收件" },
    },
    ko: {
      common: { "layout.nav.inbox": "받은편지함", "layout.nav.chat": "채팅" },
    },
  },
}));

vi.mock("@multica/core/i18n", () => ({
  DEFAULT_LOCALE: "en",
  SUPPORTED_LOCALES: ["en", "zh-Hans", "ko", "ja"] as const,
}));

vi.mock("i18next", () => ({
  default: { init: mockInit },
}));

import { initI18n } from "./i18n";

describe("lib/i18n", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  function lastInitArgs() {
    return mockInit.mock.calls[mockInit.mock.calls.length - 1][0];
  }

  describe("initI18n", () => {
    it("uses system locale when it matches exactly", () => {
      mockGetLocales.mockReturnValue([
        { languageTag: "zh-Hans", languageCode: "zh" },
      ] as any);
      initI18n();
      expect(lastInitArgs().lng).toBe("zh-Hans");
    });

    it("falls back to prefix match (zh → zh-Hans)", () => {
      mockGetLocales.mockReturnValue([
        { languageTag: "zh", languageCode: "zh" },
      ] as any);
      initI18n();
      expect(lastInitArgs().lng).toBe("zh-Hans");
    });

    it("falls back to DEFAULT_LOCALE when no locale matches", () => {
      mockGetLocales.mockReturnValue([
        { languageTag: "fr", languageCode: "fr" },
      ] as any);
      initI18n();
      expect(lastInitArgs().lng).toBe("en");
    });

    it("passes buildResources() output as i18next resources", () => {
      mockGetLocales.mockReturnValue([
        { languageTag: "en", languageCode: "en" },
      ] as any);
      initI18n();
      const args = lastInitArgs();
      expect(args.resources.en.common).toEqual({
        "layout.nav.inbox": "Inbox",
        "layout.nav.chat": "Chat",
      });
      expect(args.resources["zh-Hans"].inbox).toEqual({
        "list.empty": "零收件",
      });
    });

    it("sets fallbackLng to DEFAULT_LOCALE (en)", () => {
      mockGetLocales.mockReturnValue([
        { languageTag: "en", languageCode: "en" },
      ] as any);
      initI18n();
      expect(lastInitArgs().fallbackLng).toBe("en");
    });

    it("sets defaultNS to 'common'", () => {
      mockGetLocales.mockReturnValue([
        { languageTag: "en", languageCode: "en" },
      ] as any);
      initI18n();
      expect(lastInitArgs().defaultNS).toBe("common");
    });

    it("disables XSS escaping (React Native)", () => {
      mockGetLocales.mockReturnValue([
        { languageTag: "en", languageCode: "en" },
      ] as any);
      initI18n();
      expect(lastInitArgs().interpolation.escapeValue).toBe(false);
    });

    it("disables Suspense (RN 不支持)", () => {
      mockGetLocales.mockReturnValue([
        { languageTag: "en", languageCode: "en" },
      ] as any);
      initI18n();
      expect(lastInitArgs().react.useSuspense).toBe(false);
    });

    it("picks first system locale from the list", () => {
      mockGetLocales.mockReturnValue([
        { languageTag: "ko", languageCode: "ko" },
        { languageTag: "en", languageCode: "en" },
      ] as any);
      initI18n();
      expect(lastInitArgs().lng).toBe("ko");
    });
  });
});
