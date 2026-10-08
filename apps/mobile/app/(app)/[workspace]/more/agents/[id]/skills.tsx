/**
 * Agent skills sub-screen (`more/agents/[id]/skills`, RUYI-346; inherited
 * skills + discovery import added in RUYI-418 A13) — mobile mirror of web's
 * capabilities/skills tab. Three blocks:
 *
 *  1. Assigned workspace skills (toggle / remove / add via picker).
 *  2. Skills inherited from the bound local runtime, discovered live —
 *     read-only unless the daemon reports `can_disable`; toggling writes the
 *     agent's `disabled_runtime_skills` overrides (web parity).
 *  3. The add picker's discovery section: runtime catalog sightings that
 *     aren't workspace skills yet, with import (RUYI-288 flow) that turns
 *     them into assignable rows.
 *
 * Permission: writes render for managers only (same "hide, not disable"
 * rule as env/webhooks). Discovery import inherits the picker's gate.
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router, useLocalSearchParams } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import type {
  AgentSkillSummary,
  DisabledRuntimeSkill,
  RuntimeLocalSkillSummary,
} from "@multica/core/types";
import { isRuntimeUsableForUser, runtimeDisplayLabel } from "@multica/core/runtimes";
import { Text } from "@/components/ui/text";
import { Switch } from "@/components/ui/switch";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { api } from "@/data/api";
import { agentDetailOptions, agentKeys } from "@/data/queries/agents";
import {
  skillCatalogKeys,
  skillCatalogOptions,
  skillKeys,
  skillListOptions,
} from "@/data/queries/skills";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { runtimeCapabilitiesOptions } from "@/data/queries/runtime-skills";
import {
  useAddAgentSkills,
  useRemoveAgentSkill,
  useSetAgentSkillEnabled,
} from "@/data/mutations/agents";
import { importRuntimeLocalSkill } from "@/lib/runtime-discovery";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useCanManageAgent } from "@/components/agents/use-can-manage-agent";
import { rankSkills } from "@/lib/skill-reference";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export default function AgentSkillsScreen() {
  const insets = useSafeAreaInsets();
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const me = useAuthStore((s) => s.user);
  const qc = useQueryClient();
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
  const catalog = useQuery(skillCatalogOptions(wsId));

  // Inherited runtime skills: the bound local runtime, discoverable only
  // when online and usable by this viewer (web's canReadRuntime ladder).
  const { data: runtimes } = useQuery(runtimeListOptions(wsId));
  const runtime = useMemo(
    () =>
      agent?.runtime_id
        ? runtimes?.find((r) => r.id === agent.runtime_id) ?? null
        : null,
    [agent?.runtime_id, runtimes],
  );
  const canReadRuntime =
    runtime != null && isRuntimeUsableForUser(runtime, me?.id ?? null);
  const runtimeId =
    runtime?.runtime_mode === "local" &&
    runtime.status === "online" &&
    canReadRuntime
      ? runtime.id
      : null;
  const runtimeQuery = useQuery(runtimeCapabilitiesOptions(runtimeId));
  const runtimeSkills = runtimeQuery.data?.skills ?? [];
  const runtimeToggle = useMutation({
    mutationFn: (vars: {
      skill: RuntimeLocalSkillSummary;
      enabled: boolean;
    }) =>
      api.setAgentRuntimeSkillEnabled(agentId, {
        runtime_id: runtime?.id ?? "",
        root: vars.skill.root ?? "provider",
        key: vars.skill.key,
        name: vars.skill.name,
        plugin: vars.skill.plugin,
        enabled: vars.enabled,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: agentKeys.detail(wsId, agentId) });
    },
    onError: () =>
      Alert.alert(
        t(
          "tab_body.skills.runtime_toggle_failed_toast",
          "Failed to change inherited skill status",
        ),
      ),
  });

  // Discovery import (RUYI-288 flow): enqueue, poll to terminal, then
  // refresh catalog + authored list so the row becomes an assignable skill.
  const [importingKey, setImportingKey] = useState<string | null>(null);
  const discoveries = useMemo(
    () =>
      (catalog.data ?? []).filter(
        (e) =>
          e.kind === "discovery" &&
          !e.matching_skill_id &&
          !!e.runtime_id &&
          !!e.key,
      ),
    [catalog.data],
  );
  const importDiscovery = async (runtimeId: string, key: string) => {
    setImportingKey(key);
    try {
      await importRuntimeLocalSkill(runtimeId, { skill_key: key });
      qc.invalidateQueries({ queryKey: skillCatalogKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: skillKeys.all(wsId) });
      Alert.alert(
        t(
          "tab_body.skills.add_dialog_discovery_imported_toast",
          "Skill imported and now selectable.",
        ),
      );
    } catch {
      Alert.alert(
        t(
          "tab_body.skills.add_dialog_discovery_import_failed_toast",
          "Import failed. Check the runtime connection and try again.",
        ),
      );
    } finally {
      setImportingKey(null);
    }
  };


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
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false）；顶部让出系统状态栏（RUYI-563）。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
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

        {/* A13: skills inherited from the bound local runtime */}
        <View className="gap-2">
          <View className="flex-row items-center justify-between">
            <Text className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
              {t("tab_body.skills.runtime_title", "Inherited from runtime")}
            </Text>
            {runtimeId ? (
              <Pressable
                onPress={() => void runtimeQuery.refetch()}
                disabled={runtimeQuery.isFetching}
                className="flex-row items-center gap-1"
                accessibilityRole="button"
                accessibilityLabel={t(
                  "tab_body.skills.refresh_action",
                  "Refresh",
                )}
              >
                <Ionicons
                  name="refresh"
                  size={14}
                  color={THEME[colorScheme].mutedForeground}
                />
                <Text className="text-xs text-muted-foreground">
                  {t("tab_body.skills.refresh_action", "Refresh")}
                </Text>
              </Pressable>
            ) : null}
          </View>
          <Text className="text-xs text-muted-foreground leading-4">
            {t(
              "tab_body.skills.runtime_hint",
              "Discovered automatically from {{runtime}} and available whenever this agent runs there.",
              {
                runtime: runtime ? runtimeDisplayLabel(runtime) : "Runtime",
              },
            )}
          </Text>
          {!runtime ? (
            <RuntimeNotice
              text={t(
                "tab_body.skills.runtime_missing",
                "Assign a local runtime to discover inherited skills.",
              )}
            />
          ) : !canReadRuntime ? (
            <RuntimeNotice
              text={t(
                "tab_body.skills.runtime_forbidden",
                "You don't have permission to view this runtime's skills.",
              )}
            />
          ) : runtime.status !== "online" ? (
            <RuntimeNotice
              text={t(
                "tab_body.skills.runtime_offline",
                "The local runtime is offline. Reconnect it to refresh inherited skills.",
              )}
            />
          ) : runtimeQuery.isLoading ? (
            <RuntimeNotice
              loading
              text={t(
                "tab_body.skills.runtime_discovering",
                "Discovering skills from the local runtime…",
              )}
            />
          ) : runtimeQuery.isError ? (
            <RuntimeNotice
              text={t(
                "tab_body.skills.runtime_failed",
                "Couldn't discover runtime skills. Try again.",
              )}
            />
          ) : runtimeQuery.data?.supported !== true ? (
            <RuntimeNotice
              text={t(
                "tab_body.skills.runtime_unsupported",
                "This runtime does not expose local skills.",
              )}
            />
          ) : runtimeSkills.length === 0 ? (
            <RuntimeNotice
              text={t(
                "tab_body.skills.runtime_empty",
                "No local skills were found for this runtime.",
              )}
            />
          ) : (
            <View className="rounded-lg border border-border overflow-hidden">
              {runtimeSkills.map((skill, i) => {
                const disabled = isRuntimeSkillDisabled(
                  agent?.disabled_runtime_skills,
                  runtime?.id,
                  skill,
                );
                const busyKey = runtimeSkillIdentity(skill);
                return (
                  <View
                    key={busyKey}
                    className={`flex-row items-center gap-3 px-3 py-2.5 ${
                      i > 0 ? "border-t border-border" : ""
                    } ${disabled ? "opacity-60" : ""}`}
                  >
                    <View className="flex-1 gap-0.5">
                      <Text
                        numberOfLines={1}
                        className={`text-sm font-medium text-foreground ${
                          disabled ? "text-muted-foreground" : ""
                        }`}
                      >
                        {skill.name}
                      </Text>
                      <Text numberOfLines={1} className="text-xs text-muted-foreground">
                        {skill.description || skill.source_path}
                      </Text>
                    </View>
                    <Text className="rounded border border-border px-1.5 py-0.5 text-xs text-muted-foreground">
                      {t("tab_body.skills.inherited_badge", "Inherited")}
                    </Text>
                    {canManage &&
                    skill.can_disable === true &&
                    skill.root ? (
                      <Switch
                        checked={!disabled}
                        disabled={runtimeToggle.isPending}
                        onCheckedChange={(enabled) =>
                          runtimeToggle.mutate({ skill, enabled })
                        }
                        aria-label={t(
                          "tab_body.skills.runtime_toggle_aria",
                          "Toggle inherited {{name}}",
                          { name: skill.name },
                        )}
                      />
                    ) : null}
                  </View>
                );
              })}
            </View>
          )}
        </View>

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
              {/* A13: runtime sightings that aren't workspace skills yet —
                  listed for visibility, importable into the workspace. */}
              {discoveries.length > 0 ? (
                <View className="gap-1.5">
                  <Text className="text-xs font-medium text-foreground">
                    {t(
                      "tab_body.skills.add_dialog_discoveries_title",
                      "Discovered on runtimes",
                    )}
                  </Text>
                  <Text className="text-xs text-muted-foreground">
                    {t(
                      "tab_body.skills.add_dialog_discovery_hint",
                      "Folders discovered on connected runtimes. Importing creates an assignable workspace skill; until then they are listed for visibility only.",
                    )}
                  </Text>
                  <View className="rounded-md border border-border overflow-hidden">
                    {discoveries.map((entry, i) => (
                      <View
                        key={`${entry.runtime_id}:${entry.key}`}
                        className={`flex-row items-center gap-3 px-3 py-2.5 ${
                          i > 0 ? "border-t border-border" : ""
                        }`}
                      >
                        <View className="flex-1 gap-0.5">
                          <View className="flex-row items-center gap-1.5">
                            <Text
                              numberOfLines={1}
                              className="flex-1 text-sm font-medium text-foreground"
                            >
                              {entry.name}
                            </Text>
                            <Text className="rounded border border-border px-1.5 py-0.5 text-xs text-muted-foreground">
                              {t("tab_body.skills.source_runtime", "Runtime")}
                            </Text>
                          </View>
                          <Text numberOfLines={1} className="text-xs text-muted-foreground">
                            {entry.description || entry.source_path}
                          </Text>
                        </View>
                        <Pressable
                          onPress={() =>
                            void importDiscovery(
                              entry.runtime_id as string,
                              entry.key as string,
                            )
                          }
                          disabled={importingKey !== null}
                          className="rounded-md border border-border px-3 py-1.5 active:bg-secondary"
                          accessibilityRole="button"
                          accessibilityLabel={t(
                            "tab_body.skills.add_dialog_discovery_import",
                            "Import",
                          )}
                        >
                          <Text className="text-xs text-foreground">
                            {importingKey === entry.key
                              ? t(
                                  "tab_body.skills.add_dialog_discovery_importing",
                                  "Importing…",
                                )
                              : t(
                                  "tab_body.skills.add_dialog_discovery_import",
                                  "Import",
                                )}
                          </Text>
                        </Pressable>
                      </View>
                    ))}
                  </View>
                </View>
              ) : null}
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

