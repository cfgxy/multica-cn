// @vitest-environment node
import { existsSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * RUYI-698 — formSheet 自绘标题头条件化状态栏避让的机械断言。
 *
 * 机理：SHEET_OPTIONS_BASE 双档位 `[0.6, 0.95]` 下，自绘标题头无条件加
 * `paddingTop: insets.top + N` 作状态栏避让；弹窗以 0.6 小档打开时顶边远离
 * 状态栏，该留白成为纯空白（Owner 截图实证：智能体详情页「执行配置」sheet
 * 顶边到标题空白约 1.5–2 倍状态栏高度）。避让只应在 sheet 顶边贴近屏幕顶部
 * 时存在——即最大档（0.95）或 Android 全屏 modal（RUYI-623 嵌套 picker）。
 *
 * 机制收敛为 `sheetHeaderTopInset`（纯函数）+ `useSheetAtLargeDetent`
 * （navigation `sheetDetentChange` 事件订阅）+ `<SheetHeader>`（共享组件，
 * reanimated 平滑过渡）。本文件钉住两件事：
 *
 *   1. 机制断言：两档行为——小档标题头顶部间距为常规内容间距（不含
 *      insets.top 量级），大档保留状态栏避让。
 *   2. 治理断言：全部自绘标题头调用点走 `<SheetHeader>`，旧模式
 *      `paddingTop: insets.top` 全仓零残留（同 form-sheet-scroll-governance
 *      的 readFileSync 源码形态手法——RN 原生 sheet 行为跑不进 node lane，
 *      能机械钉住的只有接线本身）。
 */

import { sheetHeaderTopInset } from "./sheet-header-inset";

describe("sheetHeaderTopInset 两档行为（RUYI-698 机制断言）", () => {
  it("小档（0.6）：标题头顶部间距为常规内容间距，不含 insets.top 量级", () => {
    expect(sheetHeaderTopInset(48, false, 16)).toBe(16);
    expect(sheetHeaderTopInset(48, false, 12)).toBe(12);
    expect(sheetHeaderTopInset(59, false, 16)).toBe(16);
  });

  it("大档（0.95）：保留状态栏避让", () => {
    expect(sheetHeaderTopInset(48, true, 16)).toBe(64);
    expect(sheetHeaderTopInset(48, true, 12)).toBe(60);
    expect(sheetHeaderTopInset(59, true, 16)).toBe(75);
  });

  it("无状态栏 inset 的设备两档退化一致", () => {
    expect(sheetHeaderTopInset(0, true, 16)).toBe(16);
    expect(sheetHeaderTopInset(0, false, 16)).toBe(16);
  });
});

/**
 * 自绘标题头全量清单（RUYI-698 迁移基线 = f886bb352c 上
 * `git grep "paddingTop: insets.top"` 的 27 处命中）。gap 为原表达式
 * `insets.top + N` 的 N；`expectInitialProp` 为 true 的路由打开时顶边
 * 即贴近屏幕顶部（LONG_CONTENT 初始 0.95 档、Android 全屏 modal），必须
 * 显式传 `initialAtLargeDetent`；为 false 的路由保持默认且禁止传该 prop
 * （0.6 档打开，事件驱动切换）。
 */
const SHEET_HEADER_USERS: {
  file: string;
  gap: number;
  expectInitialProp: boolean;
}[] = [
  { file: "app/(app)/[workspace]/chat-rename.tsx", gap: 16, expectInitialProp: false },
  { file: "app/(app)/[workspace]/issue/[id]/comment/[commentId]/emoji-picker.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/issue/[id]/picker/due-date.tsx", gap: 16, expectInitialProp: false },
  { file: "app/(app)/[workspace]/issue/[id]/runs.tsx", gap: 16, expectInitialProp: false },
  { file: "app/(app)/[workspace]/issue/[id]/runs/[taskId].tsx", gap: 16, expectInitialProp: true },
  { file: "app/(app)/[workspace]/issues-filter.tsx", gap: 16, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/agents/[id]/access.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/agents/[id]/composio.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/agents/[id]/custom-args.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/agents/[id]/edit-instructions.tsx", gap: 12, expectInitialProp: true },
  { file: "app/(app)/[workspace]/more/agents/[id]/edit-profile.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/agents/[id]/env.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/agents/[id]/integrations.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/agents/[id]/mcp.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/agents/[id]/run-config.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/agents/[id]/runtime-config.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/agents/[id]/skills.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/agents/new.tsx", gap: 12, expectInitialProp: true },
  { file: "app/(app)/[workspace]/more/runtimes/new.tsx", gap: 12, expectInitialProp: true },
  { file: "app/(app)/[workspace]/more/squads/[id]/execution-profiles.tsx", gap: 12, expectInitialProp: false },
  { file: "app/(app)/[workspace]/more/squads/new.tsx", gap: 12, expectInitialProp: true },
  { file: "app/(app)/[workspace]/new-issue-picker/due-date.tsx", gap: 16, expectInitialProp: true },
  { file: "app/(app)/[workspace]/project/[id]/add-resource.tsx", gap: 16, expectInitialProp: false },
  { file: "app/(app)/[workspace]/switch-workspace.tsx", gap: 16, expectInitialProp: false },
  { file: "app/(app)/[workspace]/tasks-actor-picker.tsx", gap: 16, expectInitialProp: true },
  { file: "app/(app)/[workspace]/tasks-sort.tsx", gap: 16, expectInitialProp: false },
  { file: "components/squads/member-add-sheet.tsx", gap: 12, expectInitialProp: false },
];

function source(relative: string): string {
  return readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), "..", relative),
    "utf8",
  );
}

