import i18n from "i18next";
import { RESOURCES } from "@multica/views/locales";
import type { SupportedLocale } from "@multica/core/i18n";
import * as Localization from "expo-localization";
import { DEFAULT_LOCALE, SUPPORTED_LOCALES } from "./i18n-constants";

/**
 * 纯数据 / 纯函数，无运行时耦合——允许 mobile 端 import。
 * 见 apps/mobile/CLAUDE.md import 白名单。
 */

/**
 * 从 expo-localization 的系统 locale 列表中匹配最合适的 SupportedLocale。
 * 简单前缀匹配（zh → zh-Hans，ko → ko），不依赖 @formatjs/intl-localematcher。
 */
function matchSystemLocale(): SupportedLocale {
  const locales = Localization.getLocales();
  const tags = locales.map((l) => l.languageTag);
  for (const tag of tags) {
    // 精确匹配
    if ((SUPPORTED_LOCALES as readonly string[]).includes(tag)) {
      return tag as SupportedLocale;
    }
    // 前缀匹配（zh → zh-Hans）
    const prefix = tag.split("-")[0];
    const match = SUPPORTED_LOCALES.find((l) => l.startsWith(prefix));
    if (match) return match;
  }
  return DEFAULT_LOCALE;
}

/**
 * 转换 RESOURCES 为 i18next 需要的扁平结构。
 * RESOURCES 是 Record<locale, Record<namespace, Record<string, string>>>>，
 * i18next.resources 需要 Record<locale, Record<namespace, { key: string }>>。
 */
function buildResources() {
  const out: Record<string, Record<string, Record<string, string> > > = {};
  for (const [locale, namespaces] of Object.entries(RESOURCES)) {
    out[locale] = {};
    for (const [ns, strings] of Object.entries(namespaces)) {
      out[locale][ns] = strings as Record<string, string>;
    }
  }
  return out;
}

/**
 * 初始化 i18next（同步），必须在 I18nProvider 包裹之前调用。
 * 跟随系统语言；无用户手动切换（mobile v1 不提供语言设置 UI）。
 */
export function initI18n() {
  const locale = matchSystemLocale();
  i18n.init({
    resources: buildResources(),
    lng: locale,
    fallbackLng: DEFAULT_LOCALE,
    defaultNS: "common",
    interpolation: {
      escapeValue: false, // React Native 不需要 XSS 转义
    },
    react: {
      useSuspense: false, // RN 不支持 Suspense 模式
    },
  });
}
