/**
 * Execution profiles manager (`more/squads/[id]/execution-profiles`,
 * RUYI-418 Q10) — mobile counterpart of web's ExecutionProfilePicker +
 * ManageSheet pair (packages/views/squads/components), flattened into one
 * formSheet: profile list on top, selected profile's editor below.
 *
 * Semantics are web parity, same endpoints via the shared core types:
 *  - Create uses the localized default name and opens the editor immediately.
 *  - Activation is confirm-gated (bulk overwrite of runtime/model/thinking on
 *    exactly the named members); entry_count === 0 blocks the confirm. A
 *    clean run is a toast, any skipped/failed member gets the itemised
 *    result section (same trigger conditions as web's result dialog).
 *  - Entries require both runtime and model (server refuses half entries);
 *    switching runtime clears model/thinking; thinking "" means "runtime
 *    default" — the editor always has an opinion, seeded from the agent's
 *    current level, like web's editor.
 *  - Activating on this screen invalidates the agents tree too (mutation).
 *
 * Adaptation: thinking options here are the static level set rather than the
 * model catalog derivation web uses (catalog-driven options land with the
 * agent settings work in the same stage); the server still validates the
 * level at activation time.
 */
import { useEffect, useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import { isRuntimeUsableForUser } from "@multica/core/runtimes";
import type {
  ExecutionProfile,
  ExecutionProfileActivationResponse,
} from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { executionProfileListOptions, executionProfileDetailOptions } from "@/data/queries/execution-profiles";
import { agentListOptions } from "@/data/queries/agents";
import { runtimeListOptions } from "@/data/queries/runtimes";
import {
  useActivateExecutionProfile,
  useCreateExecutionProfile,
  useDeleteExecutionProfile,
  useDeleteExecutionProfileEntry,
  useUpdateExecutionProfile,
  useUpsertExecutionProfileEntry,
} from "@/data/mutations/execution-profiles";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useActorLookup } from "@/data/use-actor-name";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

const THINKING_LEVELS = ["", "low", "medium", "high"] as const;