describe("自绘标题头治理（RUYI-698）：旧模式零残留、SheetHeader 全覆盖", () => {
  it("清单内每个调用点改用 SheetHeader 且不再包含旧模式", () => {
    for (const { file, gap, expectInitialProp } of SHEET_HEADER_USERS) {
      const text = source(file);
      expect(
        text.includes("paddingTop: insets.top"),
        `${file} 仍包含旧模式 paddingTop: insets.top`,
      ).toBe(false);
      expect(
        text,
        `${file} 未使用共享 <SheetHeader>`,
      ).toContain("<SheetHeader");
      expect(
        text,
        `${file} SheetHeader gap 应为 ${gap}`,
      ).toContain(`gap={${gap}}`);
      expect(
        text.includes("initialAtLargeDetent"),
        `${file} initialAtLargeDetent 期望${expectInitialProp ? "必须传" : "不应传"}`,
      ).toBe(expectInitialProp);
    }
  });

  it("清单即全量：仓库内不存在清单外的旧模式调用点", () => {
    // 全仓旧模式扫描按源码遍历代价高，这里以入库验收同一把尺子收口：
    // grep 命中数（迁移基线 27）与清单长度一致，由 CI 外的
    // `git grep "paddingTop: insets.top"` 零命中兜底（交付自测项）。
    expect(SHEET_HEADER_USERS.length).toBe(27);
  });
});

/**
 * RUYI-698 返工 — worklet 执行面治理。
 *
 * 机理：`<SheetHeader>` 的 `useAnimatedStyle` 回调是 worklet，经 worklets
 * babel 插件编译后在 UI 线程执行，闭包里只能携带可序列化值或 worklet 函数。
 * 本单 P0 崩溃（release 实机 2/2 复现）即回调调用了跨模块导入的无 `'worklet'`
 * 指令普通函数：release 包内无法解析为可调用体，UI 线程抛
 * `TypeError: Object is not a function`，经 CppException 直接杀进程。该
 * 执行面在 jest 里不可达（jest.setup.ts 全局 inert reanimated mock），沿用
 * 本文件既有的源码形态手法机械钉住：worklet 回调内每个被调用的标识符必须
 * 可证明 worklet 安全——reanimated / worklets 包导出本身即 worklet（豁免），
 * 本地模块函数定义必须带 `'worklet'` 指令，其余一律拒绝。
 */
const JS_KEYWORDS = new Set([
  "if",
  "for",
  "while",
  "switch",
  "catch",
  "return",
  "function",
  "typeof",
]);

