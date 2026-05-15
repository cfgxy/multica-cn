/**
 * Assignee picker — polymorphic single-select over members + agents, plus
 * an "Unassigned" option. Loose mirror of web
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
 * this to `useUpdateIssue.mutate({ assignee_type, assignee_id })`.
 */
import { useMemo, useState } from "react";
import { FlatList, Pressable, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import type { Agent, IssueAssigneeType, MemberWithUser } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { TextField } from "@/components/ui/text-field";
import { SheetShell } from "@/components/ui/sheet-shell";
import { memberListOptions } from "@/data/queries/members";
import { agentListOptions } from "@/data/queries/agents";
import { useWorkspaceStore } from "@/data/workspace-store";
import { cn } from "@/lib/utils";

// `IssueAssigneeType` is `"member" | "agent" | "squad"` (packages/core/types/
// issue.ts). Mobile only renders member + agent rows in this picker — squad
// listing isn't built yet — but `AssigneeValue` keeps the full union so an
// existing squad assignment from web isn't silently dropped when the
// attribute-row passes it back through. Squad rows fail `isSelected` against
// member/agent options; user can clear via Unassigned or replace with a
// member/agent.
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
  | { kind: "agent"; agent: Agent };

export function AssigneePickerSheet({
  visible,
  value,
  onChange,
  onClose,
}: Props) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const [query, setQuery] = useState("");

  const rows = useMemo<Row[]>(() => {
    const q = query.trim().toLowerCase();
    const matchMember = (m: MemberWithUser) =>
      !q || m.name.toLowerCase().includes(q);
    const matchAgent = (a: Agent) => !q || a.name.toLowerCase().includes(q);

    const memberRows: Row[] = [...members]
      .filter(matchMember)
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((m) => ({ kind: "member" as const, member: m }));
    const agentRows: Row[] = [...agents]
      .filter(matchAgent)
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((a) => ({ kind: "agent" as const, agent: a }));

    // Hide "Unassigned" while searching — matches web behaviour.
    if (q) return [...memberRows, ...agentRows];
    return [{ kind: "unassigned" }, ...memberRows, ...agentRows];
  }, [members, agents, query]);

  const isSelected = (row: Row): boolean => {
    if (row.kind === "unassigned") return value === null;
    if (value === null) return false;
    if (row.kind === "member")
      return value.type === "member" && value.id === row.member.user_id;
    return value.type === "agent" && value.id === row.agent.id;
  };

  const select = (row: Row) => {
    if (row.kind === "unassigned") onChange(null);
    else if (row.kind === "member")
      onChange({ type: "member", id: row.member.user_id });
    else onChange({ type: "agent", id: row.agent.id });
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
        keyExtractor={(row) =>
          row.kind === "unassigned"
            ? "unassigned"
            : row.kind === "member"
              ? `m:${row.member.user_id}`
              : `a:${row.agent.id}`
        }
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
            ) : (
              <ActorAvatar type="agent" id={item.agent.id} size={28} />
            )}
            <Text className="flex-1 text-sm text-foreground">
              {item.kind === "unassigned"
                ? "Unassigned"
                : item.kind === "member"
                  ? item.member.name
                  : item.agent.name}
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
