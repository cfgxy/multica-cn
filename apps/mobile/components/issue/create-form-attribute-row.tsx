/**
 * Bottom chip row for the new-issue form. Mirrors `attribute-row.tsx`'s
 * visual pattern but operates on the `useNewIssueDraftStore` instead of an
 * `issue` object + mutation. Tapping a chip pushes a formSheet picker
 * route under `new-issue-picker/<field>` — the route reads/writes the same
 * draft store, so the chip rehydrates automatically when the sheet
 * dismisses.
 *
 * Why a draft store: the picker routes are siblings of new-issue.tsx in
 * the Stack — they can't reach into the new-issue screen's local state.
 * The draft store is the cross-screen channel.
 */
import { View } from "react-native";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { AttributeChip } from "@/components/issue/attribute-chip";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { ProjectIcon } from "@/components/ui/project-icon";
import { StatusIcon } from "@/components/ui/status-icon";
import { formatDateOnly } from "@multica/core/issues/date";
import { useActorLookup } from "@/data/use-actor-name";
import { useNewIssueDraftStore } from "@/data/stores/new-issue-draft-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { displayLocale } from "@/lib/display-locale";
import { localizedStatusLabel, priorityLabel } from "@/lib/issue-status";
import { useIssueStatuses } from "@/lib/use-issue-statuses";
import { useT } from "@/lib/use-t";

/**
 * Picker fields the new-issue draft form can open. Bound to a typed map
 * of Expo Router pathnames so typos become compile errors (previously
 * the call site used `as never` on a template string).
 */
type NewIssuePickerField =
  | "status"
  | "priority"
  | "assignee"
  | "project"
  | "due-date";

const NEW_ISSUE_PICKER_PATHNAMES = {
  status: "/[workspace]/new-issue-picker/status",
  priority: "/[workspace]/new-issue-picker/priority",
  assignee: "/[workspace]/new-issue-picker/assignee",
  project: "/[workspace]/new-issue-picker/project",
  "due-date": "/[workspace]/new-issue-picker/due-date",
} as const satisfies Record<NewIssuePickerField, string>;

export function CreateFormAttributeRow() {
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const status = useNewIssueDraftStore((s) => s.status);
  const priority = useNewIssueDraftStore((s) => s.priority);
  const assignee = useNewIssueDraftStore((s) => s.assignee);
  const dueDate = useNewIssueDraftStore((s) => s.dueDate);
  const project = useNewIssueDraftStore((s) => s.project);

  const { t } = useT("issues");
  const { getName } = useActorLookup();
  const dueDateLabel = t("actions.due_date", "Due date");
  // The draft can hold a custom status the user picked in the sheet. (MUL-6243)
  // `labelOf` 换成 `localizedStatusLabel(catalog, ...)`：内置状态走 i18n，
  // 自定义状态仍取目录 `name`，分支判据与 `labelOf` 一致。
  const catalog = useIssueStatuses();
  const { categoryOf, colorOf } = catalog;
  const assigneeLabel = assignee
    ? getName(assignee.type, assignee.id)
    : t("actions.assignee", "Assignee");
  const priorityText =
    priority === "none"
      ? t("actions.priority", "Priority")
      : priorityLabel(priority);

  const open = (field: NewIssuePickerField) => {
    if (!wsSlug) return;
    router.push({
      pathname: NEW_ISSUE_PICKER_PATHNAMES[field],
      params: { workspace: wsSlug },
    });
  };

  return (
    <View>
      <View className="flex-row flex-wrap gap-2">
        <AttributeChip
          icon={
            <StatusIcon
              status={status}
              category={categoryOf(status)}
              color={colorOf(status)}
              size={12}
            />
          }
          label={localizedStatusLabel(catalog, status)}
          variant="filled"
          onPress={() => open("status")}
        />
        <AttributeChip
          icon={<PriorityIcon priority={priority} />}
          label={priorityText}
          variant={priority === "none" ? "dimmed" : "filled"}
          onPress={() => open("priority")}
        />
        <AttributeChip
          icon={
            assignee ? (
              <ActorAvatar
                type={assignee.type}
                id={assignee.id}
                size={16}
                showPresence
              />
            ) : (
              <Ionicons
                name="person-circle-outline"
                size={16}
                color="#a1a1aa"
              />
            )
          }
          label={assigneeLabel}
          variant={assignee ? "filled" : "dimmed"}
          onPress={() => open("assignee")}
        />
        <AttributeChip
          icon={
            <Ionicons
              name="calendar-outline"
              size={14}
              color={dueDate ? undefined : "#a1a1aa"}
            />
          }
          label={dueDate ? formatDueDate(dueDate, dueDateLabel) : dueDateLabel}
          variant={dueDate ? "filled" : "dimmed"}
          onPress={() => open("due-date")}
        />
        <AttributeChip
          icon={
            project ? (
              <ProjectIcon icon={project.icon} size="sm" />
            ) : (
              <Ionicons name="folder-outline" size={14} color="#a1a1aa" />
            )
          }
          label={project?.title ?? t("detail.prop_project", "Project")}
          variant={project ? "filled" : "dimmed"}
          onPress={() => open("project")}
        />
      </View>
    </View>
  );
}

/**
 * due_date is a calendar day — format timezone-safely (no offset day shift).
 *
 * locale 走 `displayLocale()`，与 `attribute-row.tsx` / `detail-label.tsx` /
 * `format-activity.ts` 同一出口。这里原先省略第三参跟随的是**系统** locale，
 * 那三处硬编码 `"en-US"` —— 同一个 due_date 在详情页显示 `Mar 5`、在新建
 * 表单显示 `3月5日`，方向相反的两种不一致。
 *
 * 空值 fallback 由调用方传入（本函数在组件外，取不到 t()）。
 */
function formatDueDate(iso: string, emptyLabel: string): string {
  return (
    formatDateOnly(iso, { month: "short", day: "numeric" }, displayLocale()) ||
    emptyLabel
  );
}
