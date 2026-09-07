/**
 * 表格行详情的纯逻辑（RUYI-72 手机端）。
 *
 * 手机竖屏下宽表格必然溢出，横向滚动会把尾部列藏起来。web 端的解法是
 * 点击行弹出「表头 → 单元格」的两列字段表（`packages/views/rich-content/
 * table-row-detail.tsx`），手机端复刻同一产品语义，只是载体换成底部弹窗。
 *
 * 这里只放形状转换与翻行边界，渲染留给 `table-block.tsx`——放在 lib/ 才能
 * 进 mobile 的 vitest lane（node 环境，不加载 RN 原生模块）。
 *
 * 与 web 的必须一致点（apps/mobile/CLAUDE.md §Behavioral parity）：
 *   - 列数取表头与该行较长者，谁都不许被截断；
 *   - 空表头用序号占位，而不是留白；
 *   - 首尾禁用而非回绕——标题里的「第 N / M 行」若跳回去没有任何提示。
 */
import type { MarkdownTableCell } from "./split-markdown";

export interface TableRowField {
  /** 字段名：对应列的表头文本，表头为空时是序号占位。 */
  label: string;
  /** 字段值：该行该列的单元格 markdown 源文；缺列为空串。 */
  value: string;
}

interface BuildOptions {
  headers: MarkdownTableCell[];
  rows: MarkdownTableCell[][];
  /** 空表头的占位文案，`index` 从 1 开始（交由调用方走 i18n）。 */
  unnamedColumnLabel: (index: number) => string;
}

export function buildTableRowFields({
  headers,
  rows,
  unnamedColumnLabel,
}: BuildOptions): TableRowField[][] {
  return rows.map((row) => {
    // 原始 markdown 允许某行比表头多/少列（尤其是手写表格），按较长者展开
    // 才能保证「点开能看到这行的全部字段」这一验收点。
    const count = Math.max(headers.length, row.length);
    return Array.from({ length: count }, (_, column) => {
      const header = headers[column];
      const headerText = header?.text?.trim() ?? "";
      return {
        label: headerText === "" ? unnamedColumnLabel(column + 1) : header.text,
        value: row[column]?.text ?? "",
      };
    });
  });
}

/** 翻行：越界即停在原位（首尾禁用而非回绕）。 */
export function stepTableRow(
  current: number,
  delta: number,
  total: number,
): number {
  const next = current + delta;
  if (next < 0 || next >= total) return current;
  return next;
}
