// @vitest-environment node
import { describe, expect, it } from "vitest";
import { modalStackedSheetOptions } from "./modal-stacked-sheet-options";

/**
 * RUYI-623 — modal 之上叠 formSheet 的呈现派生（new-issue /
 * new-project 草稿 picker 路由）。派生本身是纯函数直接覆盖；布局实际
 * 接线由 workspace-stacked-picker-sheets.test.ts 捕获 Stack.Screen
 * options 断言。
 */

// 结构上等价于 _layout 的 SHEET_OPTIONS（含 sheet 专属字段）——不注解
// StackedSheetOptions，验证泛型对完整 sheet 选项对象的透传。
const SHEET = {
  presentation: "formSheet" as const,
  sheetGrabberVisible: true,
  sheetAllowedDetents: [0.6, 0.95] as [number, number],
  sheetCornerRadius: 20,
  contentStyle: { flex: 1 },
  headerShown: false,
};

describe("modalStackedSheetOptions", () => {
  it("iOS 原样透传 sheet 选项（同一引用）", () => {
    expect(modalStackedSheetOptions(SHEET, "ios")).toBe(SHEET);
  });

  it("web 等其它平台同样透传", () => {
    expect(modalStackedSheetOptions(SHEET, "web")).toBe(SHEET);
  });

  it("Android body 自绘头部 picker：全屏 modal，sheet 专属选项全部剔除", () => {
    const result = modalStackedSheetOptions(SHEET, "android");
    expect(result).toEqual({
      presentation: "modal",
      contentStyle: { flex: 1 },
      headerShown: false,
    });
    expect(result).not.toHaveProperty("sheetAllowedDetents");
    expect(result).not.toHaveProperty("sheetGrabberVisible");
    expect(result).not.toHaveProperty("sheetCornerRadius");
  });

  it("Android 搜索头部 picker：header/title 保留，detent 配置剔除", () => {
    const result = modalStackedSheetOptions(
      {
        ...SHEET,
        headerShown: true,
        title: "Assignee",
        sheetInitialDetentIndex: "last",
      },
      "android",
    );
    expect(result).toEqual({
      presentation: "modal",
      contentStyle: { flex: 1 },
      headerShown: true,
      title: "Assignee",
    });
    expect(result).not.toHaveProperty("sheetInitialDetentIndex");
  });
});
