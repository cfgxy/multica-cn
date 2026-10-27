/**
 * 日期/数字显示 locale 的单一出口。
 *
 * 所有 `formatDateOnly` / `toLocaleDateString` 调用点都必须经这里取 locale，
 * 不许各自写 `"en-US"`，也不许省略参数跟随系统 locale：
 *
 *  - 硬编码 `"en-US"` 会让日期永远是英文（`Mar 5`），与周围的中文译文割裂；
 *  - 省略参数跟随的是 **系统** locale，而界面语言是 SUPPORTED_LOCALES 前缀
 *    匹配后的结果，二者可以不同（系统 `ko-KR` 但界面落到 `en`），那时日期
 *    又会和界面语言对不上。
 *
 * 正确的锚点是「界面当前显示的语言」= `i18n.language`。
 *
 * i18n 尚未初始化时返回 `undefined` —— 交给 Intl 用运行环境默认值，而不是
 * 悄悄退回英文；这个窗口只在 initI18n() 之前存在，此时也没有译文可对齐。
 */
import i18n from "i18next";

export function displayLocale(): string | undefined {
  return i18n.language || undefined;
}