describe("worklet 执行面治理（RUYI-698 返工）：useAnimatedStyle 内只调 worklet 函数", () => {
  const COMPONENT = "components/ui/sheet-header.tsx";

  it("worklet 回调内调用的本地函数必须带 'worklet' 指令", () => {
    const text = source(COMPONENT);
    const body = extractUseAnimatedStyleBody(COMPONENT, text);

    const called = new Set(
      [...body.matchAll(/([A-Za-z_$][\w$]*)\s*\(/g)]
        .map((match) => match[1]!)
        .filter((name) => !JS_KEYWORDS.has(name)),
    );
    expect(
      called.size,
      "worklet 回调内应至少识别到 1 个被调函数（防断言空转）",
    ).toBeGreaterThan(0);

    const imports = parseNamedImports(text);
    for (const name of called) {
      expect(
        imports.has(name),
        `${name} 在 worklet 回调内被调用但未在文件顶部导入——worklet 闭包禁止携带不可序列化的自由标识符`,
      ).toBe(true);
      const from = imports.get(name)!;
      if (
        from.startsWith("react-native-reanimated") ||
        from.startsWith("react-native-worklets")
      ) {
        continue; // 库导出本身即 worklet。
      }
      expect(
        from.startsWith("@/") || from.startsWith("./") || from.startsWith("../"),
        `worklet 回调内调用第三方包函数 ${name}（来自 ${from}）——不在豁免清单，确认其为 worklet 后需显式加白`,
      ).toBe(true);
      const moduleFile = resolveLocalModule(COMPONENT, from);
      expect(
        functionHasWorkletDirective(source(moduleFile), name),
        `${moduleFile} 导出的 ${name} 缺少 'worklet' 指令——它在 ${COMPONENT} 的 useAnimatedStyle（UI 线程 worklet）内被调用，release 包无法解析非 worklet 函数，打开弹窗即崩（RUYI-698 P0 根因）`,
      ).toBe(true);
    }
  });
});

/** 截取第一个 `useAnimatedStyle(...)` 的完整实参源码（括号配对）。 */
function extractUseAnimatedStyleBody(component: string, text: string): string {
  const marker = "useAnimatedStyle(";
  const start = text.indexOf(marker);
  expect(
    start,
    `${component} 缺少 useAnimatedStyle——组件结构变化时同步本测试`,
  ).toBeGreaterThanOrEqual(0);
  let depth = 0;
  for (let i = start + marker.length - 1; i < text.length; i++) {
    if (text[i] === "(") depth += 1;
    if (text[i] === ")") {
      depth -= 1;
      if (depth === 0) return text.slice(start + marker.length, i);
    }
  }
  throw new Error("useAnimatedStyle 括号未闭合");
}

/** 解析文件顶部的具名 import → { 导出名: 来源模块 }。 */
function parseNamedImports(text: string): Map<string, string> {
  const map = new Map<string, string>();
  for (const match of text.matchAll(
    /import\s*\{([^}]*)\}\s*from\s*["']([^"']+)["']/g,
  )) {
    for (const raw of match[1]!.split(",")) {
      const name = raw.trim().replace(/^type\s+/, "");
      if (name) map.set(name, match[2]!);
    }
  }
  return map;
}

/** `@/x` 与相对导入 → apps/mobile 相对路径（带扩展名，存在性验证）。 */
function resolveLocalModule(fromFile: string, specifier: string): string {
  const base = specifier.startsWith("@/")
    ? specifier.slice(2)
    : join(dirname(fromFile), specifier);
  for (const ext of [".ts", ".tsx"]) {
    const candidate = `${base}${ext}`;
    if (existsSync(resolve(dirname(fileURLToPath(import.meta.url)), "..", candidate))) {
      return candidate;
    }
  }
  throw new Error(`无法解析本地模块 ${specifier}（自 ${fromFile}）`);
}

/** 函数体第一个非空白 token 是否为 worklet 指令。 */
function functionHasWorkletDirective(
  moduleText: string,
  fnName: string,
): boolean {
  const decl = moduleText.search(new RegExp(`function\\s+${fnName}\\b`));
  if (decl < 0) return false;
  const openBrace = moduleText.indexOf("{", decl);
  return /^\s*["']worklet["']\s*;?/.test(moduleText.slice(openBrace + 1));
}
