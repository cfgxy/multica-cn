/**
 * Above-input suggestion bar for @mentions in the comment composer.
 *
 * iOS-native pattern (Slack iOS / Linear iOS / Discord iOS): when the user
 * is composing an `@<token>`, a horizontal-scroll-free bar appears just
 * above the input row with filtered candidates. Tap a row → caller inserts
 * the mention. The suggestion bar does NOT manage state itself.
 *
 * Sections (in order):
 *   1. `@all` (single static row, only visible when query matches "all"
 *      prefix or is empty)
 *   2. Members — sorted alphabetically
 *   3. Agents — sorted alphabetically
 *
 * Recency-sort and issue-cross-references are deferred to v2 (see
 * `~/.claude/plans/plan-polymorphic-hickey.md`).
 */
import { useMemo } from "react";
import { FlatList, Pressable, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { memberListOptions } from "@/data/queries/members";
import { agentListOptions } from "@/data/queries/agents";
import { useWorkspaceStore } from "@/data/workspace-store";
import type { MentionMarker } from "@/lib/mention-serialize";
import { cn } from "@/lib/utils";

type Row =
  | { kind: "all" }
  | { kind: "section"; label: string }
  | { kind: "member"; member: MemberWithUser }
  | { kind: "agent"; agent: Agent }
  | { kind: "empty" };

interface Props {
  visible: boolean;
  query: string;
  onSelect: (mention: MentionMarker) => void;
}

export function MentionSuggestionBar({ visible, query, onSelect }: Props) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));

  const rows = useMemo<Row[]>(() => {
    const q = query.trim().toLowerCase();
    const showAll = !q || "all".startsWith(q);

    const matchedMembers = [...members]
      .filter((m) => !q || m.name.toLowerCase().includes(q))
      .sort((a, b) => a.name.localeCompare(b.name));

    const matchedAgents = [...agents]
      .filter((a) => !q || a.name.toLowerCase().includes(q))
      .sort((a, b) => a.name.localeCompare(b.name));

    const out: Row[] = [];
    if (showAll) out.push({ kind: "all" });
    if (matchedMembers.length > 0) {
      out.push({ kind: "section", label: "Members" });
      for (const m of matchedMembers) out.push({ kind: "member", member: m });
    }
    if (matchedAgents.length > 0) {
      out.push({ kind: "section", label: "Agents" });
      for (const a of matchedAgents) out.push({ kind: "agent", agent: a });
    }
    if (out.length === 0) out.push({ kind: "empty" });
    return out;
  }, [members, agents, query]);

  if (!visible) return null;

  return (
    <View
      className="bg-background border-b border-border"
      style={{ maxHeight: 220 }}
    >
      <FlatList
        data={rows}
        keyboardShouldPersistTaps="handled"
        keyExtractor={(row, i) =>
          row.kind === "all"
            ? "row:all"
            : row.kind === "section"
              ? `row:section:${row.label}`
              : row.kind === "member"
                ? `row:m:${row.member.user_id}`
                : row.kind === "agent"
                  ? `row:a:${row.agent.id}`
                  : `row:empty:${i}`
        }
        renderItem={({ item, index }) => {
          if (item.kind === "section") {
            // Tiny separator + uppercase label gives clear visual structure
            // between @all / Members / Agents segments without taking real
            // estate.
            return (
              <View
                className={cn(
                  "px-3 pt-2 pb-1",
                  index > 0 && "border-t border-border/60 mt-1",
                )}
              >
                <Text className="text-[10px] uppercase tracking-wider text-muted-foreground/80 font-medium">
                  {item.label}
                </Text>
              </View>
            );
          }
          if (item.kind === "empty") {
            return (
              <View className="px-3 py-3">
                <Text className="text-xs text-muted-foreground">
                  No matches.
                </Text>
              </View>
            );
          }
          if (item.kind === "all") {
            return (
              <Pressable
                onPress={() =>
                  onSelect({ type: "all", id: "all", name: "all" })
                }
                className="flex-row items-center gap-3 px-3 py-2 active:bg-secondary"
              >
                <View className="size-7 rounded-full bg-brand/15 items-center justify-center">
                  <Text className="text-xs font-medium text-brand">@</Text>
                </View>
                <Text className="flex-1 text-sm text-foreground">
                  Everyone
                </Text>
                <Badge label="All" />
              </Pressable>
            );
          }
          if (item.kind === "member") {
            return (
              <Pressable
                onPress={() =>
                  onSelect({
                    type: "member",
                    id: item.member.user_id,
                    name: item.member.name,
                  })
                }
                className="flex-row items-center gap-3 px-3 py-2 active:bg-secondary"
              >
                <ActorAvatar
                  type="member"
                  id={item.member.user_id}
                  size={28}
                />
                <Text className="flex-1 text-sm text-foreground">
                  {item.member.name}
                </Text>
                <Badge label="Member" />
              </Pressable>
            );
          }
          // agent
          return (
            <Pressable
              onPress={() =>
                onSelect({
                  type: "agent",
                  id: item.agent.id,
                  name: item.agent.name,
                })
              }
              className="flex-row items-center gap-3 px-3 py-2 active:bg-secondary"
            >
              <ActorAvatar type="agent" id={item.agent.id} size={28} />
              <Text className="flex-1 text-sm text-foreground">
                {item.agent.name}
              </Text>
              <Badge label="Agent" tone="brand" />
            </Pressable>
          );
        }}
      />
    </View>
  );
}

function Badge({
  label,
  tone = "muted",
}: {
  label: string;
  tone?: "muted" | "brand";
}) {
  return (
    <View
      className={cn(
        "px-1.5 py-0.5 rounded",
        tone === "brand" ? "bg-brand/10" : "bg-secondary",
      )}
    >
      <Text
        className={cn(
          "text-[10px] uppercase tracking-wide",
          tone === "brand" ? "text-brand" : "text-muted-foreground",
        )}
      >
        {label}
      </Text>
    </View>
  );
}
