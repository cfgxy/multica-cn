/**
 * Activity-row text formatter. Subset of the web `formatActivity` in
 * packages/views/issues/components/issue-detail.tsx:95 — same actions, and
 * now the same `issues:activity.*` resource keys, so a given entry reads
 * identically on web and mobile in all four locales.
 *
 * Unknown actions fall through to the raw string in `entry.action`. NEVER
 * throw and NEVER drop the row — that's the API Response Compatibility rule
 * from repo-root CLAUDE.md (server may add new action enum values; older
 * mobile clients in the wild must render them as a generic fallback, not
 * crash). Unknown actions must also never be interpolated into a key: a
 * server-invented value would resolve to nothing and render blank.
 */
import type { IssuePriority, TimelineEntry } from "@multica/core/types";
import { formatDateOnly } from "@multica/core/issues/date";
import i18n from "i18next";
import { displayLocale } from "@/lib/display-locale";
import {
  PRIORITY_LABEL,
  isIssueStatusCategory,
  priorityLabel,
  statusLabel,
} from "@/lib/issue-status";

/**
 * Names a status KEY out of a timeline entry. `resolveLabel` comes from the
 * workspace catalog and is what names a CUSTOM status; without it (or for a key
 * the catalog never heard of) a built-in still gets its own copy and anything
 * else falls back to the raw key rather than rendering blank. Mirrors web's
 * `statusLabel` in packages/views/issues/components/issue-detail.tsx.
 * (MUL-6243)
 *
 * 汉化改动只落在内置那一支：`statusLabel()` 走 `issues:status.*`，其余分支
 * 与上游逐字一致。自定义状态名来自工作区目录，是数据不是文案，不翻译。
 */
function statusName(
  s: string | undefined,
  resolveLabel?: (statusKey: string) => string,
): string {
  if (!s) return "?";
  if (resolveLabel) return resolveLabel(s);
  return isIssueStatusCategory(s) ? statusLabel(s) : s;
}

function priorityName(p: string | undefined): string {
  if (p && p in PRIORITY_LABEL) return priorityLabel(p as IssuePriority);
  return p ?? "?";
}

// start_date / due_date are calendar days — format timezone-safely (no offset
// day shift). locale 走 displayLocale()（见 lib/display-locale.ts）：日期
// 必须跟界面语言，此处原先硬编码 "en-US"，中文界面里会蹦出 `Mar 5`。
function shortDate(date: string | undefined): string {
  if (!date) return "?";
  return formatDateOnly(
    date,
    { month: "short", day: "numeric" },
    displayLocale(),
  );
}

