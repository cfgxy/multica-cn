/**
 * Filter sheet — presented as a formSheet by the parent Stack. Which
 * view-store to read/write is selected by the `scope` URL param.
 *
 * Routes that open this sheet:
 *   - /[workspace]/issues-filter?scope=my    →  useMyIssuesViewStore
 *   - /[workspace]/issues-filter?scope=tasks →  useTasksViewStore (RUYI-344)
 *
 * The tasks scope adds the full-space facets: assignee/creator actor
 * pickers (separate formSheet routes), the three "my relation"
 * checkboxes, the agent-running toggle, and — on every tab except 全部 —
 * a locked status row replacing the status section, because the tab
 * itself owns the status window (design spec §3).
 *
 * Self-contained: reads/writes the store directly, no callback passing.
 */
import { Pressable, ScrollView, Switch, View } from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import type { IssuePriority, IssueStatus } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { StatusIcon } from "@/components/ui/status-icon";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { useMyIssuesViewStore } from "@/data/stores/my-issues-view-store";
import {
  useTasksViewStore,
  type MineRelation,
  type TaskTab,
} from "@/data/stores/tasks-view-store";
import { localizedStatusOptions, priorityLabel } from "@/lib/issue-status";
import { useIssueStatuses } from "@/lib/use-issue-statuses";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/use-t";

// Mirrors PRIORITY_ORDER in packages/core/issues/config/priority.ts.
const PRIORITY_ORDER: IssuePriority[] = [
  "urgent",
  "high",
  "medium",
  "low",
  "none",
];

type Scope = "my" | "tasks";

// Representative built-in status per tab, for the locked row's icon. The
// first category of the tab maps to its canonical built-in.
const TAB_LOCK_STATUS: Record<Exclude<TaskTab, "all">, IssueStatus> = {
  open: "todo",
  active: "in_progress",
  blocked: "blocked",
  completed: "done",
};

const MINE_RELATIONS: {
  key: MineRelation;
  labelKey: string;
  fallback: string;
}[] = [
  {
    key: "assigned",
    labelKey: "mobile.tasks.filters.mine_assigned",
    fallback: "Assigned to me",
  },
  {
    key: "created",
    labelKey: "mobile.tasks.filters.mine_created",
    fallback: "Created by me",
  },
  {
    key: "involved",
    labelKey: "mobile.tasks.filters.mine_involved",
    fallback: "I'm involved in",
  },
];

