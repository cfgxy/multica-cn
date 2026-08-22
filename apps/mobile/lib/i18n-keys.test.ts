// @vitest-environment node
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { RESOURCES } from "@multica/views/locales";

/**
 * 基于「真实资源」的 key 存在性测试（P1-5）：
 * 扫描 mobile 源码里所有 t()/i18n.t() 字面量 key，按其所在 ns 解析到
 * zh-Hans 资源——挡住「ns 分隔符用错」（P0-1）和「借用 key 语义错位」
 * （P0-3）两类缺陷：凡带默认值的调用，默认值必须与 en 资源原文一致；
 * 不带默认值的调用必须能在 zh-Hans 中解析。
 * 不 mock @multica/views/locales（参照 packages/views/locales/parity.test.ts）。
 */

const MOBILE_ROOT = join(__dirname, "..");
const zh = RESOURCES["zh-Hans"] as Record<string, Record<string, unknown>>;
const en = RESOURCES.en as Record<string, Record<string, unknown>>;

function lookup(bundle: Record<string, Record<string, unknown>>, ns: string, dotted: string): string | null {
  let cur: unknown = bundle[ns];
  if (cur == null) return null;
  for (const seg of dotted.split(".")) {
    if (cur == null || typeof cur !== "object" || cur === null) return null;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return typeof cur === "string" ? cur : null;
}

function listSourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name.startsWith(".")) continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...listSourceFiles(p));
    else if (/\.(tsx|ts)$/.test(name) && !/\.test\./.test(name) && !/\.d\.ts$/.test(name)) out.push(p);
  }
  return out;
}

type Call = { file: string; ns: string; key: string; fallback?: string };

function collectCalls(): Call[] {
  const calls: Call[] = [];
  for (const file of listSourceFiles(join(MOBILE_ROOT, "app"))) {
    calls.push(...collectFromFile(file, "app"));
  }
  for (const file of listSourceFiles(join(MOBILE_ROOT, "components"))) {
    calls.push(...collectFromFile(file, "components"));
  }
  for (const file of listSourceFiles(join(MOBILE_ROOT, "lib"))) {
    calls.push(...collectFromFile(file, "lib"));
  }
  return calls;
}

function collectFromFile(file: string, prefix: string): Call[] {
  // 先剥掉 // 行注释与 /* */ 块注释——注释里出现的 i18n.t("ns:key")
  // 示例文字不是调用点，混进采集会造成假红/假绿。
  const src = readFileSync(file, "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, (m) => " ".repeat(m.length))
    .replace(/\/\/[^\n]*/g, (m) => " ".repeat(m.length));
  const rel = `${prefix}/${file.slice(MOBILE_ROOT.length + 1)}`;
  const calls: Call[] = [];
  // ns bindings: const { t } = useT("ns") —— 同文件可出现多次（每个组件
  // 一次），按位置建索引，每个调用点就近取它之前最近的一次绑定，而不是
  // 只认文件里第一个绑定（多 ns 绑定文件会错配 ns）。
  const bindings: { at: number; ns: string }[] = [];
  const bindRe = /const\s*\{\s*t\s*\}\s*=\s*useT\(\s*(?:["']([^"']+)["'])?/g;
  for (const m of src.matchAll(bindRe)) {
    bindings.push({ at: m.index ?? 0, ns: m[1] ?? "common" });
  }
  const nsAt = (pos: number): string => {
    let ns = "common";
    for (const b of bindings) {
      if (b.at <= pos) ns = b.ns;
      else break;
    }
    return ns;
  };
  // i18n.t("ns:key", fb) / t("key", fb) —— 交替匹配 i18n.t 与裸 t，拒绝
  // 模板串与动态 key（只审字面量）。负向后顾排除标识符字符但不排除 "."
  // 之前的版本会把 45 处 i18n.t(...) 全部漏采——金小欣注入实验证明的防线
  // 漏洞；同时仍需排除 t 作为其他对象属性的情况（如 foo.t(...)）。
  const callRe = /(?<![A-Za-z0-9_])(?:i18n\.)?t\(\s*(["'])([^"']+)\1\s*(?:,\s*(["'`])([^"'`]*)\3)?\s*[,)]/g;
  for (const m of src.matchAll(callRe)) {
    const raw = m[2];
    let ns = nsAt(m.index ?? 0);
    let key = raw;
    if (raw.includes(":")) {
      const idx = raw.indexOf(":");
      ns = raw.slice(0, idx);
      key = raw.slice(idx + 1);
    }
    calls.push({ file: rel, ns, key, fallback: m[4] });
  }
  return calls;
}

describe("mobile t() key existence (real resources)", () => {
  const calls = collectCalls();

  it("finds t()/i18n.t() call sites (sanity)", () => {
    // 阈值贴近实际调用点数（当前 93，与金小欣复审口径一致）：若采集器
    // 回归漏采（如正则改动把 i18n.t 形式排除在外），此处先红——这是
    // 注入实验暴露过的防线漏洞。
    expect(calls.length).toBeGreaterThanOrEqual(90);
  });

  it("every literal t() key resolves in zh-Hans", () => {
    const misses = calls.filter((c) => lookup(zh, c.ns, c.key) == null);
    expect(
      misses.map((c) => `${c.file}  ${c.ns}:${c.key}`),
    ).toEqual([]);
  });

  it("every default value matches the en resource text", () => {
    const mismatches = calls.filter((c) => {
      if (c.fallback == null) return false;
      const enValue = lookup(en, c.ns, c.key);
      if (enValue == null) return true;
      const norm = (s: string) => s.toLowerCase().replace(/\s+/g, " ").trim();
      return norm(enValue) !== norm(c.fallback);
    });
    expect(
      mismatches.map((c) => `${c.file}  ${c.ns}:${c.key}  fb="${c.fallback}" en="${lookup(en, c.ns, c.key) ?? "?"}"`),
    ).toEqual([]);
  });
});
