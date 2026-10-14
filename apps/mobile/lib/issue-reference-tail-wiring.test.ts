// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * RUYI-731（Owner 纠正 2026-10-12）— 引用尾块 mobile 形态接线的机械契约。
 *
 * RN 组件跑不进 node vitest lane（native 模块不可用），与
 * comment-anchor-wiring 同一手法：readFileSync 源码形态断言钉住接线本身。
 * 行为（折叠/抽屉、自引过滤、行导航、降级）已由
 * __tests__/issue-reference-list.test.tsx 真渲染测试覆盖，这里钉组件层
 * 结构与两端一致性：
 *
 *   1. 两级形态在位（「不要二级弹层」仅是 web 端指令）：整块单按压目标
 *      开底部抽屉、preview 折叠算术在位、手势关闭在位——原「平铺退役」
 *      负向护栏按新预期反转。
 *   2. 折叠算术落位 mobile 策略层：partitionReferencePreview 不回
 *      @multica/core（web 已平铺、跨端同折叠理由失效），随行构建策略
 *      留在 node 可测的 lib；组件只消费。
 *   3. 自引过滤接线：消费 current-issue context（默认 null，仅详情屏挂
 *      provider，与 web 的 CurrentIssueRenderContext 同语义），构建期
 *      跳过解析后 id 等于当前 Issue 的行，预览与抽屉同样可见。
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

describe("引用尾块 mobile 两级接线（RUYI-731 恢复）", () => {
  const component = source("components/issue/issue-reference-list.tsx");

  it("两级形态在位：折叠算术、二级 sheet、手势关闭、旧文案 key 回归", () => {
    expect(component).toMatch(/partitionReferencePreview/);
    expect(component).toMatch(/<Modal\b/);
    expect(component).toMatch(/PanResponder/);
    expect(component).toMatch(/reference_tail_more/);
    expect(component).toMatch(/reference_group_/);
    expect(component).toMatch(/groupReferenceTailRows/);
    expect(component).toMatch(/shouldCloseSheetOnDragRelease/);
    expect(component).toMatch(/sheetOpen/);
  });

  it("整块单按压目标开抽屉：块级 Pressable + sheet 行 + 掩膜 + 关闭钮", () => {
    // 4 个按压目标：整块（Board 1）、sheet 行渲染器（Board 2）、抽屉关闭
    // 钮、抽屉掩膜。预览行不是按压目标（anti-mistouch）。
    expect(
      count(component, /<Pressable/g),
      "Pressable 总数（块 + sheet 行 + 关闭 + 掩膜）",
    ).toBe(4);
    expect(component).toMatch(/onPress=\{\(\) => setSheetOpen\(true\)\}/);
    expect(component).toMatch(/accessibilityRole="button"/);
    expect(component).toMatch(/testID="issue-reference-block"/);
    // sheet 行 testID 经 renderActionableRow 的 prop 变量传入，断言赋值形态。
    expect(component).toMatch(/"reference-sheet-issue-row"/);
    expect(component).toMatch(/"reference-sheet-pr-row"/);
  });

  it("折叠算术落位 mobile 策略层，构建期先过滤后折叠", () => {
    const rows = source("lib/issue-reference-rows.ts");
    expect(rows).toMatch(/export function partitionReferencePreview/);
    expect(rows).toMatch(
      /buildReferenceTailRows\(\s*references: IssueReference\[\],\s*resolutions: ReferenceTailResolution\[\],\s*currentIssueId: string \| null/,
    );
    // 组件按新签名消费（无 kindOf 回调，fold 吃 post-filter rows）。
    expect(component).toMatch(/partitionReferencePreview\(rows\)/);
    expect(component).toMatch(
      /buildReferenceTailRows\(references, results, currentIssueId\)/,
    );
  });

  it("自引过滤接线：消费 current-issue context，预览与抽屉同源过滤", () => {
    expect(component).toMatch(
      /import \{ useCurrentIssueId \} from "@\/lib\/current-issue-context"/,
    );
    expect(component).toMatch(/useCurrentIssueId\(\)/);
  });

  it("导航通道各就位：issue 行应用内路由，PR 行外链通道", () => {
    // issue 行走应用内路由（与 prose 链接同一 resolveLinkAction 决策），
    // 导航同时收起抽屉。
    expect(component).toMatch(
      /resolveLinkAction\(\s*`mention:\/\/issue\/\$\{row\.issue\.id\}`/,
    );
    expect(component).toMatch(/setSheetOpen\(false\);\s*const action = resolveLinkAction/);
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
