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
// language 固定为 zh-Hans：本文件的日期断言要能区分「跟界面语言」与
// 「硬编码 en-US」两种行为，locale 不定就分不开。
vi.mock("i18next", () => ({ default: { t: mockT, language: "zh-Hans" } }));

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

/** 模拟 i18next 的插值：把 `{{name}}` 换成实参。 */
function interpolate(text: string, opts?: Record<string, unknown>): string {
  if (!opts) return text;
  return text.replace(/\{\{(\w+)\}\}/g, (m, k: string) =>
    k in opts ? String(opts[k]) : m,
  );
}

beforeEach(() => {
  mockT.mockReset();
  // 默认实现返回 `译:key` 加插值后的实参：`译:` 前缀证明这条真的经过了
  // i18n（而不是返回英文字面量），实参部分证明动态值是作为参数传的、
  // 会被资源侧的占位符接住。
  mockT.mockImplementation(
    (key: string, _fb?: string, opts?: Record<string, unknown>) =>
      opts
        ? `译:${key}(${Object.entries(opts)
            .map(([k, v]) => `${k}=${String(v)}`)
            .join(",")})`
        : `译:${key}`,
  );
});

describe("formatActivity 的 status/priority 标签", () => {
  it("status_changed 的两端都经过 issues:status.* 而非本地英文表", () => {
    const out = formatActivity(
      entry("status_changed", { from: "todo", to: "in_progress" }),
      noName,
    );
    expect(out).toBe(
      "译:issues:activity.status_changed(from=译:issues:status.todo,to=译:issues:status.in_progress)",
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
      "译:issues:activity.priority_changed(from=译:issues:priority.none,to=译:issues:priority.urgent)",
    );
    expect(mockT).toHaveBeenCalledWith("issues:priority.none", "No priority");
    expect(mockT).toHaveBeenCalledWith("issues:priority.urgent", "Urgent");
  });

  it("未知 status 原样透出，且不拼进 i18n key", () => {
    const out = formatActivity(
      entry("status_changed", { from: "todo", to: "some_new_status" }),
      noName,
    );
    expect(out).toBe(
      "译:issues:activity.status_changed(from=译:issues:status.todo,to=some_new_status)",
    );
    // 未知值只能作为插值实参出现，绝不能被拼进 key —— 拼进去会解析落空、
    // 渲染成空白（API Response Compatibility 规则）。
    expect(mockT.mock.calls.map((c) => c[0])).not.toContain(
      "issues:status.some_new_status",
    );
  });

  it("未知 priority 原样透出，且不拼进 i18n key", () => {
    const out = formatActivity(
      entry("priority_changed", { from: "p9", to: "low" }),
      noName,
    );
    expect(out).toBe(
      "译:issues:activity.priority_changed(from=p9,to=译:issues:priority.low)",
    );
    expect(mockT.mock.calls.map((c) => c[0])).not.toContain(
      "issues:priority.p9",
    );
  });

  it('缺失的一端退化为 "?"，且不调 status/priority 的标签 key', () => {
    const out = formatActivity(entry("status_changed", {}), noName);
    expect(out).toBe("译:issues:activity.status_changed(from=?,to=?)");
    expect(mockT.mock.calls.map((c) => c[0])).toEqual([
      "issues:activity.status_changed",
    ]);
  });

  it("i18n 缺失时退回英文兜底，与 en 资源逐字一致", () => {
    mockT.mockImplementation(
      (_k: string, fb: string, opts?: Record<string, unknown>) =>
        interpolate(fb, opts),
    );
    expect(
      formatActivity(
        entry("status_changed", { from: "in_review", to: "done" }),
        noName,
      ),
    ).toBe("changed status from In Review to Done");
    expect(
      formatActivity(
        entry("priority_changed", { from: "medium", to: "high" }),
        noName,
      ),
    ).toBe("changed priority from Medium to High");
  });
});

/**
 * 活动流文案本体的 i18n 接线（批次 11 任务 1）。
 *
 * 改动前本文件的每个 case 都 `return` 英文字面量，是 issue 详情页活动流
 * 整块保持英文的直接原因。文件头注释 "mobile v1 is English-only" 是过期
 * 声明——mobile 已经有 i18n，那句话本身也一并改掉了。
 *
 * 断言的是「每个分支都真的经过 issues:activity.*」，key 存在性由
 * lib/i18n-keys.test.ts 打在真实资源上（本文件 mock 掉了 i18next）。
 */
