/**
 * i18n 求值时机扫描器（RUYI-25 批次 14 的 P1-A）。
 *
 * `initI18n()` 在 `app/_layout.tsx` 的**模块体**里调用。任何被它（直接或
 * 间接）import 的模块，其模块顶层语句都在 `initI18n()` 之前求值——此时
 * `i18n.t()` 返回空串。`more-tab-dropdown.tsx` 的 `const NAV_ITEMS` 就是
 * 这样把三个下拉标签渲染成空的，四语全中；而同文件组件体内的 `i18n.t()`
 * 一切正常，所以 `i18n-keys.test.ts`（审 key 是否存在）与
 * `i18n-coverage.test.ts`（审是否绕过 t）都抓不到这一类。
 *
 * 判据只有一条：`i18n.t(` 不得出现在模块顶层 `const/let/var` 的初始化式
 * 里。组件体、函数体、hook 内的调用都在渲染时求值，安全。
 *
 * 按缩进判断顶层：项目统一 2 空格缩进 + prettier，顶层声明必然起于第 0
 * 列，其续行至少 1 个空格，直到下一个第 0 列的非空 token。比接 TS parser
 * 轻，且误判方向保守——只可能漏报缩进异常的写法，不会误报正常代码。
 *
 * 与 `i18n-coverage.ts` 同构：纯函数放模块，IO 留给测试。
 */
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

/** 扫描单份源码，返回顶层声明中调用了 `i18n.t()` 的行号（1-based）。 */
export function scanEvalTiming(source: string): number[] {
  // 注释里的示例不是调用点，先抹成等长空白（与 i18n-keys.test.ts 同处理）。
  const src = source
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, " "))
    .replace(/\/\/[^\n]*/g, (m) => " ".repeat(m.length));
  const lines = src.split("\n");
  const hits: number[] = [];

  let declStart = -1;
  let buf: string[] = [];
  const flush = () => {
    if (declStart >= 0 && buf.join("\n").includes("i18n.t(")) {
      hits.push(declStart + 1);
    }
    declStart = -1;
    buf = [];
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (/^(?:export\s+)?(?:const|let|var)\s/.test(line)) {
      flush();
      declStart = i;
      buf = [line];
      continue;
    }
    if (declStart < 0) continue;
    if (line.length > 0 && !/^\s/.test(line)) {
      // 第 0 列的非空 token：上一个顶层声明已结束。
      flush();
      continue;
    }
    buf.push(line);
  }
  flush();
  return hits;
}

function listSourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name.startsWith(".")) continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...listSourceFiles(p));
    else if (
      /\.(tsx|ts)$/.test(name) &&
      !/\.test\./.test(name) &&
      !/\.d\.ts$/.test(name)
    ) {
      out.push(p);
    }
  }
  return out;
}

/** 扫描若干根目录，返回 `相对路径:行号` 形式的违规清单。 */
export function scanRootsForEvalTiming(
  mobileRoot: string,
  roots: string[],
): string[] {
  const offenders: string[] = [];
  for (const root of roots) {
    for (const file of listSourceFiles(join(mobileRoot, root))) {
      for (const line of scanEvalTiming(readFileSync(file, "utf8"))) {
        offenders.push(`${file.slice(mobileRoot.length + 1)}:${line}`);
      }
    }
  }
  return offenders;
}
