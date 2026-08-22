/**
 * 用户可见英文字面量的扫描器（RUYI-22 P0 防线）。
 *
 * 背景：`i18n-keys.test.ts` 只能证明「已经调用的 t() 都命中资源」，对
 * 「UI 文案压根没走 t()」完全无感 —— 登录页整屏英文却测试全绿，就是这个
 * 设计盲区造成的。本模块补上另一半：从源码里找出**没有走 t() 的**用户可见
 * 英文字面量。
 *
 * 检测面（覆盖 UI 上真正会被读到的文本）：
 *   1. `<Text>…</Text>` / `<ThemedText>…</ThemedText>` 的裸文本子节点；
 *   2. 文案类 props 的字符串字面量（title / placeholder / label /
 *      accessibilityLabel / headerTitle / headerBackTitle / …）。
 *      `prop={t(...)}` 是表达式而非字符串字面量，天然不会被采到。
 *
 * 刻意只用正则近似而不引 AST 解析器：mobile 没有 babel/TS AST 依赖，
 * 为一条测试防线引入解析器不划算；漏采的代价是防线弱一点，误采的代价
 * 由白名单兜住，两者都不会造成假绿以外的伤害——而假绿正是白名单收紧的
 * 动力。
 */
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

/** 会被用户读到的文案 props。`accessibilityLabel` 计入：读屏用户听得到。 */
const TEXT_PROPS = [
  "title",
  "placeholder",
  "label",
  "accessibilityLabel",
  "accessibilityHint",
  "headerTitle",
  "headerBackTitle",
  "headerBackTitleVisible",
  "emptyText",
  "confirmText",
  "cancelText",
];

/** 渲染纯文本的组件。RN 里任何裸字符串都必须包在这些组件内。 */
const TEXT_TAGS = ["Text", "ThemedText"];

export type Violation = {
  /** 相对 apps/mobile 的路径 */
  file: string;
  /** 命中的英文字面量原文 */
  text: string;
  /** 命中来源：JSX 文本子节点，或某个文案 prop */
  kind: string;
};

/**
 * 判定一段文本是否「用户可见的英文文案」。
 *
 * 判据：去掉插值/变量后，至少包含一个 ≥2 个字母的英文单词，且不是纯粹的
 * 标识符形态（URL、路径、枚举值、className 片段）。品牌名与占位符由调用方
 * 的白名单排除，不在这里硬编码——写死在判据里就没人能审计了。
 */
