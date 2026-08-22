import type { SupportedLocale } from "@multica/core/i18n";
import { DEFAULT_LOCALE } from "@multica/core/i18n";

/**
 * 纯类型，无运行时耦合——允许 mobile 端 import。
 * 见 apps/mobile/CLAUDE.md import 白名单。
 */

/** Mobile 端支持的语言列表，与 core/i18n 保持一致。 */
export const SUPPORTED_LOCALES: SupportedLocale[] = [
  "en",
  "zh-Hans",
  "ko",
  "ja",
];

/** 默认回退语言。 */
export { DEFAULT_LOCALE };
