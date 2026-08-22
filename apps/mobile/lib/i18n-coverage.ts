/**
 * 用户可见英文字面量的扫描器（RUYI-22 P0 防线）。
 *
 * 背景：`i18n-keys.test.ts` 只能证明「已经调用的 t() 都命中资源」，对
 * 「UI 文案压根没走 t()」完全无感 —— 登录页整屏英文却测试全绿，就是这个
 * 设计盲区造成的。本模块补上另一半：从源码里找出**没有走 t() 的**用户可见
 * 英文字面量。
 *
 * 检测面（覆盖 UI 上真正会被读到的文本）：
 *   1. 文本类标签（`Text` / `ThemedText` / `Button` / `Chip` / `Badge`）的
 *      裸文本子节点；
 *   2. 上述标签 `{}` 子表达式内的字符串字面量 —— 三元分支
 *      （`{ok ? "Saved" : "Failed"}`）与无占位符的纯模板串
 *      （`` {`Try again later`} ``）。`t(...)` / `i18n.t(...)` 调用整体
 *      剔除后再采，key 与 fallback 不会被误判；
 *   3. `Alert.alert(标题, 正文)` 的前两个位置参数；
 *   4. 文案类 props 的字符串字面量（title / placeholder / label /
 *      accessibilityLabel / headerTitle / headerBackTitle / …）。
 *      `prop={t(...)}` 是表达式而非字符串字面量，天然不会被采到。
 *
 * 刻意只用正则近似而不引 AST 解析器：mobile 没有 babel/TS AST 依赖，
 * 为一条测试防线引入解析器不划算。漏采让防线弱一点（清单见下），误采
 * 则落进 baseline 变成噪声；两者都不会误伤构建，但漏采会造成假绿，
 * 所以宁可多采一点也不要放过。
 *
 * ## 已知漏采清单（刻意不做，别重新踩一遍）
 *
 *   - **变量引用**：`const hint = "Session expired"; <Text>{hint}</Text>`。
 *     要判定得做作用域内的常量传播，正则做不到，也容易误伤纯数据变量。
 *   - **跨文件常量**：从 `constants.ts` / 后端响应导入的英文串。
 *   - **数组 / 对象取值**：`{["A","B"][i]}`、`{MAP[key]}`。字面量在表达式里
 *     但语义是数据查表，采了误报高于收益。
 *   - **含 `${}` 的模板串**：`` {`Hello ${name}`} ``。这类必然要走带插值的
 *     t()，但拆插值后的碎片易触发误报，暂不采。
 *   - **`Alert.alert` 的第三个参数**（buttons 数组里的 `text`）与
 *     `Alert.prompt`。
 *   - **props 上的表达式字面量**：`title={cond ? "A" : "B"}`；只采
 *     `prop="字面量"` 形态。
 *
 * 以上任一类进入 P1 视野时，补齐点都在 `extractStringLiterals` /
 * `extractTextProps` 附近，测试用「注入 → 应失败」的方式验证。
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

/**
 * 会渲染文本子节点的组件。RN 里裸字符串必须包在 `Text` 系组件内，但
 * `Button` / `Chip` / `Badge` 这类封装组件在本仓库里也直接接受裸文本或
 * `{cond ? "A" : "B"}` 子节点，同样是用户可见文案的入口。
 */
const TEXT_TAGS = ["Text", "ThemedText", "Button", "Chip", "Badge"];

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
 * 标识符形态（URL、路径、枚举值、className 片段）。判据只做形态过滤，
 * 不认识任何具体业务词——豁免某条具体文案属于调用方的事。
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

/**
 * 把 `t(...)` / `i18n.t(...)` 调用整段挖空（等长空格，偏移不变）。
 *
 * 调用里的字符串是 key 与英文 fallback，本来就该是英文，采进来全是误报。
 * 按括号深度找配对的 `)`，字符串内的括号不计数。
 */
