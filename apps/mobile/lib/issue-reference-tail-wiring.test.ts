// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * RUYI-731 — 引用尾块平铺接线的机械契约。
 *
 * RN 组件跑不进 node vitest lane（native 模块不可用），与
 * comment-anchor-wiring 同一手法：readFileSync 源码形态断言钉住接线本身。
 * 行为（平铺渲染、自引过滤、行导航、降级）已由
 * __tests__/issue-reference-list.test.tsx 真渲染测试覆盖，这里钉组件层
 * 结构与两端一致性：
 *
 *   1. 折叠全面退役：无 partitionReferencePreview 折叠算术、无 bottom
 *      sheet（Modal / PanResponder / 手势阈值）、无「N more」与分组文案。
 *   2. 行内行即可点行：唯一 <Pressable> 在行渲染器里，整块容器不再吞
 *      按压（旧两级形态的整块 Pressable 已退场）。
 *   3. 自引过滤接线：消费 current-issue context（默认 null，仅详情屏挂
 *      provider，与 web 的 CurrentIssueRenderContext 同语义），构建期
 *      跳过解析后 id 等于当前 Issue 的行。
 *   4. 导航通道不变：issue 行走 resolveLinkAction + router.push，PR 行
 *      走 RUYI-634 的 openExternalUrl 通道，失败兜底 Alert。
 *   5. violet 语义色两端统一（Owner 裁决 #7c3aed）——继续有效。
 */

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function source(relative: string): string {
  return readFileSync(resolve(ROOT, relative), "utf8");
}

function count(sourceText: string, pattern: RegExp): number {
  return [...sourceText.matchAll(new RegExp(pattern.source, pattern.flags))]
    .length;
}

describe("引用尾块平铺接线（RUYI-731）", () => {
  const component = source("components/issue/issue-reference-list.tsx");

  it("折叠退役：无折叠算术、无二级 sheet、无旧文案 key", () => {
    expect(component).not.toMatch(/partitionReferencePreview/);
    expect(component).not.toMatch(/<Modal\b/);
    expect(component).not.toMatch(/PanResponder/);
    expect(component).not.toMatch(/reference_tail_more/);
    expect(component).not.toMatch(/reference_group_/);
    expect(component).not.toMatch(/groupReferenceTailRows/);
    expect(component).not.toMatch(/shouldCloseSheetOnDragRelease/);
    expect(component).not.toMatch(/sheetOpen/);
  });

  it("行内行即可点行：唯一 Pressable 在行渲染器，整块不再吞按压", () => {
    expect(
      count(component, /<Pressable/g),
      "Pressable 总数（仅行渲染器一处）",
    ).toBe(1);
    // 行渲染器可点（onPress）且带行 testID（平铺行的查询句柄）。
    expect(component).toMatch(/<Pressable\b[\s\S]{0,400}?onPress=/);
    expect(component).toMatch(/testID="issue-reference-row"/);
    // 整块容器是纯 View：旧形态的按钮语义（accessibilityRole button +
    // 整块 onPress 开 sheet）一并退场。
    expect(component).not.toMatch(/accessibilityRole="button"/);
    expect(component).toMatch(/testID="issue-reference-block"/);
  });

  it("自引过滤接线：消费 current-issue context，构建期跳过自引行", () => {
    expect(component).toMatch(
      /import \{ useCurrentIssueId \} from "@\/lib\/current-issue-context"/,
    );
    expect(component).toMatch(/useCurrentIssueId\(\)/);
    // 过滤算术在 lib 策略层（node 单测覆盖），组件只把 context 传进去。
    expect(component).toMatch(
      /buildReferenceTailRows\(references, results, currentIssueId\)/,
    );
  });

  it("导航通道各就位：issue 行应用内路由，PR 行外链通道", () => {
    // issue 行走应用内路由（与 prose 链接同一 resolveLinkAction 决策）。
    expect(component).toMatch(
      /resolveLinkAction\(\s*`mention:\/\/issue\/\$\{row\.issue\.id\}`/,
    );
    // PR 行走 RUYI-634 的 canonical URL 通道，失败兜底 Alert。
    expect(component).toMatch(/openExternalUrl\(row\.pr\.html_url, Linking\)/);
    expect(component).toMatch(/pull_request_open_failed/);
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
