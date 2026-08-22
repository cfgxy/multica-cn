// @vitest-environment node
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  looksLikeUserFacingEnglish,
  scanRoots,
  scanSource,
} from "./i18n-coverage";

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
 * 这里曾有一份「不需要翻译」的白名单（品牌名 / 示例地址 / 示例邮箱），
 * 但审计下来三条正则实际命中 0 条 —— 品牌名与示例值早已走 t() 或被
 * `looksLikeUserFacingEnglish` 的判据直接排除。留着只会让人误以为防线
 * 有豁免口，故删除。真需要豁免时再按具体条目加回，并在注释里写明理由。
 */
function currentViolations() {
  return scanRoots(MOBILE_ROOT, ROOTS);
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

describe("扫描规则", () => {
  it("认得重命名的翻译绑定（tIssues / tProjects），不误报已走 t() 的文案", () => {
    const src = `
      const { t: tIssues } = useT("issues");
      Alert.alert(tIssues("edit.save_failed", "Failed to save"), "");
      <Text>{tIssues("edit.title", "Edit this issue")}</Text>
    `;
    expect(scanSource(src)).toEqual([]);
  });

  it("不放过没走 t() 的三元 / 模板串 / Alert.alert", () => {
    const src = `
      <Text>{busy ? "Sending your message" : "Tap to continue"}</Text>
      <Text>{\`Could not reach the server\`}</Text>
      Alert.alert("Upload failed", "Pick a smaller file and retry.");
    `;
    expect(
      scanSource(src)
        .map((v) => v.text)
        .sort(),
    ).toEqual([
      "Could not reach the server",
      "Pick a smaller file and retry.",
      "Sending your message",
      "Tap to continue",
      "Upload failed",
    ]);
  });

  it("字符串里的 // 不被当成行注释剥掉", () => {
    // 曾把 `https://github.com/o/r` 之后的整行抹掉，采出半截 `https:`
    // 条目，同时把同一行剩下的真文案一起吞掉（造成假绿）。
    const src = `
      <TextInput placeholder="https://github.com/owner/repo" />
      <Text>Paste the repository address here</Text>
    `;
    expect(scanSource(src).map((v) => v.text)).toEqual([
      "Paste the repository address here",
    ]);
  });

  it("HTML 实体不算待译文案", () => {
    expect(scanSource(`<Text>&quot;</Text>`)).toEqual([]);
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
