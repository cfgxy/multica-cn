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
 *   - **外部定义的数组 / 映射取值**：`{MAP[key]}`、`{LABELS[i]}` —— 字面量
 *     不在 JSX 里，取不到。注意**内联**写法 `{["A","B"][i]}` 实际会被采到：
 *     字面量就在 `{}` 内，`extractStringLiterals` 一视同仁。行为比这里的
 *     声明更严，不是 bug。
 *   - **含 `${}` 的模板串**：`` {`Hello ${name}`} ``。这类必然要走带插值的
 *     t()，但拆插值后的碎片易触发误报，暂不采。
 *   - **`Alert.alert` 的第三个参数**（buttons 数组里的 `text`）与
 *     `Alert.prompt`。
 *   - **props 上的表达式字面量**：`title={cond ? "A" : "B"}`；只采
 *     `prop="字面量"` 形态。
 *   - **文件内局部封装组件的裸文本**：`<SectionLabel>Status</SectionLabel>`
 *     —— 标签名不在 `TEXT_TAGS` 里就不采，哪怕它内部渲染的就是 `<Text>`
 *     （见 `issues-filter.tsx:150` 的 `function SectionLabel(...)`）。把每个
 *     项目自定义组件都列进 `TEXT_TAGS` 不现实，判定「该组件最终渲染文本」
 *     又要跨函数分析。这类只能靠人工 review 兜住。
 *   - **裸文本里的 URL 会截断同行后续文案**：`<Text>See https://x.dev for
 *     details</Text>` 中 `//` 之后到行尾被 `stripComments` 当行注释抹掉，
 *     只采到 `See https:`。`stripComments` 只跟踪引号状态，JSX 裸文本区不在
 *     引号内，认不出来。失效方向是**漏采**（同行后续文案静默丢失），不是
 *     误采。仓库现无此写法，代价小于为它改造 stripComments 的 JSX 感知。
 *   - **写进内部状态字段、但没有渲染消费点的英文串**：如
 *     `message-composer.tsx` 上传失败分支的 `"Unknown error"` —— 它落进
 *     `ComposerAttachmentItem.error`（定义见
 *     `components/issue/composer-attachment-row.tsx`），而该字段全仓无任何
 *     读取点，失败态 UI 只渲染重试图标 + destructive 色 + 文件名。这类既
 *     不在 JSX / props / Alert 三个检测面内（采不到），**采到了也不该报**
 *     （不进 UI，翻译它是无效工作）。判据是「有没有渲染消费点」，不是
 *     「像不像用户文案」；字段一旦被渲染就必须走 t()，届时清单要同步删掉
 *     这条。
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
  // HTML 实体（`&quot;` / `&amp;`）：源码里的转义写法，不是待译文案。
  if (/^&[a-z]+;$/.test(text)) return false;
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

/**
 * 剥掉行注释与块注释，保持偏移不变（用等长空格填充）。
 *
 * 必须带引号状态逐字符扫，不能直接用正则：`placeholder="https://github.com/o/r"`
 * 里的 `//` 会被朴素正则当成行注释，把后半截 URL 连同同一行剩下的属性一起
 * 抹掉，采出 `https:` 这种半截垃圾条目落进 baseline。
 */
