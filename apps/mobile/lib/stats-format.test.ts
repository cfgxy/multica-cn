// @vitest-environment node
import { describe, expect, it } from "vitest";
import { FAILURE_CLASSES } from "@multica/core/dashboard";
import {
  FAILURE_CLASS_OPACITY,
  formatCompactNumber,
  formatRatePercent,
  formatUsd,
} from "./stats-format";

/**
 * RUYI-638 阶段3：mobile 统计屏的数值格式化与 web 双端对照（验收 G2）。
 *
 * 三个格式化器逐一镜像 web 侧对应实现，测试断言即「双端同值同形」的静态
 * 锚点：
 *   - formatCompactNumber ↔ packages/ui number-flow CompactNumberFlow
 *     （KPI tokens 卡的 K/M/B/T 阶梯 + 非整数保留 1 位小数）
 *   - formatUsd          ↔ 同文件 CurrencyNumberFlow
 *     （$ 前缀、<100 两位小数、≥100 整数、无千分位）
 *   - formatRatePercent  ↔ views failure-class-visuals formatRate
 *     （total≤0 为 "—"、≥10% 取整、<10% 保留 1 位）
 */
describe("formatCompactNumber（web CompactNumberFlow 镜像）", () => {
  it("小值原样，无后缀", () => {
    expect(formatCompactNumber(0)).toBe("0");
    expect(formatCompactNumber(7)).toBe("7");
    expect(formatCompactNumber(999)).toBe("999");
  });

  it("K/M/B/T 阶梯按幅值进位", () => {
    expect(formatCompactNumber(1000)).toBe("1K");
    expect(formatCompactNumber(1234)).toBe("1.2K");
    expect(formatCompactNumber(1_500_000)).toBe("1.5M");
    expect(formatCompactNumber(2_000_000_000)).toBe("2B");
    expect(formatCompactNumber(7_400_000_000_000)).toBe("7.4T");
  });

  it("999,999 进位到 M（web 的 1000× 阶梯上浮逻辑）", () => {
    expect(formatCompactNumber(999_999)).toBe("1M");
  });
});

describe("formatUsd（web CurrencyNumberFlow 镜像）", () => {
  it("小于 100 保留两位小数", () => {
    expect(formatUsd(0)).toBe("$0.00");
    expect(formatUsd(4.5)).toBe("$4.50");
  });

  it("大于等于 100 取整", () => {
    expect(formatUsd(123.456)).toBe("$123");
  });

  it("负数带符号前缀", () => {
    expect(formatUsd(-3.5)).toBe("$-3.50");
  });
});

describe("formatRatePercent（web formatRate 镜像）", () => {
  it("无分母时为占位符", () => {
    expect(formatRatePercent(0, 0)).toBe("—");
    expect(formatRatePercent(1, 0)).toBe("—");
  });

  it("零失败读作 0% 而非空态", () => {
    expect(formatRatePercent(0, 10)).toBe("0%");
  });

  it("大于等于 10% 取整，小于 10% 保留一位", () => {
    expect(formatRatePercent(3, 10)).toBe("30%");
    expect(formatRatePercent(1, 3)).toBe("33%");
    expect(formatRatePercent(1, 30)).toBe("3.3%");
    expect(formatRatePercent(1, 100)).toBe("1.0%");
  });
});

describe("FAILURE_CLASS_OPACITY（web destructive 色阶的 mobile 不透明度映射）", () => {
  it("覆盖 FAILURE_CLASSES 全部枚举（state-enum parity 锚点）", () => {
    expect(Object.keys(FAILURE_CLASS_OPACITY).sort()).toEqual(
      [...FAILURE_CLASSES].sort(),
    );
  });

  it("按枚举顺序单调递减，最暗档不低于可见下限", () => {
    const ramp = FAILURE_CLASSES.map((c) => FAILURE_CLASS_OPACITY[c]);
    for (let i = 1; i < ramp.length; i++) {
      expect(ramp[i]!).toBeLessThan(ramp[i - 1]!);
    }
    expect(ramp[ramp.length - 1]).toBeGreaterThanOrEqual(0.3);
  });
});