export function formatActivity(
  entry: TimelineEntry,
  resolveActorName: (
    type: string | null | undefined,
    id: string | null | undefined,
  ) => string,
  resolveStatusLabel?: (statusKey: string) => string,
): string {
  const details = (entry.details ?? {}) as Record<string, string>;
  switch (entry.action) {
    case "created":
      return i18n.t("issues:activity.created", "created this issue");
    case "status_changed":
      // 上游新增的 `resolveStatusLabel` 透传下去（自定义状态才能拿到目录里
      // 的名字），外层文案仍走 i18n —— 两边改的是同一句的不同部分。
      return i18n.t(
        "issues:activity.status_changed",
        "changed status from {{from}} to {{to}}",
        {
          from: statusName(details.from, resolveStatusLabel),
          to: statusName(details.to, resolveStatusLabel),
        },
      );
    case "priority_changed":
      return i18n.t(
        "issues:activity.priority_changed",
        "changed priority from {{from}} to {{to}}",
        { from: priorityName(details.from), to: priorityName(details.to) },
      );
    case "assignee_changed": {
      const isSelf =
        details.to_type === entry.actor_type &&
        details.to_id === entry.actor_id;
      if (isSelf)
        return i18n.t(
          "issues:activity.self_assigned",
          "self-assigned this issue",
        );
      if (details.from_id && !details.to_id)
        return i18n.t("issues:activity.removed_assignee", "removed assignee");
      const toName =
        details.to_id && details.to_type
          ? resolveActorName(details.to_type, details.to_id)
          : null;
      if (toName)
        return i18n.t("issues:activity.assigned_to", "assigned to {{name}}", {
          name: toName,
        });
      return i18n.t("issues:activity.changed_assignee", "changed assignee");
    }
    case "start_date_changed": {
      if (!details.to)
        return i18n.t(
          "issues:activity.start_date_removed",
          "removed start date",
        );
      return i18n.t(
        "issues:activity.start_date_set",
        "set start date to {{date}}",
        { date: shortDate(details.to) },
      );
    }
    case "due_date_changed": {
      if (!details.to)
        return i18n.t("issues:activity.due_date_removed", "removed due date");
      return i18n.t("issues:activity.due_date_set", "set due date to {{date}}", {
        date: shortDate(details.to),
      });
    }
    case "title_changed":
      return i18n.t(
        "issues:activity.title_renamed",
        'renamed this issue from "{{from}}" to "{{to}}"',
        { from: details.from ?? "?", to: details.to ?? "?" },
      );
    case "description_updated":
      return i18n.t(
        "issues:activity.description_updated",
        "updated the description",
      );
    // 单复数交给 i18next 的 count 规则，不手写三元：中日韩没有语法数，
    // `completed 2 tasks` / `completed a task` 这种英语分叉翻不出来。
    // 默认值给 _other 分支的原文（i18n-keys.test.ts 的 lookup 也按
    // _other 解析基名）。
    //
    // 这两条的文案语义是「完成了 N 个不同 task」，不是「同一 task 完成了
    // N 次」：`coalesced_count` 数的是被合并的连续活动条数，而一条
    // task_completed 活动对应一个 task 行的唯一一次完成——
    // `CompleteAgentTask`（server/pkg/db/queries/agent.sql）是
    // `WHERE id = $1 AND status = 'running'` 的幂等守卫，二次调用匹配不到行
    // 就回滚，走不到 broadcastTaskEvent；重试走 CreateRetryTask 克隆出独立
    // id 的子行。活动记录的 Details 又是 "{}"，不含 task_id。所以 N 条连续
    // 活动 = N 个不同 task。
    //
    // 译文一律把数量内联进句子而非放进补充括号：中日韩按 CLDR 只有 _other
    // 分支，「完成了 task（{{count}} 次）」这类括号形态在 count=1 时会渲染
    // 出多余的「（1 次）」，内联写法读作「完成了 1 个 task」则自然。
    case "task_completed":
      return i18n.t(
        "issues:activity.task_completed",
        "completed {{count}} tasks",
        { count: entry.coalesced_count ?? 1 },
      );
    case "task_failed":
      return i18n.t(
        "issues:activity.task_failed",
        "{{count}} tasks failed",
        { count: entry.coalesced_count ?? 1 },
      );
    case "squad_leader_evaluated": {
      const reason = details.reason?.trim();
      switch (details.outcome) {
        case "action":
          return reason
            ? i18n.t(
                "issues:activity.squad_leader_action_reason",
                "evaluated and took action: {{reason}}",
                { reason },
              )
            : i18n.t(
                "issues:activity.squad_leader_action",
                "evaluated and took action",
              );
        case "no_action":
          return reason
            ? i18n.t(
                "issues:activity.squad_leader_no_action_reason",
                "evaluated: no action needed ({{reason}})",
                { reason },
              )
            : i18n.t(
                "issues:activity.squad_leader_no_action",
                "evaluated: no action needed",
              );
        case "failed":
          return reason
            ? i18n.t(
                "issues:activity.squad_leader_failed_reason",
                "evaluation failed: {{reason}}",
                { reason },
              )
            : i18n.t(
                "issues:activity.squad_leader_failed",
                "evaluation failed",
              );
        default:
          return i18n.t(
            "issues:activity.squad_leader_evaluated",
            "evaluated the squad trigger",
          );
      }
    }
    default:
      return entry.action ?? "";
  }
}
