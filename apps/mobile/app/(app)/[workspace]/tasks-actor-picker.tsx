/**
 * Assignee/creator multi-select for the full-space Tasks tab (RUYI-344) —
 * presented as a formSheet by the parent Stack (SHEET_OPTIONS in the
 * workspace layout). `kind` route param picks which facet the sheet edits;
 * toggles write `useTasksViewStore` directly and never dismiss (the user
 * closes via the sheet grabber), same self-contained pattern as
 * `issues-filter.tsx` / `tasks-sort.tsx`.
 */
import { useLocalSearchParams } from "expo-router";
import { View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { Text } from "@/components/ui/text";
import { ActorFilterPickerBody } from "@/components/issues/actor-filter-picker-body";
import { useTasksViewStore } from "@/data/stores/tasks-view-store";
import { useT } from "@/lib/use-t";

export default function TasksActorPickerRoute() {
  const { kind } = useLocalSearchParams<{ kind?: string }>();
  const actorKind = kind === "creator" ? "creator" : "assignee";
  const { t } = useT("issues");

  const assigneeRefs = useTasksViewStore((s) => s.assigneeRefs);
  const includeNoAssignee = useTasksViewStore((s) => s.includeNoAssignee);
  const creatorRefs = useTasksViewStore((s) => s.creatorRefs);

  const isCreator = actorKind === "creator";
  const insets = useSafeAreaInsets();
  const selected = isCreator ? creatorRefs : assigneeRefs;

  return (
    <View className="flex-1">
      {/* 顶部让出系统状态栏（RUYI-563）。 */}
      <View className="px-4 pb-3" style={{ paddingTop: insets.top + 16 }}>
        <Text className="text-base font-semibold text-foreground">
          {isCreator
            ? t("filters.section_creator", "Creator")
            : t("filters.section_assignee", "Assignee")}
        </Text>
      </View>
      <ActorFilterPickerBody
        kind={actorKind}
        selected={selected}
        includeNoAssignee={isCreator ? false : includeNoAssignee}
        onToggleRef={(ref) => {
          if (isCreator) {
            useTasksViewStore.getState().setCreatorRefs(
              creatorRefs.some((r) => r.type === ref.type && r.id === ref.id)
                ? creatorRefs.filter((r) => !(r.type === ref.type && r.id === ref.id))
                : [...creatorRefs, ref],
            );
          } else {
            useTasksViewStore.getState().setAssigneeRefs(
              assigneeRefs.some((r) => r.type === ref.type && r.id === ref.id)
                ? assigneeRefs.filter((r) => !(r.type === ref.type && r.id === ref.id))
                : [...assigneeRefs, ref],
            );
          }
        }}
        onToggleNoAssignee={() =>
          useTasksViewStore.getState().setIncludeNoAssignee(!includeNoAssignee)
        }
      />
    </View>
  );
}