function stripTCalls(expr: string): string {
  const re = /(?<![A-Za-z0-9_$.])(?:i18n\.)?t\s*\(/g;
  let out = expr;
  for (const m of expr.matchAll(re)) {
    const start = m.index ?? 0;
    let depth = 0;
    let quote: string | null = null;
    let i = start + m[0].length - 1;
    for (; i < expr.length; i++) {
      const ch = expr[i];
      if (quote) {
        if (ch === "\\") i += 1;
        else if (ch === quote) quote = null;
        continue;
      }
      if (ch === '"' || ch === "'" || ch === "`") quote = ch;
      else if (ch === "(") depth += 1;
      else if (ch === ")") {
        depth -= 1;
        if (depth === 0) break;
      }
    }
    const end = Math.min(i + 1, expr.length);
    out = out.slice(0, start) + " ".repeat(end - start) + out.slice(end);
  }
  return out;
}

/**
 * 取出一段表达式里的「静态字符串字面量」。
 *
 * 覆盖 `"…"` / `'…'` 与不含 `${}` 的模板串；带插值的模板串、变量引用、
 * 数组取值都不采（见模块头「已知漏采清单」）。
 */
function extractStringLiterals(expr: string): string[] {
  const src = stripTCalls(expr);
  const out: string[] = [];
  for (let i = 0; i < src.length; i++) {
    const q = src[i];
    if (q !== '"' && q !== "'" && q !== "`") continue;
    // 逐字符消费到配对引号。模板串必须整段消费而不能用正则截取：
    // `Switch to "${ws.name}"?` 里的双引号会让朴素正则从中途起头，
    // 把 `${ws.name}` 当成一条独立字面量采进来。
    let body = "";
    let interpolated = false;
    let j = i + 1;
    for (; j < src.length; j++) {
      const ch = src[j];
      if (ch === "\\") {
        // `\n` / `\t` 还原成真空白，否则会残留成字母 n/t 混进文案里。
        const esc = src[j + 1] ?? "";
        body += esc === "n" ? "\n" : esc === "t" ? "\t" : esc;
        j += 1;
        continue;
      }
      if (ch === q) break;
      if (q === "`" && ch === "$" && src[j + 1] === "{") {
        interpolated = true;
        // 跳过 `${…}`，内部大括号计深度。
        let depth = 0;
        for (j += 1; j < src.length; j++) {
          if (src[j] === "{") depth += 1;
          else if (src[j] === "}" && --depth === 0) break;
        }
        continue;
      }
      body += ch;
    }
    i = j;
    // 与 JSX 裸文本同口径归一空白，保证 baseline 指纹稳定。
    const text = body.replace(/\s+/g, " ").trim();
    if (text && !interpolated) out.push(text);
  }
  return out;
}

/** 从开标签结束处出发，跳到配对 `</tag>` 之后。找不到配对时返回原位。 */
function skipElement(src: string, from: number, tag: string): number {
  let depth = 1;
  let i = from;
  while (i < src.length && depth > 0) {
    if (src.startsWith(`</${tag}>`, i)) {
      depth -= 1;
      i += tag.length + 3;
      continue;
    }
    if (
      src.startsWith(`<${tag}`, i) &&
      !/[A-Za-z0-9_]/.test(src[i + tag.length + 1] ?? "")
    ) {
      const head = findTagEnd(src, i + 1);
      if (!head) return from;
      if (!head.selfClosing) depth += 1;
      i = head.end;
      continue;
    }
    i += 1;
  }
  return depth === 0 ? i : from;
}

/**
 * 取出 `<Text …>` 与配对 `</Text>` 之间的用户可见文案。
 *
 * 两个来源：标签与 `{}` 之外的裸文本，以及 `{}` 子表达式内的静态字符串
 * 字面量。后者是补上的关键一环——此前 `{}` 内容整体丢弃，导致
 * `{cond ? "A" : "B"}` 这类最常见的写法完全不设防。`{t("k")}` 由
 * `stripTCalls` 剔除，`{count}` 没有字面量，都不会误报。
 *
 * 嵌套同名标签用计数配对；嵌套的其他文本标签整段跳过，由它自己那轮扫描
 * 负责，避免一句文案被重复上报。
 */
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
    // `{}` 子表达式的原文，`}` 收口时结算出里面的静态字符串字面量。
    let expr = "";
    while (i < src.length && depth > 0) {
      if (braces === 0 && src.startsWith(`</${tag}>`, i)) {
        depth -= 1;
        i += tag.length + 3;
        continue;
      }
      const ch = src[i];
      if (ch === "{") {
        braces += 1;
        if (braces === 1) expr = "";
        else expr += ch;
      } else if (ch === "}") {
        braces = Math.max(0, braces - 1);
        if (braces === 0) out.push(...extractStringLiterals(expr));
        else expr += ch;
      } else if (ch === "<" && /^<\/?[A-Za-z]/.test(src.slice(i, i + 3))) {
        // 跳过任意子标签本身，只丢标签不丢文本。同名嵌套要计深度，
        // 且非自闭合才算多一层。`{}` 内的内联 JSX 同样跳过——其属性串
        // （className 等）不是文案，其裸文本由外层循环单独匹配到。
        const head = findTagEnd(src, i + 1);
        if (!head) break;
        const name = /^<\/?([A-Za-z][A-Za-z0-9_.]*)/.exec(src.slice(i))?.[1];
        const nested = braces === 0 && name === tag;
        if (nested && !head.selfClosing) depth += 1;
        // 内层也是文本标签（`<Button><Text>…</Text></Button>`）时整段跳过：
        // 它会在自己那轮扫描里被处理，否则同一句文案重复上报两次。
        if (
          !nested &&
          name &&
          name !== tag &&
          TEXT_TAGS.includes(name) &&
          !head.selfClosing &&
          !src.startsWith("</", i)
        ) {
          i = skipElement(src, head.end, name);
          continue;
        }
        i = head.end;
        continue;
      } else if (braces === 0) {
        buf += ch;
      } else {
        expr += ch;
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

/**
 * 取出 `Alert.alert(标题, 正文)` 的前两个位置参数里的静态字符串。
 *
 * 弹窗文案和 `<Text>` 一样是用户直读的内容，此前完全不在检测面内——
 * 仓库里 6 处英文 Alert 就是这么漏过去的。第三个参数（buttons 数组）
 * 暂不采，见模块头「已知漏采清单」。
 */
function extractAlertTexts(src: string): string[] {
  const out: string[] = [];
  for (const m of src.matchAll(/(?<![A-Za-z0-9_$.])Alert\s*\.\s*alert\s*\(/g)) {
    const open = (m.index ?? 0) + m[0].length - 1;
    // 按括号深度切出实参区，同时记录顶层逗号的位置以划分参数。
    let depth = 0;
    let quote: string | null = null;
    const commas: number[] = [];
    let i = open;
    for (; i < src.length; i++) {
      const ch = src[i];
      if (quote) {
        if (ch === "\\") i += 1;
        else if (ch === quote) quote = null;
        continue;
      }
      if (ch === '"' || ch === "'" || ch === "`") quote = ch;
      else if (ch === "(" || ch === "[" || ch === "{") depth += 1;
      else if (ch === ")" || ch === "]" || ch === "}") {
        depth -= 1;
        if (depth === 0) break;
      } else if (ch === "," && depth === 1) commas.push(i);
    }
    const bounds = [open, ...commas, i];
    // 前两个参数 = bounds[0..1] 与 bounds[1..2] 之间的片段。
    for (let a = 0; a < 2 && a + 1 < bounds.length; a++) {
      const arg = src.slice(bounds[a] + 1, bounds[a + 1]);
      out.push(...extractStringLiterals(arg));
    }
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
  for (const text of extractAlertTexts(src)) {
    if (looksLikeUserFacingEnglish(text)) {
      found.push({ file: relPath, text, kind: "Alert.alert" });
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
