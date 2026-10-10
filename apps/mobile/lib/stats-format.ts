/**
 * RUYI-638 阶段3：mobile 统计屏的数值/色阶格式化。
 *
 * 每个函数逐一镜像 web 侧对应实现（验收 G2：同类数值双端格式一致）——
 * web 的实现挂在 NumberFlow / CSS 色彩系统上，无法进 RN，这里按相同
 * 规则重写为纯函数；双端一致性由 stats-format.test.ts 的逐值断言锚定。
 */
import type { FailureClass } from "@multica/core/dashboard";

// CompactNumberFlow（packages/ui/components/ui/number-flow.tsx）的
// K/M/B/T 阶梯：按幅值选档、999,999 上浮进位到下一档、非整数保留
// 1 位小数。
const COMPACT_UNITS = [
  { divisor: 1, suffix: "" },
  { divisor: 1_000, suffix: "K" },
  { divisor: 1_000_000, suffix: "M" },
  { divisor: 1_000_000_000, suffix: "B" },
  { divisor: 1_000_000_000_000, suffix: "T" },
] as const;

export function formatCompactNumber(value: number): string {
  const magnitude = Math.abs(value);
  let unitIndex = 0;
  for (let i = COMPACT_UNITS.length - 1; i >= 0; i--) {
    if (magnitude >= COMPACT_UNITS[i]!.divisor) {
      unitIndex = i;
      break;
    }
  }
  let unit = COMPACT_UNITS[unitIndex]!;
  let scaledValue = value / unit.divisor;

  if (
    Math.abs(Number(scaledValue.toFixed(1))) >= 1_000 &&
    unitIndex < COMPACT_UNITS.length - 1
  ) {
    unit = COMPACT_UNITS[unitIndex + 1]!;
    scaledValue = value / unit.divisor;
  }

  const roundedValue = Number(scaledValue.toFixed(1));
  const fractionDigits =
    unit.divisor > 1 && !Number.isInteger(roundedValue) ? 1 : 0;
  return (
    roundedValue.toLocaleString("en-US", {
      minimumFractionDigits: fractionDigits,
      maximumFractionDigits: fractionDigits,
      useGrouping: false,
    }) + unit.suffix
  );
}

// CurrencyNumberFlow 的金额格式：$ 前缀、|v| < 100 两位小数、
// ≥ 100 取整、无千分位。
export function formatUsd(value: number): string {
  const fractionDigits = Math.abs(value) >= 100 ? 0 : 2;
  return (
    "$" +
    value.toLocaleString("en-US", {
      minimumFractionDigits: fractionDigits,
      maximumFractionDigits: fractionDigits,
      useGrouping: false,
    })
  );
}

// views failure-class-visuals formatRate 的镜像：total ≤ 0 为占位符
// （不读作 0%），≥ 10% 取整，< 10% 保留 1 位。
export function formatRatePercent(failed: number, total: number): string {
  if (total <= 0) return "—";
  const pct = (failed / total) * 100;
  return `${pct >= 10 || pct === 0 ? Math.round(pct) : pct.toFixed(1)}%`;
}

// web 侧失败分类色阶是把 --destructive 向 --card 混合出的七档
// （FAILURE_CLASS_COLOR，color-mix/CSS 变量，RN 解析不了）。mobile 用
// 同一 destructive 色 + 按同比例递减的不透明度表达同一 ramps——顺序仍
// 跟随 FAILURE_CLASSES（最该先看的一档最深），最暗档 0.3 保底可见。
export const FAILURE_CLASS_OPACITY: Record<FailureClass, number> = {
  auth: 1,
  rate_limit: 0.86,
  timeout: 0.72,
  provider: 0.6,
  runtime: 0.48,
  agent: 0.38,
  other: 0.3,
};
