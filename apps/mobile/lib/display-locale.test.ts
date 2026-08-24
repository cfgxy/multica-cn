import { describe, expect, it, vi, beforeEach } from "vitest";

/**
 * 日期显示 locale 的单一出口（批次 11 任务 2）。
 *
 * 改动前全库有两种口径并存：三处 `formatDateOnly(…, "en-US")` 强制英文，
 * 一处省略第三参跟随系统 locale。同一个 due_date 在 issue 详情页显示
 * `Mar 5`、在新建表单显示 `3月5日` —— 同一业务对象日期格式分裂。
 *
 * 收敛到 i18n.language：日期格式必须跟界面语言走，而不是跟系统 locale
 * 走。二者可以不同（系统 ko-KR、界面因 SUPPORTED_LOCALES 匹配落到 en），
 * 那时跟系统会给出与周围译文不一致的日期。
 */

const { mockI18n } = vi.hoisted(() => ({
  mockI18n: { language: "" as string | undefined },
}));
vi.mock("i18next", () => ({ default: mockI18n }));

import { displayLocale } from "./display-locale";

beforeEach(() => {
  mockI18n.language = "zh-Hans";
});

describe("displayLocale", () => {
  it("返回当前界面语言", () => {
    expect(displayLocale()).toBe("zh-Hans");
  });

  it("i18n 尚未初始化时返回 undefined（退回 Intl 的系统默认），不硬编码 en-US", () => {
    mockI18n.language = undefined;
    expect(displayLocale()).toBeUndefined();
    mockI18n.language = "";
    expect(displayLocale()).toBeUndefined();
  });

  it("四种受支持语言都能被 Intl 识别，且中日韩不退化成英文月份", () => {
    // 这条是本次收敛的实质断言：把 "en-US" 换成 i18n.language 之后，
    // 日期真的会跟着界面语言变——否则这次改动只是换了个写法。
    const d = new Date(Date.UTC(2026, 2, 5));
    const fmt = (locale: string | undefined) =>
      d.toLocaleDateString(locale, {
        month: "short",
        day: "numeric",
        timeZone: "UTC",
      });
    for (const locale of ["zh-Hans", "ja", "ko"]) {
      mockI18n.language = locale;
      expect(fmt(displayLocale())).not.toBe(fmt("en-US"));
    }
    mockI18n.language = "en";
    expect(fmt(displayLocale())).toBe(fmt("en-US"));
  });
});
