import { useTranslation } from "react-i18next";

/**
 * Mobile 端的翻译 hook，等价于 web 的 useT()。
 * 默认 namespace 为 "common"，可传入指定 namespace。
 */
export function useT(ns?: string) {
  return useTranslation(ns);
}
