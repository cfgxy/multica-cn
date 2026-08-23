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
import type {
  IssuePriority,
  IssueStatus,
  TimelineEntry,
} from "@multica/core/types";
import { formatDateOnly } from "@multica/core/issues/date";
import i18n from "i18next";
import { displayLocale } from "@/lib/display-locale";
import {
  PRIORITY_LABEL,
  STATUS_LABEL,
  priorityLabel,
  statusLabel,
} from "@/lib/issue-status";

// 状态/优先级标签复用 lib/issue-status.ts —— 那两张表与本文件原先各自
// 持有的副本逐字相同，接 i18n 时再维护两份等于给「两处只改一处」留口子。
// `in` 判断仍打在英文兜底表上：它是 key 的权威集合，用来把服务端新增的
// 未知枚举值原样透出（顶部注释的 API Response Compatibility 规则）。
function statusName(s: string | undefined): string {
  if (s && s in STATUS_LABEL) return statusLabel(s as IssueStatus);
  return s ?? "?";
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
): string {
  const details = (entry.details ?? {}) as Record<string, string>;
  switch (entry.action) {
    case "created":
      return i18n.t("issues:activity.created", "created this issue");
    case "status_changed":
      return i18n.t(
        "issues:activity.status_changed",
        "changed status from {{from}} to {{to}}",
        { from: statusName(details.from), to: statusName(details.to) },
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
    case "task_completed":
      return i18n.t(
        "issues:activity.task_completed",
        "completed the task ({{count}} times)",
        { count: entry.coalesced_count ?? 1 },
      );
    case "task_failed":
      return i18n.t(
        "issues:activity.task_failed",
        "task failed ({{count}} times)",
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
