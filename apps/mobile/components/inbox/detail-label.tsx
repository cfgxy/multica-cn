/**
 * Mobile InboxDetailLabel — type-aware second-line for inbox rows.
 *
 * Mirrors packages/views/inbox/components/inbox-detail-label.tsx exactly:
 * for each InboxItemType the user sees the same label they would see on
 * web/desktop. This is a Behavioral parity concern — if web shows "Set
 * status to ✓ Done", mobile must show "Set status to ✓ Done" (rendered
 * with mobile primitives, not the literal HTML).
 *
 * Web is i18n-driven (useT); mobile now is too, and shares web's `inbox:types.*`
 * / `issues:status.*` / `issues:priority.*` keys rather than minting mobile-only
 * ones — same surface, same words, one place to retranslate.
 */
import { View } from "react-native";
import type {
  InboxItem,
  InboxItemType,
  IssueStatus,
  IssuePriority,
} from "@multica/core/types";
import { formatDateOnly } from "@multica/core/issues/date";
import { Text } from "@/components/ui/text";
import { StatusIcon } from "@/components/ui/status-icon";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { useActorLookup } from "@/data/use-actor-name";
import { displayLocale } from "@/lib/display-locale";
import { priorityLabel, statusLabel } from "@/lib/issue-status";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";

// Mirrors useTypeLabels in packages/views/inbox/components/inbox-detail-label.tsx
// — same ns, same keys, so the two surfaces can't drift apart in translation.
// English fallbacks are web's en copy verbatim (a few differed from mobile's
// old hand-written strings; web's wording wins, it's the mirrored source).
function useTypeLabels(): Record<InboxItemType, string> {
  const { t } = useT("inbox");
  return {
    issue_assigned: t("types.issue_assigned", "Assigned"),
    issue_subscribed: t("types.issue_subscribed", "Subscribed"),
    unassigned: t("types.unassigned", "Unassigned"),
    assignee_changed: t("types.assignee_changed", "Assignee changed"),
    status_changed: t("types.status_changed", "Status changed"),
    priority_changed: t("types.priority_changed", "Priority changed"),
    start_date_changed: t("types.start_date_changed", "Start date changed"),
    due_date_changed: t("types.due_date_changed", "Due date changed"),
    new_comment: t("types.new_comment", "New comment"),
    mentioned: t("types.mentioned", "Mentioned"),
    review_requested: t("types.review_requested", "Review requested"),
    task_completed: t("types.task_completed", "Task completed"),
    task_failed: t("types.task_failed", "Task failed"),
    agent_blocked: t("types.agent_blocked", "Agent blocked"),
    agent_completed: t("types.agent_completed", "Agent completed"),
    reaction_added: t("types.reaction_added", "Reacted"),
    quick_create_done: t("types.quick_create_done", "Created with agent"),
    quick_create_failed: t(
      "types.quick_create_failed",
      "Create with agent failed",
    ),
    quick_create_unconfirmed: t(
      "types.quick_create_unconfirmed",
      "Create with agent unconfirmed",
    ),
  };
}

// due_date is a calendar day — format timezone-safely (no offset day shift).
function shortDate(dateStr: string): string {
  return formatDateOnly(
    dateStr,
    { month: "short", day: "numeric" },
    displayLocale(),
  );
}

function singleLine(value: string | null | undefined): string {
  return (value ?? "").replace(/\s+/g, " ").trim();
}

export function InboxDetailLabel({
  item,
  className,
}: {
  item: InboxItem;
  className?: string;
}) {
  const { getName } = useActorLookup();
  const { t } = useT("inbox");
  const typeLabel = useTypeLabels();
  const details = item.details ?? {};

  // Cases with inline icons → Row layout.
  if (item.type === "status_changed" && details.to) {
    const status = details.to as IssueStatus;
    return (
      <View className={cn("flex-row items-center gap-1", className)}>
        <Text className="text-xs text-muted-foreground">
          {t("labels.set_status_to", "Set status to")}
        </Text>
        <StatusIcon status={status} size={12} />
        <Text className="text-xs text-muted-foreground" numberOfLines={1}>
          {statusLabel(status)}
        </Text>
      </View>
    );
  }

  if (item.type === "priority_changed" && details.to) {
    const priority = details.to as IssuePriority;
    return (
      <View className={cn("flex-row items-center gap-1", className)}>
        <Text className="text-xs text-muted-foreground">
          {t("labels.set_priority_to", "Set priority to")}
        </Text>
        <PriorityIcon priority={priority} size={12} />
        <Text className="text-xs text-muted-foreground" numberOfLines={1}>
          {priorityLabel(priority)}
        </Text>
      </View>
    );
  }

  // Single-string cases. 整句插值，不做「前缀 + 值」的字符串拼接——中日韩
  // 里名字/日期的位置与英文不同。
  const text = (() => {
    switch (item.type) {
      case "issue_assigned":
      case "assignee_changed":
        if (details.new_assignee_id) {
          const name = getName(
            (details.new_assignee_type ?? "member") as "member" | "agent",
            details.new_assignee_id,
          );
          return t("labels.assigned_to", "Assigned to {{name}}", { name });
        }
        return typeLabel[item.type];
      case "unassigned":
        return t("labels.removed_assignee", "Removed assignee");
      case "due_date_changed":
        return details.to
          ? t("labels.set_due_date_to", "Set due date to {{date}}", {
              date: shortDate(details.to),
            })
          : t("labels.removed_due_date", "Removed due date");
      case "new_comment":
        return singleLine(item.body) || typeLabel[item.type];
      case "reaction_added":
        return details.emoji
          ? t("mobile.label.reacted_with", "Reacted with {{emoji}}", {
              emoji: details.emoji,
            })
          : typeLabel[item.type];
      case "quick_create_done":
        return details.identifier
          ? t(
              "labels.created_with_agent",
              "Created with agent: {{identifier}}",
              { identifier: details.identifier },
            )
          : typeLabel[item.type];
      case "quick_create_failed": {
        const detail = singleLine(details.error) || singleLine(item.body);
        return detail
          ? t("labels.failed_with_detail", "Failed: {{detail}}", { detail })
          : typeLabel[item.type];
      }
      // Mirrors packages/views/inbox/components/inbox-detail-label.tsx: the
      // unconfirmed outcome deliberately drops the "Failed:" prefix, because
      // the issue may actually have been created.
      case "quick_create_unconfirmed": {
        const detail = singleLine(details.error) || singleLine(item.body);
        return detail || typeLabel[item.type];
      }
      default:
        return typeLabel[item.type] ?? item.type;
    }
  })();

  return (
    <Text
      className={cn("text-xs text-muted-foreground", className)}
      numberOfLines={1}
    >
      {text}
    </Text>
  );
}
