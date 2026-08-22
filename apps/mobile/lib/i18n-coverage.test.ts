// @vitest-environment node
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { looksLikeUserFacingEnglish, scanRoots } from "./i18n-coverage";

/**
 * i18n 覆盖率防线（RUYI-22）。
 *
 * 与 `i18n-keys.test.ts` 互补：那条线证明「已调用的 t() 都能命中资源」，
 * 这条线证明「该走 t() 的文案没有绕过 t()」。登录页整屏英文而测试全绿，
 * 就是缺了这一半。
 *
 * 两层断言：
 *   1. **P0 路径零容忍** —— 未登录首屏（登录 / 验证 / 服务器设置）不允许
 *      出现任何未走 t() 的英文文案，新增即红。
 *   2. **全量只减不增** —— 其余目录的遗留量写死在 baseline 快照里；新增
 *      硬编码会让实际条目超出基线而失败，修完一处则要求同步删基线，
 *      避免基线变成许可证。
 */

const MOBILE_ROOT = join(__dirname, "..");
const ROOTS = ["app", "components"];

/**
 * 白名单：确认「不需要翻译」的字面量。
 * 品牌名、协议标识、示例地址属于此类；任何业务文案不得进入。
 */
const ALLOWLIST: RegExp[] = [
  /^Multica$/,
  /^https:\/\/api\.example\.com$/,
  /^you@example\.com$/,
];

function currentViolations() {
  return scanRoots(MOBILE_ROOT, ROOTS).filter(
    (v) => !ALLOWLIST.some((re) => re.test(v.text)),
  );
}

function fingerprint(v: { file: string; kind: string; text: string }): string {
  return `${v.file}\t${v.kind}\t${v.text}`;
}

const BASELINE: string[] = JSON.parse(
  readFileSync(join(__dirname, "i18n-coverage-baseline.json"), "utf8"),
);

describe("mobile i18n coverage (hardcoded English guard)", () => {
  const violations = currentViolations();

  it("P0 未登录首屏没有未走 t() 的英文文案", () => {
    const p0 = violations.filter((v) =>
      /^app\/\(auth\)\/|^app\/server-settings\//.test(v.file),
    );
    expect(p0.map(fingerprint)).toEqual([]);
  });

  it("其余目录的遗留硬编码不得新增（baseline 只减不增）", () => {
    const baseline = new Set(BASELINE);
    const added = violations.map(fingerprint).filter((f) => !baseline.has(f));
    expect(added).toEqual([]);
  });

  it("baseline 不得残留已修复条目（修完必须同步删基线）", () => {
    const current = new Set(violations.map(fingerprint));
    const stale = BASELINE.filter((f) => !current.has(f));
    expect(stale).toEqual([]);
  });
});

describe("looksLikeUserFacingEnglish 判据", () => {
  it("识别用户可见的英文句子与短语", () => {
    expect(looksLikeUserFacingEnglish("Sign in to Multica")).toBe(true);
    expect(looksLikeUserFacingEnglish("Send code")).toBe(true);
    expect(looksLikeUserFacingEnglish("Servers")).toBe(true);
  });

  it("排除非文案字面量", () => {
    expect(looksLikeUserFacingEnglish("")).toBe(false);
    expect(looksLikeUserFacingEnglish("登录 Multica")).toBe(false);
    expect(looksLikeUserFacingEnglish("https://api.multica.ai")).toBe(false);
    expect(looksLikeUserFacingEnglish("text-sm text-muted")).toBe(false);
    expect(looksLikeUserFacingEnglish("in_progress")).toBe(false);
    expect(looksLikeUserFacingEnglish("OK")).toBe(false);
  });
});