export function looksLikeUserFacingEnglish(raw: string): boolean {
  const text = raw.trim();
  if (!text) return false;
  // 含 CJK 字符的文案已经是翻译产物或占位，交给人工判断，不算违规。
  if (/[一-鿿぀-ヿ가-힯]/.test(text)) return false;
  // URL / 路径 / 点分标识符 / 纯 kebab-snake 常量：不是句子。
  if (/^https?:\/\//.test(text)) return false;
  if (/^[\w.-]+@[\w.-]+$/.test(text)) return false;
  if (/^[/@#.]/.test(text)) return false;
  if (/^[a-z0-9_.-]+$/.test(text)) return false;
  // className 串（`text-sm text-muted-foreground`）：每个 token 都带
  // `-` / `:` 且全小写，没有任何一个是普通英文单词。
  const tokens = text.split(/\s+/);
  if (tokens.every((tk) => /^[a-z0-9]+[:-][a-z0-9:./-]*$/.test(tk))) {
    return false;
  }
  // 至少两个英文单词，或一个 ≥4 字母的单词——单个短词（"OK"、"ID"）大多是
  // 图标标签或缩写，误报率高于收益。
  const words = text.match(/[A-Za-z][A-Za-z']+/g) ?? [];
  if (words.length === 0) return false;
  if (words.length === 1 && words[0].length < 4) return false;
  return true;
}

/** 剥掉行注释与块注释，保持偏移不变（用等长空格填充）。 */
function stripComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, (m) => " ".repeat(m.length))
    .replace(/\/\/[^\n]*/g, (m) => " ".repeat(m.length));
}

/**
 * 取出 `<Text …>` 与配对 `</Text>` 之间的裸文本。
 *
 * 只保留标签之外、`{}` 表达式之外的字符：`{t("k")}` 与 `{count}` 都属于
 * 表达式，天然被剔除；剩下的裸字符才是硬编码。嵌套标签用计数配对，避免
 * `<Text><Text/></Text>` 提前收尾。
 */
/**
 * 从开标签名之后的位置出发，找到真正结束这个标签的 `>`。
 *
 * 不能直接 `indexOf(">")`：属性里的表达式常含比较运算符
 * （`className={cooldown > 0 ? …}`），朴素查找会在属性中途截断，把半截
 * 表达式当成文本采进来。这里按 `{}` 深度与引号状态跳过属性区。
 */
function findTagEnd(
  src: string,
  from: number,
): { end: number; selfClosing: boolean } | null {
  let braces = 0;
  let quote: string | null = null;
  for (let i = from; i < src.length; i++) {
    const ch = src[i];
    if (quote) {
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'" || ch === "`") {
      quote = ch;
      continue;
    }
    if (ch === "{") braces += 1;
    else if (ch === "}") braces = Math.max(0, braces - 1);
    else if (ch === ">" && braces === 0) {
      return { end: i + 1, selfClosing: src[i - 1] === "/" };
    }
  }
  return null;
}

function extractTextChildren(src: string, tag: string): string[] {
  const out: string[] = [];
  const open = new RegExp(`<${tag}(?![A-Za-z0-9_])`, "g");
  for (const m of src.matchAll(open)) {
    const head = findTagEnd(src, (m.index ?? 0) + m[0].length);
    // 自闭合标签没有子节点
    if (!head || head.selfClosing) continue;
    let i = head.end;
    let depth = 1;
    let braces = 0;
    let buf = "";
    while (i < src.length && depth > 0) {
      if (braces === 0 && src.startsWith(`</${tag}>`, i)) {
        depth -= 1;
        i += tag.length + 3;
        continue;
      }
      const ch = src[i];
      if (ch === "{") braces += 1;
      else if (ch === "}") braces = Math.max(0, braces - 1);
      else if (braces === 0 && ch === "<") {
        // 跳过任意子标签本身，只丢标签不丢文本。同名嵌套要计深度，
        // 且非自闭合才算多一层。
        const head = findTagEnd(src, i + 1);
        if (!head) break;
        const nested =
          src.startsWith(`<${tag}`, i) &&
          !/[A-Za-z0-9_]/.test(src[i + tag.length + 1] ?? "");
        if (nested && !head.selfClosing) depth += 1;
        i = head.end;
        continue;
      } else if (braces === 0) {
        buf += ch;
      }
      i += 1;
    }
    const text = buf.replace(/\s+/g, " ").trim();
    if (text) out.push(text);
  }
  return out;
}

/** 取出文案类 props 上的字符串字面量（`prop="…"` / `prop='…'`）。 */
function extractTextProps(src: string): { prop: string; text: string }[] {
  const out: { prop: string; text: string }[] = [];
  const re = new RegExp(
    `(?<![A-Za-z0-9_])(${TEXT_PROPS.join("|")})\\s*=\\s*(["'])([^"']*)\\2`,
    "g",
  );
  for (const m of src.matchAll(re)) {
    out.push({ prop: m[1], text: m[3] });
  }
  return out;
}

export function listTsxFiles(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name.startsWith(".")) continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...listTsxFiles(p));
    else if (name.endsWith(".tsx") && !/\.test\./.test(name)) out.push(p);
  }
  return out;
}

/** 扫描单个文件，返回未走 t() 的用户可见英文字面量。 */
export function scanFile(absPath: string, relPath: string): Violation[] {
  const src = stripComments(readFileSync(absPath, "utf8"));
  const found: Violation[] = [];
  for (const tag of TEXT_TAGS) {
    for (const text of extractTextChildren(src, tag)) {
      if (looksLikeUserFacingEnglish(text)) {
        found.push({ file: relPath, text, kind: `<${tag}>` });
      }
    }
  }
  for (const { prop, text } of extractTextProps(src)) {
    if (looksLikeUserFacingEnglish(text)) {
      found.push({ file: relPath, text, kind: `prop:${prop}` });
    }
  }
  return found;
}

/** 扫描若干根目录（相对 apps/mobile）。 */
export function scanRoots(mobileRoot: string, roots: string[]): Violation[] {
  const out: Violation[] = [];
  for (const root of roots) {
    for (const abs of listTsxFiles(join(mobileRoot, root))) {
      const rel = abs.slice(mobileRoot.length + 1);
      out.push(...scanFile(abs, rel));
    }
  }
  return out;
}
