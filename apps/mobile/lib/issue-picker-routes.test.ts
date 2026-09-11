// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  ISSUE_PICKER_PATHNAMES,
  issuePickerHref,
  type IssuePickerField,
} from "./issue-picker-routes";

/**
 * 手机端 Issue 详情「更多」菜单快捷设置（RUYI-129）。
 *
 * 判定逻辑本身是纯函数（`issuePickerHref`），直接覆盖；「菜单确实接上了
 * 这些 picker」跑不进 node lane —— 详情页依赖 expo-router / RN 原生模块，
 * 故沿用 `comment-anchor-wiring.test.ts` 的源码接线断言，这是本仓能观察到
 * 接线断裂的唯一层。
 */
function source(relative: string): string {
  return readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), relative),
    "utf8",
  );
}

const detail = source("../app/(app)/[workspace]/issue/[id].tsx");
const attributeRow = source("../components/issue/attribute-row.tsx");
const layout = source("../app/(app)/[workspace]/_layout.tsx");

describe("issuePickerHref", () => {
  it("补全 workspace slug 与 issue id 后产出注册过的 pathname", () => {
    expect(issuePickerHref("status", "acme", "issue-1")).toEqual({
      pathname: "/[workspace]/issue/[id]/picker/status",
      params: { workspace: "acme", id: "issue-1" },
    });
  });

  it.each<IssuePickerField>([
    "status",
    "priority",
    "assignee",
    "label",
    "project",
    "due-date",
  ])("%s 的 pathname 在 _layout.tsx 里注册了 Stack.Screen", (field) => {
    // 路由名去掉前导 `/[workspace]/` 就是 Stack.Screen 的 name。
    const screenName = ISSUE_PICKER_PATHNAMES[field].replace(
      "/[workspace]/",
      "",
    );
    expect(layout).toContain(`name="${screenName}"`);
  });

  it("workspace slug 未就绪时返回 null，不推半成品路由", () => {
    expect(issuePickerHref("assignee", null, "issue-1")).toBeNull();
    expect(issuePickerHref("assignee", "", "issue-1")).toBeNull();
  });

  it("issue id 缺失时同样返回 null", () => {
    expect(issuePickerHref("project", "acme", null)).toBeNull();
    expect(issuePickerHref("project", "acme", undefined)).toBeNull();
  });
});

describe("更多菜单快捷设置接线", () => {
  const quickFields: IssuePickerField[] = [
    "assignee",
    "status",
    "project",
    "priority",
  ];

  it.each(quickFields)("菜单里有 %s 的快捷入口", (field) => {
    expect(detail).toContain(`openPicker("${field}")`);
  });

  it("菜单与 chip 行共用同一张路由表，不各自维护字符串", () => {
    expect(detail).toContain('from "@/lib/issue-picker-routes"');
    expect(attributeRow).toContain('from "@/lib/issue-picker-routes"');
    // chip 行里不得再留本地副本，否则两处会各自漂移。
    expect(attributeRow).not.toContain("const ISSUE_PICKER_PATHNAMES");
  });

  it("先关菜单再在交互结束后 push，避免 popover 卸载与 sheet 呈现打架", () => {
    // @rn-primitives 的 Item 在 onPress 里同步 onOpenChange(false)，
    // 与紧随其后的 formSheet 呈现同帧发生时，iOS 会吞掉这次呈现。
    expect(detail).toContain("InteractionManager.runAfterInteractions");
    const importAt = detail.indexOf("InteractionManager");
    expect(importAt).toBeGreaterThan(-1);
  });
});