function RuntimeNotice({
  text,
  loading = false,
}: {
  text: string;
  loading?: boolean;
}) {
  const { colorScheme } = useColorScheme();
  return (
    <View className="flex-row items-center gap-2 rounded-lg border border-dashed border-border px-3 py-4">
      <Ionicons
        name={loading ? "sync" : "server-outline"}
        size={14}
        color={THEME[colorScheme].mutedForeground}
      />
      <Text className="flex-1 text-xs text-muted-foreground">{text}</Text>
    </View>
  );
}

// Mirrors web skills-tab: identity keys and the per-agent disable lookup
// must match the overrides the daemon compares against.
function runtimeSkillIdentity(skill: RuntimeLocalSkillSummary): string {
  return `runtime:${skill.root ?? "unknown"}:${skill.key}:${skill.plugin ?? ""}`;
}

function isRuntimeSkillDisabled(
  disabledSkills: DisabledRuntimeSkill[] | undefined,
  runtimeId: string | undefined,
  skill: RuntimeLocalSkillSummary,
): boolean {
  if (!runtimeId || !skill.root) return false;
  return (disabledSkills ?? []).some(
    (disabled) =>
      disabled.runtime_id === runtimeId &&
      disabled.provider === skill.provider &&
      disabled.root === skill.root &&
      disabled.key === skill.key &&
      (disabled.plugin ?? "") === (skill.plugin ?? ""),
  );
}