describe("formatActivity 的活动文案接线", () => {
  // 每个分支一条：入参 → 期望 key。`译:` 前缀来自 beforeEach 的 mock，
  // 出现它就证明这一条真的经过了 i18n 而不是返回英文字面量。
  const cases: [string, string, Record<string, string>, string][] = [
    ["created", "created", {}, "issues:activity.created"],
    [
      "self_assigned",
      "assignee_changed",
      { to_type: "member", to_id: "u1" },
      "issues:activity.self_assigned",
    ],
    [
      "removed_assignee",
      "assignee_changed",
      { from_id: "u9" },
      "issues:activity.removed_assignee",
    ],
    [
      "changed_assignee",
      "assignee_changed",
      { from_id: "u9", to_id: "u2" },
      "issues:activity.changed_assignee",
    ],
    [
      "start_date_removed",
      "start_date_changed",
      {},
      "issues:activity.start_date_removed",
    ],
    [
      "start_date_set",
      "start_date_changed",
      { to: "2026-03-05" },
      "issues:activity.start_date_set",
    ],
    [
      "due_date_removed",
      "due_date_changed",
      {},
      "issues:activity.due_date_removed",
    ],
    [
      "due_date_set",
      "due_date_changed",
      { to: "2026-03-05" },
      "issues:activity.due_date_set",
    ],
    [
      "title_renamed",
      "title_changed",
      { from: "a", to: "b" },
      "issues:activity.title_renamed",
    ],
    [
      "description_updated",
      "description_updated",
      {},
      "issues:activity.description_updated",
    ],
    [
      "squad_leader_evaluated",
      "squad_leader_evaluated",
      {},
      "issues:activity.squad_leader_evaluated",
    ],
    [
      "squad_leader_action",
      "squad_leader_evaluated",
      { outcome: "action" },
      "issues:activity.squad_leader_action",
    ],
    [
      "squad_leader_action_reason",
      "squad_leader_evaluated",
      { outcome: "action", reason: "why" },
      "issues:activity.squad_leader_action_reason",
    ],
    [
      "squad_leader_no_action",
      "squad_leader_evaluated",
      { outcome: "no_action" },
      "issues:activity.squad_leader_no_action",
    ],
    [
      "squad_leader_no_action_reason",
      "squad_leader_evaluated",
      { outcome: "no_action", reason: "why" },
      "issues:activity.squad_leader_no_action_reason",
    ],
    [
      "squad_leader_failed",
      "squad_leader_evaluated",
      { outcome: "failed" },
      "issues:activity.squad_leader_failed",
    ],
    [
      "squad_leader_failed_reason",
      "squad_leader_evaluated",
      { outcome: "failed", reason: "why" },
      "issues:activity.squad_leader_failed_reason",
    ],
  ];

  it.each(cases)("%s 分支经过 i18n", (_name, action, details, key) => {
    const out = formatActivity(entry(action, details), noName);
    expect(out).toContain("译:");
    expect(mockT.mock.calls.map((c) => c[0])).toContain(key);
  });

  it("assigned_to 走带 {{name}} 插值的 key，名字作为参数而非拼接", () => {
    formatActivity(
      entry("assignee_changed", { from_id: "u9", to_type: "member", to_id: "u2" }),
      () => "张三",
    );
    expect(mockT).toHaveBeenCalledWith(
      "issues:activity.assigned_to",
      "assigned to {{name}}",
      { name: "张三" },
    );
  });

  it("status/priority 文案本体也走 i18n，不再手拼 `changed status:`", () => {
    formatActivity(
      entry("status_changed", { from: "todo", to: "done" }),
      noName,
    );
    expect(mockT.mock.calls.map((c) => c[0])).toContain(
      "issues:activity.status_changed",
    );
  });

  it("task_completed / task_failed 交给 i18next 的 count 复数，不手写三元", () => {
    // 中日韩没有语法数，`completed 2 tasks` / `completed a task` 这种
    // 英语单复数分叉翻不出来——交给 count 规则由资源侧决定。
    mockT.mockClear();
    formatActivity(
      { action: "task_completed", details: {}, coalesced_count: 3 } as never,
      noName,
    );
    expect(mockT).toHaveBeenCalledWith(
      "issues:activity.task_completed",
      expect.any(String),
      { count: 3 },
    );
    mockT.mockClear();
    formatActivity(
      { action: "task_failed", details: {}, coalesced_count: 1 } as never,
      noName,
    );
    expect(mockT).toHaveBeenCalledWith(
      "issues:activity.task_failed",
      expect.any(String),
      { count: 1 },
    );
  });

  it("fallback 按「N 个不同 task」写，不含「N 次」式的重复计数措辞", () => {
    // 回归锁：这两条曾写作「completed the task ({{count}} times)」，语义与
    // 数据相反——coalesced_count 数的是 N 个不同 task（CompleteAgentTask 的
    // `WHERE ... AND status = 'running'` 幂等守卫使同一行至多产生一条完成
    // 活动）。锁住 fallback 措辞，挡住改回「N 次」式表述的回归。
    for (const action of ["task_completed", "task_failed"]) {
      mockT.mockClear();
      formatActivity(
        { action, details: {}, coalesced_count: 2 } as never,
        noName,
      );
      const fallback = mockT.mock.calls[0]?.[1] as string;
      expect(fallback).toContain("{{count}} task");
      expect(fallback).not.toMatch(/times|the task\b/);
    }
  });

  it("未知 action 仍原样透出，不进 i18n —— API Response Compatibility 规则", () => {
    mockT.mockClear();
    expect(
      formatActivity(entry("some_future_action", {}), noName),
    ).toBe("some_future_action");
    expect(mockT).not.toHaveBeenCalled();
  });

  it("日期走 displayLocale()，不再硬编码 en-US", async () => {
    const { formatDateOnly } = await import("@multica/core/issues/date");
    mockT.mockImplementation((_k: string, _fb: string, opts?: object) =>
      JSON.stringify(opts),
    );
    const out = formatActivity(
      entry("due_date_changed", { to: "2026-03-05" }),
      noName,
    );
    // mockI18nLanguage 在文件顶部固定为 zh-Hans，所以期望的是中文月份。
    expect(out).toContain(
      formatDateOnly(
        "2026-03-05",
        { month: "short", day: "numeric" },
        "zh-Hans",
      ),
    );
  });
});
