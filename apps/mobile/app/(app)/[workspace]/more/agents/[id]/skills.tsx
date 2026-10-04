/**
 * Agent skills sub-screen (`more/agents/[id]/skills`, RUYI-346) — workspace
 * skills assigned to this agent, mobile subset of web's capabilities/skills
 * tab. P0 covers assigned workspace skills (toggle / remove / add via picker);
 * inherited runtime skills and the discovery-import flow stay web-only
 * (adaptation noted in the delivery report).
 *
 * Permission: toggle/remove/add render for managers only (hidden otherwise —
 * same "hide, not disable" rule as env/webhooks). The skill set itself is
 * read from the agent payload's `skills` array; writes ride the optimistic
 * detail-payload mutations.
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
import { router, useLocalSearchParams } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import type { AgentSkillSummary } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Switch } from "@/components/ui/switch";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { agentDetailOptions } from "@/data/queries/agents";
import { skillListOptions } from "@/data/queries/skills";
import {
  useAddAgentSkills,
  useRemoveAgentSkill,
  useSetAgentSkillEnabled,
} from "@/data/mutations/agents";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useCanManageAgent } from "@/components/agents/use-can-manage-agent";
import { rankSkills } from "@/lib/skill-reference";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export default function AgentSkillsScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");

  const { data: agent } = useQuery(agentDetailOptions(wsId, agentId));
  const canManage = useCanManageAgent(agent);
  const assigned = useMemo(() => agent?.skills ?? [], [agent]);

  const setEnabled = useSetAgentSkillEnabled(agentId);
  const removeSkill = useRemoveAgentSkill(agentId);
  const addSkills = useAddAgentSkills(agentId);

  const [addOpen, setAddOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const { data: allSkills } = useQuery(skillListOptions(wsId));

  // Addable = workspace catalog minus already-assigned, ranked by the shared
  // search scorer.
  const addable = useMemo(() => {
    const assignedIds = new Set(assigned.map((s) => s.id));
    return rankSkills(allSkills ?? [], search).filter(
      (s) => !assignedIds.has(s.id),
    );
  }, [allSkills, assigned, search]);

  const toggleInPicker = (skillId: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(skillId)) next.delete(skillId);
      else next.add(skillId);
      return next;
    });
  };

  const confirmAdd = () => {
    const ids = [...selected];
    if (ids.length === 0) return;
    addSkills.mutate(
      { skill_ids: ids },
      {
        onSuccess: () => {
          setSelected(new Set());
          setAddOpen(false);
          setSearch("");
        },
        onError: () =>
          Alert.alert(
            t("tab_body.skills.add_failed_toast", "Failed to add skill"),
          ),
      },
    );
  };

  const closeLabel = t("create_dialog.cancel", "Cancel");

  return (
    <View className="flex-1 bg-background">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false） */}
      <View className="flex-row items-center px-4 pt-3 pb-2 border-b border-border">
        <Pressable
          onPress={() => router.back()}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={closeLabel}
        >
          <Ionicons
            name="close"
            size={22}
            color={THEME[colorScheme].foreground}
          />
        </Pressable>
        <Text className="flex-1 text-center text-lg font-semibold text-foreground">
          {t("tabs.skills", "Skills")}
        </Text>
        <View className="w-6" />
      </View>

      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 pt-3 pb-8 gap-3"
        keyboardShouldPersistTaps="handled"
      >
        <Text className="text-xs text-muted-foreground leading-4">
          {t(
            "tab_body.skills.intro",
            "This is the agent's effective skill set. Workspace skills are managed here; inherited Codex and Claude Code skills can also be turned off per agent.",
          )}
        </Text>

        {assigned.length === 0 ? (
          <View className="items-center gap-1 py-8">
            <Ionicons
              name="library-outline"
              size={36}
              color={THEME[colorScheme].mutedForeground}
            />
            <Text className="text-base font-medium text-foreground">
              {t("tab_body.skills.empty_title", "No skills assigned")}
            </Text>
            {canManage ? (
              <Text className="text-sm text-muted-foreground text-center">
                {t(
                  "tab_body.skills.empty_hint",
                  "Add workspace skills to share team knowledge with this agent.",
                )}
              </Text>
            ) : null}
          </View>
        ) : (
          <View className="gap-2">
            <Text className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
              {t("tab_body.skills.assigned_title", "Assigned to agent")}
            </Text>
            {assigned.map((skill) => (
              <SkillRow
                key={skill.id}
                skill={skill}
                canManage={canManage}
                togglePending={
                  setEnabled.isPending &&
                  setEnabled.variables?.skillId === skill.id
                }
                onToggle={(enabled) =>
                  setEnabled.mutate(
                    { skillId: skill.id, enabled },
                    {
                      onError: () =>
                        Alert.alert(
                          t(
                            "tab_body.skills.toggle_failed_toast",
                            "Failed to change skill status",
                          ),
                        ),
                    },
                  )
                }
                onRemove={() =>
                  removeSkill.mutate(skill.id, {
                    onError: () =>
                      Alert.alert(
                        t(
                          "tab_body.skills.remove_failed_toast",
                          "Failed to remove skill",
                        ),
                      ),
                  })
                }
              />
            ))}
          </View>
        )}

        {canManage ? (
          addOpen ? (
            <View className="rounded-lg border border-border px-3 py-3 gap-2.5">
              <Text className="text-sm font-semibold text-foreground">
                {t("tab_body.skills.add_dialog_title", "Add skill")}
              </Text>
              <Text className="text-xs text-muted-foreground">
                {t(
                  "tab_body.skills.add_dialog_description",
                  "Select a workspace skill to assign to this agent.",
                )}
              </Text>
              <TextInput
                value={search}
                onChangeText={setSearch}
                placeholder={t(
                  "tab_body.skills.add_dialog_search_placeholder",
                  "Search skills",
                )}
                placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
                autoCorrect={false}
                className="rounded-md border border-border px-3 py-2 text-sm text-foreground"
              />
              {addable.length === 0 ? (
                <Text className="text-sm text-muted-foreground py-2">
                  {search.trim()
                    ? t("tab_body.skills.add_dialog_no_match", "No skills match your search.")
                    : allSkills && allSkills.length > 0
                      ? t(
                          "tab_body.skills.add_dialog_empty_partial",
                          "No more skills to add — this agent already has all of them.",
                        )
                      : t(
                          "tab_body.skills.add_dialog_empty",
                          "All workspace skills are already assigned.",
                        )}
                </Text>
              ) : (
                <View className="rounded-md border border-border overflow-hidden">
                  {addable.map((skill, i) => {
                    const checked = selected.has(skill.id);
                    return (
                      <Pressable
                        key={skill.id}
                        onPress={() => toggleInPicker(skill.id)}
                        className={`flex-row items-start gap-3 px-3 py-2.5 active:bg-secondary ${
                          i > 0 ? "border-t border-border" : ""
                        }`}
                        accessibilityRole="checkbox"
                        accessibilityState={{ checked }}
                      >
                        <View
                          className={`mt-0.5 size-4 rounded border items-center justify-center ${
                            checked
                              ? "bg-brand border-brand"
                              : "border-muted-foreground"
                          }`}
                        >
                          {checked ? (
                            <Ionicons
                              name="checkmark"
                              size={12}
                              color={THEME[colorScheme].primaryForeground}
                            />
                          ) : null}
                        </View>
                        <View className="flex-1 gap-0.5">
                          <Text className="text-sm font-medium text-foreground">
                            {skill.name}
                          </Text>
                          {skill.description ? (
                            <Text
                              numberOfLines={2}
                              className="text-xs text-muted-foreground"
                            >
                              {skill.description}
                            </Text>
                          ) : null}
                        </View>
                      </Pressable>
                    );
                  })}
                </View>
              )}
              <View className="flex-row items-center justify-end gap-2">
                <Pressable
                  onPress={() => {
                    setAddOpen(false);
                    setSearch("");
                    setSelected(new Set());
                  }}
                  disabled={addSkills.isPending}
                  className="rounded-lg border border-border px-4 py-2 active:opacity-80"
                  accessibilityRole="button"
                  accessibilityLabel={t("tab_body.skills.add_dialog_cancel", "Cancel")}
                >
                  <Text className="text-sm text-foreground">
                    {t("tab_body.skills.add_dialog_cancel", "Cancel")}
                  </Text>
                </Pressable>
                <Pressable
                  onPress={confirmAdd}
                  disabled={selected.size === 0 || addSkills.isPending}
                  className={`rounded-lg bg-primary px-4 py-2 ${
                    selected.size > 0 && !addSkills.isPending
                      ? "active:opacity-80"
                      : "opacity-50"
                  }`}
                  accessibilityRole="button"
                  accessibilityLabel={confirmAddLabel(selected.size)}
                >
                  <Text className="text-sm font-medium text-primary-foreground">
                    {addSkills.isPending
                      ? t("tab_body.skills.add_dialog_saving", "Adding…")
                      : confirmAddLabel(selected.size)}
                  </Text>
                </Pressable>
              </View>
            </View>
          ) : (
            <Pressable
              onPress={() => setAddOpen(true)}
              className="flex-row items-center justify-center gap-2 rounded-lg border border-dashed border-border py-2.5 active:bg-secondary"
              accessibilityRole="button"
              accessibilityLabel={t("tab_body.skills.add_action", "Add skill")}
            >
              <Ionicons
                name="add"
                size={16}
                color={THEME[colorScheme].mutedForeground}
              />
              <Text className="text-sm text-muted-foreground">
                {t("tab_body.skills.add_action", "Add skill")}
              </Text>
            </Pressable>
          )
        ) : null}
      </ScrollView>
    </View>
  );

  function confirmAddLabel(count: number): string {
    if (count <= 0)
      return t("tab_body.skills.add_dialog_confirm_default", "Add");
    // 复数交给 i18next 按 count 选 `_one`/`_other` 分支（zh/ja/ko 的 CLDR
    // 只有 `_other`，源码写基名即可，见 lib/i18n-keys.test.ts 的查找规则）。
    return t("tab_body.skills.add_dialog_confirm", "Add {{count}} skills", {
      count,
    });
  }
}

