// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * RUYI-635 增量 — 引用尾部两级交互的机械契约。
 *
 * RN 组件跑不进 node vitest lane（native 模块不可用），与
 * comment-anchor-wiring / form-sheet-scroll-governance 同一手法：
 * readFileSync 源码形态断言钉住接线本身。行为算术（zip、跨形态合并、
 * 分组、折叠）已由 lib/issue-reference-rows.test.ts 真单测覆盖，这里只
 * 钉组件层的防误触结构与两端一致性：
 *
 *   1. 第一级整块只有一个 Pressable——行内 preview 行是纯 View/Text，
 *      任何按压缩开的都是 Bottom Sheet 而非页面跳转（防误触核心）。
 *   2. 折叠算术复用 core 的 partitionReferencePreview（与 web 同一刀口）。
 *   3. sheet 内 issue 行走 resolveLinkAction + router.push、PR 行走
 *      RUYI-634 的 openExternalUrl 通道；ScrollView 开
 *      nestedScrollEnabled、Modal 带 onRequestClose（RUYI-637 配方）。
 *   4. violet 语义色两端统一（Owner 裁决 #7c3aed）：theme 双色板带
 *      violet 槽位，pull-request-display 映射 merged→violet。
 */

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function source(relative: string): string {
  return readFileSync(resolve(ROOT, relative), "utf8");
}

function count(sourceText: string, pattern: RegExp): number {
  return [...sourceText.matchAll(new RegExp(pattern.source, pattern.flags))]
    .length;
}

describe("引用尾部两级交互接线（RUYI-635 增量）", () => {
  const component = source("components/issue/issue-reference-list.tsx");

  it("第一级整块单命中：preview 行内无 onPress、无嵌套 Pressable", () => {
    // previews.map 段：行是 View/Text（.numberOfLines 2 对齐 web clamp-2），
    // 出现 onPress 或 Pressable 即行内命中面回潮。
    const previewsBlock = component.match(
      /\{previews\.map\((?:\([^)]*\)|row) => \{[\s\S]*?\}\)\}/,
    );
    expect(previewsBlock, "previews.map 段存在").not.toBeNull();
    expect(previewsBlock![0]).not.toMatch(/onPress/);
    expect(previewsBlock![0]).not.toMatch(/<Pressable/);
    expect(previewsBlock![0]).toMatch(/numberOfLines=\{2\}/);

    // 整块 Pressable 是第一级唯一命中面；加上 sheet 的行/关闭/遮罩共 4 处。
    expect(component).toMatch(/testID="issue-reference-block"/);
    expect(
      count(component, /<Pressable/g),
      "Pressable 总数（整块+遮罩+关闭+sheet 行）",
    ).toBe(4);
  });

  it("折叠与预览刀口复用 core 的 partitionReferencePreview（与 web 同算术）", () => {
    expect(component).toMatch(
      /import \{[^}]*partitionReferencePreview[^}]*\} from "@multica\/core\/markdown"/,
    );
    expect(component).toMatch(/partitionReferencePreview\(rows/);
  });

  it("第二级 sheet：分组头、行 testID、导航与外链通道各就位", () => {
    expect(component).toMatch(/testID\s*=\s*"reference-sheet-issue-row"/);
    expect(component).toMatch(/testID\s*=\s*"reference-sheet-pr-row"/);
    // issue 行走应用内路由（与 prose 链接同一 resolveLinkAction 决策）。
    expect(component).toMatch(
      /resolveLinkAction\(\s*`mention:\/\/issue\/\$\{row\.issue\.id\}`/,
    );
    // PR 行走 RUYI-634 的 canonical URL 通道，失败兜底 Alert。
    expect(component).toMatch(/openExternalUrl\(row\.pr\.html_url, Linking\)/);
    expect(component).toMatch(/pull_request_open_failed/);
  });

  it("sheet 滚动治理：nestedScrollEnabled + onRequestClose（RUYI-637 配方）", () => {
    expect(component).toMatch(/<ScrollView nestedScrollEnabled>/);
    expect(component).toMatch(/onRequestClose=\{\(\) => setSheetOpen\(false\)\}/);
  });

  it("violet 语义色两端统一：双色板带槽位，merged 映射 violet", () => {
    const theme = source("lib/theme.ts");
    // light 与 dark 各一处 violet 定义（Owner 裁决 #7c3aed 的移动端落点）。
    expect(count(theme, /^\s{4}violet: "/gm)).toBe(2);

    const display = source("lib/pull-request-display.ts");
    expect(display).toMatch(/violet: "violet"/);
    expect(display).toMatch(/derivePullRequestStateVisual/);
  });
});
