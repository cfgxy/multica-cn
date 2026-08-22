import { describe, expect, it, vi, beforeEach } from "vitest";
import type { TimelineEntry } from "@multica/core/types";

/**
 * lib/format-activity.ts 的 status/priority 标签接线单测（P1 批次 8）。
 *
 * 本批次把这里原先各自持有的 STATUS_LABEL / PRIORITY_LABEL 副本换成了
 * lib/issue-status.ts 的 statusLabel() / priorityLabel()。两条不变量：
 *  1. 已知枚举值必须走 i18n（拿到译文，不是英文兜底）；
 *  2. 未知枚举值仍原样透出——顶部注释的 API Response Compatibility 规则：
 *     服务端可能新增 action/status 值，装机版本必须能渲染而不是崩或吞行。
 *
 * 只 mock i18next：断言的是「有没有真的经过 i18n」，真实资源的 key 解析由
 * lib/i18n-keys.test.ts 覆盖。
 */

const { mockT } = vi.hoisted(() => ({ mockT: vi.fn() }));
vi.mock("i18next", () => ({ default: { t: mockT } }));

import { formatActivity } from "./format-activity";

const noName = () => "someone";

function entry(action: string, details: Record<string, string>): TimelineEntry {
  return {
    action,
    details,
    actor_type: "member",
    actor_id: "u1",
  } as unknown as TimelineEntry;
}

beforeEach(() => {
  mockT.mockReset();
  mockT.mockImplementation((key: string) => `译:${key}`);
});

describe("formatActivity 的 status/priority 标签", () => {
  it("status_changed 的两端都经过 issues:status.* 而非本地英文表", () => {
    const out = formatActivity(
      entry("status_changed", { from: "todo", to: "in_progress" }),
      noName,
    );
    expect(out).toBe(
      "changed status: 译:issues:status.todo → 译:issues:status.in_progress",
    );
    expect(mockT).toHaveBeenCalledWith("issues:status.todo", "Todo");
    expect(mockT).toHaveBeenCalledWith(
      "issues:status.in_progress",
      "In Progress",
    );
  });

  it("priority_changed 的两端都经过 issues:priority.*", () => {
    const out = formatActivity(
      entry("priority_changed", { from: "none", to: "urgent" }),
      noName,
    );
    expect(out).toBe(
      "changed priority: 译:issues:priority.none → 译:issues:priority.urgent",
    );
    expect(mockT).toHaveBeenCalledWith("issues:priority.none", "No priority");
    expect(mockT).toHaveBeenCalledWith("issues:priority.urgent", "Urgent");
  });

  it("未知 status 原样透出，且不拼进 i18n key", () => {
    const out = formatActivity(
      entry("status_changed", { from: "todo", to: "some_new_status" }),
      noName,
    );
    expect(out).toBe("changed status: 译:issues:status.todo → some_new_status");
    expect(JSON.stringify(mockT.mock.calls)).not.toContain("some_new_status");
  });

  it("未知 priority 原样透出，且不拼进 i18n key", () => {
    const out = formatActivity(
      entry("priority_changed", { from: "p9", to: "low" }),
      noName,
    );
    expect(out).toBe("changed priority: p9 → 译:issues:priority.low");
    expect(JSON.stringify(mockT.mock.calls)).not.toContain("p9");
  });

  it('缺失的一端退化为 "?"，不调 i18n', () => {
    const out = formatActivity(entry("status_changed", {}), noName);
    expect(out).toBe("changed status: ? → ?");
    expect(mockT).not.toHaveBeenCalled();
  });

  it("i18n 缺失时退回英文兜底，与改动前的输出逐字一致", () => {
    mockT.mockImplementation((_k: string, fb: string) => fb);
    expect(
      formatActivity(
        entry("status_changed", { from: "in_review", to: "done" }),
        noName,
      ),
    ).toBe("changed status: In Review → Done");
    expect(
      formatActivity(
        entry("priority_changed", { from: "medium", to: "high" }),
        noName,
      ),
    ).toBe("changed priority: Medium → High");
  });
});
