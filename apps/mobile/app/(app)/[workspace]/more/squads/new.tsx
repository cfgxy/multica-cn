/**
 * Create squad (`more/squads/new`, RUYI-346 S3) — modal form mirroring web's
 * create-squad dialog for the P0 field set: name (required), description,
 * leader agent (required — receives all issues assigned to the squad) and
 * optional instructions. Additional members are added later from the detail
 * screen (web parity: "Can be added later").
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import type { CreateSquadRequest } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { agentListOptions } from "@/data/queries/agents";
import { useCreateSquad } from "@/data/mutations/squads";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useActorLookup } from "@/data/use-actor-name";
import { useT } from "@/lib/use-t";

export default function NewSquadScreen() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { workspace: wsSlug } = useLocalSearchParams<{ workspace: string }>();
  const { t } = useT("squads");
  const { t: tModals } = useT("modals");
  const { getName } = useActorLookup();
  const create = useCreateSquad();

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [leaderId, setLeaderId] = useState("");

  const { data: agents, isLoading: agentsLoading } = useQuery(
    agentListOptions(wsId),
  );

  // Leader candidates: non-archived agents, same universe web's picker
  // groups into My/Workspace agents (grouping collapsed for P0 mobile).
  const leaderChoices = useMemo(
    () => (agents ?? []).filter((a) => !a.archived_at),
    [agents],
  );

  const canCreate = name.trim().length > 0 && leaderId !== "" && !create.isPending;

  const onCreate = () => {
    if (!canCreate) return;
    const body: CreateSquadRequest = {
      name: name.trim(),
      description,
      leader_id: leaderId,
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

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      {/* modal 自绘头部 */}
      <View className="flex-row items-center px-4 pt-3 pb-2 border-b border-border">
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
          ) : (
            <View className="rounded-md border border-border overflow-hidden">
              {leaderChoices.map((a, i) => {
                const selected = a.id === leaderId;
                return (
                  <Pressable
                    key={a.id}
                    onPress={() => setLeaderId(a.id)}
                    className={`flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary ${
                      i > 0 ? "border-t border-border" : ""
                    }`}
                    accessibilityRole="radio"
                    accessibilityState={{ selected }}
                    accessibilityLabel={getName("agent", a.id)}
                  >
                    <View
                      className={`size-4 rounded-full border items-center justify-center ${
                        selected ? "border-brand" : "border-muted-foreground"
                      }`}
                    >
                      {selected ? (
                        <View className="size-2 rounded-full bg-brand" />
                      ) : null}
                    </View>
                    <ActorAvatar type="agent" id={a.id} size={28} />
                    <Text
                      numberOfLines={1}
                      className="flex-1 text-sm text-foreground"
                    >
                      {getName("agent", a.id)}
                    </Text>
                  </Pressable>
                );
              })}
            </View>
          )}
        </Field>
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
