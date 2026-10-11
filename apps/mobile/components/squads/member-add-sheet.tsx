/**
 * Add-squad-member sheet body (RUYI-346 S2) — mobile counterpart of web's
 * add_member_dialog: single target picker over workspace members and active
 * agents (already-joined composite keys excluded), optional role text.
 * Rendered inside the `add-member` formSheet route; this component holds the
 * logic so the screen stays a shell.
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import type { SquadMemberType } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { squadMembersOptions } from "@/data/queries/squads";
import { agentListOptions } from "@/data/queries/agents";
import { memberListOptions } from "@/data/queries/members";
import { useAddSquadMember } from "@/data/mutations/squads";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useActorLookup } from "@/data/use-actor-name";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

interface Candidate {
  key: string;
  memberType: SquadMemberType;
  memberId: string;
  name: string;
  archived?: boolean;
}

export function MemberAddSheet({ squadId }: { squadId: string }) {
  const insets = useSafeAreaInsets();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { t } = useT("squads");
  const { getName } = useActorLookup();
  const addMember = useAddSquadMember(squadId);

  const { data: existingMembers } = useQuery(squadMembersOptions(wsId, squadId));
  const { data: agents } = useQuery(agentListOptions(wsId));
  const { data: members } = useQuery(memberListOptions(wsId));

  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Candidate | null>(null);
  const [role, setRole] = useState("");

  const candidates = useMemo<Candidate[]>(() => {
    const taken = new Set(
      (existingMembers ?? []).map((m) => `${m.member_type}:${m.member_id}`),
    );
    const agentRows: Candidate[] = (agents ?? [])
      .filter((a) => !a.archived_at && !taken.has(`agent:${a.id}`))
      .map((a) => ({
        key: `agent:${a.id}`,
        memberType: "agent" as const,
        memberId: a.id,
        name: getName("agent", a.id),
      }));
    const memberRows: Candidate[] = (members ?? [])
      .filter((m) => !taken.has(`member:${m.user_id}`))
      .map((m) => ({
        key: `member:${m.user_id}`,
        memberType: "member" as const,
        memberId: m.user_id,
        name: getName("member", m.user_id),
      }));
    return [...agentRows, ...memberRows];
  }, [existingMembers, agents, members, getName]);

  const visible = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return candidates;
    return candidates.filter((c) => c.name.toLowerCase().includes(q));
  }, [candidates, search]);

  const agentRows = visible.filter((c) => c.memberType === "agent");
  const memberRows = visible.filter((c) => c.memberType === "member");

  const submit = () => {
    if (!selected || addMember.isPending) return;
    addMember.mutate(
      {
        member_type: selected.memberType,
        member_id: selected.memberId,
        ...(role.trim() ? { role: role.trim() } : {}),
      },
      {
        onSuccess: () => {
          Alert.alert(t("toasts.member_added", "Member added"));
          router.back();
        },
        onError: () =>
          Alert.alert(t("toasts.member_add_failed", "Failed to add member")),
      },
    );
  };

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false）；顶部让出系统状态栏（RUYI-563）。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
        <Pressable
          onPress={() => router.back()}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={t("add_member_dialog.cancel", "Cancel")}
        >
          <Text className="text-base text-muted-foreground">
            {t("add_member_dialog.cancel", "Cancel")}
          </Text>
        </Pressable>
        <Text className="flex-1 text-center text-lg font-semibold text-foreground">
          {t("add_member_dialog.title", "Add Member")}
        </Text>
        <Pressable
          onPress={submit}
          disabled={!selected || addMember.isPending}
          className={`px-2 py-1 ${
            selected && !addMember.isPending ? "" : "opacity-40"
          }`}
          accessibilityRole="button"
          accessibilityLabel={t("add_member_dialog.add", "Add")}
        >
          <Text className="text-base text-brand font-semibold">
            {t("add_member_dialog.add", "Add")}
          </Text>
        </Pressable>
      </View>

      <ScrollView
        nestedScrollEnabled
        className="flex-1"
        contentContainerClassName="px-4 pt-3 pb-8 gap-3"
        keyboardShouldPersistTaps="handled"
      >
        <Text className="text-xs text-muted-foreground">
          {t(
            "add_member_dialog.description",
            "Add a workspace member or agent to this squad.",
          )}
        </Text>
        <TextInput
          value={search}
          onChangeText={setSearch}
          placeholder={t(
            "add_member_dialog.search_placeholder",
            "Search members or agents...",
          )}
          placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
          autoCorrect={false}
          className="rounded-md border border-border px-3 py-2 text-sm text-foreground"
        />

        {visible.length === 0 ? (
          <Text className="text-sm text-muted-foreground py-4 text-center">
            {t("add_member_dialog.select_target", "Select a member or agent")}
          </Text>
        ) : null}

        {agentRows.length > 0 ? (
          <View className="gap-2">
            <Text className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
              {t("add_member_dialog.agents_section", "Agents")}
            </Text>
            {agentRows.map((c) => (
              <CandidateRow
                key={c.key}
                candidate={c}
                selected={selected?.key === c.key}
                onSelect={() => setSelected(c)}
              />
            ))}
          </View>
        ) : null}

        {memberRows.length > 0 ? (
          <View className="gap-2">
            <Text className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
              {t("add_member_dialog.members_section", "Members")}
            </Text>
            {memberRows.map((c) => (
              <CandidateRow
                key={c.key}
                candidate={c}
                selected={selected?.key === c.key}
                onSelect={() => setSelected(c)}
              />
            ))}
          </View>
        ) : null}

        <View className="gap-1.5 pt-1">
          <Text className="text-xs uppercase tracking-wider text-muted-foreground">
            {t("add_member_dialog.label_role", "Role")}{" "}
            {t("add_member_dialog.label_optional", "(optional)")}
          </Text>
          <TextInput
            value={role}
            onChangeText={setRole}
            placeholder={t("add_member_dialog.role_placeholder", "e.g. Reviewer, Frontend Lead")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
            editable={!addMember.isPending}
          />
        </View>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

function CandidateRow({
  candidate,
  selected,
  onSelect,
}: {
  candidate: Candidate;
  selected: boolean;
  onSelect: () => void;
}) {
  const { colorScheme } = useColorScheme();
  return (
    <Pressable
      onPress={onSelect}
      className={`flex-row items-center gap-3 rounded-md border px-3 py-2.5 active:bg-secondary ${
        selected ? "border-brand bg-brand/5" : "border-border"
      }`}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
      accessibilityLabel={candidate.name}
    >
      <ActorAvatar
        type={candidate.memberType}
        id={candidate.memberId}
        size={32}
      />
      <Text numberOfLines={1} className="flex-1 text-sm text-foreground">
        {candidate.name}
      </Text>
      {selected ? (
        <Ionicons
          name="checkmark"
          size={18}
          color={THEME[colorScheme].primary}
        />
      ) : null}
    </Pressable>
  );
}
