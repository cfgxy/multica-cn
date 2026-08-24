// @vitest-environment node
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { scanEvalTiming, scanRootsForEvalTiming } from "./i18n-eval-timing";

/**
 * 见 `i18n-eval-timing.ts` 顶部：挡住「模块顶层求值 i18n.t()，早于
 * initI18n()，标签渲染为空」这一类。RUYI-25 批次 14 的 P1-A 就是它。
 */

const MOBILE_ROOT = join(__dirname, "..");
const ROOTS = ["app", "components", "lib"];

describe("i18n 求值时机", () => {
  it("模块顶层的 const/let/var 初始化式不得调用 i18n.t()", () => {
    expect(scanRootsForEvalTiming(MOBILE_ROOT, ROOTS)).toEqual([]);
  });

  it("抓得到修复前的 NAV_ITEMS 写法，且不误伤组件体内的调用", () => {
    const before = [
      "const NAV_ITEMS = [",
      '  { label: i18n.t("layout:nav.issues", "Issues") },',
      "];",
      "",
      "export function C() {",
      '  const label = i18n.t("layout:nav.projects", "Projects");',
      "  return label;",
      "}",
    ].join("\n");
    // 顶层的第 1 行命中；组件体内第 6 行的 const 虽也是 const 开头，但它
    // 缩进在函数体内，不匹配顶层判据。
    expect(scanEvalTiming(before)).toEqual([1]);
  });

  it("不把注释里的 i18n.t() 示例当作调用点", () => {
    const src = [
      "// const NAV_ITEMS = [{ label: i18n.t(\"a:b\") }];",
      "/* const X = i18n.t(\"a:b\"); */",
      "const SAFE = 1;",
    ].join("\n");
    expect(scanEvalTiming(src)).toEqual([]);
  });
});
