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
  const src = readFileSync(file, "utf8");
  const rel = `${prefix}/${file.slice(MOBILE_ROOT.length + 1)}`;
  const calls: Call[] = [];
  // ns binding: const { t } = useT("ns")
  const bindMatch = src.match(/const\s*\{\s*t\s*\}\s*=\s*useT\(\s*(?:["']([^"']+)["'])?/);
  const boundNs = bindMatch?.[1] ?? "common";
  // t("key", "fallback") — 拒绝模板串与动态 key（只审字面量）
  const callRe = /(?<![A-Za-z0-9_.])t\(\s*(["'])([^"']+)\1\s*(?:,\s*(["'`])([^"'`]*)\3)?\s*[,)]/g;
  for (const m of src.matchAll(callRe)) {
    const raw = m[2];
    let ns = boundNs;
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

  it("finds t() call sites (sanity)", () => {
    expect(calls.length).toBeGreaterThan(40);
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
