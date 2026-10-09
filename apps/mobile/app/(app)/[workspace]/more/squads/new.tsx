/**
 * Create squad (`more/squads/new`, RUYI-346 S3 / RUYI-418 B1) — modal form
 * mirroring web's create-squad dialog: name (required), description, avatar
 * (Q5), leader agent (required) and optional instructions. Additional members
 * are added later from the detail screen (web parity: "Can be added later").
 *
 * RUYI-418 B1 alignment with web's picker:
 *  - S1: leader candidates are unarchived agents bound to a runtime the
 *    current user can actually use (`isRuntimeUsableForUser`), not just any
 *    unarchived agent — a leader whose runtime the viewer can't reach would
 *    queue work it can never run.
 *  - Q6: candidates are grouped My agents / Workspace agents (owner match)
 *    with a name search, same split as web's LeaderPicker.
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { isRuntimeUsableForUser } from "@multica/core/runtimes";
import type { CreateSquadRequest } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { agentListOptions } from "@/data/queries/agents";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { useCreateSquad } from "@/data/mutations/squads";
import { ActionSheetModal } from "@/components/ui/action-sheet";
import { useAvatarUploader } from "@/lib/avatar";
import { resolveAttachmentUrl } from "@/lib/attachment-url";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useAuthStore } from "@/data/auth-store";
import { useActorLookup } from "@/data/use-actor-name";
import { useT } from "@/lib/use-t";

export default function NewSquadScreen() {
  const insets = useSafeAreaInsets();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { workspace: wsSlug } = useLocalSearchParams<{ workspace: string }>();
  const { t } = useT("squads");
  const { t: tModals } = useT("modals");
  const { getName } = useActorLookup();
  const me = useAuthStore((s) => s.user);
  const create = useCreateSquad();
  const { uploading, showAvatarSheet, modalProps } = useAvatarUploader();

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [leaderId, setLeaderId] = useState("");
  const [avatarUrl, setAvatarUrl] = useState<string | null>(null);
  const [search, setSearch] = useState("");

  const { data: agents, isLoading: agentsLoading } = useQuery(
    agentListOptions(wsId),
  );
  const { data: runtimes } = useQuery(runtimeListOptions(wsId));

  // Leader candidates (S1): unarchived + bound to a runtime the viewer can
  // use. A deleted runtime is absent from the list, so its agents drop out.
  const leaderChoices = useMemo(() => {
    if (!agents) return [];
    const usableRuntimeIds = new Set(
      (runtimes ?? [])
        .filter((r) => isRuntimeUsableForUser(r, me?.id ?? null))
        .map((r) => r.id),
    );
    return agents.filter(
      (a) => !a.archived_at && a.runtime_id && usableRuntimeIds.has(a.runtime_id),
    );
  }, [agents, runtimes, me]);

  // Q6: My agents (owner match) vs the rest, both narrowed by the search
  // text (lowercase substring, web LeaderPicker parity).
  const { myAgents, workspaceAgents } = useMemo(() => {
    const q = search.trim().toLowerCase();
    const matches = (name: string) => name.toLowerCase().includes(q);
    const withNames = leaderChoices.map((a) => ({
      agent: a,
      name: getName("agent", a.id) || a.name,
    }));
    const mine = withNames.filter(
      (x) => x.agent.owner_id === me?.id && matches(x.name),
    );
    const rest = withNames.filter(
      (x) => x.agent.owner_id !== me?.id && matches(x.name),
    );
    return { myAgents: mine, workspaceAgents: rest };
  }, [leaderChoices, search, me, getName]);

  const canCreate = name.trim().length > 0 && leaderId !== "" && !create.isPending;

  const onPickAvatar = async () => {
    const url = await showAvatarSheet(avatarUrl);
    if (url !== null) setAvatarUrl(url);
  };

  const onCreate = () => {
    if (!canCreate) return;
    const body: CreateSquadRequest = {
      name: name.trim(),
      description,
      leader_id: leaderId,
      ...(avatarUrl ? { avatar_url: avatarUrl } : {}),
    };
    create.mutate(body, {
      onSuccess: (squad) => {
        Alert.alert(tModals("create_squad.toast_created", "Squad created"));
        // Replace so back from the detail doesn't return to the form.
        router.replace({
          pathname: "/[workspace]/more/squads/[id]",
          params: { workspace: wsSlug, id: squad.id },
        });
      },
      onError: () =>
        Alert.alert(tModals("create_squad.toast_failed", "Failed to create squad")),
    });
  };

  const renderLeaderRow = (item: { agent: (typeof leaderChoices)[number] }) => {
    const a = item.agent;
    const selected = a.id === leaderId;
    return (
      <Pressable
        key={a.id}
        onPress={() => setLeaderId(a.id)}
        className="flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary"
        accessibilityRole="radio"
        accessibilityState={{ selected }}
        accessibilityLabel={getName("agent", a.id)}
      >
        <View
          className={`size-4 rounded-full border items-center justify-center ${
            selected ? "border-brand" : "border-muted-foreground"
          }`}
        >
          {selected ? <View className="size-2 rounded-full bg-brand" /> : null}
        </View>
        <ActorAvatar type="agent" id={a.id} size={28} />
        <Text numberOfLines={1} className="flex-1 text-sm text-foreground">
          {getName("agent", a.id)}
        </Text>
      </Pressable>
    );
  };

  const renderGroup = (
    label: string,
    items: { agent: (typeof leaderChoices)[number] }[],
  ) => {
    if (items.length === 0) return null;
    return (
      <View className="gap-1.5">
        <Text className="text-xs text-muted-foreground">{label}</Text>
        <View className="rounded-md border border-border overflow-hidden">
          {items.map((item, i) => (
            <View key={item.agent.id} className={i > 0 ? "border-t border-border" : ""}>
              {renderLeaderRow(item)}
            </View>
          ))}
        </View>
      </View>
    );
  };

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      {/* modal 自绘头部；顶部让出系统状态栏（RUYI-540），见 more/runtimes/new。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
        <Pressable
          onPress={() => router.back()}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={tModals("create_squad.cancel", "Cancel")}
        >
          <Text className="text-base text-muted-foreground">
            {tModals("create_squad.cancel", "Cancel")}
          </Text>
        </Pressable>
        <Text className="flex-1 text-center text-lg font-semibold text-foreground">
          {tModals("create_squad.title", "Create Squad")}
        </Text>
        <Pressable
          onPress={onCreate}
          disabled={!canCreate}
          className={`px-2 py-1 ${canCreate ? "" : "opacity-40"}`}
          accessibilityRole="button"
          accessibilityLabel={tModals("create_squad.submit", "Create Squad")}
        >
          <Text className="text-base text-brand font-semibold">
            {create.isPending
              ? tModals("create_squad.submitting", "Creating...")
              : tModals("create_squad.submit", "Create Squad")}
          </Text>
        </Pressable>
      </View>

      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 pt-4 pb-8 gap-4"
        keyboardShouldPersistTaps="handled"
      >
        {/* ── Avatar (Q5) ── */}
        <View className="flex-row items-center gap-3">
          <Pressable
            onPress={onPickAvatar}
            disabled={uploading}
            accessibilityRole="button"
            accessibilityLabel={t("mobile.create.avatar_add", "Set avatar")}
          >
            <Avatar alt={t("mobile.create.avatar_add", "Set avatar")} className="size-16">
              {avatarUrl ? (
                <AvatarImage source={{ uri: resolveAttachmentUrl(avatarUrl) ?? avatarUrl }} />
              ) : (
                <AvatarFallback className="border border-dashed border-border">
                  <Text className="text-xl text-muted-foreground">+</Text>
                </AvatarFallback>
              )}
            </Avatar>
          </Pressable>
          <Text className="flex-1 text-xs text-muted-foreground">
            {t("mobile.create.avatar_hint", "Optional. Shown wherever the squad appears.")}
          </Text>
        </View>

        <Field label={tModals("create_squad.name_label", "Name")}>
          <TextInput
            value={name}
            onChangeText={setName}
            placeholder={tModals("create_squad.name_placeholder", "e.g. Frontend Team")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            autoFocus
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
            editable={!create.isPending}
          />
        </Field>

        <Field label={tModals("create_squad.description_label", "Description")}>
          <TextInput
            value={description}
            onChangeText={setDescription}
            placeholder={tModals(
              "create_squad.description_placeholder",
              "Describe what this squad is responsible for...",
            )}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            multiline
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2 min-h-16"
            textAlignVertical="top"
            editable={!create.isPending}
          />
        </Field>

        <Field
          label={tModals("create_squad.leader_label", "Leader Agent")}
          hint={tModals(
            "create_squad.leader_hint",
            "The leader receives all issues assigned to this squad and coordinates the team.",
          )}
        >
          <TextInput
            value={search}
            onChangeText={setSearch}
            placeholder={t("mobile.create.search_placeholder", "Search agents…")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2"
            autoCorrect={false}
          />
          {agentsLoading ? (
            <Text className="text-sm text-muted-foreground py-1">
              {t("execution_profile.loading_label", "Loading")}
            </Text>
          ) : leaderChoices.length === 0 ? (
            <Text className="text-sm text-muted-foreground py-1">
              {tModals(
                "create_squad.no_agents",
                "No active agents available. Create an agent first.",
              )}
            </Text>
          ) : myAgents.length === 0 && workspaceAgents.length === 0 ? (
            <Text className="text-sm text-muted-foreground py-1">
              {t("mobile.create.search_empty", "No agents match this search.")}
            </Text>
          ) : (
            <View className="gap-3">
              {renderGroup(t("mobile.create.group_my", "My agents"), myAgents)}
              {renderGroup(
                t("mobile.create.group_workspace", "Workspace agents"),
                workspaceAgents,
              )}
            </View>
          )}
        </Field>
        <ActionSheetModal {...modalProps} />
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <View className="gap-1.5">
      <Text className="text-xs uppercase tracking-wider text-muted-foreground">
        {label}
      </Text>
      {children}
      {hint ? (
        <Text className="text-xs text-muted-foreground">{hint}</Text>
      ) : null}
    </View>
  );
}