export default function IssuesFilterRoute() {
  const { t } = useT("issues");
  const router = useRouter();
  const { colorScheme } = useColorScheme();
  const { scope, workspace } = useLocalSearchParams<{
    scope?: string;
    workspace?: string;
  }>();
  const resolvedScope: Scope = scope === "tasks" ? "tasks" : "my";

  // Same option list the status picker offers, so every status a user can set
  // is also a status they can filter by. (MUL-6243)
  const catalog = useIssueStatuses();
  // 本地化版本：选项结构与 `statusOptions` 一致，只把内置状态的 label
  // 换成 i18n 取值，自定义状态仍用目录里的 `name`。
  const statusChoices = localizedStatusOptions(catalog);

  const taskTab = useTasksViewStore((s) => s.tab);
  const taskStatusFilters = useTasksViewStore((s) => s.statusFilters);
  const taskPriorityFilters = useTasksViewStore((s) => s.priorityFilters);
  const mineRelations = useTasksViewStore((s) => s.mineRelations);
  const assigneeRefs = useTasksViewStore((s) => s.assigneeRefs);
  const includeNoAssignee = useTasksViewStore((s) => s.includeNoAssignee);
  const creatorRefs = useTasksViewStore((s) => s.creatorRefs);
  const agentRunning = useTasksViewStore((s) => s.agentRunning);
  const myStatusFilters = useMyIssuesViewStore((s) => s.statusFilters);
  const myPriorityFilters = useMyIssuesViewStore((s) => s.priorityFilters);

  const statusFilters = resolvedScope === "tasks" ? taskStatusFilters : myStatusFilters;
  const priorityFilters = resolvedScope === "tasks" ? taskPriorityFilters : myPriorityFilters;
  const statusLocked = resolvedScope === "tasks" && taskTab !== "all";

  const onToggleStatus = (s: IssueStatus) => {
    if (resolvedScope === "tasks") {
      useTasksViewStore.getState().toggleStatusFilter(s);
    } else {
      useMyIssuesViewStore.getState().toggleStatusFilter(s);
    }
  };
  const onTogglePriority = (p: IssuePriority) => {
    if (resolvedScope === "tasks") {
      useTasksViewStore.getState().togglePriorityFilter(p);
    } else {
      useMyIssuesViewStore.getState().togglePriorityFilter(p);
    }
  };
  const onClearFilters = () => {
    if (resolvedScope === "tasks") {
      useTasksViewStore.getState().clearFilters();
    } else {
      useMyIssuesViewStore.getState().clearFilters();
    }
  };
  const openActorPicker = (kind: "assignee" | "creator") => {
    if (!workspace) return;
    router.push({
      pathname: "/[workspace]/tasks-actor-picker",
      params: { workspace, kind },
    });
  };

  const taskHasActive =
    taskStatusFilters.length > 0 ||
    taskPriorityFilters.length > 0 ||
    Object.values(mineRelations).some(Boolean) ||
    assigneeRefs.length > 0 ||
    includeNoAssignee ||
    creatorRefs.length > 0 ||
    agentRunning;
  const myHasActive = statusFilters.length > 0 || priorityFilters.length > 0;
  const hasActive = resolvedScope === "tasks" ? taskHasActive : myHasActive;

  const tabLabel = (tab: TaskTab) => t(`mobile.tasks.tabs.${tab}`);

  return (
    <View className="flex-1">
      <View className="flex-row items-center justify-between px-4 pt-4 pb-3">
        <Text className="text-base font-semibold text-foreground">
          {t("filters.tooltip", "Filter")}
        </Text>
        {hasActive ? (
          <Pressable
            onPress={onClearFilters}
            hitSlop={8}
            className="px-2 py-1 active:opacity-60"
          >
            <Text className="text-sm text-primary font-medium">
              {t("filters.reset", "Reset")}
            </Text>
          </Pressable>
        ) : null}
      </View>
      <ScrollView className="flex-1" showsVerticalScrollIndicator={false}>
        {statusLocked ? (
          // Non-全部 tabs own their status window server-side; show it as a
          // locked hint instead of offering a contradictory multi-select.
          <View className="mx-4 mt-1 mb-1 flex-row items-center gap-2 rounded-lg bg-secondary/40 px-3 py-2.5">
            <StatusIcon status={TAB_LOCK_STATUS[taskTab]} size={16} />
            <Text className="flex-1 text-sm text-muted-foreground">
              {t("mobile.tasks.filters.status_locked", "Status: {{tab}} (set by tab)", {
                tab: tabLabel(taskTab),
              })}
            </Text>
            <Ionicons
              name="lock-closed-outline"
              size={14}
              color={THEME[colorScheme].mutedForeground}
            />
          </View>
        ) : (
          <>
            <SectionLabel>{t("filters.section_status", "Status")}</SectionLabel>
            {statusChoices.map((option) => {
              const checked = statusFilters.includes(option.key);
              return (
                <Pressable
                  key={option.key}
                  onPress={() => onToggleStatus(option.key)}
                  className={cn(
                    "flex-row items-center gap-3 px-4 py-2.5 active:bg-secondary",
                    checked && "bg-secondary/60",
                  )}
                >
                  <StatusIcon
                    status={option.key}
                    category={option.category}
                    color={option.color}
                    size={16}
                  />
                  <Text className="flex-1 text-sm text-foreground">
                    {option.label}
                  </Text>
                  <CheckMark checked={checked} />
                </Pressable>
              );
            })}
          </>
        )}

        <SectionLabel>{t("filters.section_priority", "Priority")}</SectionLabel>
        {PRIORITY_ORDER.map((priority) => {
          const checked = priorityFilters.includes(priority);
          return (
            <Pressable
              key={priority}
              onPress={() => onTogglePriority(priority)}
              className={cn(
                "flex-row items-center gap-3 px-4 py-2.5 active:bg-secondary",
                checked && "bg-secondary/60",
              )}
            >
              <PriorityIcon priority={priority} />
              <Text className="flex-1 text-sm text-foreground">
                {priorityLabel(priority)}
              </Text>
              <CheckMark checked={checked} />
            </Pressable>
          );
        })}

        {resolvedScope === "tasks" ? (
          <>
            <SectionLabel>{t("filters.section_assignee", "Assignee")}</SectionLabel>
            <Pressable
              onPress={() => openActorPicker("assignee")}
              className="flex-row items-center gap-3 px-4 py-2.5 active:bg-secondary"
            >
              <Ionicons
                name="person-outline"
                size={16}
                color={THEME[colorScheme].mutedForeground}
              />
              <Text className="flex-1 text-sm text-foreground">
                {t("filters.section_assignee", "Assignee")}
              </Text>
              {(assigneeRefs.length > 0 || includeNoAssignee) && (
                <Text className="text-sm text-muted-foreground">
                  {t("mobile.tasks.filters.selected_count", "{{count}} selected", {
                    count: assigneeRefs.length + (includeNoAssignee ? 1 : 0),
                  })}
                </Text>
              )}
              <Ionicons
                name="chevron-forward"
                size={16}
                color={THEME[colorScheme].mutedForeground}
              />
            </Pressable>
            <Pressable
              onPress={() => openActorPicker("creator")}
              className="flex-row items-center gap-3 px-4 py-2.5 active:bg-secondary"
            >
              <Ionicons
                name="person-circle-outline"
                size={16}
                color={THEME[colorScheme].mutedForeground}
              />
              <Text className="flex-1 text-sm text-foreground">
                {t("filters.section_creator", "Creator")}
              </Text>
              {creatorRefs.length > 0 && (
                <Text className="text-sm text-muted-foreground">
                  {t("mobile.tasks.filters.selected_count", "{{count}} selected", {
                    count: creatorRefs.length,
                  })}
                </Text>
              )}
              <Ionicons
                name="chevron-forward"
                size={16}
                color={THEME[colorScheme].mutedForeground}
              />
            </Pressable>

            <SectionLabel>
              {t("mobile.tasks.filters.section_mine", "My relation")}
            </SectionLabel>
            {MINE_RELATIONS.map(({ key, labelKey, fallback }) => {
              const checked = mineRelations[key];
              return (
                <Pressable
                  key={key}
                  onPress={() =>
                    useTasksViewStore.getState().toggleMineRelation(key)
                  }
                  className={cn(
                    "flex-row items-center gap-3 px-4 py-2.5 active:bg-secondary",
                    checked && "bg-secondary/60",
                  )}
                >
                  <Text className="flex-1 text-sm text-foreground">
                    {t(labelKey, fallback)}
                  </Text>
                  <CheckMark checked={checked} />
                </Pressable>
              );
            })}

            <View className="flex-row items-center gap-3 px-4 py-2.5">
              <Text className="flex-1 text-sm text-foreground">
                {t("mobile.tasks.filters.agent_running", "Agent running")}
              </Text>
              <Switch
                value={agentRunning}
                onValueChange={() =>
                  useTasksViewStore.getState().toggleAgentRunning()
                }
                trackColor={{
                  false: THEME[colorScheme].secondary,
                  true: THEME[colorScheme].primary,
                }}
              />
            </View>
          </>
        ) : null}
      </ScrollView>
    </View>
  );
}

function SectionLabel({ children }: { children: string }) {
  return (
    <View className="px-4 pt-3 pb-1.5">
      <Text className="text-xs uppercase tracking-wider text-muted-foreground font-medium">
        {children}
      </Text>
    </View>
  );
}

function CheckMark({ checked }: { checked: boolean }) {
  if (!checked) return null;
  return <Text className="text-sm text-primary font-semibold">✓</Text>;
}
