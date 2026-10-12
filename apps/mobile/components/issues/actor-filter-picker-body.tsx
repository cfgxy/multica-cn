/**
 * Pure multi-select picker body for the Tasks tab's assignee/creator
 * filters (RUYI-344). Grouped members / agents / squads with checkmarks —
 * toggling never dismisses the sheet. Same value semantics as
 * `assignee-picker-body.tsx`: a member actor ref carries the USER id
 * (issues carry `assignee_id = user uuid` for member assignees), agents
 * and squads carry their own ids.
 *
 * The 未分配 row exists only for the assignee kind (`include_no_assignee`
 * server param; there is no "no creator" facet). Mirrors web's
 * ActorSubContent facet, which the design spec names as the reference.
 */
import { useMemo } from "react";
import { FlatList, Pressable, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import type { Agent, MemberWithUser, Squad } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { memberListOptions } from "@/data/queries/members";
import { agentListOptions } from "@/data/queries/agents";
import { squadListOptions } from "@/data/queries/squads";
import { useWorkspaceStore } from "@/data/workspace-store";
import type { TaskActorRef } from "@/data/stores/tasks-view-store";
import { isAgentRuntimeBound } from "@/lib/is-agent-runtime-bound";
import { useT } from "@/lib/use-t";

type Row =
  | { kind: "header"; key: string; label: string }
  | { kind: "unassigned"; key: string }
  | {
      kind: "member" | "agent" | "squad";
      key: string;
      ref: TaskActorRef;
      name: string;
      avatarUrl: string | null;
    };

interface Props {
  kind: "assignee" | "creator";
  selected: TaskActorRef[];
  includeNoAssignee: boolean;
  onToggleRef: (ref: TaskActorRef) => void;
  onToggleNoAssignee: () => void;
}

function actorHeader(key: string, label: string): Row {
  return { kind: "header", key, label };
}

export function ActorFilterPickerBody({
  kind,
  selected,
  includeNoAssignee,
  onToggleRef,
  onToggleNoAssignee,
}: Props) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { t } = useT("issues");
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));

  const rows = useMemo<Row[]>(() => {
    const byName = (a: { name: string }, b: { name: string }) =>
      a.name.localeCompare(b.name);
    const memberRows: Row[] = [...(members as MemberWithUser[])]
      .sort(byName)
      .map((m) => ({
        kind: "member" as const,
        key: `member-${m.user_id}`,
        ref: { type: "member" as const, id: m.user_id },
        name: m.name,
        avatarUrl: m.avatar_url,
      }));
    // Archived/unbound agents can't take new work; they also shouldn't
    // widen the filter list (same rule the assignee picker applies).
    const agentRows: Row[] = [...(agents as Agent[])]
      .filter((a) => !a.archived_at && isAgentRuntimeBound(a))
      .sort(byName)
      .map((a) => ({
        kind: "agent" as const,
        key: `agent-${a.id}`,
        ref: { type: "agent" as const, id: a.id },
        name: a.name,
        avatarUrl: a.avatar_url,
      }));
    const squadRows: Row[] = [...(squads as Squad[])]
      .filter((s) => !s.archived_at)
      .sort(byName)
      .map((s) => ({
        kind: "squad" as const,
        key: `squad-${s.id}`,
        ref: { type: "squad" as const, id: s.id },
        name: s.name,
        avatarUrl: s.avatar_url,
      }));

    return [
      ...(kind === "assignee"
        ? [{ kind: "unassigned" as const, key: "unassigned" }]
        : []),
      ...(memberRows.length
        ? [actorHeader("h-members", t("mobile.tasks.filters.group_members", "Members"))]
        : []),
      ...memberRows,
      ...(agentRows.length
        ? [actorHeader("h-agents", t("mobile.tasks.filters.group_agents", "Agents"))]
        : []),
      ...agentRows,
      ...(squadRows.length
        ? [actorHeader("h-squads", t("mobile.tasks.filters.group_squads", "Squads"))]
        : []),
      ...squadRows,
    ];
  }, [members, agents, squads, kind, t]);

  const isRefSelected = (ref: TaskActorRef) =>
    selected.some((s) => s.type === ref.type && s.id === ref.id);

  return (
    <FlatList
      nestedScrollEnabled
      data={rows}
      className="flex-1"
      keyExtractor={(row) => row.key}
      renderItem={({ item }) => {
        if (item.kind === "header") {
          return (
            <View className="px-4 pt-3 pb-1.5">
              <Text className="text-xs uppercase tracking-wider text-muted-foreground font-medium">
                {item.label}
              </Text>
            </View>
          );
        }
        if (item.kind === "unassigned") {
          return (
            <Pressable
              onPress={onToggleNoAssignee}
              className="flex-row items-center gap-3 px-4 py-2.5 active:bg-secondary"
            >
              <View className="size-9 items-center justify-center rounded-full border border-dashed border-muted-foreground/50">
                <Text className="text-muted-foreground">—</Text>
              </View>
              <Text className="flex-1 text-sm text-foreground">
                {t("filters.no_assignee", "No assignee")}
              </Text>
              {includeNoAssignee ? (
                <Text className="text-sm text-primary font-semibold">✓</Text>
              ) : null}
            </Pressable>
          );
        }
        return (
          <Pressable
            onPress={() => onToggleRef(item.ref)}
            className="flex-row items-center gap-3 px-4 py-2.5 active:bg-secondary"
          >
            <ActorAvatar
              type={item.kind}
              id={item.ref.id}
              name={item.name}
              avatarUrl={item.avatarUrl}
              size={36}
            />
            <Text numberOfLines={1} className="flex-1 text-sm text-foreground">
              {item.name}
            </Text>
            {isRefSelected(item.ref) ? (
              <Text className="text-sm text-primary font-semibold">✓</Text>
            ) : null}
          </Pressable>
        );
      }}
    />
  );
}
