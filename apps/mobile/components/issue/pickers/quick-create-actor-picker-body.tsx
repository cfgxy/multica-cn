/**
 * Pure picker body for the smart-mode (agent quick-create) actor pick —
 * agents + squads only, no members and no "Unassigned" row. Web's
 * counterpart is the AgentPicker popover in
 * `packages/views/modals/quick-create-issue.tsx`; its visibility rules are
 * NOT re-derived here — they come from `visibleQuickCreateActors` in
 * `lib/quick-create.ts` (mirror of the panel's useMemo filters).
 *
 * Visual/structural mirror of `assignee-picker-body.tsx` (same FlatList
 * contract: route's direct child so RNSScreenContentWrapper applies the iOS
 * formSheet header offset, see react-native-screens#3634). The alpha sort
 * replaces web's popover order for consistency with the assignee picker.
 */
import { useMemo } from "react";
import { FlatList, Pressable, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useColorScheme } from "nativewind";
import type { Agent, Squad } from "@multica/core/types";
import { useQuery } from "@tanstack/react-query";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { agentListOptions } from "@/data/queries/agents";
import { memberListOptions } from "@/data/queries/members";
import { squadListOptions } from "@/data/queries/squads";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { THEME } from "@/lib/theme";
import { cn } from "@/lib/utils";
import {
  resolveQuickCreateActor,
  visibleQuickCreateActors,
  type QuickCreateActorRef,
} from "@/lib/quick-create";
import { useT } from "@/lib/use-t";
import { useScrollToTopOnChange } from "@/lib/use-scroll-to-top-on-change";

const AVATAR_SIZE = 36;

interface Props {
  value: QuickCreateActorRef | null;
  query: string;
  onChange: (next: QuickCreateActorRef) => void;
}

type Row =
  | { kind: "agent"; agent: Agent }
  | { kind: "squad"; squad: Squad };

export function QuickCreateActorPickerBody({ value, query, onChange }: Props) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const userId = useAuthStore((s) => s.user?.id);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  // Same derivation as chat.tsx — members query is the mobile role source
  // (workspace-store carries no role field).
  const memberRole = useMemo(
    () => members.find((m) => m.user_id === userId)?.role ?? null,
    [members, userId],
  );

  const { t } = useT("modals");
  const { colorScheme } = useColorScheme();
  const checkColor =
    colorScheme === "dark" ? THEME.dark.primary : THEME.light.primary;

  // Same visibility rule as the smart-create panel renders with — the list
  // never offers a pick the server would reject (web ActorPicker parity).
  const visible = useMemo(
    () => visibleQuickCreateActors(agents, squads, { userId, memberRole }),
    [agents, squads, userId, memberRole],
  );
  // Seed-chain consistency: an unset value previews the same default the
  // panel would submit with (first visible agent), mirroring web's picker
  // showing the resolved actor.
  const effective = resolveQuickCreateActor(
    [value],
    visible.agents,
    visible.squads,
  );

  const listRef = useScrollToTopOnChange(query);

  const rows = useMemo<Row[]>(() => {
    const q = query.trim().toLowerCase();
    const matchName = (name: string) => !q || name.toLowerCase().includes(q);
    return [
      ...visible.agents
        .filter((a) => matchName(a.name))
        .sort((a, b) => a.name.localeCompare(b.name))
        .map((agent) => ({ kind: "agent" as const, agent })),
      ...visible.squads
        .filter((s) => matchName(s.name))
        .sort((a, b) => a.name.localeCompare(b.name))
        .map((squad) => ({ kind: "squad" as const, squad })),
    ];
  }, [visible, query]);

  const isSelected = (row: Row) =>
    !!effective &&
    row.kind === effective.type &&
    (row.kind === "agent" ? row.agent.id : row.squad.id) === effective.id;

  // FlatList is returned as the route's direct child — see file header.
  return (
    <FlatList
      ref={listRef}
      data={rows}
      className="flex-1"
      keyboardShouldPersistTaps="handled"
      automaticallyAdjustKeyboardInsets
      contentInsetAdjustmentBehavior="automatic"
      keyExtractor={(row) =>
        row.kind === "agent" ? `a:${row.agent.id}` : `s:${row.squad.id}`
      }
      renderItem={({ item }) => (
        <Pressable
          onPress={() => onChange({ type: item.kind, id: item.kind === "agent" ? item.agent.id : item.squad.id })}
          className={cn(
            "flex-row items-center gap-3 px-4 py-3 active:bg-secondary",
          )}
        >
          <ActorAvatar
            type={item.kind}
            id={item.kind === "agent" ? item.agent.id : item.squad.id}
            size={AVATAR_SIZE}
          />
          <Text className="flex-1 text-base text-foreground">
            {item.kind === "agent" ? item.agent.name : item.squad.name}
          </Text>
          <Text className="text-sm text-muted-foreground">
            {item.kind === "agent"
              ? t("create_issue.agent.agents_group", "Agents")
              : t("create_issue.agent.squads_group", "Squads")}
          </Text>
          {isSelected(item) ? (
            <Ionicons name="checkmark" size={20} color={checkColor} />
          ) : null}
        </Pressable>
      )}
      ListEmptyComponent={
        <View className="px-3 py-8 items-center">
          <Text className="text-sm text-muted-foreground">
            {t("create_issue.agent.no_agents", "No agents or squads available.")}
          </Text>
        </View>
      }
    />
  );
}
