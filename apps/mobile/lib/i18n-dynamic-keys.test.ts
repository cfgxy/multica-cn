// @vitest-environment node
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { RESOURCES } from "@multica/views/locales";
import type {
  AgentTask,
  IssueStatus,
  IssuePriority,
} from "@multica/core/types";

/**
 * 动态拼接 key 的资源解析测试（P1 批次 8）。
 *
 * 为什么单独一支：lib/i18n-keys.test.ts 的采集器只审「字面量」key，明确
 * 拒绝模板串与动态 key。本批次接的五张表全部是 `t(\`前缀.${枚举值}\`)`
 * 形态——被那支防线整体漏过。漏过的后果不是假红而是假绿：key 拼错一个
 * 字符，i18next 静默退回英文 fallback，四项闸门全绿而中文永远出不来。
 * 这里把枚举集合与资源做双向对账，补上那个缺口。
 *
 * 断言分三层：
 *  1. 每个枚举值在 zh-Hans 都解析得到非空字符串（key 拼对了）；
 *  2. 解析结果 != en 的同 key 值（真的翻译过，不是把英文抄进 zh 充数）；
 *  3. 四语齐备（en/zh-Hans/ja/ko 都有，不缺一门）。
 *
 * 第 2 层有意为之的豁免：极少数条目中英同形（如 `run_summary.direct` 的
 * "task"），逐条列名豁免而不是整体放宽——放宽等于把这层断言废掉。
 */

const LOCALES = ["en", "zh-Hans", "ja", "ko"] as const;
type Bundle = Record<string, Record<string, unknown>>;