function SkillRow({
  skill,
  canManage,
  togglePending,
  onToggle,
  onRemove,
}: {
  skill: AgentSkillSummary;
  canManage: boolean;
  togglePending: boolean;
  onToggle: (enabled: boolean) => void;
  onRemove: () => void;
}) {
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");

  return (
    <View
      className={`rounded-lg border border-border px-3 py-2.5 gap-1 ${
        skill.enabled === false ? "opacity-60" : ""
      }`}
    >
      <View className="flex-row items-center gap-2">
        <Text numberOfLines={1} className="flex-1 text-sm font-medium text-foreground">
          {skill.name}
        </Text>
        {canManage ? (
          <>
            <Pressable
              onPress={onRemove}
              hitSlop={8}
              accessibilityRole="button"
              accessibilityLabel={t("tab_body.skills.remove_aria", "Remove {{name}}", {
                name: skill.name,
              })}
            >
              <Ionicons
                name="trash-outline"
                size={16}
                color={THEME[colorScheme].destructive}
              />
            </Pressable>
            <Switch
              checked={skill.enabled !== false}
              disabled={togglePending}
              onCheckedChange={onToggle}
              aria-label={t("tab_body.skills.toggle_aria", "Toggle {{name}}", {
                name: skill.name,
              })}
            />
          </>
        ) : null}
      </View>
      <Text numberOfLines={2} className="text-xs text-muted-foreground">
        {skill.description ||
          t("tab_body.skills.no_description", "No description")}
      </Text>
    </View>
  );
}
