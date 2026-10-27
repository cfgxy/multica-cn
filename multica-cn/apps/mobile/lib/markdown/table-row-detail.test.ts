import { describe, expect, it } from "vitest";
import { buildTableRowFields, stepTableRow } from "./table-row-detail";
import type { MarkdownTableCell } from "./split-markdown";

const cell = (text: string): MarkdownTableCell => ({ text, tokens: [] });

/**
 * 行详情的纯逻辑（RUYI-72 手机端）。
 *
 * 与 web 的 `packages/views/rich-content/table-row-detail.tsx` 语义对齐：
 * 每行按列展开成「表头标签 → 单元格内容」；列数以表头与该行较长者为准；
 * 表头为空时用「第 N 列」占位；上一行/下一行到边界即停，不回绕。
 */
describe("buildTableRowFields", () => {
  it("按列把每行展开成 label/value 对", () => {
    const fields = buildTableRowFields({
      headers: [cell("姓名"), cell("角色")],
      rows: [[cell("甲"), cell("开发")]],
      unnamedColumnLabel: (index) => `第 ${index} 列`,
    });

    expect(fields).toEqual([
      [
        { label: "姓名", value: "甲" },
        { label: "角色", value: "开发" },
      ],
    ]);
  });

  it("表头为空时用序号占位，不丢列", () => {
    const fields = buildTableRowFields({
      headers: [cell("姓名"), cell("   ")],
      rows: [[cell("甲"), cell("开发")]],
      unnamedColumnLabel: (index) => `第 ${index} 列`,
    });

    expect(fields[0][1].label).toBe("第 2 列");
  });

  it("行比表头长时按行的实际列数展开，多出的列补占位表头", () => {
    const fields = buildTableRowFields({
      headers: [cell("姓名")],
      rows: [[cell("甲"), cell("多出来的")]],
      unnamedColumnLabel: (index) => `第 ${index} 列`,
    });

    expect(fields[0]).toEqual([
      { label: "姓名", value: "甲" },
      { label: "第 2 列", value: "多出来的" },
    ]);
  });

  it("行比表头短时缺列补空值，标签仍按表头给出", () => {
    const fields = buildTableRowFields({
      headers: [cell("姓名"), cell("角色")],
      rows: [[cell("甲")]],
      unnamedColumnLabel: (index) => `第 ${index} 列`,
    });

    expect(fields[0]).toEqual([
      { label: "姓名", value: "甲" },
      { label: "角色", value: "" },
    ]);
  });
});

describe("stepTableRow", () => {
  it("在范围内前进后退", () => {
    expect(stepTableRow(1, 1, 3)).toBe(2);
    expect(stepTableRow(1, -1, 3)).toBe(0);
  });

  it("首尾边界不回绕，返回原位", () => {
    expect(stepTableRow(0, -1, 3)).toBe(0);
    expect(stepTableRow(2, 1, 3)).toBe(2);
  });

  it("空表任何方向都停在 0", () => {
    expect(stepTableRow(0, 1, 0)).toBe(0);
    expect(stepTableRow(0, -1, 0)).toBe(0);
  });
});