export default function ExecutionProfilesScreen() {
  const insets = useSafeAreaInsets();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const me = useAuthStore((s) => s.user);
  const { t } = useT("squads");
  // Entry editor labels reuse the agents namespace where web keeps them.
  const { t: tAgents } = useT("agents");
  const { getName } = useActorLookup();
  const { colorScheme } = useColorScheme();

  const listQuery = useQuery(executionProfileListOptions(wsId));
  const { data: agents } = useQuery(agentListOptions(wsId));
  const { data: runtimes } = useQuery(runtimeListOptions(wsId));

  const create = useCreateExecutionProfile();
  const update = useUpdateExecutionProfile();
  const remove = useDeleteExecutionProfile();
  const activate = useActivateExecutionProfile();
  const upsertEntry = useUpsertExecutionProfileEntry();
  const deleteEntry = useDeleteExecutionProfileEntry();

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const detailQuery = useQuery({
    ...executionProfileDetailOptions(wsId, selectedId ?? ""),
    enabled: !!selectedId,
  });

  const [nameDraft, setNameDraft] = useState("");
  const [nameDirty, setNameDirty] = useState(false);
  const [addingEntry, setAddingEntry] = useState(false);
  const [entryAgentId, setEntryAgentId] = useState("");
  const [entryRuntimeId, setEntryRuntimeId] = useState("");
  const [entryModel, setEntryModel] = useState("");
  const [entryThinking, setEntryThinking] = useState("");
  const [result, setResult] = useState<ExecutionProfileActivationResponse | null>(
    null,
  );

  const profiles = listQuery.data?.execution_profiles ?? [];
  const selected: ExecutionProfile | null =
    (selectedId ? detailQuery.data : null) ??
    profiles.find((p) => p.id === selectedId) ??
    null;

  // Seed the rename field whenever a different profile is opened.
  useEffect(() => {
    if (selected) {
      setNameDraft(selected.name);
      setNameDirty(false);
    }
    setAddingEntry(false);
    setEntryAgentId("");
    setEntryRuntimeId("");
    setEntryModel("");
    setEntryThinking("");
  }, [selected?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  const usableRuntimes = useMemo(
    () =>
      (runtimes ?? []).filter(
        (r) =>
          r.status === "online" && isRuntimeUsableForUser(r, me?.id ?? null),
      ),
    [runtimes, me],
  );

  // Entry candidates: unarchived agents not already named by the profile.
  const entryCandidates = useMemo(
    () =>
      (agents ?? []).filter(
        (a) =>
          !a.archived_at &&
          !(selected?.entries ?? []).some((e) => e.agent_id === a.id),
      ),
    [agents, selected],
  );

  const handleCreate = async () => {
    try {
      const created = await create.mutateAsync({
        name: t("execution_profile.default_new_name", "New profile"),
      });
      setSelectedId(created.id);
    } catch {
      Alert.alert(
        t("execution_profile.create_failed", "Could not create the profile"),
      );
    }
  };

  const handleActivate = (profile: ExecutionProfile) => {
    if (profile.entry_count === 0) {
      Alert.alert(
        t(
          "execution_profile.confirm_empty_hint",
          "This profile has no members configured yet, so it cannot be activated.",
        ),
      );
      return;
    }
    Alert.alert(
      t("execution_profile.confirm_title", 'Activate "{{name}}"?', {
        name: profile.name,
      }),
      [
        t(
          "execution_profile.confirm_overwrite_other",
          "This overwrites the runtime, model and thinking level of {{count}} members named by the profile. Members it does not name are left alone.",
          { count: profile.entry_count },
        ),
        t(
          "execution_profile.confirm_running_tasks",
          "Running tasks are not interrupted; the new configuration takes effect on the next run.",
        ),
        t(
          "execution_profile.confirm_audit",
          "The configuration being replaced is recorded in the activity log.",
        ),
      ].join("\n"),
      [
        { text: t("execution_profile.cancel", "Cancel"), style: "cancel" },
        {
          text: t("execution_profile.confirm_activate", "Activate"),
          onPress: async () => {
            try {
              const res = await activate.mutateAsync(profile.id);
              // Same rule as web: a clean run is a toast, anything the user
              // must read (skipped/failed/nothing applied) gets the report.
              if (res.skipped > 0 || res.failed > 0 || res.applied === 0) {
                setResult(res);
                return;
              }
              Alert.alert(
                t(
                  "execution_profile.activated_toast",
                  'Activated "{{name}}" — {{applied}}/{{total}} members updated',
                  { name: profile.name, applied: res.applied, total: res.results.length },
                ),
              );
            } catch {
              Alert.alert(
                t(
                  "execution_profile.activate_failed",
                  "Could not activate the profile",
                ),
              );
            }
          },
        },
      ],
    );
  };

  const handleDelete = (profile: ExecutionProfile) => {
    Alert.alert(
      t("execution_profile.delete_confirm_title", 'Delete "{{name}}"?', {
        name: profile.name,
      }),
      [
        t(
          "execution_profile.delete_confirm_description",
          "The profile and its member configuration are deleted. This cannot be undone.",
        ),
        ...(profile.is_active
          ? [
              t(
                "execution_profile.delete_active_warning",
                "This profile is currently active. Deleting it only clears the active marker — the configuration it wrote to members is not rolled back.",
              ),
            ]
          : []),
      ].join("\n"),
      [
        { text: t("execution_profile.cancel", "Cancel"), style: "cancel" },
        {
          text: t("execution_profile.delete_action", "Delete profile"),
          style: "destructive",
          onPress: async () => {
            try {
              await remove.mutateAsync(profile.id);
              setSelectedId(null);
            } catch {
              Alert.alert(
                t("execution_profile.delete_failed", "Could not delete the profile"),
              );
            }
          },
        },
      ],
    );
  };

  const commitRename = () => {
    if (!selected) return;
    const next = nameDraft.trim();
    setNameDirty(false);
    if (!next || next === selected.name) return;
    update.mutate(
      { profileId: selected.id, patch: { name: next } },
      {
        onError: () =>
          Alert.alert(
            t("execution_profile.rename_failed", "Could not rename the profile"),
          ),
      },
    );
  };

  const saveEntry = () => {
    if (!selected || !entryAgentId || !entryRuntimeId || !entryModel.trim()) return;
    upsertEntry.mutate(
      {
        profileId: selected.id,
        body: {
          agent_id: entryAgentId,
          runtime_id: entryRuntimeId,
          model: entryModel.trim(),
          thinking_level: entryThinking,
        },
      },
      {
        onSuccess: () => {
          setAddingEntry(false);
          setEntryAgentId("");
          setEntryRuntimeId("");
          setEntryModel("");
          setEntryThinking("");
        },
        onError: () =>
          Alert.alert(
            t(
              "execution_profile.entry_save_failed",
              "Could not save this member's configuration",
            ),
          ),
      },
    );
  };

  const reasonLabel = (reason: string | undefined): string | null => {
    switch (reason) {
      case "agent_not_found":
        return t(
          "execution_profile.reason_agent_not_found",
          "No longer a member of this workspace",
        );
      case "agent_archived":
        return t("execution_profile.reason_agent_archived", "Member is archived");
      case "runtime_unavailable":
        return t(
          "execution_profile.reason_runtime_unavailable",
          "The configured runtime is no longer available",
        );
      case "runtime_forbidden":
        return t(
          "execution_profile.reason_runtime_forbidden",
          "The runtime is now private and only its owner can use it",
        );
      case "thinking_level_unsupported":
        return t(
          "execution_profile.reason_thinking_unsupported",
          "The current model does not accept that thinking level",
        );
      case "update_failed":
        return t(
          "execution_profile.reason_update_failed",
          "The write failed",
        );
      default:
        return reason ?? null;
    }
  };

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false）；顶部让出系统状态栏（RUYI-563）。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
        <Text className="flex-1 text-lg font-semibold text-foreground">
          {t("execution_profile.manage_action", "Manage profiles")}
        </Text>
        <Pressable
          onPress={() => router.back()}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={t("execution_profile.close", "Close")}
        >
          <Text className="text-base text-muted-foreground">
            {t("execution_profile.close", "Close")}
          </Text>
        </Pressable>
      </View>

      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 pt-4 pb-8 gap-4"
        keyboardShouldPersistTaps="handled"
      >
        {/* ── Profile list ── */}
        <View className="gap-2">
          <View className="flex-row items-center justify-between">
            <Text className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
              {t("execution_profile.trigger_prefix", "Profile")}
            </Text>
            <Pressable
              onPress={handleCreate}
              disabled={create.isPending}
              className="flex-row items-center gap-1 rounded-lg border border-border px-2.5 py-1.5 active:bg-secondary"
              accessibilityRole="button"
              accessibilityLabel={t(
                "execution_profile.create_action",
                "New profile",
              )}
            >
              <Ionicons name="add" size={14} color={THEME[colorScheme].foreground} />
              <Text className="text-xs text-foreground">
                {t("execution_profile.create_action", "New profile")}
              </Text>
            </Pressable>
          </View>

          {listQuery.isPending ? (
            <Text className="text-sm text-muted-foreground py-1">
              {t("execution_profile.loading_label", "Loading")}
            </Text>
          ) : profiles.length === 0 ? (
            <Text className="text-sm text-muted-foreground py-1">
              {t(
                "execution_profile.empty_description",
                "Create a profile to store a runtime and model for each member, then switch them all in one click.",
              )}
            </Text>
          ) : (
            <View className="rounded-md border border-border overflow-hidden">
              {profiles.map((profile, i) => {
                const isSelected = profile.id === selectedId;
                return (
                  <Pressable
                    key={profile.id}
                    onPress={() => setSelectedId(profile.id)}
                    className={`flex-row items-center gap-2 px-3 py-2.5 active:bg-secondary ${
                      i > 0 ? "border-t border-border" : ""
                    }`}
                    accessibilityRole="radio"
                    accessibilityState={{ selected: isSelected }}
                    accessibilityLabel={profile.name}
                  >
                    <View className="size-4 items-center justify-center">
                      {profile.is_active ? (
                        <Ionicons
                          name="checkmark"
                          size={14}
                          color={THEME[colorScheme].foreground}
                        />
                      ) : null}
                    </View>
                    <Text
                      numberOfLines={1}
                      className={`flex-1 text-sm ${
                        profile.is_active
                          ? "font-semibold text-foreground"
                          : "text-foreground"
                      }`}
                    >
                      {profile.name}
                    </Text>
                    <Text className="text-xs text-muted-foreground">
                      {t(
                        profile.entry_count === 1
                          ? "execution_profile.entry_count_one"
                          : "execution_profile.entry_count_other",
                        "{{count}} members",
                        { count: profile.entry_count },
                      )}
                    </Text>
                    <Ionicons
                      name={isSelected ? "chevron-down" : "chevron-forward"}
                      size={14}
                      color={THEME[colorScheme].mutedForeground}
                    />
                  </Pressable>
                );
              })}
            </View>
          )}
        </View>

        {/* ── Selected profile editor ── */}
        {selected ? (
          <View className="gap-3 rounded-lg border border-border px-3 py-3">
            <View className="gap-1.5">
              <Text className="text-xs uppercase tracking-wider text-muted-foreground">
                {t("execution_profile.name_label", "Profile name")}
              </Text>
              <View className="flex-row items-center gap-2">
                <TextInput
                  value={nameDraft}
                  onChangeText={(v) => {
                    setNameDraft(v);
                    setNameDirty(true);
                  }}
                  onBlur={commitRename}
                  onSubmitEditing={commitRename}
                  className="flex-1 text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2"
                  editable={!update.isPending}
                />
                {nameDirty ? (
                  <Pressable
                    onPress={commitRename}
                    className="rounded-md bg-primary px-3 py-2"
                    accessibilityRole="button"
                    accessibilityLabel={t("execution_profile.entry_save", "Save")}
                  >
                    <Text className="text-xs text-primary-foreground">
                      {t("execution_profile.entry_save", "Save")}
                    </Text>
                  </Pressable>
                ) : null}
              </View>
            </View>

            <Text className="text-xs text-muted-foreground">
              {t(
                "execution_profile.members_hint",
                "Only members saved here are overwritten on activation; everyone else is left as is.",
              )}
            </Text>

            {/* Entries */}
            <View className="gap-1.5">
              {(selected.entries ?? []).map((entry) => (
                <View
                  key={entry.agent_id}
                  className="flex-row items-center gap-2 rounded-md border border-border px-2.5 py-2"
                >
                  <ActorAvatar type="agent" id={entry.agent_id} size={24} />
                  <View className="flex-1 gap-0.5">
                    <Text numberOfLines={1} className="text-sm text-foreground">
                      {getName("agent", entry.agent_id)}
                    </Text>
                    <Text numberOfLines={1} className="text-xs text-muted-foreground">
                      {runtimes?.find((r) => r.id === entry.runtime_id)
                        ?.custom_name ||
                        runtimes?.find((r) => r.id === entry.runtime_id)?.name ||
                        entry.runtime_id}{" "}
                      · {entry.model}
                      {entry.thinking_level
                        ? ` · ${entry.thinking_level}`
                        : entry.thinking_level === ""
                          ? ` · ${t("execution_profile.thinking_default", "runtime default")}`
                          : ""}
                    </Text>
                  </View>
                  <Pressable
                    onPress={() =>
                      deleteEntry.mutate(
                        { profileId: selected.id, agentId: entry.agent_id },
                        {
                          onError: () =>
                            Alert.alert(
                              t(
                                "execution_profile.entry_remove_failed",
                                "Could not remove this member from the profile",
                              ),
                            ),
                        },
                      )
                    }
                    hitSlop={8}
                    accessibilityRole="button"
                    accessibilityLabel={t(
                      "execution_profile.entry_remove",
                      "Remove",
                    )}
                  >
                    <Ionicons
                      name="trash-outline"
                      size={16}
                      color={THEME[colorScheme].mutedForeground}
                    />
                  </Pressable>
                </View>
              ))}

              {!addingEntry ? (
                <Pressable
                  onPress={() => setAddingEntry(true)}
                  className="flex-row items-center justify-center gap-1 rounded-md border border-dashed border-border px-3 py-2 active:bg-secondary"
                  accessibilityRole="button"
                  accessibilityLabel={t(
                    "execution_profile.create_action",
                    "New profile",
                  )}
                >
                  <Ionicons name="add" size={14} color={THEME[colorScheme].foreground} />
                  <Text className="text-xs text-foreground">
                    {t("mobile.execution.add_member", "Add member")}
                  </Text>
                </Pressable>
              ) : (
                <View className="gap-2 rounded-md border border-border px-2.5 py-2.5">
                  <Text className="text-xs uppercase tracking-wider text-muted-foreground">
                    {t("mobile.execution.add_member", "Add member")}
                  </Text>
                  {/* agent picker */}
                  <View className="flex-row flex-wrap gap-1.5">
                    {entryCandidates.map((a) => {
                      const on = a.id === entryAgentId;
                      return (
                        <Pressable
                          key={a.id}
                          onPress={() => setEntryAgentId(a.id)}
                          className={`flex-row items-center gap-1.5 rounded-full border px-2 py-1 ${
                            on ? "border-brand bg-brand/10" : "border-border"
                          }`}
                          accessibilityRole="radio"
                          accessibilityState={{ selected: on }}
                        >
                          <ActorAvatar type="agent" id={a.id} size={18} />
                          <Text className="text-xs text-foreground" numberOfLines={1}>
                            {getName("agent", a.id)}
                          </Text>
                        </Pressable>
                      );
                    })}
                    {entryCandidates.length === 0 ? (
                      <Text className="text-xs text-muted-foreground">
                        {t("mobile.execution.no_candidates", "Every active agent is already named.")}
                      </Text>
                    ) : null}
                  </View>

                  {entryAgentId ? (
                    <>
                      {/* runtime picker */}
                      <Text className="text-xs text-muted-foreground">
                        {tAgents("create_dialog.runtime_label", "Runtime")}
                      </Text>
                      <View className="rounded-md border border-border overflow-hidden">
                        {usableRuntimes.map((r, i) => {
                          const on = r.id === entryRuntimeId;
                          return (
                            <Pressable
                              key={r.id}
                              onPress={() => {
                                setEntryRuntimeId(r.id);
                                // Model/thinking are runtime-native — reset on
                                // switch, same as web's editor.
                                setEntryModel("");
                                setEntryThinking("");
                              }}
                              className={`flex-row items-center gap-2 px-3 py-2 active:bg-secondary ${
                                i > 0 ? "border-t border-border" : ""
                              }`}
                              accessibilityRole="radio"
                              accessibilityState={{ selected: on }}
                            >
                              <View
                                className={`size-3.5 rounded-full border items-center justify-center ${
                                  on ? "border-brand" : "border-muted-foreground"
                                }`}
                              >
                                {on ? (
                                  <View className="size-1.5 rounded-full bg-brand" />
                                ) : null}
                              </View>
                              <Text className="flex-1 text-xs text-foreground" numberOfLines={1}>
                                {r.custom_name || r.name}
                              </Text>
                            </Pressable>
                          );
                        })}
                      </View>

                      {/* model */}
                      <Text className="text-xs text-muted-foreground">
                        {tAgents("inspector.prop_model", "Model")}
                      </Text>
                      <TextInput
                        value={entryModel}
                        onChangeText={setEntryModel}
                        placeholder={tAgents(
                          "mobile.edit.model_placeholder",
                          "Leave empty for the runtime default",
                        )}
                        placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
                        autoCapitalize="none"
                        autoCorrect={false}
                        className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2"
                      />

                      {/* thinking */}
                      <Text className="text-xs text-muted-foreground">
                        {t("execution_profile.thinking_label", "Thinking")}
                      </Text>
                      <View className="flex-row flex-wrap gap-1.5">
                        {THINKING_LEVELS.map((level) => {
                          const on = entryThinking === level;
                          const label =
                            level === ""
                              ? t(
                                  "execution_profile.thinking_default",
                                  "runtime default",
                                )
                              : level;
                          return (
                            <Pressable
                              key={level || "default"}
                              onPress={() => setEntryThinking(level)}
                              className={`rounded-full border px-2.5 py-1 ${
                                on ? "border-brand bg-brand/10" : "border-border"
                              }`}
                              accessibilityRole="radio"
                              accessibilityState={{ selected: on }}
                            >
                              <Text className="text-xs text-foreground">{label}</Text>
                            </Pressable>
                          );
                        })}
                      </View>
                    </>
                  ) : null}

                  {!entryRuntimeId || !entryModel.trim() ? (
                    <Text className="text-xs text-muted-foreground">
                      {t(
                        "execution_profile.entry_incomplete_hint",
                        "Pick both a runtime and a model",
                      )}
                    </Text>
                  ) : null}

                  <View className="flex-row justify-end gap-2">
                    <Pressable
                      onPress={() => setAddingEntry(false)}
                      className="rounded-md border border-border px-3 py-1.5"
                      accessibilityRole="button"
                      accessibilityLabel={t("execution_profile.cancel", "Cancel")}
                    >
                      <Text className="text-xs text-foreground">
                        {t("execution_profile.cancel", "Cancel")}
                      </Text>
                    </Pressable>
                    <Pressable
                      onPress={saveEntry}
                      disabled={
                        !entryAgentId ||
                        !entryRuntimeId ||
                        !entryModel.trim() ||
                        upsertEntry.isPending
                      }
                      className={`rounded-md bg-primary px-3 py-1.5 ${
                        !entryAgentId || !entryRuntimeId || !entryModel.trim()
                          ? "opacity-40"
                          : ""
                      }`}
                      accessibilityRole="button"
                      accessibilityLabel={t("execution_profile.entry_save", "Save")}
                    >
                      <Text className="text-xs text-primary-foreground">
                        {t("execution_profile.entry_save", "Save")}
                      </Text>
                    </Pressable>
                  </View>
                </View>
              )}
            </View>

            {/* Profile actions */}
            <View className="flex-row items-center gap-2 pt-1">
              <Pressable
                onPress={() => handleActivate(selected)}
                disabled={activate.isPending}
                className="flex-1 rounded-md bg-primary px-3 py-2 active:opacity-80"
                accessibilityRole="button"
                accessibilityLabel={t(
                  "execution_profile.confirm_activate",
                  "Activate",
                )}
              >
                <Text className="text-center text-sm font-medium text-primary-foreground">
                  {activate.isPending
                    ? t("execution_profile.activating", "Activating")
                    : t("execution_profile.confirm_activate", "Activate")}
                </Text>
              </Pressable>
              <Pressable
                onPress={() => handleDelete(selected)}
                className="rounded-md border border-border px-3 py-2 active:bg-secondary"
                accessibilityRole="button"
                accessibilityLabel={t(
                  "execution_profile.delete_action",
                  "Delete profile",
                )}
              >
                <Ionicons
                  name="trash-outline"
                  size={16}
                  color={THEME[colorScheme].foreground}
                />
              </Pressable>
            </View>
          </View>
        ) : null}

        {/* ── Activation result report (partial / failed runs) ── */}
        {result ? (
          <View className="gap-1.5 rounded-lg border border-border px-3 py-3">
            <Text className="text-sm font-semibold text-foreground">
              {result.applied === 0
                ? t(
                    "execution_profile.result_failed_title",
                    "Nothing was activated",
                  )
                : t(
                    "execution_profile.result_partial_title",
                    "Some members were not updated",
                  )}
            </Text>
            <Text className="text-xs text-muted-foreground">
              {result.applied === 0
                ? t(
                    "execution_profile.result_failed_description",
                    "No member was updated, the active profile is unchanged and no configuration was written.",
                  )
                : t(
                    "execution_profile.result_partial_description",
                    "{{applied}} of {{total}} members were updated. Here is what happened to each.",
                    { applied: result.applied, total: result.results.length },
                  )}
            </Text>
            {result.results
              .filter((row) => row.status !== "applied")
              .map((row) => (
                <View key={row.agent_id} className="flex-row items-start gap-2">
                  <Ionicons
                    name={row.status === "failed" ? "close-circle" : "alert-circle"}
                    size={14}
                    color={THEME[colorScheme].mutedForeground}
                  />
                  <Text className="flex-1 text-xs text-foreground">
                    {getName("agent", row.agent_id)}
                    {reasonLabel(row.reason)
                      ? ` — ${reasonLabel(row.reason)}`
                      : ""}
                  </Text>
                </View>
              ))}
            <Pressable
              onPress={() => setResult(null)}
              className="self-end rounded-md border border-border px-3 py-1.5"
              accessibilityRole="button"
              accessibilityLabel={t("execution_profile.close", "Close")}
            >
              <Text className="text-xs text-foreground">
                {t("execution_profile.close", "Close")}
              </Text>
            </Pressable>
          </View>
        ) : null}
      </ScrollView>
    </KeyboardAvoidingView>
  );
}