function stripComments(src: string): string {
  const out = src.split("");
  let quote: string | null = null;
  for (let i = 0; i < src.length; i++) {
    const ch = src[i];
    if (quote) {
      if (ch === "\\") i += 1;
      else if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'" || ch === "`") {
      quote = ch;
      continue;
    }
    if (ch === "/" && src[i + 1] === "/") {
      while (i < src.length && src[i] !== "\n") out[i++] = " ";
      i -= 1;
    } else if (ch === "/" && src[i + 1] === "*") {
      const end = src.indexOf("*/", i + 2);
      const stop = end === -1 ? src.length : end + 2;
      for (; i < stop; i++) if (src[i] !== "\n") out[i] = " ";
      i -= 1;
    }
  }
  return out.join("");
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
 * 从源码里取出实际存在的翻译函数绑定名。
 *
 * 一个组件绑定多个 namespace 时写成
 * `const { t: tIssues } = useT("issues")`，调用点就成了 `tIssues(...)`。
 * 这些名字必须和 `t` / `i18n.t` 一样被挖空，否则已走 t() 的文案会被报成
 * 违规（误伤方向）。
 *
 * 只认**本文件里真实出现过的**解构绑定，不用 `t[A-Z]\w*` 这类模式匹配：
 * 模式匹配是开放式的，`tParse(...)`、`tFormatDate(...)` 这种与 i18n 无关
 * 的工具函数会被一并挖空，其中的英文字面量就此静默逃过检测——**失效方向
 * 是假绿，且没有任何红灯提示**。收敛成精确集合后，非绑定名一律照常上报。
 *
 * 两步拆解，不堆单条正则：先定位 `useT` / `useTranslation` 调用，再往左
 * 找配对的解构块，在块内独立提取 `t: <name>`。曾经的单条正则把四件事
 * 硬编码进一个匹配式（`t:` 紧贴左括号、`}` 紧贴 `=`、`(` 紧跟函数名），
 * 于是多属性、prettier 尾逗号、类型注解、泛型参数每种写法都要补一个
 * 分支；拆成两步后这些是同一处逻辑天然覆盖。
 *
 * 失效方向是**误伤**（已走 t() 的文案被报成违规）。开发者面对「明明已翻译
 * 却报红」最省事的处置就是把条目塞进 baseline，防线从此退化成许可证——
 * 所以这里宁可提取宽一点。
 */
function collectTranslatorNames(src: string): string[] {
  const out = new Set<string>();
  for (const m of src.matchAll(/use(?:T|Translation)\b/g)) {
    // 往左跳过空白、泛型参数 `<"issues">`、类型注解，找解构块的 `}`。
    // 只认紧邻的一段：中间出现别的实义字符就说明不是解构赋值。
    const head = src.slice(0, m.index ?? 0);
    const eq = head.lastIndexOf("=");
    if (eq === -1 || !/^[\s<>"'`,\w.$[\]]*$/.test(head.slice(eq + 1))) continue;
    // 往左逐个吃掉大括号块。`const { t: tIssues }: { t: TFunction } = useT()`
    // 里最靠近 `=` 的那个块是**类型注解**，提取它只会拿到类型名；块左侧
    // 紧跟 `:` 就是这种情况，跳过它继续往左找真正的解构块。
    let close = head.lastIndexOf("}", eq);
    if (close === -1 || head.slice(close + 1, eq).trim() !== "") continue;
    let open = matchingOpenBrace(head, close);
    while (open > 0 && head.slice(0, open).trimEnd().endsWith(":")) {
      close = head.lastIndexOf("}", open);
      if (close === -1) break;
      open = matchingOpenBrace(head, close);
    }
    if (open === -1 || close === -1) continue;
    // 块内按顶层逗号切属性，逐个找 `t: name`。属性自身的类型注解
    // （`t: tIssues as TFunction`）落在名字之后，正则到此截断即可。
    for (const prop of splitTopLevel(head.slice(open + 1, close))) {
      const hit = /^\s*t\s*:\s*([A-Za-z_$][A-Za-z0-9_$]*)/.exec(prop);
      if (hit) out.add(hit[1]);
    }
  }
  return [...out];
}

/** 从 `close` 处的 `}` 往左找配对的 `{`，计深度。找不到返回 -1。 */
function matchingOpenBrace(src: string, close: number): number {
  let depth = 0;
  for (let i = close; i >= 0; i--) {
    if (src[i] === "}") depth += 1;
    else if (src[i] === "{") {
      depth -= 1;
      if (depth === 0) return i;
    }
  }
  return -1;
}

/**
 * 按顶层逗号切分解构块内容。嵌套的 `{}` / `[]` / `<>` 与字符串内的逗号
 * 不算分隔符（`{ t, i18n: { language } }`、`{ t }: Props<"a,b">`）。
 */
function splitTopLevel(body: string): string[] {
  const parts: string[] = [];
  let depth = 0;
  let quote: string | null = null;
  let start = 0;
  for (let i = 0; i < body.length; i++) {
    const ch = body[i];
    if (quote) {
      if (ch === "\\") i += 1;
      else if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'" || ch === "`") quote = ch;
    else if (ch === "{" || ch === "[" || ch === "<") depth += 1;
    else if (ch === "}" || ch === "]" || ch === ">") depth -= 1;
    else if (ch === "," && depth === 0) {
      parts.push(body.slice(start, i));
      start = i + 1;
    }
  }
  parts.push(body.slice(start));
  return parts;
}

/**
 * 把翻译函数调用整段挖空（等长空格，偏移不变）。
 *
 * 调用里的字符串是 key 与英文 fallback，本来就该是英文，采进来全是误报。
 * 按括号深度找配对的 `)`，字符串内的括号不计数。
 *
 * 认 `t(…)` / `i18n.t(…)`，外加 `names` 里由 `collectTranslatorNames`
 * 从本文件实际提取到的重命名绑定。
 */
function stripTCalls(expr: string, names: string[] = []): string {
  // 名字来自源码，拼进 RegExp 前必须转义：JS 标识符允许 `$`，而 `$` 在
  // 正则里是行尾锚点（`t$x` 会拼成一个永不匹配的模式）。
  const escaped = names.map((n) => n.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  // 长的排前面：alternation 取先匹配上的分支，`t` 排在 `tIssues` 前会让
  // `tIssues(` 走上更曲折的回溯路径，直接按长度降序更稳。
  const alts = ["i18n\\.t", ...escaped, "t"].sort(
    (a, b) => b.length - a.length,
  );
  const re = new RegExp(
    `(?<![A-Za-z0-9_$.])(?:${alts.join("|")})(?![A-Za-z0-9_$])\\s*\\(`,
    "g",
  );
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
function extractStringLiterals(expr: string, names: string[] = []): string[] {
  const src = stripTCalls(expr, names);
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
function extractTextChildren(
  src: string,
  tag: string,
  names: string[] = [],
): string[] {
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
        if (braces === 0) out.push(...extractStringLiterals(expr, names));
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
function extractAlertTexts(src: string, names: string[] = []): string[] {
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
      out.push(...extractStringLiterals(arg, names));
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
  return scanSource(readFileSync(absPath, "utf8"), relPath);
}

/**
 * 扫描一段源码。给测试用的纯字符串入口 —— 不落盘就能对单条规则做
 * 「注入应报红 / 已走 t() 应放行」的断言。
 */
export function scanSource(rawSrc: string, relPath = "<source>"): Violation[] {
  const src = stripComments(rawSrc);
  const names = collectTranslatorNames(src);
  const found: Violation[] = [];
  for (const tag of TEXT_TAGS) {
    for (const text of extractTextChildren(src, tag, names)) {
      if (looksLikeUserFacingEnglish(text)) {
        found.push({ file: relPath, text, kind: `<${tag}>` });
      }
    }
  }
  for (const text of extractAlertTexts(src, names)) {
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
