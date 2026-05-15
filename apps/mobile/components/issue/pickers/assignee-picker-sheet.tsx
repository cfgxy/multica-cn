/**
 * Assignee picker — polymorphic single-select over members + agents +
 * squads, plus an "Unassigned" option. Loose mirror of web
 * `packages/views/issues/components/pickers/assignee-picker.tsx` (mobile v1
 * skips the frequency-sort optimization — sorts alphabetically instead).
 *
 * Container: iOS pageSheet via shared `<SheetShell>` (see CLAUDE.md
 * Lesson #6). Search box sits at the top of the body; FlatList of rows
 * below. On iOS pageSheet, keyboard appears layered over the sheet —
 * FlatList sets `automaticallyAdjustsKeyboardInsets` so rows above the
 * keyboard stay reachable when filtering.
 *
 * Selection emits `{ type, id } | null` (null = Unassigned). Parent passes
 * this to `useUpdateIssue.mutate({ assignee_type, assignee_id })`. The
 * backend routes a squad assignee to its leader agent
 * (server/internal/handler/issue.go:944).
 */
import { useMemo, useState } from "react";
import { FlatList, Pressable, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import type {
  Agent,
  IssueAssigneeType,
  MemberWithUser,
  Squad,
} from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { TextField } from "@/components/ui/text-field";
import { SheetShell } from "@/components/ui/sheet-shell";
import { memberListOptions } from "@/data/queries/members";
import { agentListOptions } from "@/data/queries/agents";
import { squadListOptions } from "@/data/queries/squads";
import { useWorkspaceStore } from "@/data/workspace-store";
import { cn } from "@/lib/utils";

export type AssigneeValue = {
  type: IssueAssigneeType;
  id: string;
} | null;

interface Props {
  visible: boolean;
  value: AssigneeValue;
  onChange: (next: AssigneeValue) => void;
  onClose: () => void;
}

type Row =
  | { kind: "unassigned" }
  | { kind: "member"; member: MemberWithUser }
  | { kind: "agent"; agent: Agent }
  | { kind: "squad"; squad: Squad };

export function AssigneePickerSheet({
  visible,
  value,
  onChange,
  onClose,
}: Props) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const [query, setQuery] = useState("");

  const rows = useMemo<Row[]>(() => {
    const q = query.trim().toLowerCase();
    const matchName = (name: string) => !q || name.toLowerCase().includes(q);

    const memberRows: Row[] = [...members]
      .filter((m) => matchName(m.name))
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((m) => ({ kind: "member" as const, member: m }));
    const agentRows: Row[] = [...agents]
      .filter((a) => matchName(a.name))
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((a) => ({ kind: "agent" as const, agent: a }));
    // Archived squads are excluded — matches web
    // (packages/views/issues/components/pickers/assignee-picker.tsx:93).
    const squadRows: Row[] = [...squads]
      .filter((s) => !s.archived_at && matchName(s.name))
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((s) => ({ kind: "squad" as const, squad: s }));

    // Hide "Unassigned" while searching — matches web behaviour.
    if (q) return [...memberRows, ...agentRows, ...squadRows];
    return [
      { kind: "unassigned" },
      ...memberRows,
      ...agentRows,
      ...squadRows,
    ];
  }, [members, agents, squads, query]);

  const isSelected = (row: Row): boolean => {
    if (row.kind === "unassigned") return value === null;
    if (value === null) return false;
    if (row.kind === "member")
      return value.type === "member" && value.id === row.member.user_id;
    if (row.kind === "agent")
      return value.type === "agent" && value.id === row.agent.id;
    return value.type === "squad" && value.id === row.squad.id;
  };

  const select = (row: Row) => {
    if (row.kind === "unassigned") onChange(null);
    else if (row.kind === "member")
      onChange({ type: "member", id: row.member.user_id });
    else if (row.kind === "agent")
      onChange({ type: "agent", id: row.agent.id });
    else onChange({ type: "squad", id: row.squad.id });
    onClose();
  };

  return (
    <SheetShell visible={visible} onClose={onClose} title="Assignee">
      <View className="px-3 pt-2 pb-2 border-b border-border">
        <TextField
          value={query}
          onChangeText={setQuery}
          placeholder="Search people"
          autoCapitalize="none"
          autoCorrect={false}
        />
      </View>
      <FlatList
        data={rows}
        className="flex-1"
        keyboardShouldPersistTaps="handled"
        automaticallyAdjustKeyboardInsets
        keyExtractor={(row) => {
          if (row.kind === "unassigned") return "unassigned";
          if (row.kind === "member") return `m:${row.member.user_id}`;
          if (row.kind === "agent") return `a:${row.agent.id}`;
          return `s:${row.squad.id}`;
        }}
        renderItem={({ item }) => (
          <Pressable
            onPress={() => select(item)}
            className={cn(
              "flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary",
              isSelected(item) && "bg-secondary",
            )}
          >
            {item.kind === "unassigned" ? (
              <View className="size-7 rounded-full border border-dashed border-muted-foreground/40 items-center justify-center">
                <Text className="text-xs text-muted-foreground">∅</Text>
              </View>
            ) : item.kind === "member" ? (
              <ActorAvatar
                type="member"
                id={item.member.user_id}
                size={28}
              />
            ) : item.kind === "agent" ? (
              <ActorAvatar type="agent" id={item.agent.id} size={28} />
            ) : (
              <ActorAvatar type="squad" id={item.squad.id} size={28} />
            )}
            <Text className="flex-1 text-sm text-foreground">
              {item.kind === "unassigned"
                ? "Unassigned"
                : item.kind === "member"
                  ? item.member.name
                  : item.kind === "agent"
                    ? item.agent.name
                    : item.squad.name}
            </Text>
            {isSelected(item) ? (
              <Text className="text-xs text-muted-foreground">✓</Text>
            ) : null}
          </Pressable>
        )}
        ListEmptyComponent={
          <View className="px-3 py-8 items-center">
            <Text className="text-xs text-muted-foreground">No matches.</Text>
          </View>
        }
      />
    </SheetShell>
  );
}
