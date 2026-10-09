/**
 * Sort picker for the full-space Tasks tab (RUYI-344) — presented as a
 * formSheet by the parent Stack (SHEET_OPTIONS in the workspace layout).
 *
 * Two choices, always descending (v1 has no ascending toggle — mobile
 * simplification per the design spec; the server supports `direction` if
 * that changes). Reads/writes `useTasksViewStore` directly, same
 * self-contained pattern as `issues-filter.tsx`.
 */
import { Pressable, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { Ionicons } from "@expo/vector-icons";
import { Text } from "@/components/ui/text";
import { useTasksViewStore, type TaskSortKey } from "@/data/stores/tasks-view-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/use-t";

// 模块顶层常量会在 i18n 初始化之前固化（切语言不重算），所以选项的
// label 在组件内跟 t 一起算——同 my-issues scope pills 的处理。
const SORT_CHOICES: { value: TaskSortKey; labelKey: string; fallback: string }[] = [
  {
    value: "updated_at",
    labelKey: "mobile.tasks.sort.updated_at",
    fallback: "Updated at",
  },
  {
    value: "created_at",
    labelKey: "mobile.tasks.sort.created_at",
    fallback: "Created at",
  },
];

export default function TasksSortRoute() {
  const { t } = useT("issues");
  const { colorScheme } = useColorScheme();
  const insets = useSafeAreaInsets();
  const sortBy = useTasksViewStore((s) => s.sortBy);

  return (
    <View className="flex-1">
      {/* 顶部让出系统状态栏（RUYI-563）。 */}
      <View className="flex-row items-center justify-between px-4 pb-3" style={{ paddingTop: insets.top + 16 }}>
        <Text className="text-base font-semibold text-foreground">
          {t("mobile.tasks.sort.title", "Sort")}
        </Text>
      </View>
      {SORT_CHOICES.map((choice) => {
        const checked = sortBy === choice.value;
        return (
          <Pressable
            key={choice.value}
            onPress={() => useTasksViewStore.getState().setSortBy(choice.value)}
            className={cn(
              "flex-row items-center gap-3 px-4 py-2.5 active:bg-secondary",
              checked && "bg-secondary/60",
            )}
          >
            <Ionicons
              name="time-outline"
              size={16}
              color={THEME[colorScheme].mutedForeground}
            />
            <Text className="flex-1 text-sm text-foreground">
              {t(choice.labelKey, choice.fallback)}
            </Text>
            {checked ? (
              <Text className="text-sm text-primary font-semibold">✓</Text>
            ) : null}
          </Pressable>
        );
      })}
    </View>
  );
}