function lookup(locale: string, ns: string, dotted: string): string | null {
  let cur: unknown = (RESOURCES as unknown as Record<string, Bundle>)[locale]?.[
    ns
  ];
  for (const seg of dotted.split(".")) {
    if (cur == null || typeof cur !== "object") return null;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return typeof cur === "string" ? cur : null;
}

/** 中英同形、不要求 zh != en 的条目（ns:key 全路径）。 */
const SAME_AS_EN_OK = new Set<string>([
  // 「task」在本产品的中文语境里保留英文原词（见 zh-Hans 各处 "task 已完成"）。
  "issues:mobile.run_summary.direct",
]);

/**
 * 一组动态 key 的通用对账。
 * @param label   报错时的可读名
 * @param ns      namespace
 * @param prefix  key 前缀（不含尾点）
 * @param枚举值   源码里会拼进 key 的全部取值
 */
function checkTable(
  label: string,
  ns: string,
  prefix: string,
  values: string[],
) {
  describe(label, () => {
    it("枚举值非空（防止表被清空后本组断言退化成空跑）", () => {
      expect(values.length).toBeGreaterThan(0);
    });

    it("每个枚举值都在 zh-Hans 解析到非空字符串", () => {
      const misses = values.filter((v) => {
        const s = lookup("zh-Hans", ns, `${prefix}.${v}`);
        return s == null || s.trim() === "";
      });
      expect(misses).toEqual([]);
    });

    it("zh-Hans 的值确实是译文，而不是照抄 en", () => {
      const untranslated = values.filter((v) => {
        const full = `${ns}:${prefix}.${v}`;
        if (SAME_AS_EN_OK.has(full)) return false;
        const zh = lookup("zh-Hans", ns, `${prefix}.${v}`);
        const en = lookup("en", ns, `${prefix}.${v}`);
        return zh != null && en != null && zh === en;
      });
      expect(untranslated).toEqual([]);
    });

    it("四语齐备", () => {
      const gaps: string[] = [];
      for (const loc of LOCALES) {
        for (const v of values) {
          const s = lookup(loc, ns, `${prefix}.${v}`);
          if (s == null || s.trim() === "")
            gaps.push(`${loc} ${ns}:${prefix}.${v}`);
        }
      }
      expect(gaps).toEqual([]);
    });
  });
}

// ─── 各表的枚举集合 ────────────────────────────────────────────────
// 这些数组是源码里那几张 Record 的 key 集合的副本。副本会漂移，所以下面
// 每组都配一条「与源码表同步」的对账断言，从真实模块里读回来比对。

const FAILURE_REASONS = [
  "queued_expired",
  "runtime_offline",
  "runtime_recovery",
  "timeout",
  "iteration_limit",
  "agent_blocked",
  "api_invalid_request",
  "skill_bundle_unavailable",
  "runtime_cli_timeout",
  "agent_error__provider_auth_or_access",
  "agent_error__provider_quota_limit",
  "agent_error__provider_capacity_or_rate_limit",
  "agent_error__provider_server_error",
  "agent_error__provider_network",
  "agent_error__process_failure",
  "agent_error__empty_or_unparseable_output",
  "agent_error__agent_timeout",
  "agent_error__context_overflow",
  "agent_error__missing_config",
  "agent_error__model_not_found_or_unavailable",
  "agent_error__runtime_version_unsupported",
  "agent_error__runtime_missing_executable",
  "agent_error__unknown",
  "agent_error",
  "codex_semantic_inactivity",
  "manual",
];

const TASK_STATUSES: AgentTask["status"][] = [
  "queued",
  "dispatched",
  "waiting_local_directory",
  "running",
  "completed",
  "failed",
  "cancelled",
];

const TASK_KINDS = ["comment", "autopilot", "chat", "quick_create", "direct"];

const ISSUE_STATUSES: IssueStatus[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
  "cancelled",
];

const ISSUE_PRIORITIES: IssuePriority[] = [
  "urgent",
  "high",
  "medium",
  "low",
  "none",
];

const INBOX_TYPES = [
  "issue_assigned",
  "issue_subscribed",
  "unassigned",
  "assignee_changed",
  "status_changed",
  "priority_changed",
  "start_date_changed",
  "due_date_changed",
  "new_comment",
  "mentioned",
  "review_requested",
  "task_completed",
  "task_failed",
  "agent_blocked",
  "agent_completed",
  "reaction_added",
  "quick_create_done",
  "quick_create_failed",
  "quick_create_unconfirmed",
];

const PILL_STAGES = [
  "offline",
  "reconnecting",
  "retrying",
  "queued",
  "starting_up",
  "thinking",
  "typing",
];

const PILL_TOOLS = [
  "running_command",
  "reading_files",
  "searching_code",
  "making_edits",
  "searching_web",
  "fallback",
];

// lib/failure-reason-label.ts 的长文案表（聊天气泡）
checkTable(
  "chat:mobile.failure_reason.*（长版，聊天气泡）",
  "chat",
  "mobile.failure_reason",
  [...FAILURE_REASONS, "fallback"],
);

// run-row.tsx 的短文案表（badge，与状态词共享一行）
checkTable(
  "issues:mobile.run_failure.*（短版，run-row badge）",
  "issues",
  "mobile.run_failure",
  FAILURE_REASONS,
);

checkTable(
  "issues:mobile.run_status.*",
  "issues",
  "mobile.run_status",
  TASK_STATUSES as string[],
);

checkTable(
  "issues:mobile.run_summary.*",
  "issues",
  "mobile.run_summary",
  TASK_KINDS,
);

checkTable(
  "issues:status.*（format-activity / detail-label 共用）",
  "issues",
  "status",
  ISSUE_STATUSES as string[],
);

checkTable(
  "issues:priority.*（format-activity / detail-label 共用）",
  "issues",
  "priority",
  ISSUE_PRIORITIES as string[],
);

checkTable("inbox:types.*", "inbox", "types", INBOX_TYPES);

checkTable(
  "chat:status_pill.stages.*",
  "chat",
  "status_pill.stages",
  PILL_STAGES,
);

checkTable("chat:status_pill.tools.*", "chat", "status_pill.tools", PILL_TOOLS);

describe("两份 failure 表的关系", () => {
  it("key 集合完全一致（长版多一个 fallback）", () => {
    const long = Object.keys(
      (
        (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
          .chat as Record<string, Record<string, unknown>>
      ).mobile.failure_reason as Record<string, string>,
    )
      .filter((k) => k !== "fallback")
      .sort();
    const short = Object.keys(
      (
        (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
          .issues as Record<string, Record<string, unknown>>
      ).mobile.run_failure as Record<string, string>,
    ).sort();
    expect(short).toEqual(long);
  });

  it("同 key 的两版中文文案不同 —— badge 那版必须另写更短的", () => {
    // 合并成一套是本批次明确否决的做法：badge 与状态词、时间戳共享一行，
    // 中文同样需要更短的呈现。若两版逐字相同，说明有人偷懒复制了长版。
    const chat = (
      (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
        .chat as Record<string, Record<string, unknown>>
    ).mobile.failure_reason as Record<string, string>;
    const issues = (
      (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
        .issues as Record<string, Record<string, unknown>>
    ).mobile.run_failure as Record<string, string>;
    const identical = FAILURE_REASONS.filter((k) => chat[k] === issues[k]);
    expect(identical).toEqual([]);
  });

  it("badge 版中文两两不同 —— 缩写撞车会让两种失败原因在徽标上无法区分", () => {
    // 实际踩到过：agent_timeout 缩成「超时」正好与 timeout 那条同字，
    // 用户在 badge 上完全分不出是调度侧超时还是 agent 自身超时。
    const issues = (
      (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
        .issues as Record<string, Record<string, unknown>>
    ).mobile.run_failure as Record<string, string>;
    // agent_error 与 agent_error.unknown 是同一语义的新旧两个 wire 值，
    // 文案相同是正确的（源码 LABELS 表里也一样），成对放行。
    const ALIASES = [new Set(["agent_error", "agent_error__unknown"])];
    const isAlias = (a: string, b: string) =>
      ALIASES.some((s) => s.has(a) && s.has(b));

    const seen = new Map<string, string>();
    const collisions: string[] = [];
    for (const k of FAILURE_REASONS) {
      const v = issues[k]!;
      const prev = seen.get(v);
      if (prev && !isAlias(prev, k))
        collisions.push(`${prev} / ${k} 同为 "${v}"`);
      seen.set(v, k);
    }
    expect(collisions).toEqual([]);
  });

  it("badge 版的中文普遍不长于气泡版", () => {
    const chat = (
      (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
        .chat as Record<string, Record<string, unknown>>
    ).mobile.failure_reason as Record<string, string>;
    const issues = (
      (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
        .issues as Record<string, Record<string, unknown>>
    ).mobile.run_failure as Record<string, string>;
    const longer = FAILURE_REASONS.filter(
      (k) => (issues[k]?.length ?? 0) > (chat[k]?.length ?? 0),
    );
    expect(longer).toEqual([]);
  });
});

describe("枚举副本与源码表同步", () => {
  it("failure reason 集合覆盖 lib/failure-reason-label.ts 的全部 wire 值", () => {
    // 从真实源码读回 LABELS 的 key —— 源码加了新 reason 而这里没跟，
    // 本文件的所有断言都会对新增项失效（假绿）。这条把它挡住。
    const src = readFileSync(
      join(__dirname, "failure-reason-label.ts"),
      "utf8",
    );
    const body = src.slice(
      src.indexOf("const LABELS"),
      src.indexOf("export function"),
    );
    const wire = [...body.matchAll(/^\s*"?([a-z_][a-z_.]*)"?:\s*"/gm)].map(
      (m) => m[1]!,
    );
    expect(wire.length).toBeGreaterThan(0);
    const normalized = wire.map((w) => w.replace(/\./g, "__")).sort();
    expect(normalized).toEqual([...FAILURE_REASONS].sort());
  });
});
