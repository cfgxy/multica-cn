import { useMemo } from "react";
import { ActivityIndicator, FlatList, Pressable, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import { agentListOptions } from "@/data/queries/agents";
import { skillListOptions } from "@/data/queries/skills";
import { useSkillDraftStore } from "@/data/stores/skill-draft-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useNativeSearchBar } from "@/lib/use-native-search-bar";
import { useScrollToTopOnChange } from "@/lib/use-scroll-to-top-on-change";
import { useT } from "@/lib/use-t";
import { rankSkills } from "@/lib/skill-reference";
import { Text } from "@/components/ui/text";

export default function SkillPickerRoute() {
  const wsId = useWorkspaceStore((state) => state.currentWorkspaceId);
  const { data: skills = [], isPending, isError, refetch } = useQuery(skillListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { t } = useT("issues");
  const query = useNativeSearchBar(t("mobile.skill.search", "Search skills"), { autoFocus: true });
  const listRef = useScrollToTopOnChange(query);
  const rows = useMemo(() => {
    return rankSkills(skills, query).map((skill) => ({
      skill,
      supporters: agents.filter((agent) => !agent.archived_at &&
        agent.skills?.some((assigned) => assigned.id === skill.id && assigned.enabled !== false)),
    }));
  }, [skills, agents, query]);
  const activeCount = agents.filter((agent) => !agent.archived_at).length;

  return (
    <FlatList<(typeof rows)[number]>
      ref={listRef}
      data={rows}
      keyExtractor={({ skill }) => skill.id}
      keyboardShouldPersistTaps="handled"
      automaticallyAdjustKeyboardInsets
      ListEmptyComponent={isPending
        ? <ActivityIndicator className="py-5" accessibilityLabel={t("mobile.skill.loading", "Loading skills")} />
        : isError
          ? <Pressable className="px-4 py-5" accessibilityRole="button" onPress={() => void refetch()}>
              <Text className="text-muted-foreground">{t("mobile.skill.load_failed", "Couldn't load skills")} · {t("mobile.skill.retry", "Retry")}</Text>
            </Pressable>
          : <Text className="px-4 py-5 text-muted-foreground">{t("mobile.skill.empty", "No matching skills")}</Text>}
      renderItem={({ item: { skill, supporters } }) => (
        <Pressable
          className="px-4 py-3 border-b border-border active:bg-secondary"
          accessibilityRole="button"
          accessibilityLabel={`/${skill.name}`}
          onPress={() => {
            useSkillDraftStore.getState().setSelected({ id: skill.id, name: skill.name });
            router.back();
          }}
        >
          <Text className="font-medium text-foreground" numberOfLines={1}>/{skill.name}</Text>
          {skill.description ? <Text className="text-sm text-muted-foreground" numberOfLines={2}>{skill.description}</Text> : null}
          <View className="flex-row items-center mt-1">
            <Text className="text-xs text-muted-foreground" numberOfLines={1}>
              {activeCount > 0 && supporters.length === activeCount
                ? t("mobile.skill.all", "All agents")
                : supporters.length === 0
                  ? t("mobile.skill.none", "No agents")
                  : supporters.slice(0, 3).map((agent) => agent.name).join(", ") + (supporters.length > 3 ? ` +${supporters.length - 3}` : "")}
            </Text>
          </View>
        </Pressable>
      )}
    />
  );
}
