/**
 * Squad detail (RUYI-346 S2) — mobile counterpart of web's squad page, P0
 * field set: profile card (rename / description edit for managers), squad
 * instructions, and the member roster with the four manager operations
 * (add / remove / change role / make leader) + server-derived status pills.
 *
 * Parity rules:
 *  - canManage mirrors the server's canManageSquad (MUL-4223): workspace
 *    owner/admin OR squad creator. Non-managers get read-only everything —
 *    the edit affordances don't render at all (hide, not disable).
 *  - Member rows are addressed by the (member_type, member_id) pair; status
 *    pills come from GET /members/status (five-way bucket, humans null).
 *  - Archiving is one-way (no restore endpoint) — confirm dialog says so.
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
import { router, useLocalSearchParams, Stack } from "expo-router";
import { useNavigation, usePreventRemove } from "@react-navigation/native";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import type { SquadMemberStatus } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import {
  squadDetailOptions,
  squadMembersOptions,
  squadMemberStatusOptions,
} from "@/data/queries/squads";
import { memberListOptions } from "@/data/queries/members";
import {
  useRemoveSquadMember,
  useUpdateSquad,
  useUpdateSquadMemberRole,
} from "@/data/mutations/squads";
import { ActionSheetModal } from "@/components/ui/action-sheet";
import { useAvatarUploader } from "@/lib/avatar";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useActorLookup } from "@/data/use-actor-name";
import { SquadMemberRow } from "@/components/squads/squad-member-row";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export default function SquadDetailScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const { workspace: wsSlug } = useLocalSearchParams<{ workspace: string }>();
  const squadId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const me = useAuthStore((s) => s.user);
  const { colorScheme } = useColorScheme();
  const { t } = useT("squads");
  const { getName } = useActorLookup();

  const { data: squad } = useQuery(squadDetailOptions(wsId, squadId));
  const { data: members } = useQuery(squadMembersOptions(wsId, squadId));
  const { data: wsMembers } = useQuery(memberListOptions(wsId));
  const { data: memberStatus } = useQuery(
    squadMemberStatusOptions(wsId, squadId),
  );

  const updateSquad = useUpdateSquad(squadId);
  const removeMember = useRemoveSquadMember(squadId);
  const updateRole = useUpdateSquadMemberRole(squadId);
  const { uploading, showAvatarSheet, modalProps } = useAvatarUploader();
  const navigation = useNavigation();

  const [editingName, setEditingName] = useState(false);
  const [nameDraft, setNameDraft] = useState("");
  const [editingDescription, setEditingDescription] = useState(false);
  const [descriptionDraft, setDescriptionDraft] = useState("");
  const [instructions, setInstructions] = useState("");
  const [instructionsSeeded, setInstructionsSeeded] = useState(false);

  // Seed instruction editor once the payload lands (pattern shared with
  // edit-profile: seed once, never fight a refetch mid-edit).
  if (squad && !instructionsSeeded) {
    setInstructions(squad.instructions);
    setInstructionsSeeded(true);
  }

  const isWorkspaceAdmin = useMemo(() => {
    if (!me) return false;
    const mine = wsMembers?.find((m) => m.user_id === me.id);
    return mine?.role === "owner" || mine?.role === "admin";
  }, [wsMembers, me]);

  const canManage =
    !!squad && (isWorkspaceAdmin || (!!me && squad.creator_id === me.id));
  const isArchived = !!squad?.archived_at;

  // S2 (RUYI-418): leaving with unsaved instructions silently discards them.
  // Prevent the removal (back gesture, header back, router.back alike) and
  // re-dispatch the original action only after an explicit discard.
  const instructionsDirty = !!squad && instructions !== squad.instructions;
  usePreventRemove(!!instructionsDirty, ({ data }) => {
    Alert.alert(
      t("mobile.detail.unsaved_title", "Unsaved changes"),
      t(
        "mobile.detail.unsaved_body",
        "Your edits to the instructions haven't been saved yet.",
      ),
      [
        {
          text: t("mobile.detail.unsaved_keep", "Keep editing"),
          style: "cancel",
        },
        {
          text: t("mobile.detail.unsaved_discard", "Discard"),
          style: "destructive",
          onPress: () => navigation.dispatch(data.action),
        },
      ],
    );
  });

  const statusByMember = useMemo(() => {
    const map = new Map<string, SquadMemberStatus>();
    for (const row of memberStatus?.members ?? []) {
      map.set(`${row.member_type}:${row.member_id}`, row);
    }
    return map;
  }, [memberStatus]);

  if (!squad) {
    return (
      <View className="flex-1 items-center justify-center" testID="squad-detail">
        <Text className="text-sm text-muted-foreground">
          {t("profile_card.unavailable", "Squad unavailable")}
        </Text>
      </View>
    );
  }

  const leaderName = squad.leader_id
    ? getName("agent", squad.leader_id)
    : null;
  const created = new Date(squad.created_at);
  const createdLabel = Number.isNaN(created.getTime())
    ? null
    : new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(created);

  // Q5 (RUYI-418): managers can replace or remove the squad avatar in place;
  // the ActionSheet resolves to "" (remove) or an uploaded attachment URL.
  const onPickAvatar = async () => {
    if (!canManage) return;
    const url = await showAvatarSheet(squad.avatar_url);
    if (url === null) return;
    updateSquad.mutate(
      { avatar_url: url },
      {
        onError: () =>
          Alert.alert(t("name_editor.save_failed", "Failed to save")),
      },
    );
  };

  const commitName = () => {
    const next = nameDraft.trim();
    if (!next || next === squad.name) {
      setEditingName(false);
      return;
    }
    updateSquad.mutate(
      { name: next },
      {
        onSuccess: () =>
          Alert.alert(t("name_editor.saved", "Saved")),
        onError: () => Alert.alert(t("name_editor.save_failed", "Failed to save")),
      },
    );
    setEditingName(false);
  };

  const commitDescription = () => {
    if (descriptionDraft === squad.description) {
      setEditingDescription(false);
      return;
    }
    updateSquad.mutate(
      { description: descriptionDraft },
      {
        onError: () =>
          Alert.alert(t("name_editor.save_failed", "Failed to save")),
      },
    );
    setEditingDescription(false);
  };

  const saveInstructions = () => {
    if (!instructionsDirty || updateSquad.isPending) return;
    updateSquad.mutate(
      { instructions },
      {
        onSuccess: () => Alert.alert(t("toasts.instructions_saved", "Instructions saved")),
        onError: () => Alert.alert(t("name_editor.save_failed", "Failed to save")),
      },
    );
  };

  const confirmRemove = (memberType: string, memberId: string, name: string) => {
    Alert.alert(
      t("members_tab.remove_member_tooltip", "Remove from squad"),
      name,
      [
        { text: t("archive_dialog.cancel", "Cancel"), style: "cancel" },
        {
          text: t("members_tab.remove_member_tooltip", "Remove from squad"),
          style: "destructive",
          onPress: () =>
            removeMember.mutate(
              { member_type: memberType as "agent" | "member", member_id: memberId },
              {
                onSuccess: () =>
                  Alert.alert(t("toasts.member_removed", "Member removed")),
                onError: () =>
                  Alert.alert(
                    t("toasts.member_remove_failed", "Failed to remove member"),
                  ),
              },
            ),
        },
      ],
    );
  };

  const makeLeader = (memberId: string) => {
    updateSquad.mutate(
      { leader_id: memberId },
      {
        onSuccess: () => Alert.alert(t("toasts.leader_updated", "Leader updated")),
        onError: () =>
          Alert.alert(t("toasts.leader_update_failed", "Failed to update leader")),
      },
    );
  };

  return (
    <View className="flex-1 bg-background" testID="squad-detail">
      <Stack.Screen
        options={{
          title: squad.name,
        }}
      />
      <ScrollView contentContainerClassName="px-4 pt-4 pb-8 gap-4">
        {/* ── Profile card ── */}
        <View className="rounded-lg border border-border px-3 py-3 gap-2">
          <View className="flex-row items-center gap-3">
            <Pressable
              onPress={onPickAvatar}
              disabled={!canManage || uploading}
              accessibilityRole={canManage ? "button" : undefined}
              accessibilityLabel={t("mobile.create.avatar_add", "Set avatar")}
            >
              <ActorAvatar type="squad" id={squad.id} size={44} />
            </Pressable>
            {editingName ? (
              <TextInput
                value={nameDraft}
                onChangeText={setNameDraft}
                placeholder={t("name_editor.placeholder", "Squad name")}
                placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
                autoFocus
                onSubmitEditing={commitName}
                onBlur={commitName}
                className="flex-1 text-base font-semibold text-foreground border-b border-border pb-1"
              />
            ) : (
              <Pressable
                onPress={() => {
                  if (!canManage) return;
                  setNameDraft(squad.name);
                  setEditingName(true);
                }}
                className="flex-1"
                disabled={!canManage}
                accessibilityRole={canManage ? "button" : undefined}
                accessibilityLabel={t("name_editor.title", "Rename squad")}
              >
                <View className="flex-row items-center gap-1.5">
                  <Text
                    numberOfLines={1}
                    className="flex-shrink text-lg font-semibold text-foreground"
                  >
                    {squad.name}
                  </Text>
                  {canManage ? (
                    <Ionicons
                      name="pencil"
                      size={14}
                      color={THEME[colorScheme].mutedForeground}
                    />
                  ) : null}
                </View>
              </Pressable>
            )}
            {isArchived ? (
              <View className="rounded bg-muted-foreground/10 px-1.5 py-0.5">
                <Text className="text-[10px] text-muted-foreground">
                  {t("profile_card.archived", "Archived")}
                </Text>
              </View>
            ) : null}
          </View>

          {editingDescription ? (
            <View className="gap-2">
              <TextInput
                value={descriptionDraft}
                onChangeText={setDescriptionDraft}
                placeholder={t(
                  "description_dialog.responsibility_placeholder",
                  "What is this squad responsible for?",
                )}
                placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
                multiline
                autoFocus
                className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2 min-h-16"
                textAlignVertical="top"
              />
              <View className="flex-row justify-end gap-2">
                <Pressable
                  onPress={() => setEditingDescription(false)}
                  className="rounded-md border border-border px-3 py-1.5"
                  accessibilityRole="button"
                  accessibilityLabel={t("description_dialog.cancel", "Cancel")}
                >
                  <Text className="text-xs text-foreground">
                    {t("description_dialog.cancel", "Cancel")}
                  </Text>
                </Pressable>
                <Pressable
                  onPress={commitDescription}
                  className="rounded-md bg-primary px-3 py-1.5"
                  accessibilityRole="button"
                  accessibilityLabel={t("description_dialog.save", "Save")}
                >
                  <Text className="text-xs text-primary-foreground">
                    {t("description_dialog.save", "Save")}
                  </Text>
                </Pressable>
              </View>
            </View>
          ) : (
            <Pressable
              onPress={() => {
                if (!canManage) return;
                setDescriptionDraft(squad.description);
                setEditingDescription(true);
              }}
              disabled={!canManage}
              accessibilityRole={canManage ? "button" : undefined}
              accessibilityLabel={t("description_dialog.title", "Edit description")}
            >
              <Text
                numberOfLines={3}
                className={`text-sm ${
                  squad.description
                    ? "text-muted-foreground"
                    : "text-muted-foreground/50 italic"
                }`}
              >
                {squad.description ||
                  t("description_dialog.placeholder_empty", "Add a description")}
              </Text>
            </Pressable>
          )}

          <View className="gap-1 pt-1">
            <DetailLine
              label={t("details.leader", "Leader")}
              value={leaderName ?? "—"}
            />
            <DetailLine
              label={t("details.members", "Members")}
              value={String(members?.length ?? squad.member_count ?? 0)}
            />
            <DetailLine
              label={t("details.created", "Created")}
              value={createdLabel ?? "—"}
            />
          </View>
        </View>

        {/* ── Instructions ── */}
        <View className="gap-1.5">
          <Text className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
            {t("detail_tabs.instructions", "Instructions")}
          </Text>
          <Text className="text-xs text-muted-foreground leading-4">
            {t(
              "instructions_tab.description",
              "Squad instructions are injected into the leader agent's prompt whenever it works on an issue assigned to this squad. Use them to give the leader squad-wide guidance, working agreements, or context the leader should follow on every task.",
            )}
          </Text>
          {canManage ? (
            <View className="gap-2">
              <TextInput
                value={instructions}
                onChangeText={setInstructions}
                placeholder={t(
                  "instructions_tab.placeholder",
                  "e.g. Always start by writing a failing test. Prefer small, atomic commits.",
                )}
                placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
                multiline
                textAlignVertical="top"
                editable={!updateSquad.isPending}
                className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2 min-h-24 border border-transparent"
              />
              {instructionsDirty ? (
                <Pressable
                  onPress={saveInstructions}
                  disabled={updateSquad.isPending}
                  className={`self-end rounded-lg bg-primary px-4 py-2 ${
                    updateSquad.isPending ? "opacity-50" : "active:opacity-80"
                  }`}
                  accessibilityRole="button"
                  accessibilityLabel={t("instructions_tab.save_button", "Save")}
                >
                  <Text className="text-sm font-medium text-primary-foreground">
                    {t("instructions_tab.save_button", "Save")}
                  </Text>
                </Pressable>
              ) : null}
            </View>
          ) : squad.instructions ? (
            <Text className="text-sm text-foreground">{squad.instructions}</Text>
          ) : null}
        </View>

        {/* ── Execution profiles (Q10, managers only) ── */}
        {canManage ? (
          <Pressable
            onPress={() => {
              if (!wsSlug) return;
              router.push({
                pathname: "/[workspace]/more/squads/[id]/execution-profiles",
                params: { workspace: wsSlug, id: squad.id },
              });
            }}
            className="flex-row items-center gap-3 rounded-lg border border-border px-3 py-3 active:bg-secondary"
            accessibilityRole="button"
            accessibilityLabel={t(
              "execution_profile.manage_action",
              "Manage profiles",
            )}
          >
            <View className="flex-1 gap-0.5">
              <Text className="text-sm font-medium text-foreground">
                {t("execution_profile.manage_action", "Manage profiles")}
              </Text>
              <Text className="text-xs text-muted-foreground">
                {t(
                  "execution_profile.empty_description",
                  "Create a profile to store a runtime and model for each member, then switch them all in one click.",
                )}
              </Text>
            </View>
            <Ionicons
              name="chevron-forward"
              size={16}
              color={THEME[colorScheme].mutedForeground}
            />
          </Pressable>
        ) : null}

        {/* ── Members ── */}
        <View className="gap-2">
          <View className="flex-row items-center justify-between">
            <Text className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
              {t("members_tab.section_title", "Members")}
            </Text>
            {canManage ? (
              <Pressable
                onPress={() => {
                  if (!wsSlug) return;
                  router.push({
                    pathname: "/[workspace]/more/squads/[id]/add-member",
                    params: { workspace: wsSlug, id: squad.id },
                  });
                }}
                className="flex-row items-center gap-1 rounded-lg border border-border px-2.5 py-1.5 active:bg-secondary"
                accessibilityRole="button"
                accessibilityLabel={t(
                  "members_tab.add_member_button",
                  "Add Member",
                )}
              >
                <Ionicons
                  name="add"
                  size={14}
                  color={THEME[colorScheme].foreground}
                />
                <Text className="text-xs text-foreground">
                  {t("members_tab.add_member_button", "Add Member")}
                </Text>
              </Pressable>
            ) : null}
          </View>
          <Text className="text-xs text-muted-foreground">
            {t(
              members?.length === 1
                ? "members_tab.section_count_one"
                : "members_tab.section_count_other",
              "{{count}} members in this squad",
              { count: members?.length ?? 0 },
            )}
          </Text>
          {(members ?? []).map((member) => {
            const key = `${member.member_type}:${member.member_id}`;
            const busy =
              removeMember.isPending &&
              removeMember.variables?.member_id === member.member_id;
            return (
              <SquadMemberRow
                key={key}
                member={member}
                isLeader={
                  member.member_type === "agent" &&
                  member.member_id === squad.leader_id
                }
                status={statusByMember.get(key) ?? null}
                canManage={canManage}
                busy={busy}
                onOpenAgent={
                  member.member_type === "agent"
                    ? () =>
                        router.push({
                          pathname: "/[workspace]/more/agents/[id]",
                          params: { workspace: wsSlug, id: member.member_id },
                        })
                    : undefined
                }
                onMakeLeader={
                  member.member_type === "agent" &&
                  member.member_id !== squad.leader_id
                    ? () => makeLeader(member.member_id)
                    : undefined
                }
                onUpdateRole={
                  canManage
                    ? (role) =>
                        updateRole.mutate(
                          {
                            member_type: member.member_type,
                            member_id: member.member_id,
                            role,
                          },
                          {
                            onSuccess: () =>
                              Alert.alert(t("toasts.role_updated", "Role updated")),
                            onError: () =>
                              Alert.alert(
                                t("toasts.role_update_failed", "Failed to update role"),
                              ),
                          },
                        )
                    : undefined
                }
                onRemove={
                  canManage
                    ? () =>
                        confirmRemove(
                          member.member_type,
                          member.member_id,
                          getName(member.member_type, member.member_id),
                        )
                    : undefined
                }
              />
            );
          })}
          {(members ?? []).length === 0 ? (
            <Text className="text-sm text-muted-foreground py-2">
              {t("page.empty_no_squads", "No squads yet. Create one to get started.")}
            </Text>
          ) : null}
        </View>
        <ActionSheetModal {...modalProps} />
      </ScrollView>
    </View>
  );
}

function DetailLine({ label, value }: { label: string; value: string }) {
  return (
    <View className="flex-row items-center gap-2">
      <Text className="text-xs text-muted-foreground w-20">{label}</Text>
      <Text numberOfLines={1} className="flex-1 text-xs text-foreground">
        {value}
      </Text>
    </View>
  );
}
