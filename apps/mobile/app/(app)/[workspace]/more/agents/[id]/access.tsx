/**
 * Agent access (`more/agents/[id]/access`, RUYI-418 S5) — mobile mirror of
 * packages/views/agents/components/inspector/access-picker.tsx. Three-state
 * invocation scope (owner-only / entire workspace / specific people) with a
 * member checklist for the "specific people" state.
 *
 * Semantics kept identical to web (MUL-3963):
 *   - Draft-first: scope changes are local until Save (visibility is
 *     security-sensitive; nothing persists on tap).
 *   - Save payload: private → `{ permission_mode: "private", targets: [] }`;
 *     workspace → `public_to` + workspace target; members → `public_to` +
 *     one target per selected member, PRESERVING persisted team targets
 *     (mobile has no team picker, so teams ride along untouched).
 *   - "Specific people" requires at least one target to be committable.
 *   - Only the owner can edit; everyone else gets the read-only summary
 *     (access.owner_only_readonly), same as web's `canEdit` branch.
 *   - Switching away from private on an agent with a Composio allowlist
 *     shows the composio_switch_hint warning.
 *
 * Scope derivation is the shared `effectiveAccessScope` (same predicate as
 * the server's canInvokeAgent gate) — not re-derived here.
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { effectiveAccessScope } from "@multica/core/agents/effective-access";
import type {
  AgentInvocationTarget,
  AgentInvocationTargetInput,
  AgentPermissionMode,
} from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { agentDetailOptions } from "@/data/queries/agents";
import { memberListOptions } from "@/data/queries/members";
import { useUpdateAgent } from "@/data/mutations/agents";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

type DraftScope = "private" | "workspace" | "members";

function selectedTargetIds(
  targets: AgentInvocationTarget[] | undefined | null,
  type: "member" | "team",
): string[] {
  return (targets ?? [])
    .filter((target) => target.target_type === type && target.target_id !== null)
    .map((target) => target.target_id as string);
}

export default function AgentAccess() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const me = useAuthStore((s) => s.user);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");
  const update = useUpdateAgent(agentId);

  const { data: agent } = useQuery(agentDetailOptions(wsId, agentId));
  const { data: members } = useQuery(memberListOptions(wsId));

  const permissionMode: AgentPermissionMode = agent?.permission_mode ?? "private";
  const canonical = effectiveAccessScope(
    agent?.permission_mode,
    agent?.invocation_targets,
  );
  const persistedScope: DraftScope =
    permissionMode === "private"
      ? "private"
      : canonical === "workspace"
        ? "workspace"
        : "members";
  const persistedMembers = useMemo(
    () => selectedTargetIds(agent?.invocation_targets, "member"),
    [agent?.invocation_targets],
  );
  const teamIds = useMemo(
    () => selectedTargetIds(agent?.invocation_targets, "team"),
    [agent?.invocation_targets],
  );

  const [draftScope, setDraftScope] = useState<DraftScope | null>(null);
  const [draftMembers, setDraftMembers] = useState<string[] | null>(null);
  const scope = draftScope ?? persistedScope;
  const selectedMembers = draftMembers ?? persistedMembers;

  const canEdit = !!agent && !!me && agent.owner_id === me.id;
  const hasComposioAllowlist =
    (agent?.composio_toolkit_allowlist ?? []).length > 0;

  const editableMembers = useMemo(() => {
    if (!members) return [];
    return agent?.owner_id
      ? members.filter((member) => member.user_id !== agent.owner_id)
      : members;
  }, [members, agent?.owner_id]);

  const sameMembers =
    selectedMembers.length === persistedMembers.length &&
    selectedMembers.every((id) => persistedMembers.includes(id));
  const dirty = canEdit && (scope !== persistedScope || (scope === "members" && !sameMembers));

  // The payload this draft would commit — null when not committable
  // (specific-people with zero targets), mirroring web's draftChange.
  const draftChange: {
    permission_mode: AgentPermissionMode;
    invocation_targets: AgentInvocationTargetInput[];
  } | null = useMemo(() => {
    if (scope === "private") {
      return { permission_mode: "private", invocation_targets: [] };
    }
    const targets: AgentInvocationTargetInput[] = [];
    if (scope === "workspace") {
      targets.push({ target_type: "workspace" });
    }
    if (scope === "members") {
      if (selectedMembers.length === 0) return null;
      for (const memberId of selectedMembers) {
        targets.push({ target_type: "member", target_id: memberId });
      }
      for (const teamId of teamIds) {
        targets.push({ target_type: "team", target_id: teamId });
      }
    }
    return { permission_mode: "public_to", invocation_targets: targets };
  }, [scope, selectedMembers, teamIds]);

  const toggleMember = (userId: string) => {
    setDraftMembers((current) => {
      const base = new Set(current ?? persistedMembers);
      if (base.has(userId)) base.delete(userId);
      else base.add(userId);
      return Array.from(base);
    });
  };

  const onSave = () => {
    if (!dirty || !draftChange) return;
    update.mutate(draftChange, {
      onSuccess: () => {
        setDraftScope(null);
        setDraftMembers(null);
      },
      onError: (err) => {
        Alert.alert(
          t("access.save_failed", "Failed to update access"),
          err instanceof Error ? err.message : undefined,
        );
      },
    });
  };

  if (!agent) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <Text className="text-sm text-muted-foreground">
          {t("page.list_loading", "Loading agents…")}
        </Text>
      </View>
    );
  }

  const summaryLabel =
    persistedScope === "private"
      ? t("access.trigger_private", "Only me")
      : persistedScope === "workspace"
        ? t("access.trigger_workspace", "Workspace")
        : persistedMembers.length > 0
          ? t("access.trigger_members_count", "{{count}} people", {
              count: persistedMembers.length,
            })
          : t("access.trigger_members_empty", "Specific people");

  return (
    <View className="flex-1 bg-background">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false） */}
      <View className="flex-row items-center px-4 pt-3 pb-2 border-b border-border">
        <Text className="flex-1 text-lg font-semibold text-foreground">
          {t("access.section_title", "Who can run this agent")}
        </Text>
        {canEdit ? (
          <Pressable
            onPress={onSave}
            disabled={!dirty || !draftChange || update.isPending}
            className={`px-2 py-1 ${
              dirty && draftChange && !update.isPending ? "" : "opacity-40"
            }`}
            accessibilityRole="button"
            accessibilityLabel={t("tab_body.common.save", "Save")}
          >
            <Text className="text-base text-brand font-semibold">
              {update.isPending
                ? t("create_dialog.creating", "Creating...")
                : t("tab_body.common.save", "Save")}
            </Text>
          </Pressable>
        ) : null}
      </View>

      {canEdit ? (
        <ScrollView
          className="flex-1"
          contentContainerClassName="px-4 pt-4 pb-8 gap-3"
        >
          <AccessChoice
            icon="lock-closed-outline"
            title={t("access.private_title", "Only me")}
            description={t("access.private_desc", "Only you can run this agent")}
            selected={scope === "private"}
            onSelect={() => setDraftScope("private")}
          />
          <AccessChoice
            icon="globe-outline"
            title={t("access.workspace_title", "Entire workspace")}
            description={t(
              "access.workspace_desc",
              "All workspace members can run this agent",
            )}
            selected={scope === "workspace"}
            onSelect={() => setDraftScope("workspace")}
          />
          <AccessChoice
            icon="people-outline"
            title={t("access.members_title", "Specific people")}
            description={t(
              "access.members_desc",
              "Only the people you pick can run this agent",
            )}
            selected={scope === "members"}
            onSelect={() => setDraftScope("members")}
          />

          {scope === "members" ? (
            <View className="gap-2">
              {editableMembers.length === 0 ? (
                <Text className="text-xs text-muted-foreground">
                  {t("access.members_empty", "No workspace members to choose from")}
                </Text>
              ) : (
                <View className="rounded-md border border-border overflow-hidden">
                  {editableMembers.map((member, index) => {
                    const checked = selectedMembers.includes(member.user_id);
                    return (
                      <Pressable
                        key={member.user_id}
                        onPress={() => toggleMember(member.user_id)}
                        className={`flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary ${
                          index > 0 ? "border-t border-border" : ""
                        }`}
                        accessibilityRole="checkbox"
                        accessibilityState={{ checked }}
                      >
                        <Ionicons
                          name={checked ? "checkbox-outline" : "square-outline"}
                          size={18}
                          color={
                            checked
                              ? THEME[colorScheme].brand
                              : THEME[colorScheme].mutedForeground
                          }
                        />
                        <ActorAvatar
                          type="member"
                          id={member.user_id}
                          size={24}
                        />
                        <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
                          {member.name}
                        </Text>
                      </Pressable>
                    );
                  })}
                </View>
              )}
              {selectedMembers.length === 0 ? (
                <Text className="text-xs text-destructive">
                  {t(
                    "access.shared_target_required",
                    "Select at least one person before saving.",
                  )}
                </Text>
              ) : null}
            </View>
          ) : null}

          {hasComposioAllowlist && persistedScope === "private" && scope !== "private" ? (
            <View className="border-l-2 border-warning pl-3">
              <Text className="text-xs text-muted-foreground leading-5">
                {t(
                  "access.composio_switch_hint",
                  "Heads up — this agent has Composio apps enabled. Sharing it lets everyone with access use those apps through this agent.",
                )}
              </Text>
            </View>
          ) : null}
        </ScrollView>
      ) : (
        <View className="flex-1 flex-row items-start gap-3 px-4 py-5">
          <View className="size-8 rounded-full bg-muted items-center justify-center">
            <Ionicons
              name="lock-closed-outline"
              size={16}
              color={THEME[colorScheme].mutedForeground}
            />
          </View>
          <View className="flex-1 gap-1">
            <Text className="text-sm font-medium text-foreground">
              {summaryLabel}
            </Text>
            <Text className="text-xs text-muted-foreground leading-5">
              {t(
                "access.owner_only_readonly",
                "Only the agent owner can change who can run this agent.",
              )}
            </Text>
          </View>
        </View>
      )}
    </View>
  );
}

function AccessChoice({
  icon,
  title,
  description,
  selected,
  onSelect,
}: {
  icon: React.ComponentProps<typeof Ionicons>["name"];
  title: string;
  description: string;
  selected: boolean;
  onSelect: () => void;
}) {
  const { colorScheme } = useColorScheme();
  return (
    <Pressable
      onPress={onSelect}
      className={`flex-row items-start gap-3 rounded-lg border px-4 py-3.5 active:bg-secondary ${
        selected ? "border-brand bg-brand/5" : "border-border"
      }`}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
    >
      <View className="mt-0.5 size-4 rounded-full border items-center justify-center shrink-0">
        {selected ? <View className="size-2 rounded-full bg-brand" /> : null}
      </View>
      <View className="size-8 rounded-full bg-muted items-center justify-center shrink-0">
        <Ionicons name={icon} size={16} color={THEME[colorScheme].mutedForeground} />
      </View>
      <View className="flex-1 gap-0.5">
        <Text className="text-sm font-medium text-foreground">{title}</Text>
        <Text className="text-xs text-muted-foreground leading-5">
          {description}
        </Text>
      </View>
    </Pressable>
  );
}
