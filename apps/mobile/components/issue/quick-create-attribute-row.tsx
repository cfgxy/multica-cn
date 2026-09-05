/**
 * Chip row for the smart-mode (agent quick-create) form — the subset of the
 * manual `CreateFormAttributeRow` the quick-create API actually accepts:
 * project / priority / due date. Status and assignee are deliberately
 * absent: the server derives title/description and routes the task to the
 * picked agent (web AgentCreatePanel renders exactly this same trio).
 *
 * Reuses the new-issue-picker formSheet routes and the shared draft-store
 * fields, so a pick made in smart mode carries over to the manual form and
 * vice versa — the same shared-slot semantics as web's unified
 * issue-create draft.
 */
import { View } from "react-native";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { AttributeChip } from "@/components/issue/attribute-chip";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { ProjectIcon } from "@/components/ui/project-icon";
import { formatDateOnly } from "@multica/core/issues/date";
import { useNewIssueDraftStore } from "@/data/stores/new-issue-draft-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { displayLocale } from "@/lib/display-locale";
import { priorityLabel } from "@/lib/issue-status";
import { useT } from "@/lib/use-t";

const PICKER_PATHNAMES = {
  priority: "/[workspace]/new-issue-picker/priority",
  project: "/[workspace]/new-issue-picker/project",
  "due-date": "/[workspace]/new-issue-picker/due-date",
} as const;

export function QuickCreateAttributeRow() {
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const priority = useNewIssueDraftStore((s) => s.priority);
  const dueDate = useNewIssueDraftStore((s) => s.dueDate);
  const project = useNewIssueDraftStore((s) => s.project);

  const { t } = useT("issues");
  const dueDateLabel = t("actions.due_date", "Due date");
  const priorityText =
    priority === "none"
      ? t("actions.priority", "Priority")
      : priorityLabel(priority);

  const open = (field: keyof typeof PICKER_PATHNAMES) => {
    if (!wsSlug) return;
    router.push({
      pathname: PICKER_PATHNAMES[field],
      params: { workspace: wsSlug },
    });
  };

  return (
    <View className="flex-row flex-wrap gap-2">
      <AttributeChip
        icon={<PriorityIcon priority={priority} />}
        label={priorityText}
        variant={priority === "none" ? "dimmed" : "filled"}
        onPress={() => open("priority")}
      />
      <AttributeChip
        icon={
          <Ionicons
            name="calendar-outline"
            size={14}
            color={dueDate ? undefined : "#a1a1aa"}
          />
        }
        label={
          dueDate
            ? formatDateOnly(
                dueDate,
                { month: "short", day: "numeric" },
                displayLocale(),
              ) || dueDateLabel
            : dueDateLabel
        }
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
  );
}
