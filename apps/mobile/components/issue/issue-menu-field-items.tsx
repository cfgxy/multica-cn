/**
 * First group of the issue-detail "…" menu (RUYI-697): the four quick
 * property setters double as a KV readout — key left, current value
 * (+ status/priority glyph) right-aligned — so the common case no longer
 * requires opening a picker just to see the current value.
 *
 * Value sources mirror the attribute chip row (components/issue/
 * attribute-row.tsx) one-to-one: status through the workspace catalog
 * (`localizedStatusLabel` + category glyph), assignee through
 * `useActorLookup`, project through the cached project list, priority
 * through `priorityLabel` — same sources, so the menu and the chips can
 * never disagree on a value. Pressing a row still opens the same formSheet
 * picker routes (RUYI-129).
 *
 * Empty values render localized placeholders ("未分配" / "无项目" /
 * "无优先级") — never blank, never raw null/undefined. Long values
 * ellipsize; the KV layout itself is QA-verified on device.
 */
import { useMemo } from "react";
import { View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import type { Issue } from "@multica/core/types";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { Text } from "@/components/ui/text";
import { StatusIcon } from "@/components/ui/status-icon";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { useActorLookup } from "@/data/use-actor-name";
import { findProject, projectListOptions } from "@/data/queries/projects";
import { useWorkspaceStore } from "@/data/workspace-store";
import { localizedStatusLabel, priorityLabel } from "@/lib/issue-status";
import { useIssueStatuses } from "@/lib/use-issue-statuses";
import { useT } from "@/lib/use-t";
import type { IssuePickerField } from "@/lib/issue-picker-routes";

interface Props {
  issue: Issue;
  onOpenPicker: (field: IssuePickerField) => void;
}

// Value column: fills the remainder and right-aligns its content, so the
// key stays pinned left for any value length.
const VALUE_COLUMN = "flex-1 flex-row items-center justify-end gap-1.5";
const VALUE_TEXT = "text-muted-foreground";
const PLACEHOLDER_TEXT = "text-muted-foreground/70";

export function IssueMenuFieldItems({ issue, onOpenPicker }: Props) {
  const { t } = useT("issues");
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { getName } = useActorLookup();
  // The menu shows the issue's own status, which may be a custom one — name
  // and colour come from the workspace catalog, the glyph from its category.
  // Same resolution as the attribute chip row above the timeline (MUL-6243).
  const catalog = useIssueStatuses();
  const { categoryOf, colorOf } = catalog;

  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const project = useMemo(
    () => findProject(projects, issue.project_id),
    [projects, issue.project_id],
  );

  const assigneeValue =
    issue.assignee_type && issue.assignee_id
      ? { type: issue.assignee_type, id: issue.assignee_id }
      : null;
  const assigneeName = assigneeValue
    ? getName(assigneeValue.type, assigneeValue.id)
    : null;

  return (
    <>
      <DropdownMenuItem onPress={() => onOpenPicker("assignee")}>
        <Text numberOfLines={1}>{t("actions.assignee", "Assignee")}</Text>
        <View className={VALUE_COLUMN}>
          <Text
            numberOfLines={1}
            className={assigneeName ? VALUE_TEXT : PLACEHOLDER_TEXT}
          >
            {assigneeName ?? t("actions.unassigned", "Unassigned")}
          </Text>
        </View>
      </DropdownMenuItem>

      <DropdownMenuItem onPress={() => onOpenPicker("status")}>
        <Text numberOfLines={1}>{t("actions.status", "Status")}</Text>
        <View className={VALUE_COLUMN}>
          <StatusIcon
            status={issue.status}
            category={categoryOf(issue.status)}
            color={colorOf(issue.status)}
            size={14}
          />
          <Text numberOfLines={1} className={VALUE_TEXT}>
            {localizedStatusLabel(catalog, issue.status)}
          </Text>
        </View>
      </DropdownMenuItem>

      <DropdownMenuItem onPress={() => onOpenPicker("project")}>
        <Text numberOfLines={1}>{t("detail.prop_project", "Project")}</Text>
        <View className={VALUE_COLUMN}>
          <Text
            numberOfLines={1}
            className={project ? VALUE_TEXT : PLACEHOLDER_TEXT}
          >
            {project ? project.title : t("filters.no_project", "No project")}
          </Text>
        </View>
      </DropdownMenuItem>

      <DropdownMenuItem onPress={() => onOpenPicker("priority")}>
        <Text numberOfLines={1}>{t("actions.priority", "Priority")}</Text>
        <View className={VALUE_COLUMN}>
          <PriorityIcon priority={issue.priority} size={14} />
          <Text
            numberOfLines={1}
            className={
              issue.priority === "none" ? PLACEHOLDER_TEXT : VALUE_TEXT
            }
          >
            {priorityLabel(issue.priority)}
          </Text>
        </View>
      </DropdownMenuItem>
    </>
  );
}
